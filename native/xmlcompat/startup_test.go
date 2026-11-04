package xmlcompat

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// testdata lives in native/records/testdata: it holds both the binary
// fixtures and the DLL callbacks produced from the same demo session
// (reference data only, no account fields).
func loadStartup(t *testing.T) *model.Startup {
	t.Helper()
	raw, err := os.ReadFile("../records/testdata/startup-demo.hex")
	if err != nil {
		t.Fatal(err)
	}
	p, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := records.ParseStartup(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func dllXML(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../records/testdata/" + name + "-demo.xml")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(b), "\n")
}

func TestStartupMatchesDLLCallbacks(t *testing.T) {
	s := loadStartup(t)
	cases := []struct {
		name string
		v    any
	}{
		{"markets", MarketsXML(s)},
		{"boards", BoardsXML(s)},
		{"candlekinds", CandleKindsXML(s)},
	}
	for _, c := range cases {
		got, err := Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if want := dllXML(t, c.name); got != want {
			t.Errorf("%s differs from DLL\n got %.300s\nwant %.300s", c.name, got, want)
		}
	}
}
