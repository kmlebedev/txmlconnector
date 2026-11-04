package native

import (
	"context"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// NewOrder sends neworder like the DLL (O payload): B01, B00, W secid, W board,
// G client_id, B buysell, B00, L price, L quantity, 9 zero flag bytes. The
// transaction id comes from the RQ_TRANSACT answer; on an unknown outcome the
// OutcomeError carries the local number (session.Transact).
func (c *Client) NewOrder(ctx context.Context, req model.NewOrder) (model.TransactionID, error) {
	if req.BuySell != 'B' && req.BuySell != 'S' {
		return 0, fmt.Errorf("transaq: neworder buysell must be B or S")
	}
	c.mu.Lock()
	catalog := c.catalog
	if catalog == nil {
		c.mu.Unlock()
		return 0, errors.New("transaq: client is not connected")
	}
	sec, board, ok := catalog.Lookup(req.Symbol.Board, req.Symbol.SecCode)
	if !ok {
		c.mu.Unlock()
		return 0, fmt.Errorf("transaq: unknown security %s", req.Symbol)
	}
	clientID, ok := c.clientNumericIDLocked(req.Client, req.Union)
	c.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("transaq: unknown client %q", req.Client)
	}
	var w wire.Writer
	w.Byte(0)
	w.Uint16(sec.SecID)
	w.Uint16(board.Num)
	w.Int64(clientID)
	w.Byte(req.BuySell)
	w.Byte(0)
	w.Int32(int32(req.Price.Units))
	w.Int32(int32(req.Quantity))
	w.Raw(make([]byte, 9))
	conn := c.connection()
	if conn == nil {
		return 0, errors.New("transaq: client is not connected")
	}
	return conn.Transact(ctx, session.TxNewOrder, w.Bytes())
}

// CancelOrder sends cancelorder (O payload: B01, L transactionid, B00 wrapped
// by the TxCancelOrder kind byte).
func (c *Client) CancelOrder(ctx context.Context, transactionID model.TransactionID) error {
	conn := c.connection()
	if conn == nil {
		return errors.New("transaq: client is not connected")
	}
	var w wire.Writer
	w.Byte(1)
	w.Int32(int32(transactionID))
	w.Byte(0)
	_, err := conn.Transact(ctx, session.TxCancelOrder, w.Bytes())
	return err
}

// clientNumericIDLocked maps the client code (or union) to the numeric id the
// neworder payload carries (the accounts raw records keep it).
func (c *Client) clientNumericIDLocked(client, union string) (int64, bool) {
	for _, id := range c.accountOrder {
		state := c.accounts[id]
		if state == nil {
			continue
		}
		if client != "" && state.raw.Code == client {
			return state.raw.ID, true
		}
		if client == "" && union != "" && state.raw.Union == union {
			return state.raw.ID, true
		}
	}
	return 0, false
}

// storeOrderLocked keeps the order for the R_ORD_MODIFY updates.
func (c *Client) storeOrderLocked(order model.Order) {
	if c.orders == nil {
		c.orders = map[uint64]*model.Order{}
	}
	c.orders[order.TransactionID] = &order
}

// applyOrderModifyLocked applies one 0x10 update to the stored order and
// returns the updated order (nil when the order is unknown: the DLL logs
// "!!!NOT FOUND!!!" and drops the update).
func (c *Client) applyOrderModifyLocked(mod records.OrderModify) *model.Order {
	if c.orders == nil {
		return nil
	}
	order, ok := c.orders[mod.TransactionID]
	if !ok {
		return nil
	}
	mod.Apply(order)
	return order
}
