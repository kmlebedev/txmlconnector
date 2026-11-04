package xmlcompat_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// TestRenderAllTradesThroughRender checks the single Render entry point against
// the DLL <alltrades> callback: all R_ALLTRADE records of one RQ_MAIN reply
// become one batched message (testdata/alltrades-dll1.xml, milliseconds=true).
//
// VTBR 889 is not in the 499-instrument main1-demo snapshot, but the DLL knew
// it in the recorded session (the fixture prints seccode VTBR and price 0.098,
// i.e. decimals 3), so the test seeds it explicitly - the knowledge the old
// oracleCatalog helper held.
func TestRenderAllTradesThroughRender(t *testing.T) {
	oracle.CheckDLL(t, "main1-demo.hex.gz", "alltrades-inject1.hex", "alltrades-dll1.xml",
		records.SecNew{SecID: 889, SecCode: "VTBR", Pits: []records.Pit{{Board: 37, Decimals: 3, Default: true}}})
}

// TestRenderAllTradesUTCTime checks the utc_time XML option: <time> carries the
// UTC instant instead of the server wall clock (RE-1: an XML-only connect
// option).
func TestRenderAllTradesUTCTime(t *testing.T) {
	catalog := records.NewCatalog(&model.Startup{})
	trade := model.Trade{
		SecID: 26, Board: "TQBR", SecCode: "SBER", TradeNo: 1,
		Time:     time.Date(2026, 11, 3, 20, 53, 20, 0, time.UTC),
		WallTime: time.Date(2026, 11, 3, 23, 53, 20, 0, time.UTC).Unix(),
		Millis:   1000, Price: model.Decimal{Units: 30512, Scale: 2}, Quantity: 3,
		BuySell: 'B', Period: 'B',
	}
	got, ok := xmlcompat.Render(catalog, &trade, xmlcompat.Options{Milliseconds: true, UTCTime: true})
	if !ok {
		t.Fatal("Render does not accept a trade")
	}
	if !strings.Contains(got, "<time>03.11.2026 20:53:20.000</time>") {
		t.Fatalf("utc_time rendering = %s", got)
	}
	got, ok = xmlcompat.Render(catalog, &trade, xmlcompat.Options{Milliseconds: true})
	if !ok || !strings.Contains(got, "<time>03.11.2026 23:53:20.000</time>") {
		t.Fatalf("default rendering = %s", got)
	}
}
