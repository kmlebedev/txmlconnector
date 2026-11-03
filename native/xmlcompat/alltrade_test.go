package xmlcompat

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// oracleCatalog holds what the DLL knew in the replayed demo session: TQBR is
// board 37; SBER, GAZP, LKOH, VTBR with their TQBR price decimals; trading day
// 2026-11-03.
func oracleCatalog(t *testing.T) *Catalog {
	c := NewCatalog(loadStartup(t))
	for _, s := range []struct {
		id   uint16
		code string
		dec  byte
	}{{26, "SBER", 2}, {379, "GAZP", 2}, {4, "LKOH", 1}, {889, "VTBR", 3}} {
		c.AddSecurity(SecNew{SecID: s.id, SecCode: s.code, Pits: []Pit{{Board: 37, Decimals: s.dec, Default: true}}})
	}
	var w wire.Writer
	w.Int32(1793664000)
	if _, err := c.Apply(Record{Tag: TagDate, Body: w.Bytes()}); err != nil {
		t.Fatal(err)
	}
	return c
}

// testdata/alltrades-inject*.hex are R_ALLTRADE records built from the
// decoded layout and served to the DLL by a replay of a real demo session;
// alltrades-dll*.xml is the <alltrades> the DLL emitted for them
// (connect with milliseconds=true).
func TestAllTradesMatchDLL(t *testing.T) {
	c := oracleCatalog(t)
	var dedup TradeDedup
	for _, n := range []string{"1", "2", "3"} {
		raw, err := os.ReadFile("testdata/alltrades-inject" + n + ".hex")
		if err != nil {
			t.Fatal(err)
		}
		payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		recs, err := Records(payload)
		if err != nil {
			t.Fatal(err)
		}
		var trades []AllTrade
		for _, r := range recs {
			if r.Tag != TagAllTrade {
				t.Fatalf("unexpected tag %#x", r.Tag)
			}
			tr, err := ParseAllTrade(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if dedup.Fresh(tr) {
				trades = append(trades, tr)
			}
		}
		want, err := os.ReadFile("testdata/alltrades-dll" + n + ".xml")
		if err != nil {
			t.Fatal(err)
		}
		if got := c.AllTradesXML(trades, true); got != strings.TrimSpace(string(want)) {
			t.Errorf("set %s differs from DLL\n got %s\nwant %s", n, got, want)
		}
	}
}

func TestParseAllTradeOptionalFields(t *testing.T) {
	raw, _ := os.ReadFile("testdata/alltrades-inject1.hex")
	payload, _ := hex.DecodeString(strings.TrimSpace(string(raw)))
	recs, err := Records(payload)
	if err != nil {
		t.Fatal(err)
	}
	gazp, err := ParseAllTrade(recs[2].Body)
	if err != nil {
		t.Fatal(err)
	}
	if gazp.SecID != 379 || gazp.Field4 != 555555 || gazp.Text != "REF12" || gazp.OpenInt != 777 ||
		gazp.Currency != "RUB" || gazp.Millis != 250 || gazp.Price != 12345 {
		t.Fatalf("GAZP = %+v", gazp)
	}
	lkoh, _ := ParseAllTrade(recs[3].Body)
	if lkoh.Block20 != [4]int64{11, 22, 33, 44} {
		t.Fatalf("LKOH = %+v", lkoh)
	}
}

func TestFormatPrice(t *testing.T) {
	for _, c := range []struct {
		units int64
		dec   int
		want  string
	}{{30512, 2, "305.12"}, {30500, 2, "305.0"}, {98, 3, "0.098"}, {-30512, 2, "-305.12"}, {7, 0, "7"}, {5, 4, "0.0005"}} {
		if got := FormatPrice(c.units, c.dec); got != c.want {
			t.Errorf("FormatPrice(%d, %d) = %s, want %s", c.units, c.dec, got, c.want)
		}
	}
}

// After subscribing, RQ_MAIN replies interleave security attribute updates
// (R_SEC_RATE, 0x0a) with sized records; the splitter must not stop at them.
func TestRecordsAttributeUpdates(t *testing.T) {
	var w wire.Writer
	w.Byte(TagSecRate)
	w.Uint16(0x1e16)
	w.Int32(5380)
	w.Raw([]byte{0x0d, 0x00, 0x1a, 0x01})
	w.Raw([]byte{TagAllTrade, 0, 0})
	recs, err := Records(w.Bytes())
	if err != nil || len(recs) != 3 || recs[0].Tag != TagSecRate || recs[1].Tag != 0x0d || recs[2].Tag != TagAllTrade {
		t.Fatalf("records = %+v, %v", recs, err)
	}
}
