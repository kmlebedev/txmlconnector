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

type tickKey struct{ secID, board uint16 }

func decodeTick(c *Client, body []byte) ([]model.Event, error) {
	raw, err := records.ParseTickRecord(body)
	if err != nil {
		return nil, err
	}
	if c.tickTradeNos == nil {
		c.tickTradeNos = map[tickKey]int64{}
		c.tickTimes = map[tickKey]int64{}
	}
	items := make([]model.Tick, 0, len(raw))
	for _, tick := range raw {
		key := tickKey{secID: tick.SecID, board: tick.Board}
		// tradeno/time are cumulative deltas: add them to the per-security state
		// (D/S). A zero accumulated time falls back to the start of the day.
		c.tickTradeNos[key] += tick.TradeNo
		c.tickTimes[key] += tick.Time
		tm := c.tickTimes[key]
		wall := time.Unix(tm, 0).UTC()
		if tm == 0 {
			now := time.Now().UTC()
			wall = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		}
		event := model.Tick{
			SecID:    tick.SecID,
			TradeNo:  c.tickTradeNos[key],
			Time:     wall,
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
		items = append(items, event)
	}
	batch := &model.Ticks{Items: items}
	return []model.Event{batch}, nil
}
