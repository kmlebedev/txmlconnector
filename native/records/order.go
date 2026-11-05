package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// R_ORD_NEW (0x03) and R_ORD_MODIFY (0x10) are sized records; the DLL answers
// them with the <orders> callback and the body layouts are D+O (orders.md).
const (
	TagOrderNew    byte = 0x03
	TagOrderModify byte = 0x10
)

// OrderNew is one decoded R_ORD_NEW body plus the wire board number the catalog
// needs for the board code.
type OrderNew struct {
	// Short marks a cancelled/removed/denied/rejected order whose R_ORD_NEW
	// body is shorter than the linear 52-read scheme (D status branches not
	// recovered): it is skipped instead of failing the whole snapshot (I).
	Short    bool
	Order    model.Order
	BoardID  uint16
	ClientID int64 // numeric client id: the DLL drops orders of other clients (O)
}

// ParseOrderNew decodes one 0x03 body. The wire fields are the oracle-confirmed
// part of the 52-read body (orders.md / M6 experiment): order id, secid, board,
// numeric client id, buysell, status, price, time, yield, quantity, client,
// condition value/after/before, brokerref, accepttime, orderno, condition and
// balance. The rest of the body is consumed without mapping (I).
func ParseOrderNew(body []byte) (OrderNew, error) {
	var out OrderNew
	var o model.Order
	r := wire.NewReader(body)
	id, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW order_id: %w", err)
	}
	o.TransactionID = uint64(uint32(id))
	if secID, err := r.Uint16(); err != nil {
		return out, fmt.Errorf("R_ORD_NEW secid: %w", err)
	} else {
		o.SecID = secID
	}
	boardID, err := r.Uint16()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW board: %w", err)
	}
	out.BoardID = boardID
	if out.ClientID, err = r.Int64(); err != nil {
		return out, fmt.Errorf("R_ORD_NEW client id: %w", err)
	}
	if o.BuySell, err = r.Byte(); err != nil {
		return out, fmt.Errorf("R_ORD_NEW buysell: %w", err)
	}
	status, err := r.Byte()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW status: %w", err)
	}
	o.Status = int(status)
	price, err := r.Int32()
	if err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	o.Price = model.Decimal{Units: int64(price), Scale: 2}
	if _, err := r.Uint16(); err != nil { // field 7 is not rendered (I)
		out.Short = true
		out.Order = o
		return out, nil
	}
	yield, err := r.Int32()
	if err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	o.Yield = model.Decimal{Units: int64(yield), Scale: 2}
	quantity, err := r.Int32()
	if err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	o.Quantity = int64(quantity)
	if o.Client, err = r.String(20); err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	// hidden byte (not rendered; the oracle fixture shows 0) then the mapped
	// condition fields.
	if _, err := r.Byte(); err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	condValue, err := r.Int32()
	if err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	o.ConditionValue = model.Decimal{Units: int64(condValue), Scale: 2}
	if o.ValidAfter, err = r.Int32(); err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	if o.ValidBefore, err = r.Int32(); err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	if _, err := r.Byte(); err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	if _, err := r.Int32(); err != nil { // maxcomission (rendered as 0.0 in the fixture, I)
		out.Short = true
		out.Order = o
		return out, nil
	}
	if _, err := r.Int64(); err != nil { // numeric client id duplicate (I)
		out.Short = true
		out.Order = o
		return out, nil
	}
	if o.BrokerRef, err = r.String(12); err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	if o.AcceptTime, err = r.Int32(); err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	orderNo, err := r.Int64()
	if err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	o.OrderNo = uint64(orderNo)
	cond, err := r.Byte()
	if err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	o.Condition = int(cond)
	// Orders table (docs/protocol/orders.md, D+O): field 22 is <time> (seconds
	// of day) and field 23 is <balance>.
	timeRaw, err := r.Int32()
	if err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	o.Time = timeRaw
	balance, err := r.Int32()
	if err != nil {
		out.Short = true
		out.Order = o
		return out, nil
	}
	o.Balance = int64(balance)
	// The remaining reads of the D body are not mapped (I); the record is
	// sized, so skip the rest.
	if r.Len() > 0 {
		if _, err := r.Raw(r.Len()); err != nil {
			out.Short = true
			out.Order = o
			return out, nil
		}
	}
	out.Order = o
	return out, nil
}

