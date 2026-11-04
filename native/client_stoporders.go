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

// StopOrderLegs is the scaled wire form of the stop order legs.
type StopOrderLegs struct {
	SLActivation, SLOrderPrice, SLQuantity int64
	TPActivation, TPQuantity               int64
	HasSL, HasTP                           bool
}

// stopOrderPayload builds the O newstoporder RQ_TRANSACT body. The RE-3 probes
// (capture 2026-11-05, no live trading) pin the leg-dependent flags and size:
//   - SL only: flags 00 01, 27-byte payload (SL block + three reserved zeros);
//   - TP only: flags 03 10, 26-byte payload (TP block only);
//   - both:    flags 03 11, 35-byte payload (SL block + two zeros + TP block).
//
// The flags do not depend on validafter/validbefore (O).
func stopOrderPayload(secID, board uint16, clientID int64, side byte, legs StopOrderLegs) []byte {
	flagA, flagB := byte(0x03), byte(0x11)
	switch {
	case legs.HasSL && !legs.HasTP:
		flagA, flagB = 0x00, 0x01
	case !legs.HasSL && legs.HasTP:
		flagA, flagB = 0x03, 0x10
	}
	var w wire.Writer
	// Transact prepends the TxNewStopOrder type byte (0x14): the body starts
	// with the two flag bytes at payload offsets 1-2.
	w.Byte(0)
	w.Byte(0)
	w.Uint16(secID)
	w.Uint16(board)
	w.Int64(clientID)
	w.Byte(side)
	w.Byte(flagA)
	w.Byte(flagB)
	w.Raw(make([]byte, 4))
	if legs.HasSL {
		w.Int32(int32(legs.SLActivation))
		w.Int32(int32(legs.SLOrderPrice))
		w.Int32(int32(legs.SLQuantity))
		if legs.HasTP {
			w.Raw(make([]byte, 2))
		} else {
			w.Raw(make([]byte, 3))
		}
	}
	if legs.HasTP {
		w.Int32(int32(legs.TPActivation))
		w.Byte(0)
		w.Byte(0)
		w.Int32(int32(legs.TPQuantity))
		w.Raw(make([]byte, 3))
	}
	return w.Bytes()
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

// NewStopOrder sends newstoporder (O payload offsets per task-47/RE-3). Prices
// are rescaled to the pit decimals (x10^decimals) like the DLL.
func (c *Client) NewStopOrder(ctx context.Context, order model.NewStopOrder) (model.TransactionID, error) {
	if order.BuySell != 'B' && order.BuySell != 'S' {
		return 0, fmt.Errorf("Buisell can be only 'B' or 'S'.")
	}
	if order.StopLoss == nil && order.TakeProfit == nil {
		return 0, fmt.Errorf("Cannot find stop-loss activation price.")
	}
	if order.LinkedOrderNo != 0 || order.BrokerRef != "" {
		return 0, fmt.Errorf("transaq: newstoporder linkedorderno/brokerref are not encoded (I)")
	}
	sec, board, clientID, decimals, err := c.stopOrderParts(order.Client, order.Union, order.Symbol)
	if err != nil {
		return 0, err
	}
	scale := func(d model.Decimal) int64 { return rescaleUnits(d.Units, d.Scale, decimals) }
	legs := StopOrderLegs{}
	if order.StopLoss != nil {
		if order.StopLoss.Quantity <= 0 {
			return 0, fmt.Errorf("Cannot find stop-loss quantity.")
		}
		legs.HasSL = true
		legs.SLActivation = scale(order.StopLoss.ActivationPrice)
		legs.SLOrderPrice = scale(order.StopLoss.OrderPrice)
		legs.SLQuantity = order.StopLoss.Quantity
	}
	if order.TakeProfit != nil {
		legs.HasTP = true
		legs.TPActivation = scale(order.TakeProfit.ActivationPrice)
		legs.TPQuantity = order.TakeProfit.Quantity
	}
	conn := c.connection()
	if conn == nil {
		return 0, errors.New("transaq: client is not connected")
	}
	return conn.Transact(ctx, session.TxNewStopOrder, stopOrderPayload(sec.SecID, board.Num, clientID, order.BuySell, legs))
}

// condTypeByte maps the newcondorder <cond_type> to the payload byte 16 (O:
// Bid=0x01, BidOrLast=0x02, AskOrLast=0x04; Last and Time are rejected by the
// DLL / need another value type, I).
func condTypeByte(condType string) (byte, error) {
	switch condType {
	case "Bid":
		return 0x01, nil
	case "BidOrLast":
		return 0x02, nil
	case "AskOrLast":
		return 0x04, nil
	case "Last":
		return 0, fmt.Errorf("Incorrect condition type.")
	case "Time":
		return 0, fmt.Errorf("transaq: cond_type Time needs a time value, not recovered (I)")
	default:
		return 0, fmt.Errorf("Incorrect condition type.")
	}
}

// NewCondOrder sends newcondorder: the base TRN_NEWORDER type 0x01 with the cond
// fields appended (task-47 offsets). The condition type is encoded in byte 16
// (O, RE-3); cond_value is a price-like Decimal (the Time condition needs a
// time value and is rejected, I).
func (c *Client) NewCondOrder(ctx context.Context, order model.NewCondOrder) (model.TransactionID, error) {
	if order.BuySell != 'B' && order.BuySell != 'S' {
		return 0, fmt.Errorf("Buisell can be only 'B' or 'S'.")
	}
	if order.CondType == "" {
		return 0, fmt.Errorf("You have to specify condition type.")
	}
	condByte, err := condTypeByte(order.CondType)
	if err != nil {
		return 0, err
	}
	if order.ValidAfter == nil {
		return 0, fmt.Errorf("Cannot find validafter param.")
	}
	if order.ValidBefore == nil {
		return 0, fmt.Errorf("Cannot find validbefore param.")
	}
	if order.BrokerRef != "" {
		return 0, fmt.Errorf("transaq: newcondorder brokerref is not encoded (I)")
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
	w.Byte(condByte)
	w.Int32(int32(scale(order.CondValue)))
	w.Int32(int32(order.ValidAfter.Unix()))
	w.Int32(int32(order.ValidBefore.Unix()))
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
			decimals, hasDecimals = pit.Decimals, true
		}
	}
	c.mu.Unlock()
	if !clientOK {
		return records.SecNew{}, model.Board{}, 0, 0, fmt.Errorf("transaq: unknown client %q", client)
	}
	if !hasDecimals {
		return records.SecNew{}, model.Board{}, 0, 0, fmt.Errorf("transaq: pit decimals for %s are not loaded (I)", symbol)
	}
	return sec, board, clientID, decimals, nil
}
