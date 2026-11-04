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
	TagSysParams    byte = 0x5b // R_SYSPARAMS: L size + size-byte blob
	TagState        byte = 0x05 // empty state record (dispatcher case 4)
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

// AmBookLevel is one level of the AmBOOK loop. The flags byte selects which
// optional fields follow (bits 1,2,4,8,16 -> B, L, L, Z4, L; I).
type AmBookLevel struct {
	Flags byte
	Byte  byte
	Int1  int32
	Int2  int32
	Text  string
	Int3  int32
}

// AmBook is the AmBOOK body: W secid then the level loop; 0xff ends the record
// (RE-1 FUN_1803bea70; live r10 body 20 86 ff = an empty book).
type AmBook struct {
	SecID  uint16
	Levels []AmBookLevel
}

// ParseAmBook decodes AmBOOK (0x5c).
func ParseAmBook(body []byte) (AmBook, error) {
	var a AmBook
	r := wire.NewReader(body)
	var err error
	if a.SecID, err = r.Uint16(); err != nil {
		return a, fmt.Errorf("AmBOOK secid: %w", err)
	}
	for {
		flags, err := r.Byte()
		if err != nil {
			return a, fmt.Errorf("AmBOOK flags: %w", err)
		}
		if flags == 0xff {
			return a, nil
		}
		if flags&^0x1f != 0 {
			return a, fmt.Errorf("AmBOOK: unsupported flags %#02x", flags)
		}
		level := AmBookLevel{Flags: flags}
		if flags&0x01 != 0 {
			if level.Byte, err = r.Byte(); err != nil {
				return a, fmt.Errorf("AmBOOK byte: %w", err)
			}
		}
		if flags&0x02 != 0 {
			if level.Int1, err = r.Int32(); err != nil {
				return a, fmt.Errorf("AmBOOK int1: %w", err)
			}
		}
		if flags&0x04 != 0 {
			if level.Int2, err = r.Int32(); err != nil {
				return a, fmt.Errorf("AmBOOK int2: %w", err)
			}
		}
		if flags&0x08 != 0 {
			if level.Text, err = r.String(4); err != nil {
				return a, fmt.Errorf("AmBOOK text: %w", err)
			}
		}
		if flags&0x10 != 0 {
			if level.Int3, err = r.Int32(); err != nil {
				return a, fmt.Errorf("AmBOOK int3: %w", err)
			}
		}
		a.Levels = append(a.Levels, level)
	}
}

// ParseFortsSession decodes R_FORTS_SESSION (0x6b): L session. The optional
// trailing B (read by the DLL only "if bytes remain", records.md:37) is not
// read: the live E07 records carry only the packed session, and reading it
// unconditionally would swallow the next record's tag (I). A future RE pass can
// pin the condition.
func ParseFortsSession(body []byte) (int32, error) {
	r := wire.NewReader(body)
	session, err := r.Int32()
	if err != nil {
		return 0, fmt.Errorf("R_FORTS_SESSION session: %w", err)
	}
	return session, nil
}
