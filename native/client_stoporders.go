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

// stopOrderPayload builds the O newstoporder RQ_TRANSACT body (offsets from the
// task-47 delta table): B 0x14, flags, W secid, W board, G client, B buysell,
// B 0x03, B 0x11, 4 zero bytes, activationprice, orderprice, quantity, one flag
// byte, takeprofit activationprice and its quantity, then reserved zeros.
func stopOrderPayload(secID, board uint16, clientID int64, side byte, sl StopLossWire) []byte {
	var w wire.Writer
	// Transact prepends the TxNewStopOrder type byte (0x14): the body itself
	// starts with the two flag bytes seen at payload offsets 1–2.
	w.Byte(0)
	w.Byte(0)
	w.Uint16(secID)
	w.Uint16(board)
	w.Int64(clientID)
	w.Byte(side)
	w.Byte(0x03)
	w.Byte(0x11)
	w.Raw(make([]byte, 4))
	w.Int32(int32(sl.activation))
	w.Int32(int32(sl.orderPrice))
	w.Int32(int32(sl.quantity))
	w.Byte(0)
	w.Byte(0)
	w.Int32(int32(sl.takeActivation))
	w.Byte(0)
	w.Byte(0)
	w.Int32(int32(sl.takeQuantity))
	w.Raw(make([]byte, 3))
	return w.Bytes()
}

// StopLossWire is the scaled integer form of the stop parameters.
type StopLossWire struct {
	activation, orderPrice, quantity, takeActivation, takeQuantity int64
}

// CancelStopOrder sends cancelstoporder: the O payload is B 0x17 followed by
// the packed L transactionid (2 bytes for id 1: 17 10).
func (c *Client) CancelStopOrder(ctx context.Context, transactionID model.TransactionID) error {
	conn := c.connection()
	if conn == nil {
		return errors.New("transaq: client is not connected")
	}
	var w wire.Writer
	w.Int32(int32(transactionID))
	_, err := conn.Transact(ctx, session.TxCancelStopOrder, w.Bytes())
	return err
}

// NewStopOrder sends newstoporder (O payload offsets per task-47). Prices are
// rescaled to the pit decimals (×10^decimals) like the DLL.
func (c *Client) NewStopOrder(ctx context.Context, order model.NewStopOrder) (model.TransactionID, error) {
	if order.BuySell != 'B' && order.BuySell != 'S' {
		return 0, fmt.Errorf("transaq: newstoporder buysell must be B or S")
	}
	sec, board, clientID, decimals, err := c.stopOrderParts(order.Client, order.Union, order.Symbol)
	if err != nil {
		return 0, err
	}
	scale := func(d model.Decimal) int64 {
		return rescaleUnits(d.Units, d.Scale, decimals)
	}
	wireParams := StopLossWire{
		activation:     scale(order.StopLoss.ActivationPrice),
		orderPrice:     scale(order.StopLoss.OrderPrice),
		quantity:       order.StopLoss.Quantity,
		takeActivation: scale(order.TakeProfit.ActivationPrice),
		takeQuantity:   order.TakeProfit.Quantity,
	}
	if wireParams.quantity == 0 {
		wireParams.quantity = order.Quantity
	}
	conn := c.connection()
	if conn == nil {
		return 0, errors.New("transaq: client is not connected")
	}
	return conn.Transact(ctx, session.TxNewStopOrder, stopOrderPayload(sec.SecID, board.Num, clientID, order.BuySell, wireParams))
}

// NewCondOrder sends newcondorder: the DLL uses the base TRN_NEWORDER type 0x01
// with the cond fields appended (task-47 offsets). cond_type is not encoded by
// the oracle payload (I); validbefore is passed as the second time value.
func (c *Client) NewCondOrder(ctx context.Context, order model.NewCondOrder) (model.TransactionID, error) {
	if order.BuySell != 'B' && order.BuySell != 'S' {
		return 0, fmt.Errorf("transaq: newcondorder buysell must be B or S")
	}
	sec, board, clientID, decimals, err := c.stopOrderParts(order.Client, order.Union, order.Symbol)
	if err != nil {
		return 0, err
	}
	scale := func(d model.Decimal) int64 { return rescaleUnits(d.Units, d.Scale, decimals) }
	var w wire.Writer
	// Transact prepends the base TxNewCondOrder type (0x01); the body starts
	// with the flag byte at payload offset 1.
	w.Byte(0)
	w.Uint16(sec.SecID)
	w.Uint16(board.Num)
	w.Int64(clientID)
	w.Byte(order.BuySell)
	w.Byte(0)
	w.Int32(int32(scale(order.Price)))
	w.Int32(int32(order.Quantity))
	w.Byte(0)
	w.Byte(1)
	w.Int32(int32(scale(order.CondValue)))
	var after, before int64
	if order.ValidAfter != nil {
		after = order.ValidAfter.Unix()
	}
	if order.ValidBefore != nil {
		before = order.ValidBefore.Unix()
	}
	w.Int32(int32(after))
	w.Int32(int32(before))
	w.Raw(make([]byte, 9))
	conn := c.connection()
	if conn == nil {
		return 0, errors.New("transaq: client is not connected")
	}
	return conn.Transact(ctx, session.TxNewCondOrder, w.Bytes())
}

// stopOrderParts resolves the stop/cond order identity and the price scale.
func (c *Client) stopOrderParts(client, union string, symbol model.Symbol) (records.SecNew, model.Board, int64, uint8, error) {
	c.mu.Lock()
	catalog := c.catalog
	if catalog == nil {
		c.mu.Unlock()
		return records.SecNew{}, model.Board{}, 0, 0, errors.New("transaq: client is not connected")
	}
	sec, board, ok := catalog.Lookup(symbol.Board, symbol.SecCode)
	if !ok {
		c.mu.Unlock()
		return records.SecNew{}, model.Board{}, 0, 0, fmt.Errorf("transaq: unknown security %s", symbol)
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
		return records.SecNew{}, model.Board{}, 0, 0, fmt.Errorf("transaq: unknown client %q", client)
	}
	return sec, board, clientID, decimals, nil
}
