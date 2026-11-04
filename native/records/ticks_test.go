package records_test

import (
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestTicksReplyParsed pins the D-format parser of the RQ_TICKS reply (the live
// format is unconfirmed: S-negative, oracle blocked, so this synthetic body is
// marked I in the plan): W secid, W board, G tradeno, L time, L price, B field,
// L volume until secid 0xffff. The time/price deltas are accumulated as the DLL
// does (I).
func TestTicksReplyParsed(t *testing.T) {
	var w wire.Writer
	w.Uint16(26)      // SBER
	w.Uint16(37)      // TQBR
	w.Int64(0)        // tradeno
	w.Int32(29896980) // time (synthetic; I)
	w.Int32(291420)   // price raw (scale 2 from the catalog)
	w.Byte(1)         // period/buysell pack byte (I)
	w.Int32(10)       // volume
	w.Uint16(26)
	w.Uint16(37)
	w.Int64(1)  // tradeno delta (accumulated, I)
	w.Int32(1)  // time delta
	w.Int32(10) // price delta
	w.Byte(1)
	w.Int32(20)
	w.Uint16(0xffff)
	payload := w.Bytes()
	ticks, err := records.ParseTicksReply(payload)
	if err != nil {
		t.Fatalf("ParseTicksReply: %v", err)
	}
	if len(ticks) != 2 {
		t.Fatalf("ticks = %d, want 2", len(ticks))
	}
	first, second := ticks[0], ticks[1]
	if first.SecID != 26 || first.Board != 37 {
		t.Fatalf("first identity = %+v", first)
	}
	if first.TradeNo != 0 {
		t.Fatalf("first tradeno = %d", first.TradeNo)
	}
	if second.TradeNo != 1 {
		t.Fatalf("second tradeno = %d (accumulated, I)", second.TradeNo)
	}
	if first.Time != 29896980 || second.Time != 29896981 {
		t.Fatalf("times = %d/%d", first.Time, second.Time)
	}
	if first.Price != 291420 || second.Price != 291430 {
		t.Fatalf("prices = %d/%d (accumulated, I)", first.Price, second.Price)
	}
	if first.Volume != 10 || second.Volume != 20 {
		t.Fatalf("volumes = %d/%d", first.Volume, second.Volume)
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	ev := ticks[0].Event(catalog)
	if ev.Symbol != (model.Symbol{Board: "TQBR", SecCode: "SBER"}) {
		t.Fatalf("symbol = %+v", ev.Symbol)
	}
	if ev.Price.Units != 291420 || ev.Price.Scale != 2 {
		t.Fatalf("event price = %+v", ev.Price)
	}
	if !ev.Time.Equal(time.Unix(29896980, 0).UTC()) {
		t.Fatalf("event time = %v", ev.Time)
	}
}
