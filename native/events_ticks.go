package native

import (
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// Ticks are RQ_MAIN 0x5a records (S: no RQ_TICKS polling on production).
func init() {
	registerDecoder(records.TagTick, decodeTick)
}

func decodeTick(c *Client, body []byte) ([]model.Event, error) {
	raw, err := records.ParseTickRecord(body)
	if err != nil {
		return nil, err
	}
	events := make([]model.Event, 0, len(raw))
	for _, tick := range raw {
		event := model.Tick{
			SecID:    tick.SecID,
			TradeNo:  tick.TradeNo,
			Time:     time.Unix(tick.Time, 0).UTC(),
			Quantity: tick.Volume,
			Period:   tick.Period,
			BuySell:  tick.BuySell,
		}
		if c.catalog != nil {
			if sec, ok := c.catalog.Security(tick.SecID); ok {
				event.Symbol.SecCode = sec.SecCode
			}
			if board, ok := c.catalog.Board(tick.Board); ok {
				event.Symbol.Board = board.Code
				event.Board = board.Code
			}
			if decimals, ok := c.catalog.Decimals(tick.SecID, tick.Board); ok {
				event.Price = model.Decimal{Units: tick.Price, Scale: decimals}
			}
		}
		events = append(events, &event)
	}
	return events, nil
}
