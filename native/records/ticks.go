package records

import (
	"fmt"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Tick is one decoded tick of the RQ_TICKS reply (D layout, docs/protocol/
// history.md): W secid, W board, G tradeno, L time, L price, B field, L volume,
// repeated until secid 0xffff. The DLL accumulates tradeno/time/price relative
// to the previous tick (I: the live format is not confirmed, S-negative).
type Tick struct {
	SecID   uint16
	Board   uint16
	TradeNo int64
	Time    int64 // accumulated wire time (I)
	Price   int64 // accumulated wire price units (I)
	Field   byte
	Volume  int64
}

// ParseTicksReply decodes one RQ_TICKS reply payload.
func ParseTicksReply(payload []byte) ([]Tick, error) {
	var (
		out           []Tick
		tradeNo, tm   int64
		price         int64
	)
	r := wire.NewReader(payload)
	for {
		secID, err := r.Uint16()
		if err != nil {
			return out, fmt.Errorf("RQ_TICKS secid: %w", err)
		}
		if secID == 0xffff {
			return out, nil
		}
		board, err := r.Uint16()
		if err != nil {
			return out, fmt.Errorf("RQ_TICKS board: %w", err)
		}
		deltaTradeNo, err := r.Int64()
		if err != nil {
			return out, fmt.Errorf("RQ_TICKS tradeno: %w", err)
		}
		deltaTime, err := r.Int32()
		if err != nil {
			return out, fmt.Errorf("RQ_TICKS time: %w", err)
		}
		deltaPrice, err := r.Int32()
		if err != nil {
			return out, fmt.Errorf("RQ_TICKS price: %w", err)
		}
		field, err := r.Byte()
		if err != nil {
			return out, fmt.Errorf("RQ_TICKS field: %w", err)
		}
		volume, err := r.Int32()
		if err != nil {
			return out, fmt.Errorf("RQ_TICKS volume: %w", err)
		}
		tradeNo += deltaTradeNo
		tm += int64(deltaTime)
		price += int64(deltaPrice)
		out = append(out, Tick{SecID: secID, Board: board, TradeNo: tradeNo, Time: tm, Price: price, Field: field, Volume: int64(volume)})
	}
}

// Event builds the typed tick event, resolving the identity and the price scale
// from the catalog. The time is stored as Unix seconds as-is (wire time units
// are I until the live capture confirms them).
func (t Tick) Event(cat *Catalog) model.Tick {
	ev := model.Tick{
		SecID:    t.SecID,
		TradeNo:  t.TradeNo,
		Time:     time.Unix(t.Time, 0).UTC(),
		Quantity: t.Volume,
		Period:   t.Field,
	}
	if cat != nil {
		if sec, ok := cat.Security(t.SecID); ok {
			ev.Symbol = model.Symbol{SecCode: sec.SecCode}
			if board, ok := cat.Board(t.Board); ok {
				ev.Symbol.Board = board.Code
			}
			if decimals, ok := cat.Decimals(t.SecID, t.Board); ok {
				ev.Price = model.Decimal{Units: t.Price, Scale: decimals}
			}
		}
	}
	return ev
}
