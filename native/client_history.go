package native

import (
	"context"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
)

// HistoryRequest is one gethistorydata query.
type HistoryRequest struct {
	Symbol model.Symbol
	Period int // candlekinds id from <candlekinds>
	Count  int
	Reset  bool
}

type historyKey struct {
	secID  uint16
	board  uint16
	period byte
}

type historyCollector struct {
	ch    chan model.Candles
	parts []records.Candles
}

// History runs gethistorydata like the DLL: it sends RQ_HISTORY_EXT and waits
// for the RQ_MAIN candle records. Parts with status 2 are collected and the
// clock/parts are merged into one chronological Candles; Run does not emit the
// individual parts as events.
func (c *Client) History(ctx context.Context, req HistoryRequest) (model.Candles, error) {
	var empty model.Candles
	c.mu.Lock()
	catalog := c.catalog
	c.mu.Unlock()
	if catalog == nil {
		return empty, errors.New("transaq: client is not connected")
	}
	sec, board, ok := catalog.Lookup(req.Symbol.Board, req.Symbol.SecCode)
	if !ok {
		return empty, fmt.Errorf("transaq: unknown security %s", req.Symbol)
	}
	conn := c.connection()
	if conn == nil {
		return empty, errors.New("transaq: client is not connected")
	}
	key := historyKey{secID: sec.SecID, board: board.Num, period: byte(req.Period)}
	coll := &historyCollector{ch: make(chan model.Candles, 1)}
	c.mu.Lock()
	if c.history == nil {
		c.history = map[historyKey]*historyCollector{}
	}
	if _, busy := c.history[key]; busy {
		c.mu.Unlock()
		return empty, errors.New("transaq: history request is already in flight")
	}
	c.history[key] = coll
	c.mu.Unlock()
	payload := session.HistoryExtPayload(req.Reset, sec.SecID, board.Num, byte(req.Period), uint16(req.Count))
	if err := conn.HistoryExt(ctx, payload); err != nil {
		c.dropHistory(key, coll)
		return empty, err
	}
	select {
	case candles := <-coll.ch:
		return candles, nil
	case <-ctx.Done():
		c.dropHistory(key, coll)
		return empty, ctx.Err()
	}
}

func (c *Client) dropHistory(key historyKey, coll *historyCollector) {
	c.mu.Lock()
	if c.history[key] == coll {
		delete(c.history, key)
	}
	c.mu.Unlock()
}

// addHistoryPart is called by the candles decoder under the client lock.
func (c *Client) addHistoryPart(part records.Candles) {
	key := historyKey{secID: part.SecID, board: part.Board, period: part.Period}
	coll := c.history[key]
	if coll == nil {
		return
	}
	coll.parts = append(coll.parts, part)
	if part.Status == 2 {
		return
	}
	delete(c.history, key)
	candles := records.CombineCandles(coll.parts, c.catalog)
	select {
	case coll.ch <- candles:
	default:
	}
}

// SymbolByBoard resolves the <security> form of gethistorydata (board code plus
// seccode) through the local catalog.
func (c *Client) SymbolByBoard(board, secCode string) (model.Symbol, bool) {
	c.mu.Lock()
	catalog := c.catalog
	c.mu.Unlock()
	if catalog == nil {
		return model.Symbol{}, false
	}
	if _, _, ok := catalog.Lookup(board, secCode); !ok {
		return model.Symbol{}, false
	}
	return model.Symbol{Board: board, SecCode: secCode}, true
}
