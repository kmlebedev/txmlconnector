package dlllog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// clock returns a fixed local time that advances 1 µs per call.
func clock(start time.Time) func() time.Time {
	t := start
	return func() time.Time {
		t = t.Add(time.Microsecond)
		return t
	}
}

func cp1251(t *testing.T, s string) string {
	t.Helper()
	b, err := charmap.Windows1251.NewEncoder().String(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var base = time.Date(2026, 11, 5, 18, 43, 18, 671446000, time.Local)

func open(t *testing.T, dir string, level int) *Logger {
	t.Helper()
	l, err := Open(Options{Dir: dir, Level: level, PID: 0x32, Now: clock(base), ThreadID: func() uint64 { return 0x36 }})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// The first lines of a session reproduce the DLL bytes (O: RE-3 log oracle
// 2026-11-05, docs/protocol/logs.md): CP1251, CRLF, "HHMMSS.uuuuuu [pid]
// [thread] <kind> text".
func TestOpenWritesDLLHeader(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, 1)
	l.Close()
	dsp := readFile(t, filepath.Join(dir, "20261105_dsp.log"))
	want := "184318.671447 [0032] [0036] <info> Open log " + dir + ", level 1\r\n" +
		"184318.671448 [0032] [0036] <info> System version 6.47. TXmlConnector version 2.26.4\r\n" +
		"184318.671449 [0032] [0036] <info> Initializing...\r\n"
	if dsp != want {
		t.Fatalf("dsp:\n%q\nwant\n%q", dsp, want)
	}
	xdf := readFile(t, filepath.Join(dir, "20261105_xdf.log"))
	if want := "184318.671450 [0032] [0036] <cmd> [V] System version 6.47. TXmlConnector version 2.26.4\r\n"; xdf != want {
		t.Fatalf("xdf:\n%q\nwant\n%q", xdf, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "20261105_ts.log")); !os.IsNotExist(err) {
		t.Fatalf("_ts.log must appear only with a connection: %v", err)
	}
}

func TestOpenRejectsMissingDirectory(t *testing.T) {
	_, err := Open(Options{Dir: filepath.Join(t.TempDir(), "absent"), Level: 1})
	if !errors.Is(err, ErrPathNotExist) || err.Error() != "Указанный путь не существует" {
		t.Fatalf("err = %v", err)
	}
}

func TestCommandsResultsAndCallbacksByLevel(t *testing.T) {
	for _, tc := range []struct {
		level int
		want  []string // xdf lines after the [V] line, without timestamps
	}{
		{1, []string{
			"[0036] <cmd> [I] <command id=\"connect\"><login>LOGINX</login><password>*</password><host>127.0.0.1</host></command>",
			"[0036] <res> [R] <result success=\"true\"/>",
			"[0036] <bad_cmd> [I] <x/>",
			"[0036] <error> [R] <error>Only &amp;lt;command&amp;gt; tags could be passed to SendCommand function</error>",
		}},
		{2, []string{
			"[0036] <cmd> [I] <command id=\"connect\"><login>LOGINX</login><password>*</password><host>127.0.0.1</host></command>",
			"[0036] <res> [R] <result success=\"true\"/>",
			"[0036] <bad_cmd> [I] <x/>",
			"[0036] <error> [R] <error>Only &amp;lt;command&amp;gt; tags could be passed to SendCommand function</error>",
			"[clbk] <info> [O] [403u] <server_status connected=\"error\">Сервер не доступен</server_status>",
		}},
		{3, []string{
			"[0036] <cmd> [I] <command id=\"connect\"><login>LOGINX</login><password>*</password><host>127.0.0.1</host></command>",
			"[0036] <res> [R] <result success=\"true\"/>",
			"[0036] <bad_cmd> [I] <x/>",
			"[0036] <error> [R] <error>Only &amp;lt;command&amp;gt; tags could be passed to SendCommand function</error>",
			"[clbk] <info> [O] [3709u] <connector_version>6.47.2.26.4</connector_version>",
			"[clbk] <info> [O] [403u] <server_status connected=\"error\">Сервер не доступен</server_status>",
		}},
	} {
		dir := t.TempDir()
		l := open(t, dir, tc.level)
		l.Command("", `<command id="connect"><login>LOGINX</login><password>PASSWX</password><host>127.0.0.1</host></command>`, true)
		l.Result("", `<result success="true"/>`, false)
		l.Command("", `<x/>`, false)
		l.Result("", `<error>Only &amp;lt;command&amp;gt; tags could be passed to SendCommand function</error>`, true)
		l.Callback(3709*time.Microsecond, `<connector_version>6.47.2.26.4</connector_version>`)
		l.Callback(403*time.Microsecond+900*time.Nanosecond, `<server_status connected="error">Сервер не доступен</server_status>`)
		l.Close()
		xdf := readFile(t, filepath.Join(dir, "20261105_xdf.log"))
		lines := strings.Split(strings.TrimSuffix(xdf, "\r\n"), "\r\n")[1:]
		var got []string
		for _, line := range lines {
			got = append(got, strings.SplitN(line, " ", 3)[2])
		}
		var want []string
		for _, w := range tc.want {
			want = append(want, cp1251(t, w))
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("level %d:\n%s\nwant\n%s", tc.level, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

// Callback severity (D: FUN_18017ffc0, message type 1..7 -> 3, else 2; O:
// level-2 demo session logged only these four of 17 observed types).
func TestCallbackLevel(t *testing.T) {
	for name, want := range map[string]int{
		"server_status": 2, "client": 2, "orders": 2, "positions": 2, "trades": 2,
		"markets": 3, "boards": 3, "candlekinds": 3, "securities": 3, "pits": 3,
		"sec_info_upd": 3, "sec_info": 3, "overnight": 3, "connector_version": 3,
		"alltrades": 3, "quotations": 3, "quotes": 3, "candles": 3,
	} {
		if got := CallbackLevel("<" + name + " a=\"1\">x</" + name + ">"); got != want {
			t.Errorf("%s: level %d, want %d", name, got, want)
		}
	}
}

func TestSetLevelLines(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, 1)
	l.SetLevel("", 3)
	l.SetLevelFailed("", 5, "Invalid log level. Should be in range [1,3]")
	l.Callback(time.Microsecond, `<connector_version>v</connector_version>`) // level 3 now
	l.Close()
	dsp := readFile(t, filepath.Join(dir, "20261105_dsp.log"))
	for _, want := range []string{
		"[0036] <info> Changing log level. Requested level is 3\r\n",
		"[0036] <info> Log level changed successfully. New level is 3\r\n",
		"[0036] <info> Changing log level. Requested level is 5\r\n",
		"[0036] <error> Log level change failed: Invalid log level. Should be in range [1,3]\r\n",
	} {
		if !strings.Contains(dsp, want) {
			t.Fatalf("dsp lacks %q:\n%s", want, dsp)
		}
	}
	if xdf := readFile(t, filepath.Join(dir, "20261105_xdf.log")); !strings.Contains(xdf, "[O] [1u] <connector_version>") {
		t.Fatalf("callback not logged after SetLevel(3):\n%s", xdf)
	}
}

// The trade-stream log starts every connection with two empty lines and the
// version header (O: CRLF, "dd/mm/yyyy HH:MM:SS"), then "HH:MM:SS.mmm {tid}".
func TestTradeStreamLog(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, 1)
	l.TSStart()
	l.TS("Connecting to 127.0.0.1:1 buf=65512...")
	l.TS("Trade Stream Closed. Reason: 'Connect/Login failed'. S:0/0, R:0/0, E:0, Rl:0")
	l.Close()
	ts := readFile(t, filepath.Join(dir, "20261105_ts.log"))
	want := "\r\n\r\nStart logging 05/11/2026 18:43:18 Transaq TXmlConnector, ver.6.47-T78-37-2.26.4\r\n" +
		"18:43:18.671 {36} Connecting to 127.0.0.1:1 buf=65512...\r\n" +
		"18:43:18.671 {36} Trade Stream Closed. Reason: 'Connect/Login failed'. S:0/0, R:0/0, E:0, Rl:0\r\n"
	if ts != want {
		t.Fatalf("ts:\n%q\nwant\n%q", ts, want)
	}
}

// Before Initialize the DLL prints its records to stdout in the boost console
// format (O: "[2026-11-05 18:51:36.331889] [0x000000f4] [info]    text").
func TestConsoleLine(t *testing.T) {
	var buf bytes.Buffer
	Console(&buf, time.Date(2026, 11, 5, 18, 51, 36, 331889000, time.Local), 0xf4, "Failed to initialize library: Указанный путь не существует")
	want := "[2026-11-05 18:51:36.331889] [0x000000f4] [info]    " + cp1251(t, "Failed to initialize library: Указанный путь не существует") + consoleEOL
	if buf.String() != want {
		t.Fatalf("console:\n%q\nwant\n%q", buf.String(), want)
	}
}

// logfile_lifetime (O): files named YYYYMMDD_{dsp,xdf,ts}.log older than N
// days by their name are removed at open; the default is 7, -1 keeps all,
// other names are never touched.
func TestPruneByNameDate(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.Local)
	names := func(dir string) []string {
		ents, _ := os.ReadDir(dir)
		var out []string
		for _, e := range ents {
			out = append(out, e.Name())
		}
		sort.Strings(out)
		return out
	}
	for _, tc := range []struct {
		lifetime int
		keep     []string
	}{
		{DefaultLifetime, []string{"20261029_dsp.log", "20261104_xdf.log", "20201010_dsp.log.bak", "zz_dsp.log"}},
		{3, []string{"20261104_xdf.log", "20201010_dsp.log.bak", "zz_dsp.log"}},
		{0, []string{"20201010_dsp.log.bak", "zz_dsp.log"}},
		{-1, []string{"20201010_ts.log", "20261028_dsp.log", "20261029_dsp.log", "20261104_xdf.log", "20201010_dsp.log.bak", "zz_dsp.log"}},
	} {
		dir := t.TempDir()
		for _, n := range []string{"20201010_ts.log", "20261028_dsp.log", "20261029_dsp.log", "20261104_xdf.log", "20201010_dsp.log.bak", "zz_dsp.log"} {
			if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		Prune(dir, tc.lifetime, now)
		want := append([]string(nil), tc.keep...)
		sort.Strings(want)
		if got := names(dir); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("lifetime %d: %v, want %v", tc.lifetime, got, want)
		}
	}
}

func TestMaskCommand(t *testing.T) {
	for in, want := range map[string]string{
		`<command id="connect"><login>L</login><password>secret</password></command>`: `<command id="connect"><login>L</login><password>*</password></command>`,
		`<command id="change_pass" oldpass="a1" newpass="b2"/>`:                       `<command id="change_pass" oldpass="*" newpass="*"/>`,
		`<command id="server_status"/>`:                                               `<command id="server_status"/>`,
	} {
		if got := MaskCommand(in); got != want {
			t.Errorf("MaskCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
