package records

import (
	"compress/gzip"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
)

// readGz reads a gzipped reference file from testdata.
func readGz(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// mustHexGz reads a gzipped hex fixture and returns the raw bytes.
func mustHexGz(t *testing.T, name string) []byte {
	t.Helper()
	p, err := hex.DecodeString(strings.TrimSpace(string(readGz(t, name))))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// readHex reads a plain hex fixture.
func readHex(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestParseSecNewSnapshot decodes all R_SEC_NEW records of the first RQ_MAIN
// reply of the demo session: 499 instruments, fully consumed bodies (no Extra
// bytes left), and checks one share against the values the DLL reported.
func TestParseSecNewSnapshot(t *testing.T) {
	recs, err := Records(mustHexGz(t, "main1-demo.hex.gz"))
	if err != nil {
		t.Fatal(err)
	}
	var got []SecNew
	for _, r := range recs {
		if r.Tag != TagSecNew {
			continue
		}
		s, err := ParseSecNew(r.Body)
		if err != nil {
			t.Fatalf("record %d: %v", len(got), err)
		}
		if len(s.Extra) != 0 {
			t.Fatalf("secid %d: %d undecoded bytes", s.SecID, len(s.Extra))
		}
		got = append(got, s)
	}
	if len(got) != 499 {
		t.Fatalf("decoded %d securities, want 499", len(got))
	}
	var sber SecNew
	for _, s := range got {
		if s.SecCode == "SBER" {
			sber = s
		}
	}
	if sber.SecID != 26 || sber.Market != 1 || sber.Kind != 0 || len(sber.Pits) == 0 {
		t.Fatalf("SBER = %+v", sber)
	}
	pit, ok := sber.DefaultPit()
	if !ok || pit.Board != 37 || pit.Decimals != 2 {
		t.Fatalf("SBER default pit = %+v, %v", pit, ok)
	}
}
