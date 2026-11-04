package native

import (
	"context"
	"errors"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

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

// NewStopOrder and NewCondOrder are not implemented yet: the oracle payloads
// (transact-newstoporder/newcondorder-payload.hex) are captured, but the exact
// stop/cond field offsets are I (RE task-43), so the builders are pending.
func (c *Client) NewStopOrder(ctx context.Context, order model.NewStopOrder) (model.TransactionID, error) {
	return 0, errors.New("transaq: newstoporder payload offsets are not recovered (I)")
}

// NewCondOrder is pending the same RE offsets as NewStopOrder.
func (c *Client) NewCondOrder(ctx context.Context, order model.NewCondOrder) (model.TransactionID, error) {
	return 0, errors.New("transaq: newcondorder payload offsets are not recovered (I)")
}
