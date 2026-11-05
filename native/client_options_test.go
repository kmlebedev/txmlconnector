package native

import (
	"bytes"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

func optionFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	out, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// TestConnectorOptionFamilies: get_option_families sends RQ_OPTION_FAMILIES 149
// with the O payload and maps the empty/text reply (O).
func TestConnectorOptionFamilies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reply   []byte
		success bool
		message string
	}{
		{"ok", optionFixture(t, "option-families-reply-ok.hex"), true, ""},
		{"error", optionFixture(t, "option-families-reply-error.hex"), false, "No families"},
		// S (demo 2026-11-05): the server has no options service; CP1251 text.
		{"no service", optionFixture(t, "option-families-reply-231-live.hex"), false, "[231]Информационный сервис опционов недоступен"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRunServer(t)
			srv.Snapshot = clientSnapshot(t)
			var captured []byte
			srv.Handle(session.RqOptionFamilies, func(payload []byte) (byte, []byte) {
				captured = append([]byte(nil), payload...)
				return wire.KindReply | wire.FlagPlain, tc.reply
			})
			c := New(Config{Dial: srv.Dial})
			rec := newRecorder()
			if err := c.Start(rec.handle); err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			send(t, c, connectCommand())
			rec.next(t, "<server_status")
			cmd := "<command id=" + quote + "get_option_families" + quote + "><security><board>TQBR</board><seccode>SBER</seccode></security></command>"
			got := send(t, c, cmd)
			if tc.success {
				if got != "<result success="+quote+"true"+quote+"/>" {
					t.Fatalf("get_option_families = %s", got)
				}
			} else if want := "<result success=" + quote + "false" + quote + "><message>" + tc.message + "</message></result>"; got != want {
				t.Fatalf("get_option_families = %s, want %s", got, want)
			}
			if hex.EncodeToString(captured) != "001a" {
				t.Fatalf("149 payload = %x, want 001a", captured)
			}
		})
	}
}

// TestConnectorGetOptions: get_options sends the Z25 code list (151) and maps
// the reply; an empty list is the DLL local error.
func TestConnectorGetOptions(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Protocol = session.OptionsMinProtocol
	var captured []byte
	srv.Handle(session.RqCertainOptions, func(payload []byte) (byte, []byte) {
		captured = append([]byte(nil), payload...)
		return wire.KindReply | wire.FlagPlain, optionFixture(t, "option-families-reply-ok.hex")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "get_options" + quote + "><opt_code>OPT1</opt_code><opt_code>OPT2</opt_code></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_options = %s", got)
	}
	var w wire.Writer
	w.String("OPT1", 25)
	w.String("OPT2", 25)
	if hex.EncodeToString(captured) != hex.EncodeToString(w.Bytes()) {
		t.Fatalf("151 payload = %x, want %x", captured, w.Bytes())
	}
	if got := send(t, c, "<command id="+quote+"get_options"+quote+"/>"); !strings.Contains(got, "No seccodes found") {
		t.Fatalf("empty get_options = %s", got)
	}
	srv.Handle(session.RqCertainOptions, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, optionFixture(t, "option-families-reply-error.hex")
	})
	out := "<command id=" + quote + "get_options" + quote + "><opt_code>OPT1</opt_code></command>"
	if got := send(t, c, out); got != "<result success="+quote+"false"+quote+"><message>No families</message></result>" {
		t.Fatalf("get_options error = %s", got)
	}
}

// TestConnectorFamilyStrikesErrors: mat_date local errors (D+O) and the honest
// I-error for the not-recovered family mapping.
func TestConnectorFamilyStrikesErrors(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"no mat_date", "<command id=" + quote + "get_family_strikes" + quote + "/>", "No mat_date found"},
		{"bad mat_date", "<command id=" + quote + "get_family_strikes" + quote + "><mat_date>2026-02-01</mat_date></command>", "Failed to parse mat_date=2026-02-01"},
		{"valid mat_date below the version gate", "<command id=" + quote + "get_family_strikes" + quote + "><security><board>TQBR</board><seccode>SBER</seccode></security><mat_date>01.02.2026</mat_date></command>", "<message>Unsupported request.</message>"},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want %q", tc.name, got, tc.want)
		}
	}
}

