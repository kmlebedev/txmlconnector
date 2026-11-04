// Package oracle is the shared helper of the DLL-oracle tests: it loads the
// binary fixtures, decodes RQ_MAIN records into model events and compares the
// xmlcompat rendering with the XML the vendor DLL emitted for the same bytes.
//
// It is a non-test package so both the native packages and the external test
// package of native/xmlcompat can use it (a _test.go helper would not be
// importable); see docs/deepseek/task-index.md §8.3 for the deviation from the
// records.LoadFixture location named by the plan.
package oracle

import (
	"compress/gzip"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// fixturesDir is native/records/testdata, resolved from this file so tests of
// every package find it regardless of their working directory: go test runs
// each package with its own cwd.
var fixturesDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "records", "testdata")
}()

// LoadFixture reads a hex fixture (plain .hex or gzipped .hex.gz) from
// native/records/testdata and returns the decoded payload bytes.
func LoadFixture(t testing.TB, name string) []byte {
	t.Helper()
	raw := readFixture(t, name)
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("oracle: %s: %v", name, err)
	}
	return b
}

// LoadTextFixture reads a plain-text fixture (for example a DLL XML callback)
// from native/records/testdata, decompressing a .gz suffix.
func LoadTextFixture(t testing.TB, name string) string {
	t.Helper()
	return string(readFixture(t, name))
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join(fixturesDir, name))
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatalf("oracle: %s: %v", name, err)
		}
		defer zr.Close()
		r = zr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("oracle: %s: %v", name, err)
	}
	return b
}

// LoadCatalog builds a session catalog from the RQ_STARTUP fixture and applies
// the catalog records (R_SEC_NEW, R_DATE, attribute updates) of catalogFixture.
func LoadCatalog(t testing.TB, catalogFixture string) *records.Catalog {
	t.Helper()
	startup, err := records.ParseStartup(LoadFixture(t, "startup-demo.hex"))
	if err != nil {
		t.Fatalf("oracle: RQ_STARTUP: %v", err)
	}
	c := records.NewCatalog(startup)
	for _, rec := range recordsOf(t, catalogFixture) {
		if _, err := c.ApplyMutations(rec); err != nil {
			t.Fatalf("oracle: %s: %v", catalogFixture, err)
		}
	}
	return c
}

// recordsOf splits a fixture RQ_MAIN payload; a partial split (unknown record
// layout) is a failure for the oracle fixtures.
func recordsOf(t testing.TB, name string) []records.Record {
	t.Helper()
	recs, err := records.Records(LoadFixture(t, name))
	if err != nil {
		t.Fatalf("oracle: %s: %v", name, err)
	}
	return recs
}

// CheckDLL decodes injectFixture into events, renders them with
// xmlcompat.Render and compares the merged messages with the DLL XML callback
// byte for byte. The catalog comes from catalogFixture (the recorded snapshot);
// extra securities are added afterwards because the DLL session knew securities
// that one recorded snapshot chunk may not contain (for example VTBR 889, see
// TestRenderAllTradesThroughRender).
func CheckDLL(t testing.TB, catalogFixture, injectFixture, dllXML string, extra ...records.SecNew) {
	t.Helper()
	c := LoadCatalog(t, catalogFixture)
	for _, s := range extra {
		c.AddSecurity(s)
	}
	var messages []string
	for _, rec := range recordsOf(t, injectFixture) {
		if rec.Tag != records.TagAllTrade {
			if _, err := c.ApplyMutations(rec); err != nil {
				t.Fatalf("oracle: %s: %v", injectFixture, err)
			}
			continue
		}
		raw, err := records.ParseAllTrade(rec.Body)
		if err != nil {
			t.Fatalf("oracle: %s: %v", injectFixture, err)
		}
		trade := c.Trade(raw, 0)
		// The recorded oracle runs were made with <milliseconds>true</milliseconds>,
		// so <time> carries the .fff suffix in the DLL fixtures.
		msg, ok := xmlcompat.Render(c, &trade, xmlcompat.Options{Milliseconds: true})
		if !ok {
			t.Fatalf("oracle: %s: no XML form for the decoded R_ALLTRADE record", injectFixture)
		}
		messages = append(messages, msg)
	}
	got, want := Merge(messages), strings.TrimSpace(LoadTextFixture(t, dllXML))
	if got != want {
		t.Fatalf("oracle: %s rendering differs from the DLL %s\n got %s\nwant %s", injectFixture, dllXML, got, want)
	}
}

// Merge batches adjacent messages with the same root tag: the DLL collects all
// records of one RQ_MAIN reply into one callback per message type, so N
// single-trade <alltrades> messages become the one batched <alltrades> the
// alltrades-dll*.xml fixture holds.
func Merge(messages []string) string {
	var (
		out                        strings.Builder
		curOpen, curRoot, curInner string
	)
	flush := func() {
		if curOpen == "" {
			return
		}
		out.WriteString(curOpen + curInner + "</" + curRoot + ">")
		curOpen, curRoot, curInner = "", "", ""
	}
	for _, msg := range messages {
		open, root, inner, ok := messageParts(msg)
		if !ok {
			flush()
			out.WriteString(msg)
			continue
		}
		if curOpen == open && curRoot == root {
			curInner += inner
			continue
		}
		flush()
		curOpen, curRoot, curInner = open, root, inner
	}
	flush()
	return out.String()
}

// messageParts splits a rendered message into its opening tag, root tag and
// inner XML. ok is false for a self-closing or malformed message; Merge passes
// those through unchanged.
func messageParts(msg string) (open, root, inner string, ok bool) {
	if !strings.HasPrefix(msg, "<") {
		return "", "", "", false
	}
	end := strings.IndexByte(msg, '>')
	if end < 0 || strings.HasSuffix(msg[:end+1], "/>") {
		return "", "", "", false
	}
	open = msg[:end+1]
	name := strings.TrimLeft(open, "<")
	if i := strings.IndexAny(name, " >"); i >= 0 {
		name = name[:i]
	}
	root = name
	closeTag := "</" + root + ">"
	if root == "" || !strings.HasSuffix(msg, closeTag) {
		return "", "", "", false
	}
	return open, root, msg[len(open) : len(msg)-len(closeTag)], true
}
