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
// the same security and board, as the DLL does ("old ALLTRADE No" in its log).
// It keeps replays after a reconnect out of the feed.
type TradeDedup struct {
	last map[[2]uint16]int64
}

// Fresh reports whether t is new and records it.
func (d *TradeDedup) Fresh(t model.AllTrade) bool {
	if d.last == nil {
		d.last = map[[2]uint16]int64{}
	}
	key := [2]uint16{t.SecID, t.Board}
	if last, ok := d.last[key]; ok && t.TradeNo <= last {
		return false
	}
	d.last[key] = t.TradeNo
	return true
}