// OrderModify is one decoded R_ORD_MODIFY: the target transaction id and the
// patch to apply to the stored order.
type OrderModify struct {
	// Short marks a body whose mask-selected fields ran out; the update is
	// skipped instead of failing the load (I).
	Short         bool
	TransactionID uint64
	Apply         func(*model.Order)
}

// orderStatusResult is the DLL <result> text per status; only the disabled case
// is oracle-proven, the rest is I.
func orderStatusResult(status int) string {
	if status == 2 {
		return "{37} Заявка отменена"
	}
	return ""
}

// ParseOrderModify decodes one 0x10 body: L order id, BE32 mask, then the fields
// selected by the mask (D). Only the status branch is proven live; the other
// branches are applied by the D order (price/quantity/balance/... are I) so a
// valid update never aborts the poll.
func ParseOrderModify(body []byte) (OrderModify, error) {
	var out OrderModify
	r := wire.NewReader(body)
	id, err := r.Int32()
	if err != nil {
		out.Short = true
		return out, nil
	}
	maskBytes, err := r.Raw(4)
	if err != nil {
		out.Short = true
		return out, nil
	}
	mask := uint32(maskBytes[0])<<24 | uint32(maskBytes[1])<<16 | uint32(maskBytes[2])<<8 | uint32(maskBytes[3])
	if mask&^uint32(0x7ff) != 0 {
		out.Short = true
		return out, nil
	}
	out.TransactionID = uint64(uint32(id))
	out.Apply = func(*model.Order) {}
	apply := func(fn func(*model.Order)) {
		prev := out.Apply
		out.Apply = func(o *model.Order) { prev(o); fn(o) }
	}
	if mask&1 != 0 {
		status, err := r.Byte()
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) {
			o.Status = int(status)
			o.Result = orderStatusResult(int(status))
		})
	}
	if mask&2 != 0 {
		value, err := r.Int64()
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) { o.OrderNo = uint64(value) })
	}
	if mask&4 != 0 {
		value, err := r.Int32()
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) { o.Price = model.Decimal{Units: int64(value), Scale: 2} })
	}
	if mask&8 != 0 {
		value, err := r.Int32()
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) { o.Quantity = int64(value) })
	}
	if mask&0x10 != 0 {
		value, err := r.Int32()
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) { o.Balance = int64(value) })
	}
	if mask&0x20 != 0 {
		value, err := r.Int32()
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) { o.ConditionValue = model.Decimal{Units: int64(value), Scale: 2} })
	}
	if mask&0x40 != 0 {
		value, err := r.String(12)
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) { o.BrokerRef = value })
	}
	if mask&0x80 != 0 {
		value, err := r.Int32()
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) { o.ValidAfter = value })
	}
	if mask&0x100 != 0 {
		value, err := r.Int32()
		if err != nil {
			out.Short = true
			return out, nil
		}
		apply(func(o *model.Order) { o.ValidBefore = value })
	}
	if mask&0x200 != 0 {
		if _, err := r.Int32(); err != nil { // maxcomission, not rendered yet (I)
			out.Short = true
			return out, nil
		}
	}
	if mask&0x400 != 0 {
		// Status-dependent special block (D): a B flag and, when set, a Z512
		// string. It is a valid record, not an error; the content is I.
		flag, err := r.Byte()
		if err != nil {
			out.Short = true
			return out, nil
		}
		if flag != 0 {
			if _, err := r.String(512); err != nil {
				out.Short = true
				return out, nil
			}
		}
	}
	if r.Len() > 0 {
		if _, err := r.Raw(r.Len()); err != nil {
			out.Short = true
			return out, nil
		}
	}
	return out, nil
}
