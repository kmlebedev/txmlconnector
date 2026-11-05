package session

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var errTableRow = regexp.MustCompile(`^\| (\d+) \| (.*?) \|`)

// TestInternalErrorTable reads the DLL internal table (codes 0..66) from
// docs/protocol/errors.md and pins every RU row, not just a couple of samples.
func TestInternalErrorTable(t *testing.T) {
	f, err := os.Open("../../docs/protocol/errors.md")
	if err != nil {
		t.Skipf("errors.md not available: %v", err)
	}
	defer f.Close()
	want := map[int]string{}
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "## Внутренняя таблица кодов DLL"):
			in = true
			continue
		case in && strings.HasPrefix(line, "## "):
			in = false
		}
		if !in {
			continue
		}
		m := errTableRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		code, err := strconv.Atoi(m[1])
		if err != nil || code < 0 || code > 66 {
			continue
		}
		want[code] = m[2]
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(want) != 67 {
		t.Fatalf("docs internal table = %d codes, want 67", len(want))
	}
	for code := 0; code <= 66; code++ {
		got, ok := InternalErrorText(code)
		if !ok || got != want[code] {
			t.Errorf("code %d = %q, want %q", code, got, want[code])
		}
		if formatted, ok := FormatInternalError(code); !ok || formatted != "{"+strconv.Itoa(code)+"} "+want[code] {
			t.Errorf("format %d = %q, %v", code, formatted, ok)
		}
	}
	if _, ok := InternalErrorText(67); ok {
		t.Fatal("code 67 accepted")
	}
	if _, ok := FormatInternalError(999); ok {
		t.Fatal("unknown code formatted")
	}
}
