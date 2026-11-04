package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// ParseAllTrade decodes an R_ALLTRADE body.
func ParseAllTrade(body []byte) (model.AllTrade, error) {
	r := &reader{Reader: wire.NewReader(body)}
	t := model.AllTrade{SecID: r.word(), Board: r.word(), TradeNo: r.int64(), Time: int64(r.int32()),
		Price: int64(r.int32()), Quantity: int64(r.int32()), Flags: r.word(), Millis: 1000}
	f := t.Flags
	if f&model.AllTradePeriod != 0 {
		t.Period = r.byte()
	}
	if f&model.AllTradeField2 != 0 {
		t.Field2 = r.int32()
	}
	if f&model.AllTradeField4 != 0 {
		t.Field4 = r.int64()
	}
	if f&model.AllTradeField2 != 0 {
		t.Field2Long = r.int64()
	}
	if f&model.AllTradeBuySell != 0 {
		t.BuySell = r.byte()
	}
	if f&model.AllTradeText != 0 {
		t.Text = r.str(0xc)
	}
	if f&model.AllTradeBlock20 != 0 {
		t.Block20 = [4]int64{int64(r.int32()), int64(r.int32()), r.int64(), r.int64()}
	}
	if f&model.AllTradeOpenInterest != 0 {
		t.OpenInt = int64(r.int32())
	}
	if f&model.AllTradeCurrency != 0 {
		t.Currency = r.str(4)
	}
	if f&model.AllTradeMillis != 0 {
		t.Millis = r.word()
	}
	if r.err != nil {
		return t, fmt.Errorf("R_ALLTRADE: %w", r.err)
	}
	return t, nil
}

// TradeDedup drops trades whose number is not above the last one seen for
// the same security, as the DLL does ("old ALLTRADE No" in its log). It keeps
// replays after a reconnect out of the feed.
//
// The caller that knows the stable symbol (board code + seccode, which survives
// a reconnect that changes the session-local secid) passes it to FreshSymbol;
// Fresh is the fallback keyed by the raw (secid, board) pair for securities the
// catalog does not know.
type TradeDedup struct {
	last    map[model.Symbol]int64
	lastRaw map[[2]uint16]int64
}

// FreshSymbol reports whether t is new for sym and records it. The symbol is
// stable across sessions, so a trade replayed by the new session after a
// reconnect (same trade number, new secid) is dropped.
func (d *TradeDedup) FreshSymbol(t model.AllTrade, sym model.Symbol) bool {
	if d.last == nil {
		d.last = map[model.Symbol]int64{}
	}
	if last, ok := d.last[sym]; ok && t.TradeNo <= last {
		return false
	}
	d.last[sym] = t.TradeNo
	return true
}

// Fresh reports whether t is new, keyed by the session-local (secid, board)
// pair. It is used when the security cannot be resolved to a stable symbol.
func (d *TradeDedup) Fresh(t model.AllTrade) bool {
	if d.lastRaw == nil {
		d.lastRaw = map[[2]uint16]int64{}
	}
	key := [2]uint16{t.SecID, t.Board}
	if last, ok := d.lastRaw[key]; ok && t.TradeNo <= last {
		return false
	}
	d.lastRaw[key] = t.TradeNo
	return true
}
