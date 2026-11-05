package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// ServerMessage is one R_RES 0x0f body (RE-2 task-68): B trn_type, B trr_status,
// Z512 CP1251 C-string text, packed-L transactionid. Types 1/2 end there; other
// tails are not recovered (I).
type ServerMessage struct {
	Type          byte
	Status        byte
	Text          string // decoded to UTF-8
	TransactionID int64
}

// ParseServerMessage decodes one 0x0f body.
func ParseServerMessage(body []byte) (ServerMessage, error) {
	var out ServerMessage
	r := wire.NewReader(body)
	var err error
	if out.Type, err = r.Byte(); err != nil {
		return out, fmt.Errorf("R_RES type: %w", err)
	}
	if out.Status, err = r.Byte(); err != nil {
		return out, fmt.Errorf("R_RES status: %w", err)
	}
	text, err := readCString(r, 512)
	if err != nil {
		return out, fmt.Errorf("R_RES text: %w", err)
	}
	out.Text = DecodeCP1251(text)
	if out.TransactionID, err = r.Int64(); err != nil {
		return out, fmt.Errorf("R_RES transactionid: %w", err)
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("%w: R_RES tail %d bytes (I)", ErrUnknownRecord, r.Len())
	}
	if (out.Type != 1 && out.Type != 2) || out.Status == 4 {
		return out, fmt.Errorf("%w: R_RES type=%d status=%d tail is I", ErrUnknownRecord, out.Type, out.Status)
	}
	return out, nil
}
