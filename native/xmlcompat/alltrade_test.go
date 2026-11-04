package xmlcompat

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// oracleCatalog holds what the DLL knew in the replayed demo session: TQBR is
// board 37; SBER, GAZP, LKOH, VTBR with their TQBR price decimals; trading day
// 2026-11-03.
func oracleCatalog(t *testing.T) *records.Catalog {
	t.Helper()
	c := records.NewCatalog(loadStartup(t))
	for _, s := range []struct {
		id   uint16
		code string
		dec  byte
	}{{26, "SBER", 2}, {379, "GAZP", 2}, {4, "LKOH", 1}, {889, "VTBR", 3}} {
		c.AddSecurity(records.SecNew{SecID: s.id, SecCode: s.code, Pits: []records.Pit{{Board: 37, Decimals: s.dec, Default: true}}})
	}
	var w wire.Writer
	w.Int32(1793664000)
	if _, err := c.Apply(records.Record{Tag: records.TagDate, Body: w.Bytes()}); err != nil {
		t.Fatal(err)
	}
	return c
}

func hexFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func textFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// testdata/alltrades-inject*.hex are R_ALLTRADE records built from the
// decoded layout and served to the DLL by a replay of a real demo session;
// alltrades-dll*.xml is the <alltrades> the DLL emitted for them
// (connect with milliseconds=true).
func TestAllTradesMatchDLL(t *testing.T) {
	c := oracleCatalog(t)
	var dedup records.TradeDedup
	for _, n := range []string{"1", "2", "3"} {
		recs, err := records.Records(hexFixture(t, "alltrades-inject"+n+".hex"))
		if err != nil {
			t.Fatal(err)
		}
		var trades []model.Trade
		for _, r := range recs {
			if r.Tag != records.TagAllTrade {
				t.Fatalf("unexpected tag %#x", r.Tag)
			}
			tr, err := records.ParseAllTrade(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if dedup.Fresh(tr) {
				trades = append(trades, c.Trade(tr, 0))
			}
		}
		want := strings.TrimSpace(textFixture(t, "alltrades-dll"+n+".xml"))
		if got := AllTradesXML(trades, true); got != want {
			t.Errorf("set %s differs from DLL\n got %s\nwant %s", n, got, want)
		}
	}
}
