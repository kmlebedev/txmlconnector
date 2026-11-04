package records

import (
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// oracleCatalog holds what the DLL knew in the replayed demo session: TQBR is
// board 37; SBER, GAZP, LKOH, VTBR with their TQBR price decimals; trading day
// 2026-11-03.
func oracleCatalog(t *testing.T) *Catalog {
	t.Helper()
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

func TestParseAllTradeOptionalFields(t *testing.T) {
	recs, err := Records(readHex(t, "alltrades-inject1.hex"))
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

// TestCatalogTrade checks the typed conversion against the values the DLL and
// the native client tests expect: symbols and decimals from the catalog, the
// UTC instant from the -3h server bias, the wall clock for <time>.
func TestCatalogTrade(t *testing.T) {
	c := oracleCatalog(t)
	recs, err := Records(readHex(t, "alltrades-inject1.hex"))
	if err != nil {
		t.Fatal(err)
	}
	var dedup TradeDedup
	var trades []model.Trade
	for _, r := range recs {
		raw, err := ParseAllTrade(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if dedup.Fresh(raw) {
			trades = append(trades, c.Trade(raw, -3*time.Hour))
		}
	}
	if len(trades) != 5 {
		t.Fatalf("converted %d trades, want 5", len(trades))
	}
	sber := trades[0]
	if sber.SecID != 26 || sber.SecCode != "SBER" || sber.Board != "TQBR" ||
		sber.TradeNo != 9000000001 || sber.Price != (model.Decimal{Units: 30512, Scale: 2}) ||
		sber.Quantity != 3 || sber.Period != 'B' || sber.BuySell != 0 || sber.HasOpenInterest {
		t.Fatalf("SBER = %+v", sber)
	}
	wall := time.Date(2026, 11, 3, 23, 53, 20, 0, time.UTC).Unix()
	if sber.WallTime != wall {
		t.Fatalf("SBER wall time = %d, want %d", sber.WallTime, wall)
	}
	if !sber.Time.Equal(time.Date(2026, 11, 3, 20, 53, 20, 0, time.UTC)) {
		t.Fatalf("SBER time = %v, want 2026-11-03 20:53:20 UTC", sber.Time)
	}
	// The decoded record stays available to consumers as Trade.Raw.
	if sber.Raw.TradeNo != 9000000001 || sber.Raw.Price != 30512 || sber.Raw.SecID != 26 ||
		sber.Raw.Flags&model.AllTradeOpenInterest != 0 {
		t.Fatalf("SBER raw = %+v", sber.Raw)
	}
	gazp := trades[2]
	if gazp.SecCode != "GAZP" || !gazp.HasOpenInterest || gazp.OpenInterest != 777 ||
		gazp.Millis != 250 || !gazp.Time.Equal(time.Date(2026, 11, 3, 20, 53, 22, 250e6, time.UTC)) {
		t.Fatalf("GAZP = %+v", gazp)
	}
}

// TestTradeDedupSymbolSurvivesSecidChange covers the reconnect replay: the new
// session assigns a different session-local secid to the same security, so the
// stable model.Symbol key must still drop the trade delivered before the
// reconnect (Review Focus 3).
func TestTradeDedupSymbolSurvivesSecidChange(t *testing.T) {
	var d TradeDedup
	sym := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	if !d.FreshSymbol(model.AllTrade{SecID: 26, Board: 37, TradeNo: 9000000001}, sym) {
		t.Fatal("the first trade must be fresh")
	}
	if d.FreshSymbol(model.AllTrade{SecID: 27, Board: 37, TradeNo: 9000000001}, sym) {
		t.Fatal("a replay with the same trade number and a new secid must be dropped")
	}
	if !d.FreshSymbol(model.AllTrade{SecID: 27, Board: 37, TradeNo: 9000000002}, sym) {
		t.Fatal("the next trade must be fresh")
	}
}

// TestTradeDedupRawFallback covers securities the catalog does not know: the
// fallback keeps the session-local (secid, board) dedup of the DLL.
func TestTradeDedupRawFallback(t *testing.T) {
	var d TradeDedup
	if !d.Fresh(model.AllTrade{SecID: 889, Board: 37, TradeNo: 1}) {
		t.Fatal("the first trade must be fresh")
	}
	if d.Fresh(model.AllTrade{SecID: 889, Board: 37, TradeNo: 1}) {
		t.Fatal("the same raw trade must be dropped")
	}
}
