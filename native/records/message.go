package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TagMessage is R_MSG (0x01): an unsized record with variable C-strings
// (messages.md, D/O).
const TagMessage byte = 0x01

// RMsg is one R_MSG body: B flags, Z20 from, L packed date (server wall Unix
// seconds), Z1024 text.
type RMsg struct {
	Flags byte
	From  string
	Date  int64
	Text  string
}

// ParseRMsg decodes one R_MSG body.
func ParseRMsg(body []byte) (RMsg, error) {
	var out RMsg
	r := wire.NewReader(body)
	flags, err := r.Byte()
	if err != nil {
		return out, fmt.Errorf("R_MSG flags: %w", err)
	}
	from, err := readCString(r, 20)
	if err != nil {
		return out, fmt.Errorf("R_MSG from: %w", err)
	}
	date, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_MSG date: %w", err)
	}
	text, err := readCString(r, 1024)
	if err != nil {
		return out, fmt.Errorf("R_MSG text: %w", err)
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("R_MSG: %d trailing bytes", r.Len())
	}
	out.Flags, out.From, out.Date, out.Text = flags, from, int64(date), text
	return out, nil
}

// SkipMessage consumes one R_MSG body from the splitter reader.
func SkipMessage(r *wire.Reader) error {
	if _, err := r.Byte(); err != nil {
		return fmt.Errorf("R_MSG flags: %w", err)
	}
	if _, err := readCString(r, 20); err != nil {
		return fmt.Errorf("R_MSG from: %w", err)
	}
	if _, err := r.Int32(); err != nil {
		return fmt.Errorf("R_MSG date: %w", err)
	}
	if _, err := readCString(r, 1024); err != nil {
		return fmt.Errorf("R_MSG text: %w", err)
	}
	return nil
}

// TagServerMessage is R_SRVMSG (0x0f): server text messages in CP1251
// (RE-2 capture; the exact tail fields are I).
const TagServerMessage byte = 0x0f
