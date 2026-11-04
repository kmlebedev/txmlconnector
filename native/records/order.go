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
	Order   model.Order
	BoardID uint16
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
	if _, err := r.Int64(); err != nil { // numeric client id (filter, not rendered)
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
		return out, fmt.Errorf("R_ORD_NEW price: %w", err)
	}
	o.Price = model.Decimal{Units: int64(price), Scale: 2}
	if _, err := r.Uint16(); err != nil { // field 7 is not rendered (I)
		return out, fmt.Errorf("R_ORD_NEW field7: %w", err)
	}
	yield, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW yield: %w", err)
	}
	o.Yield = model.Decimal{Units: int64(yield), Scale: 2}
	quantity, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW quantity: %w", err)
	}
	o.Quantity = int64(quantity)
	if o.Client, err = r.String(20); err != nil {
		return out, fmt.Errorf("R_ORD_NEW client: %w", err)
	}
	// hidden byte (not rendered; the oracle fixture shows 0) then the mapped
	// condition fields.
	if _, err := r.Byte(); err != nil {
		return out, fmt.Errorf("R_ORD_NEW hidden: %w", err)
	}
	condValue, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW conditionvalue: %w", err)
	}
	o.ConditionValue = model.Decimal{Units: int64(condValue), Scale: 2}
	if o.ValidAfter, err = r.Int32(); err != nil {
		return out, fmt.Errorf("R_ORD_NEW validafter: %w", err)
	}
	if o.ValidBefore, err = r.Int32(); err != nil {
		return out, fmt.Errorf("R_ORD_NEW validbefore: %w", err)
	}
	if _, err := r.Byte(); err != nil {
		return out, fmt.Errorf("R_ORD_NEW: %w", err)
	}
	if _, err := r.Int32(); err != nil { // maxcomission (rendered as 0.0 in the fixture, I)
		return out, fmt.Errorf("R_ORD_NEW maxcomission: %w", err)
	}
	if _, err := r.Int64(); err != nil { // numeric client id duplicate (I)
		return out, fmt.Errorf("R_ORD_NEW: %w", err)
	}
	if o.BrokerRef, err = r.String(12); err != nil {
		return out, fmt.Errorf("R_ORD_NEW brokerref: %w", err)
	}
	if o.AcceptTime, err = r.Int32(); err != nil {
		return out, fmt.Errorf("R_ORD_NEW accepttime: %w", err)
	}
	orderNo, err := r.Int64()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW orderno: %w", err)
	}
	o.OrderNo = uint64(orderNo)
	cond, err := r.Byte()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW condition: %w", err)
	}
	o.Condition = int(cond)
	// Orders table (docs/protocol/orders.md, D+O): field 22 is <time> (seconds
	// of day) and field 23 is <balance>.
	timeRaw, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW time: %w", err)
	}
	o.Time = timeRaw
	balance, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_ORD_NEW balance: %w", err)
	}
	o.Balance = int64(balance)
	// The remaining reads of the D body are not mapped (I); the record is
	// sized, so skip the rest.
	if r.Len() > 0 {
		if _, err := r.Raw(r.Len()); err != nil {
			return out, fmt.Errorf("R_ORD_NEW tail: %w", err)
		}
	}
	out.Order = o
	return out, nil
}

// OrderModify is one decoded R_ORD_MODIFY: the target transaction id and the
// patch to apply to the stored order.
type OrderModify struct {
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
// selected by the mask (D/O). Only the status branch (mask&1) is proven live;
// the other branches are consumed from their D types so the record stays
// aligned, without changing the render (I).
func ParseOrderModify(body []byte) (OrderModify, error) {
	var out OrderModify
	r := wire.NewReader(body)
	id, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_ORD_MODIFY order_id: %w", err)
	}
	maskBytes, err := r.Raw(4)
	if err != nil {
		return out, fmt.Errorf("R_ORD_MODIFY mask: %w", err)
	}
	mask := uint32(maskBytes[0])<<24 | uint32(maskBytes[1])<<16 | uint32(maskBytes[2])<<8 | uint32(maskBytes[3])
	out.TransactionID = uint64(uint32(id))
	out.Apply = func(*model.Order) {}
	if mask&1 != 0 {
		status, err := r.Byte()
		if err != nil {
			return out, fmt.Errorf("R_ORD_MODIFY status: %w", err)
		}
		out.Apply = func(o *model.Order) {
			o.Status = int(status)
			o.Result = orderStatusResult(int(status))
		}
	}
	skip := func(bit uint32, read func() error) error {
		if mask&bit == 0 {
			return nil
		}
		if err := read(); err != nil {
			return fmt.Errorf("R_ORD_MODIFY mask %#x: %w", bit, err)
		}
		return nil
	}
	_ = skip
	readers := []struct {
		bit  uint32
		read func() error
	}{
		{2, func() error { _, err := r.Int64(); return err }},
		{4, func() error { _, err := r.Int32(); return err }},
		{8, func() error { _, err := r.Int32(); return err }},
		{0x10, func() error { _, err := r.Int32(); return err }},
		{0x20, func() error { _, err := r.Int32(); return err }},
		{0x40, func() error { _, err := r.String(12); return err }},
		{0x80, func() error { _, err := r.Int32(); return err }},
		{0x100, func() error { _, err := r.Int32(); return err }},
		{0x200, func() error { _, err := r.Int32(); return err }},
	}
	for _, rd := range readers {
		if mask&rd.bit == 0 {
			continue
		}
		if err := rd.read(); err != nil {
			return out, fmt.Errorf("R_ORD_MODIFY mask %#x: %w", rd.bit, err)
		}
	}
	if mask&0x400 != 0 {
		// Status-dependent special block (D): not recovered (I), fail clearly.
		return out, fmt.Errorf("R_ORD_MODIFY: special block mask 0x400 is not recovered (I)")
	}
	return out, nil
}
