package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TagStopOrder is R_STOP (0x54, sized), the conditional/stop order record.
const TagStopOrder byte = 0x54

// StopOrder is one decoded R_STOP body (orders.md, D+O).
type StopOrder struct {
	StopOrderID uint64
	Mask        uint64
	SecID       uint16
	HasSec      bool
	BoardID     uint16
	ClientID    int64
	HasClient   bool
	BuySell     byte
	Status      int
	HasStatus   bool
}

// stopStatusResult is the DLL <result> for a stop order without a status.
const stopStatusResult = "{50} Статус заявки не определен."

// ParseStopOrder decodes one 0x54 body: L stop_order_id, L mask, then the fields
// selected by the mask in the documented order (orders.md R_STOP).
func ParseStopOrder(body []byte) (StopOrder, error) {
	var out StopOrder
	r := wire.NewReader(body)
	id, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_STOP id: %w", err)
	}
	out.StopOrderID = uint64(uint32(id))
	mask, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_STOP mask: %w", err)
	}
	out.Mask = uint64(uint32(mask))
	type reader struct {
		bit  uint64
		read func() error
	}
	var readers []reader
	readers = append(readers,
		reader{1, func() error { status, err := r.Byte(); out.Status, out.HasStatus = int(status), true; return err }},
		reader{2, func() error { _, err := r.Int32(); return err }},
		reader{4, func() error { _, err := r.Int32(); return err }},
		reader{8, func() error { _, err := r.Int32(); return err }},
		reader{0x10, func() error { _, err := r.String(12); return err }},
		reader{0x20, func() error { _, err := r.Int64(); return err }},
		reader{0x40, func() error { _, err := r.Int64(); return err }},
		reader{0x100, func() error {
			sec, err := r.Uint16()
			out.SecID, out.HasSec = sec, true
			return err
		}},
		reader{0x200, func() error {
			board, err := r.Uint16()
			out.BoardID = board
			return err
		}},
		reader{0x400, func() error {
			client, err := r.Int32()
			out.ClientID, out.HasClient = int64(client), true
			return err
		}},
		reader{0x800, func() error {
			side, err := r.Byte()
			out.BuySell = side
			return err
		}},
		reader{0x1000, func() error { _, err := r.Uint16(); return err }},
		reader{0x2000, func() error { _, err := r.Int32(); return err }},
		reader{0x4000, func() error { _, err := r.String(12); return err }},
		reader{0x8000, func() error { _, err := r.Int32(); return err }},
	)
	for _, rd := range readers {
		if out.Mask&rd.bit == 0 {
			continue
		}
		if err := rd.read(); err != nil {
			return out, fmt.Errorf("R_STOP mask %#x: %w", rd.bit, err)
		}
	}
	if out.Mask&0x80 != 0 {
		return out, fmt.Errorf("R_STOP: status block mask 0x80 is not recovered (I)")
	}
	if out.Mask&^uint64(0xffff) != 0 {
		return out, fmt.Errorf("R_STOP: mask bits above 0x8000 are not recovered (I)")
	}
	return out, nil
}

// Event builds the typed stop order; the client/author fields are filled by the
// decoder (numeric client -> account code, author = session login).
func (s StopOrder) Event(cat *Catalog) model.StopOrder {
	ev := model.StopOrder{
		TransactionID: s.StopOrderID,
		SecID:         s.SecID,
		BuySell:       s.BuySell,
		Status:        s.Status,
		Result:        stopStatusResult,
	}
	if cat != nil {
		if s.HasSec {
			if sec, ok := cat.Security(s.SecID); ok {
				ev.SecCode = sec.SecCode
			}
		}
		if board, ok := cat.Board(s.BoardID); ok {
			ev.Board = board.Code
		}
	}
	if s.HasStatus {
		ev.Result = ""
	}
	return ev
}
