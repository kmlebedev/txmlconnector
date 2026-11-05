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
	// The DLL payload carries the price in 10^-decimals units: a command value
	// given as a raw Decimal scale must be rescaled to the pit decimals, never
	// silently passed as-is (100 -> 10000 for TQBR).
	if req.Unfilled != "" && req.Unfilled != "PutInQueue" {
		return 0, fmt.Errorf("transaq: unfilled %q is not supported (I)", req.Unfilled)
	}
	if req.ByMarket || req.UseCredit || req.NoSplit || req.ExpDate != nil || req.BrokerRef != "" {
		return 0, fmt.Errorf("transaq: neworder flags bymarket/usecredit/nosplit/expdate/brokerref are not encoded yet (I)")
	}
	if req.Hidden != 0 {
		// The DLL hidden flag is not mapped in the payload yet (I): reject it
		// explicitly instead of writing it as part of the zero flag bytes.
		return 0, fmt.Errorf("transaq: neworder hidden is not encoded yet (I)")
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
	decimals, hasDecimals := catalog.Decimals(sec.SecID, board.Num)
	if !hasDecimals {
		if pit, pitOK := sec.DefaultPit(); pitOK {
			decimals, hasDecimals = pit.Decimals, true
		}
	}
	c.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("transaq: unknown client %q", req.Client)
	}
	price := req.Price
	if hasDecimals && price.Scale != decimals {
		price = model.Decimal{Units: rescaleUnits(price.Units, price.Scale, decimals), Scale: decimals}
	}
	var w wire.Writer
	w.Byte(0)
	w.Uint16(sec.SecID)
	w.Uint16(board.Num)
	w.Int64(clientID)
	w.Byte(req.BuySell)
	w.Byte(0)
	w.Int32(int32(price.Units))
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

// MoveOrder sends moveorder: S payload B 01, packed L transactionid, B 00
// (fixture transact-moveorder-live.hex: 02 01 41 01 00 for txid=20). The valid
// active-order scenario is I; the server reply/order update is handled by Run.
func (c *Client) MoveOrder(ctx context.Context, req model.MoveOrder) error {
	if req.TransactionID == 0 {
		return errors.New("Wrong transactionid value.")
	}
	if req.MoveFlag > 3 {
		return errors.New("Wrong moveflag value.")
	}
	if req.MoveFlag == 1 || req.MoveFlag == 2 {
		if req.Quantity == 0 {
			return errors.New("Cannot find quantity.")
		}
	}
	var w wire.Writer
	w.Byte(1)
	w.Int32(int32(req.TransactionID))
	w.Byte(0)
	conn := c.connection()
	if conn == nil {
		return errors.New("transaq: client is not connected")
	}
	_, err := conn.Transact(ctx, session.TxMoveOrder, w.Bytes())
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
	if _, seen := c.orders[order.TransactionID]; !seen {
		c.orderOrder = append(c.orderOrder, order.TransactionID)
	}
	c.orders[order.TransactionID] = &order
}

// buildOrderEventLocked fills the catalog identity of a parsed order.
func (c *Client) buildOrderEventLocked(parsed records.OrderNew) model.Order {
	order := parsed.Order
	if c.catalog != nil {
		if sec, ok := c.catalog.Security(order.SecID); ok {
			order.SecCode = sec.SecCode
		}
		if board, ok := c.catalog.Board(parsed.BoardID); ok {
			order.Board = board.Code
		}
	}
	return order
}

// orderClientMatchLocked reports whether the numeric client id belongs to the
// session (the DLL silently drops other clients' orders, O). found=false with
// known=true means a known client store without this id = foreign.
func (c *Client) orderClientMatchLocked(clientID int64) (client string, known bool, match bool) {
	if len(c.accounts) == 0 {
		return "", false, false
	}
	for _, id := range c.accountOrder {
		state := c.accounts[id]
		if state != nil && state.raw.ID == clientID {
			return state.account.ID, true, true
		}
	}
	return "", true, false
}

// flushPendingOrdersLocked resolves the orders buffered before the R_CLN_NEW
// record arrived (the order of the load records is not guaranteed).
func (c *Client) flushPendingOrdersLocked(clientID int64) []model.Event {
	if len(c.pendingOrders) == 0 {
		return nil
	}
	var events []model.Event
	kept := c.pendingOrders[:0]
	for _, parsed := range c.pendingOrders {
		if parsed.ClientID != clientID {
			kept = append(kept, parsed)
			continue
		}
		order := c.buildOrderEventLocked(parsed)
		c.storeOrderLocked(order)
		events = append(events, &order)
	}
	c.pendingOrders = kept
	return events
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

// Orders returns the orders known to the session in arrival order. The
// Connector uses it to emit the orders decoded during the load, whose events the
// load path drops, right after the end-of-load callbacks (DLL order).
func (c *Client) Orders() []model.Order {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.orders) == 0 {
		return nil
	}
	out := make([]model.Order, 0, len(c.orders))
	seen := make(map[uint64]bool, len(c.orders))
	for _, id := range c.orderOrder {
		if order, ok := c.orders[id]; ok && !seen[id] {
			out = append(out, *order)
			seen[id] = true
		}
	}
	return out
}

// rescaleUnits converts a Decimal between scales without float arithmetic.
func rescaleUnits(units int64, from, to uint8) int64 {
	for from < to {
		units *= 10
		from++
	}
	for from > to {
		units /= 10
		from--
	}
	return units
}
