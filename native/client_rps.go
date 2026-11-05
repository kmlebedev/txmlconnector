package native

import (
	"context"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// O payload builders (rps.md). The repository term/rate/value/discount bytes
// after the base fields are the oracle constants: their exact offsets/meaning
// are I, so no invented fields are encoded.

// rpsOrderPayload: B secid/board, G client, B buysell, B flag, 11 zeros,
// L price, L quantity, 5 zero tail (30-byte O payload).
func rpsOrderPayload(secID, board uint16, clientID int64, side byte, price, quantity int64) []byte {
	var w wire.Writer
	w.Uint16(secID)
	w.Uint16(board)
	w.Int64(clientID)
	w.Byte(side)
	w.Byte(0)
	w.Raw(make([]byte, 11))
	w.Int32(int32(price))
	w.Int32(int32(quantity))
	w.Raw(make([]byte, 5))
	return w.Bytes()
}

// repoOrderPayload: the 39-byte O payload; the repo-specific middle fields are
// the oracle constants (the exact D semantics are I, rps.md).
func repoOrderPayload(secID, board uint16, clientID int64, side byte, price, quantity, repoTerm, repoRate, startDisc, lowerDisc, upperDisc int64, block byte) []byte {
	var w wire.Writer
	w.Uint16(secID)
	w.Uint16(board)
	w.Int64(clientID)
	w.Byte(side)
	w.Byte(0)
	w.Raw(make([]byte, 11))
	w.Int32(int32(repoTerm))
	w.Byte(0)
	w.Byte(0)
	w.Int32(int32(repoRate))
	w.Int32(int32(price))
	w.Int32(int32(quantity))
	w.Int32(int32(startDisc))
	w.Int32(int32(lowerDisc))
	w.Int32(int32(upperDisc))
	w.Byte(block)
	w.Raw(make([]byte, 2))
	return w.Bytes()
}

func (c *Client) rpsParts(symbol model.Symbol, client, union string) (uint16, uint16, int64, uint8, error) {
	c.mu.Lock()
	catalog := c.catalog
	if catalog == nil {
		c.mu.Unlock()
		return 0, 0, 0, 0, errors.New("transaq: client is not connected")
	}
	sec, board, ok := catalog.Lookup(symbol.Board, symbol.SecCode)
	if !ok {
		c.mu.Unlock()
		return 0, 0, 0, 0, fmt.Errorf("transaq: unknown security %s", symbol)
	}
	clientID, clientOK := c.clientNumericIDLocked(client, union)
	decimals, hasDecimals := catalog.Decimals(sec.SecID, board.Num)
	if !hasDecimals {
		if pit, pitOK := sec.DefaultPit(); pitOK {
			decimals = pit.Decimals
		}
	}
	c.mu.Unlock()
	if !clientOK {
		return 0, 0, 0, 0, fmt.Errorf("Wrong client specifited.")
	}
	return sec.SecID, board.Num, clientID, decimals, nil
}

// RPS-specific reply texts: the replay oracle shows "Specified security can't be
// traded." for newrepoorder and "{8} ..." for an empty reply, while the standard
// transaction mapping renders code 1 as "Клиент не допущен к торгам." and
// Unknown error; the exact per-command mapping is I (T29/S follow-up).
//
// NewRPSOrder sends newrpsorder (TRN_NEWRPSREPO 0x04) with the O base payload.
func (c *Client) NewRPSOrder(ctx context.Context, order model.NewRPSOrder) (model.TransactionID, error) {
	if order.BuySell != 'B' && order.BuySell != 'S' {
		return 0, fmt.Errorf("Buisell can be only 'B' or 'S'.")
	}
	secID, board, clientID, decimals, err := c.rpsParts(order.Symbol, order.Client, order.Union)
	if err != nil {
		return 0, err
	}
	price := rescaleUnits(order.Price.Units, order.Price.Scale, decimals)
	conn := c.connection()
	if conn == nil {
		return 0, errors.New("transaq: client is not connected")
	}
	return conn.Transact(ctx, session.TxNewRpsRepo, rpsOrderPayload(secID, board, clientID, order.BuySell, price, order.Quantity))
}

// NewRepoOrder sends newrepoorder (TRN_NEWREPOORDER 0x0e) with the O payload.
func (c *Client) NewRepoOrder(ctx context.Context, order model.NewRepoOrder) (model.TransactionID, error) {
	if order.BuySell != 'B' && order.BuySell != 'S' {
		return 0, fmt.Errorf("Buisell can be only 'B' or 'S'.")
	}
	secID, board, clientID, decimals, err := c.rpsParts(order.Symbol, order.Client, order.Union)
	if err != nil {
		return 0, err
	}
	price := rescaleUnits(order.Price.Units, order.Price.Scale, decimals)
	scale := func(d model.Decimal) int64 { return rescaleUnits(d.Units, d.Scale, 2) }
	// Defaults are the O-sample constants (start/lower/upper discount = 1.00,
	// blocksecurities = N); they are overridden by the XML fields when present.
	if order.StartDisc == (model.Decimal{}) {
		order.StartDisc = model.Decimal{Units: 100, Scale: 2}
	}
	if order.LowerDisc == (model.Decimal{}) {
		order.LowerDisc = model.Decimal{Units: 100, Scale: 2}
	}
	if order.UpperDisc == (model.Decimal{}) {
		order.UpperDisc = model.Decimal{Units: 100, Scale: 2}
	}
	if order.Block == 0 {
		order.Block = 'N'
	}
	conn := c.connection()
	if conn == nil {
		return 0, errors.New("transaq: client is not connected")
	}
	return conn.Transact(ctx, session.TxNewRepoOrder, repoOrderPayload(
		secID, board, clientID, order.BuySell, price, order.Quantity,
		order.RepoTerm, scale(order.RepoRate), scale(order.StartDisc), scale(order.LowerDisc), scale(order.UpperDisc), order.Block,
	))
}

// CancelNegDeal sends cancelnegdeal: TRN code 0x08 + packed L transaction id
// (O payload is 08 10 for id 1).
func (c *Client) CancelNegDeal(ctx context.Context, transactionID model.TransactionID) (model.TransactionID, error) {
	return c.cancelRPS(ctx, session.TxCancelNegDeal, transactionID)
}

// CancelReport4 sends cancelreport4: the O payload is identical to cancelnegdeal
// (rps.md; the exact TRN code difference is I).
func (c *Client) CancelReport4(ctx context.Context, reportID int64) (model.TransactionID, error) {
	return c.cancelRPS(ctx, session.TxCancelReport4, model.TransactionID(reportID))
}

func (c *Client) cancelRPS(ctx context.Context, kind session.TxKind, id model.TransactionID) (model.TransactionID, error) {
	conn := c.connection()
	if conn == nil {
		return 0, errors.New("transaq: client is not connected")
	}
	var w wire.Writer
	w.Int32(int32(id))
	return conn.Transact(ctx, kind, w.Bytes())
}

// NewMRepoOrder and NewReport4 are not encodable yet: the payload layouts are
// not recovered (I, rps.md), so they return an explicit error.
func (c *Client) NewMRepoOrder(ctx context.Context, order model.NewRepoOrder) (model.TransactionID, error) {
	_ = ctx
	_ = order
	return 0, fmt.Errorf("newmrepoorder payload layout is not recovered (I)")
}

func (c *Client) NewReport4(ctx context.Context, leg string) (model.TransactionID, error) {
	_ = ctx
	_ = leg
	return 0, fmt.Errorf("newreport4 payload layout is not recovered (I)")
}
