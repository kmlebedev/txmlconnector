package native

import (
	"context"
	"errors"
	"fmt"
	"time"

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

type historyResult struct {
	candles model.Candles
	err     error
}

type historyCollector struct {
	ch    chan historyResult
	parts []records.Candles
}

// History runs gethistorydata like the DLL: it sends RQ_HISTORY_EXT and waits
// for the RQ_MAIN candle records.
//
// The plain RQ_HISTORY (11) direct-reply fallback of the plan is I: the live
// r14/r15 captures show only the EXT exchange (S), and the plain request is not
// sent by the DLL, so the EXT path is treated as the equivalent implementation
// (TestHistoryPayloadMatchesDLL pins both count payloads). Parts with status 2 are collected and the
// clock/parts are merged into one chronological Candles; Run does not emit the
// individual parts as events.
func (c *Client) History(ctx context.Context, req HistoryRequest) (model.Candles, error) {
	var empty model.Candles
	c.mu.Lock()
	catalog := c.catalog
	if catalog == nil {
		c.mu.Unlock()
		return empty, errors.New("transaq: client is not connected")
	}
	// The catalog is mutated by Poll under c.mu, so the lookup must be guarded.
	sec, board, ok := catalog.Lookup(req.Symbol.Board, req.Symbol.SecCode)
	c.mu.Unlock()
	if !ok {
		return empty, fmt.Errorf("transaq: unknown security %s", req.Symbol)
	}
	conn := c.connection()
	if conn == nil {
		return empty, errors.New("transaq: client is not connected")
	}
	key := historyKey{secID: sec.SecID, board: board.Num, period: byte(req.Period)}
	coll := &historyCollector{ch: make(chan historyResult, 1)}
	c.mu.Lock()
	if c.recovering {
		c.mu.Unlock()
		return empty, errors.New("transaq: connection is being recovered")
	}
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
	case result := <-coll.ch:
		return result.candles, result.err
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
func (c *Client) addHistoryPart(part records.Candles, bias time.Duration) {
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
	candles := records.CombineCandles(coll.parts, c.catalog, bias)
	select {
	case coll.ch <- historyResult{candles: candles}:
	default:
	}
}

// failHistoryParts wakes every waiting History with err (used when a relogin
// resets the history state: the old collectors can never complete).
func (c *Client) failHistoryParts(err error) {
	for key, coll := range c.history {
		select {
		case coll.ch <- historyResult{err: err}:
		default:
		}
		delete(c.history, key)
	}
}

// StartHistory runs History in the background and calls onDone with the
// combined candles: the XML command returns <result success="true"/> before the
// data arrives, as the DLL does (S r14: result 7.310, candles 7.340).
func (c *Client) StartHistory(ctx context.Context, req HistoryRequest, onDone func(model.Candles, error)) error {
	c.mu.Lock()
	recovering := c.recovering
	c.mu.Unlock()
	if recovering {
		return errors.New("transaq: connection is being recovered")
	}
	// The command context ends when the XML command returns (gRPC cancels it
	// right after <result success="true"/>), long before the candles: keep its
	// values only. Close and relogin end the wait (failHistoryParts).
	ctx = context.WithoutCancel(ctx)
	go func() {
		candles, err := c.History(ctx, req)
		if onDone != nil {
			onDone(candles, err)
		}
	}()
	return nil
}

// SymbolByBoard resolves the <security> form of gethistorydata (board code plus
// seccode) through the local catalog.
func (c *Client) SymbolByBoard(board, secCode string) (model.Symbol, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return model.Symbol{}, false
	}
	if _, _, ok := c.catalog.Lookup(board, secCode); !ok {
		return model.Symbol{}, false
	}
	return model.Symbol{Board: board, SecCode: secCode}, true
}
