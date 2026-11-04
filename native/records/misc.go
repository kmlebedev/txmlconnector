package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Remaining RQ_MAIN records of task 12. None of them produces XML callbacks
// (docs/protocol/records.md, RE-1): they only update dictionaries/counters, so
// the splitter reads them (to stay aligned) and yields no events.
const (
	TagBoard        byte = 0x34 // R_BOARD, sized
	TagFortsSession byte = 0x6b // R_FORTS_SESSION
	TagEdpRate      byte = 0x6e // R_EDP_RATE (also nested inside R_WRAPPER)
	TagAmBook       byte = 0x5c // AmBOOK
	TagReadICA      byte = 0x7c // ReadICA, sized
)

// EdpRate is the R_EDP_RATE body: Z4 currency pair part, Z25 name, B, L rate.
type EdpRate struct {
	Pair string
	Name string
	Byte byte
	Rate int32
}

// ParseEdpRate decodes R_EDP_RATE (0x6e, docs/protocol/records.md).
func ParseEdpRate(body []byte) (EdpRate, error) {
	var r EdpRate
	rd := wire.NewReader(body)
	var err error
	if r.Pair, err = rd.String(4); err != nil {
		return r, fmt.Errorf("R_EDP_RATE pair: %w", err)
	}
	if r.Name, err = rd.String(25); err != nil {
		return r, fmt.Errorf("R_EDP_RATE name: %w", err)
	}
	if r.Byte, err = rd.Byte(); err != nil {
		return r, fmt.Errorf("R_EDP_RATE byte: %w", err)
	}
	if r.Rate, err = rd.Int32(); err != nil {
		return r, fmt.Errorf("R_EDP_RATE rate: %w", err)
	}
	return r, nil
}

// AmBook is the AmBOOK body: W secid, B, B, L, L, Z4, L (D; not observed with
// callbacks). The fields after the ids are not recovered (I).
type AmBook struct {
	SecID uint16
	Byte1 byte
	Byte2 byte
	Int1  int32
	Int2  int32
	Text  string
	Int3  int32
}

// ParseAmBook decodes AmBOOK (0x5c, FUN_1803bea70).
func ParseAmBook(body []byte) (AmBook, error) {
	var a AmBook
	r := wire.NewReader(body)
	var err error
	if a.SecID, err = r.Uint16(); err != nil {
		return a, fmt.Errorf("AmBOOK secid: %w", err)
	}
	if a.Byte1, err = r.Byte(); err != nil {
		return a, fmt.Errorf("AmBOOK byte1: %w", err)
	}
	if a.Byte2, err = r.Byte(); err != nil {
		return a, fmt.Errorf("AmBOOK byte2: %w", err)
	}
	if a.Int1, err = r.Int32(); err != nil {
		return a, fmt.Errorf("AmBOOK int1: %w", err)
	}
	if a.Int2, err = r.Int32(); err != nil {
		return a, fmt.Errorf("AmBOOK int2: %w", err)
	}
	if a.Text, err = r.String(4); err != nil {
		return a, fmt.Errorf("AmBOOK text: %w", err)
	}
	if a.Int3, err = r.Int32(); err != nil {
		return a, fmt.Errorf("AmBOOK int3: %w", err)
	}
	return a, nil
}

// ParseFortsSession decodes R_FORTS_SESSION (0x6b): L session. The optional
// trailing byte of the handler is not read: the live E07 records carry only the
// packed session (D/I, docs/protocol/records.md).
func ParseFortsSession(body []byte) (int32, error) {
	r := wire.NewReader(body)
	session, err := r.Int32()
	if err != nil {
		return 0, fmt.Errorf("R_FORTS_SESSION session: %w", err)
	}
	return session, nil
}
