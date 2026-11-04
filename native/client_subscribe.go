package native

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
)

// Subscription is the feed selection of the DLL's subscribe/unsubscribe command:
// the same security may be in several feeds (the RQ_SUBSCRIBE mask is their OR).
type Subscription struct {
	AllTrades  []model.Symbol
	Quotations []model.Symbol
	Quotes     []model.Symbol
}

// Subscribe adds the feeds of the subscription to the session.
func (c *Client) Subscribe(ctx context.Context, sub Subscription) error {
	return c.changeSubscription(ctx, sub, true)
}

// Unsubscribe removes the feeds of the subscription from the session.
func (c *Client) Unsubscribe(ctx context.Context, sub Subscription) error {
	return c.changeSubscription(ctx, sub, false)
}

// changeSubscription sends the full new mask of every touched security, as the
// DLL does. Subscriptions are kept per stable model.Symbol, so they survive a
// reconnect that changes the session-local secids; the ids are resolved on
// every send.
func (c *Client) changeSubscription(ctx context.Context, sub Subscription, on bool) error {
	c.mu.Lock()
	next := make(map[model.Symbol]byte)
	apply := func(symbols []model.Symbol, bit byte) {
		for _, s := range symbols {
			mask, ok := next[s]
			if !ok {
				mask = c.subs[s]
			}
			if on {
				mask |= bit
			} else {
				mask &^= bit
			}
			next[s] = mask
		}
	}
	apply(sub.AllTrades, session.SubAllTrades)
	apply(sub.Quotations, session.SubQuotations)
	apply(sub.Quotes, session.SubQuotes)
	if c.recovering {
		c.mu.Unlock()
		return errors.New("transaq: connection is being recovered")
	}
	conn := c.conn
	catalog := c.catalog
	c.mu.Unlock()
	if conn == nil || catalog == nil {
		return errors.New("transaq: client is not connected")
	}
	items := make([]session.SubscribeItem, 0, len(next))
	for s, mask := range next {
		sec, board, ok := catalog.Lookup(s.Board, s.SecCode)
		if !ok {
			return fmt.Errorf("unknown security %s", s)
		}
		items = append(items, session.SubscribeItem{SecID: sec.SecID, Board: board.Num, Mask: mask})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SecID != items[j].SecID {
			return items[i].SecID < items[j].SecID
		}
		return items[i].Board < items[j].Board
	})
	if err := conn.Subscribe(ctx, items); err != nil {
		return err
	}
	// Like the DLL, request the security specs of the subscribed securities
	// right after the subscribe (the payload order is the reverse of the XML
	// elements, RQ_SEC_SPEC needs no counter).
	var ids []uint16
	for _, s := range []struct {
		symbols []model.Symbol
	}{
		{sub.AllTrades}, {sub.Quotations}, {sub.Quotes},
	} {
		for _, symbol := range s.symbols {
			if _, ok := next[symbol]; !ok {
				continue
			}
			if sec, _, ok := catalog.Lookup(symbol.Board, symbol.SecCode); ok {
				ids = append(ids, sec.SecID)
			}
		}
	}
	reverseIDs(ids)
	if err := c.loadSecSpecs(ctx, ids); err != nil {
		return err
	}
	c.mu.Lock()
	for s, mask := range next {
		if mask == 0 {
			delete(c.subs, s)
		} else {
			c.subs[s] = mask
		}
	}
	c.mu.Unlock()
	return nil
}

// reverseIDs reverses the secid list in place (RQ_SEC_SPEC payload order).
func reverseIDs(ids []uint16) {
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
}
