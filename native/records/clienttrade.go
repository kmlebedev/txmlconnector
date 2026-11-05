package records

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TagClientTrade is R_TRD (0x04), the client <trades> record (sized).
const TagClientTrade byte = 0x04

// ClientTrade is one decoded R_TRD body: the D layout of FUN_1803c6990 with the
// oracle-confirmed fields rendered by <trades> (orders.md).
type ClientTrade struct {
	SecID      uint16
	BoardID    uint16
	ClientID   int64
	TradeType  byte
	TradeNo    uint64
	OrderNo    uint64
	Time       int32
	Price      int64
	Mask       uint16
	Quantity   int64
	Value      int64
	Comission  int64
	BrokerRef  string
	SettleCode string
	BuySell    byte
	Accrued    float64
	Bypass     byte
}

// ParseClientTrade decodes one R_TRD body. The mask-selected block is consumed
// from its D types to keep the record aligned; the fields it carries are not
// rendered by the oracle-confirmed XML yet (I).
func ParseClientTrade(body []byte) (ClientTrade, error) {
	var t ClientTrade
	r := wire.NewReader(body)
	var err error
	if t.SecID, err = r.Uint16(); err != nil {
		return t, fmt.Errorf("R_TRD secid: %w", err)
	}
	if t.BoardID, err = r.Uint16(); err != nil {
		return t, fmt.Errorf("R_TRD board: %w", err)
	}
	if t.ClientID, err = r.Int64(); err != nil {
		return t, fmt.Errorf("R_TRD client id: %w", err)
	}
	if t.TradeType, err = r.Byte(); err != nil {
		return t, fmt.Errorf("R_TRD tradetype: %w", err)
	}
	tradeNo, err := r.Int64()
	if err != nil {
		return t, fmt.Errorf("R_TRD tradeno: %w", err)
	}
	t.TradeNo = uint64(tradeNo)
	orderNo, err := r.Int64()
	if err != nil {
		return t, fmt.Errorf("R_TRD orderno: %w", err)
	}
	t.OrderNo = uint64(orderNo)
	if t.Time, err = r.Int32(); err != nil {
		return t, fmt.Errorf("R_TRD time: %w", err)
	}
	if _, err := r.Byte(); err != nil {
		return t, fmt.Errorf("R_TRD byte: %w", err)
	}
	if _, err := r.String(20); err != nil {
		return t, fmt.Errorf("R_TRD seccode: %w", err)
	}
	price, err := r.Int32()
	if err != nil {
		return t, fmt.Errorf("R_TRD price: %w", err)
	}
	t.Price = int64(price)
	mask, err := r.Uint16()
	if err != nil {
		return t, fmt.Errorf("R_TRD mask: %w", err)
	}
	t.Mask = mask
	if mask&^uint16(0x1ff) != 0 {
		// The D mask fields cover bits 1..0x100; a higher bit would leave the
		// body misaligned, so fail explicitly instead of silently misparsing.
		return t, fmt.Errorf("R_TRD: unknown mask bits %#x (I)", mask&^uint16(0x1ff))
	}
	// Mask-selected values (types per orders.md); they are not rendered by the
	// oracle-confirmed XML yet (I), but a valid record must never abort the poll.
	skip := func(bit uint16, read func() error) error {
		if mask&bit == 0 {
			return nil
		}
		return read()
	}
	reads := []struct {
		bit  uint16
		read func() error
	}{
		{0x1, func() error {
			if _, err := r.Int32(); err != nil {
				return err
			}
			if _, err := r.Int64(); err != nil {
				return err
			}
			_, err := r.Int64()
			return err
		}},
		{0x2, func() error {
			for i := 0; i < 3; i++ {
				if _, err := r.Int32(); err != nil {
					return err
				}
			}
			return nil
		}},
		{0x4, func() error {
			for i := 0; i < 3; i++ {
				if _, err := r.Int32(); err != nil {
					return err
				}
			}
			for i := 0; i < 3; i++ {
				if _, err := r.Int64(); err != nil {
					return err
				}
			}
			for i := 0; i < 4; i++ {
				if _, err := r.Int32(); err != nil {
					return err
				}
			}
			_, err := r.Byte()
			return err
		}},
		{0x8, func() error {
			if _, err := r.Int32(); err != nil {
				return err
			}
			if _, err := r.Int32(); err != nil {
				return err
			}
			_, err := r.Int64()
			return err
		}},
		{0x10, func() error {
			if _, err := r.Uint16(); err != nil {
				return err
			}
			_, err := r.Int64()
			return err
		}},
		{0x20, func() error { _, err := r.Int32(); return err }},
		{0x40, func() error { _, err := r.Byte(); return err }},
	}
	for _, rd := range reads {
		if err := skip(rd.bit, rd.read); err != nil {
			return t, fmt.Errorf("R_TRD mask %#x: %w", rd.bit, err)
		}
	}
	quantity, err := r.Int64()
	if err != nil {
		return t, fmt.Errorf("R_TRD quantity: %w", err)
	}
	t.Quantity = quantity
	if t.Value, err = r.Int64(); err != nil {
		return t, fmt.Errorf("R_TRD value: %w", err)
	}
	if t.Comission, err = r.Int64(); err != nil {
		return t, fmt.Errorf("R_TRD comission: %w", err)
	}
	if t.BrokerRef, err = r.String(12); err != nil {
		return t, fmt.Errorf("R_TRD brokerref: %w", err)
	}
	if t.SettleCode, err = r.String(12); err != nil {
		return t, fmt.Errorf("R_TRD settlecode: %w", err)
	}
	if t.BuySell, err = r.Byte(); err != nil {
		return t, fmt.Errorf("R_TRD buysell: %w", err)
	}
	if _, err := r.Byte(); err != nil {
		return t, fmt.Errorf("R_TRD: %w", err)
	}
	if _, err := r.Byte(); err != nil {
		return t, fmt.Errorf("R_TRD: %w", err)
	}
	raw, err := r.Raw(8)
	if err != nil {
		return t, fmt.Errorf("R_TRD double: %w", err)
	}
	t.Accrued = math.Float64frombits(binary.LittleEndian.Uint64(raw))
	if t.Bypass, err = r.Byte(); err != nil {
		return t, fmt.Errorf("R_TRD bypass: %w", err)
	}
	if _, err := r.Int64(); err != nil {
		return t, fmt.Errorf("R_TRD: %w", err)
	}
	if mask&0x80 != 0 {
		if _, err := r.Uint16(); err != nil {
			return t, fmt.Errorf("R_TRD mask 0x80: %w", err)
		}
	}
	if mask&0x100 != 0 {
		if _, err := r.String(12); err != nil {
			return t, fmt.Errorf("R_TRD mask 0x100: %w", err)
		}
	}
	if r.Len() > 0 {
		if _, err := r.Raw(r.Len()); err != nil {
			return t, fmt.Errorf("R_TRD tail: %w", err)
		}
	}
	return t, nil
}