// TestConnectorOptionsVersionGate: get_family_strikes (153) and get_options
// (151) are refused locally while the server protocol (PRO# of RQ_PWD, DLL
// +0x694 via FUN_180257680) is below 0x45 (D: FUN_1803b9950/FUN_1803b9cd0;
// S demo PRO#=68: only 149 reached the server, the DLL answered as below).
func TestConnectorOptionsVersionGate(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Protocol = 68
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	strikes := "<command id=" + quote + "get_family_strikes" + quote + "><security><board>TQBR</board><seccode>SBER</seccode></security><mat_date>17.12.2026</mat_date></command>"
	if got, want := send(t, c, strikes), "<result success="+quote+"false"+quote+"><message>Unsupported request.</message></result>"; got != want {
		t.Fatalf("get_family_strikes = %s, want %s", got, want)
	}
	options := "<command id=" + quote + "get_options" + quote + "><opt_code>Si76000BL6</opt_code></command>"
	if got, want := send(t, c, options), "<result success="+quote+"false"+quote+"/>"; got != want {
		t.Fatalf("get_options = %s, want %s", got, want)
	}
	if n := countRequests(srv, session.RqCertainOptions) + countRequests(srv, session.RqFamilyStrikes); n != 0 {
		t.Fatalf("%d gated requests reached the server", n)
	}
}

// TestConnectorOptionLiveForms replays the production option records (S,
// 2026-11-05: R_FAMILY x5 + R_END_OF_FAMILIES, then one R_STRIKES with 156
// strikes) and compares the callbacks with the DLL's XML byte for byte. The
// production secid of SiZ6 (0x395c) is rewritten to a security of the test
// catalog (the form does not depend on the security type), and the expected XML follows it.
func TestConnectorOptionLiveForms(t *testing.T) {
	srv := newRunServer(t)
	srv.Protocol = 78 // production PRO#
	var strikesReq []byte
	srv.Handle(session.RqOptionFamilies, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, []byte{0}
	})
	srv.Handle(session.RqFamilyStrikes, func(payload []byte) (byte, []byte) {
		strikesReq = append([]byte(nil), payload...)
		return wire.KindReply | wire.FlagPlain, []byte{0}
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")

	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	sym := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	sec, _, ok := client.Catalog().Lookup(sym.Board, sym.SecCode)
	if !ok {
		t.Fatal("SBER is not in the test catalog")
	}
	secID := sec.SecID
	patch := func(p []byte) []byte {
		out := append([]byte(nil), p...)
		for i := 0; i+2 < len(out); i++ {
			if (out[i] == records.TagFamily || out[i] == records.TagEndOfFamilies || out[i] == records.TagStrikes) &&
				out[i+1] == 0x39 && out[i+2] == 0x5c {
				out[i+1], out[i+2] = byte(secID>>8), byte(secID)
			}
		}
		return out
	}
	expected := func(name string) string {
		raw, err := os.ReadFile("records/testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Replace(strings.TrimSpace(string(raw)),
			`seccode="SiZ6" board="FUT" secid="14684"`,
			`seccode="`+sym.SecCode+`" board="`+sym.Board+`" secid="`+strconv.Itoa(int(secID))+`"`, 1)
	}
	security := "<security><board>" + sym.Board + "</board><seccode>" + sym.SecCode + "</seccode></security>"

	if got := send(t, c, "<command id="+quote+"get_option_families"+quote+">"+security+"</command>"); !strings.Contains(got, "true") {
		t.Fatalf("get_option_families = %s", got)
	}
	srv.QueueMain(patch(optionFixture(t, "option-families-live.hex")[:44])) // 5x 0x6f + 0x70
	if got, want := rec.next(t, "<option_families"), expected("option-families-dll.xml"); got != want {
		t.Fatalf("option_families\n got %s\nwant %s", got, want)
	}

	if got := send(t, c, "<command id="+quote+"get_family_strikes"+quote+">"+security+"<mat_date>17.12.2026</mat_date></command>"); !strings.Contains(got, "true") {
		t.Fatalf("get_family_strikes = %s", got)
	}
	if want := []byte{byte(secID >> 8), byte(secID), 0x42, 0x14, 0x05}; !bytes.Equal(strikesReq, want) {
		t.Fatalf("153 payload = %x, want %x (S: 395c421405)", strikesReq, want)
	}
	srv.QueueMain(patch(optionFixture(t, "option-strikes-live.hex")[33:])) // the R_STRIKES record
	if got, want := rec.next(t, "<family_strikes"), expected("family-strikes-dll.xml"); got != want {
		t.Fatalf("family_strikes\n got %.300s\nwant %.300s", got, want)
	}
}
