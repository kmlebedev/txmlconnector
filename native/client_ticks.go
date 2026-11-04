package native

import (
	"context"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
)

// TickRequest subscribes the tick stream of one security from a trade number.
type TickRequest struct {
	Symbol  model.Symbol
	TradeNo int64
}

// SubscribeTicks sends RQ_SUBSCRIBE_TICKS for the given securities (D/S: B
// filter then {W secid, W board, G tradeno}*, written in the reverse XML order).
// The DLL answers with an empty result; the tick data itself is polled with
// RQ_TICKS (see PollTicks).
func (c *Client) SubscribeTicks(ctx context.Context, filter bool, reqs ...TickRequest) error {
	items, err := c.tickItems(reqs)
	if err != nil {
		return err
	}
	conn := c.connection()
	if conn == nil {
		return errors.New("transaq: client is not connected")
	}
	return conn.SubscribeTicks(ctx, session.TicksPayload(filter, items))
}

// PollTicks polls RQ_TICKS (direct reply frame, not RQ_MAIN) and returns the
// decoded ticks. The live format is unconfirmed (S-negative), so the parser
// interpretation is marked I in native/records.
func (c *Client) PollTicks(ctx context.Context, filter bool, reqs ...TickRequest) ([]model.Tick, error) {
	items, err := c.tickItems(reqs)
	if err != nil {
		return nil, err
	}
	conn := c.connection()
	if conn == nil {
		return nil, errors.New("transaq: client is not connected")
	}
	payload, err := conn.PollTicks(ctx, session.TicksPayload(filter, items))
	if err != nil {
		return nil, err
	}
	raw, err := records.ParseTicksReply(payload)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ticks := make([]model.Tick, 0, len(raw))
	for _, tick := range raw {
		ticks = append(ticks, tick.Event(c.catalog))
	}
	return ticks, nil
}

func (c *Client) tickItems(reqs []TickRequest) ([]session.TickItem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return nil, errors.New("transaq: client is not connected")
	}
	items := make([]session.TickItem, 0, len(reqs))
	for i := len(reqs) - 1; i >= 0; i-- {
		// The DLL writes the list in the reverse XML order (S/D).
		req := reqs[i]
		sec, board, ok := c.catalog.Lookup(req.Symbol.Board, req.Symbol.SecCode)
		if !ok {
			return nil, fmt.Errorf("transaq: unknown security %s", req.Symbol)
		}
		items = append(items, session.TickItem{SecID: sec.SecID, Board: board.Num, TradeNo: req.TradeNo})
	}
	return items, nil
}
