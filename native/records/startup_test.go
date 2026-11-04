package records

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// testdata/startup-demo.hex is the RQ_STARTUP reply of the Finam demo server;
// the *-demo.xml files are the callbacks the DLL produced from the same
// session (reference data only, no account fields).
func loadStartup(t *testing.T) *model.Startup {
	t.Helper()
	raw, err := os.ReadFile("testdata/startup-demo.hex")
	if err != nil {
		t.Fatal(err)
	}
	p, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseStartup(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStartupSections(t *testing.T) {
	s := loadStartup(t)
	if len(s.Markets) != 9 || len(s.BoardTypes) != 5 || len(s.Boards) != 60 ||
		len(s.SecTypes) != 15 || len(s.CandleKinds) != 6 || len(s.Columns) != 64 {
		t.Fatalf("sections: markets %d boardtypes %d boards %d sectypes %d candlekinds %d columns %d",
			len(s.Markets), len(s.BoardTypes), len(s.Boards), len(s.SecTypes), len(s.CandleKinds), len(s.Columns))
	}
	if m := s.Markets[0]; m.ID != 15 || m.Code != "ETS" || m.Name != "Валютный рынок МБ" {
		t.Fatalf("first market = %+v", m)
	}
	if b := s.Boards[36]; b.Code != "TQBR" || b.Market != 1 || b.Type != 1 {
		t.Fatalf("TQBR = %+v", b)
	}
	if b, ok := s.BoardByNum(37); !ok || b.Code != "TQBR" {
		t.Fatalf("BoardByNum(37) = %+v, %v", b, ok)
	}
}

func TestParseStartupRejectsTruncated(t *testing.T) {
	if _, err := ParseStartup([]byte{15, 'E', 0}); err == nil {
		t.Fatal("expected error")
	}
}
