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
	// Short marks a body that ran out before the mask-selected reads ended or
	// a denied order (status 4, resume block not recovered): it is skipped
	// instead of failing the whole snapshot.
	Short    bool
	Order    model.Order
	BoardID  uint16
	ClientID int64 // numeric client id: the DLL drops orders of other clients (O)
}

// R_ORD_NEW body mask bits (D: ReadOrderBody FUN_18039b390, the W after the
// price).
const (
	orderMaskYield      = 0x01  // L yield
	orderMaskValue      = 0x02  // G value; later B + Z12 settlecode
	orderMaskWithdraw   = 0x04  // L withdrawtime
	orderMask08         = 0x08  // L (+0x1b4), not rendered
	orderMask10         = 0x10  // L (+0x14c), B (+0x150), not rendered
	orderMask20         = 0x20  // L (+0x154), G (+0x1b8), not rendered
	orderMask40         = 0x40  // L (+0x38), not rendered
	orderMask80         = 0x80  // B (+0x161), not rendered
	orderMask100        = 0x100 // W, W, not rendered
	orderStatusDenied   = 4     // D: OS_DENIED reads a resume block (not recovered)
	orderStatusRejected = 6     // D: OS_REJECTED reads a Z512 reason
)

// orderReader reads the R_ORD_NEW fields in order and keeps the first error.
type orderReader struct {
	r   *wire.Reader
	err error
}

func (o *orderReader) b() byte {
	if o.err != nil {
		return 0
	}
	v, err := o.r.Byte()
	o.err = err
	return v
}

func (o *orderReader) w() uint16 {
	if o.err != nil {
		return 0
	}
	v, err := o.r.Uint16()
	o.err = err
	return v
}

func (o *orderReader) l() int32 {
	if o.err != nil {
		return 0
	}
	v, err := o.r.Int32()
	o.err = err
	return v
}

func (o *orderReader) g() int64 {
	if o.err != nil {
		return 0
	}
	v, err := o.r.Int64()
	o.err = err
	return v
}

func (o *orderReader) z(max int) string {
	if o.err != nil {
		return ""
	}
	v, err := o.r.String(max)
	o.err = err
	return v
}

// ParseOrderNew decodes one 0x03 body: L order id, then ReadOrderBody (D:
// FUN_18039b390), whose reads depend on the W mask after the price, on the
// condition byte and on the status:
//
//	W secid, W board, G client id, B buysell, B (+0xd), L price, W mask,
//	[mask&1: L yield], L quantity, Z20 brokerref, B condition,
//	[condition: L conditionvalue, L validafter, L validbefore, B within_pos, L (+0x13c)],
//	[mask&2: G value], Z12 client, L accepttime, G orderno, B status, L time,
//	L balance, Z12 (+0x1e5), L (+0x3c), [mask&2: B, Z12 settlecode],
//	[mask&4: L withdrawtime], [mask&8: L], [mask&0x10: L, B], [mask&0x20: L, G],
//	[status 4: B + resume block; status 6: Z512 result], [mask&0x40: L],
//	[mask&0x80: B], B, B, B, B, B, [B(+0x21a)=='R': L, L, L, G, G, G], B, B,
//	[mask&0x100: W, W].
//
// O: order-new-inject.hex (active conditional order, mask 1) and the live
// cancelled order (S, mask 0x116) render byte-identical to the DLL; every
// mapped field was confirmed by the field-mutation oracle (orders.md). The
// L (+0x13c) of the condition block is read only for server protocol >= 0x3f
// in the DLL; both known servers (PRO# 68, 78) pass, so it is always read (I).
// A denied order (status 4) is marked Short: its resume block is not recovered.
func ParseOrderNew(body []byte) (OrderNew, error) {
	var out OrderNew
	var o model.Order
	r := wire.NewReader(body)
	rd := &orderReader{r: r}
	id := rd.l()
	if rd.err != nil {
		return out, fmt.Errorf("R_ORD_NEW order_id: %w", rd.err)
	}
	o.TransactionID = uint64(uint32(id))
	o.SecID = rd.w()
	out.BoardID = rd.w()
	out.ClientID = rd.g()
	if rd.err != nil {
		return out, fmt.Errorf("R_ORD_NEW header: %w", rd.err)
	}
	o.BuySell = rd.b()
	rd.b() // +0xd, not rendered (I)
	o.Price = model.Decimal{Units: int64(rd.l()), Scale: 2}
	mask := rd.w()
	short := func() (OrderNew, error) {
		out.Short = true
		out.Order = o
		return out, nil
	}
	if rd.err != nil {
		return short()
	}
	if mask&orderMaskYield != 0 {
		o.Yield = model.Decimal{Units: int64(rd.l()), Scale: 2}
	}
	o.Quantity = int64(rd.l())
	o.BrokerRef = rd.z(20)
	o.Condition = int(rd.b())
	if o.Condition != 0 {
		o.ConditionValue = model.Decimal{Units: int64(rd.l()), Scale: 2}
		o.ValidAfter = rd.l()
		o.ValidBefore = rd.l()
		o.WithinPos = rd.b() != 0 // I: the O fixture has 1 and renders true
		rd.l()
	}
	if mask&orderMaskValue != 0 {
		o.Value = model.Decimal{Units: rd.g(), Scale: 2}
	}
	o.Client = rd.z(12)
	o.AcceptTime = rd.l()
	o.OrderNo = uint64(rd.g())
	o.Status = int(rd.b())
	o.Time = rd.l()
	o.Balance = int64(rd.l())
	rd.z(12)
	rd.l()
	if mask&orderMaskValue != 0 {
		rd.b()
		o.SettleCode = rd.z(12)
	}
	if mask&orderMaskWithdraw != 0 {
		o.WithdrawTime = rd.l()
	}
	if mask&orderMask08 != 0 {
		rd.l()
	}
	if mask&orderMask10 != 0 {
		rd.l()
		rd.b()
	}
	if mask&orderMask20 != 0 {
		rd.l()
		rd.g()
	}
	switch o.Status {
	case orderStatusDenied:
		return short()
	case orderStatusRejected:
		o.Result = rd.z(512)
	}
	if mask&orderMask40 != 0 {
		rd.l()
	}
	if mask&orderMask80 != 0 {
		rd.b()
	}
	rd.b()
	rd.b()
	rd.b()
	rd.b()
	if rd.b() == 'R' {
		rd.l()
		rd.l()
		rd.l()
		rd.g()
		rd.g()
		rd.g()
	}
	rd.b()
	rd.b()
	if mask&orderMask100 != 0 {
		rd.w()
		rd.w()
	}
	if rd.err != nil {
		return short()
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
