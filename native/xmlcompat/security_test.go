package xmlcompat

import (
	"compress/gzip"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
)

func readGz(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("../records/testdata/" + name)
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

func mustHexGz(t *testing.T, name string) []byte {
	t.Helper()
	p, err := hex.DecodeString(strings.TrimSpace(string(readGz(t, name))))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFormatting(t *testing.T) {
	cases := []struct{ got, want string }{
		{FormatMinStep(0.01, 2), "0.01"},
		{FormatMinStep(0, 2), "0.0"},
		{FormatMinStep(1, 2), "1.0"},
		{FormatMinStep(0.000002, 6), "0.000002"},
		{FormatMinStep(5, 0), "5"},
		{FormatPointCost(1.0000000000000002), "1"},
		{FormatPointCost(161.1146), "161.115"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