func skipN(r *wire.Reader, n int) error {
	_, err := r.Raw(n)
	return err
}

// Event builds the typed client trade; the board code and the secid->seccode
// mapping come from the catalog, the client code is filled by the decoder from
// the account store.
func (t ClientTrade) Event(cat *Catalog) model.ClientTrade {
	ev := model.ClientTrade{
		SecID: t.SecID, TradeNo: t.TradeNo, OrderNo: t.OrderNo,
		BrokerRef: t.BrokerRef, SettleCode: t.SettleCode,
		TradeType: t.TradeType, BuySell: t.BuySell, Time: int64(t.Time),
		Price:     model.Decimal{Units: t.Price, Scale: 2},
		Value:     model.Decimal{Units: t.Value, Scale: 2},
		Comission: model.Decimal{Units: t.Comission, Scale: 2},
		Quantity:  t.Quantity, Items: t.Quantity,
		// P2: no wire field for currentpos was recovered (the oracle value equals
		// -quantity by coincidence); keep 0 instead of fabricating (I).
		CurrentPos: 0,
		Yield:      model.Decimal{},
		AccruedInt: model.Decimal{Units: int64(math.Round(t.Accrued * 100)), Scale: 2},
		Bypass:     t.Bypass,
	}
	if cat != nil {
		if sec, ok := cat.Security(t.SecID); ok {
			ev.SecCode = sec.SecCode
		}
		if board, ok := cat.Board(t.BoardID); ok {
			ev.Board = board.Code
		}
	}
	return ev
}
