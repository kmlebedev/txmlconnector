package records

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Pit is a per-board parameter block inside R_SEC_NEW.
type Pit struct {
	Board      uint16 // board number from RQ_STARTUP
	LotSize    int32
	LotDivider int32 // present for server protocol > 0x42 (demo: 68)
	MinStep    int32 // in units of 10^-Decimals
	Decimals   byte
	Byte1      byte
	PointCost  float64
	Text       string
	Default    bool // the board reported as <board> of the security
	Int1       int32
	Currency   string
}

// SecNew is an R_SEC_NEW record (FUN_180398640).
type SecNew struct {
	SecID     uint16
	Market    byte
	Kind      byte // security type id from the RQ_STARTUP sectype section
	Class     byte // instrclass character, 0 when derived from Kind
	SecCode   string
	ShortName string
	ISIN      string
	Byte1     byte
	Int1      int32
	Flags     byte // bits 0x30 mark call/put options
	Int2      int32
	// Present for futures and options (kinds 4 and 5).
	DerivFlags byte
	Underlying string
	Props      byte // bit 1 and bit 2 feed the opmask flags
	Pits       []Pit
	Currency   string
	// Optional tail, present while the record has bytes left.
	Tail1, Tail2 byte
	Tail3        int32
	TimeZone     [16]byte
	HasTimeZone  bool
	MIC, Ticker  string
	Tail4, Tail5 int32
	Text1        string
	Text2        string
	Tail6        byte
	Extra        []byte // bytes after the last known optional field
}

// ParseSecNew decodes an R_SEC_NEW body.
func ParseSecNew(body []byte) (SecNew, error) {
	r := &reader{Reader: wire.NewReader(body)}
	s := SecNew{
		SecID: r.word(), Market: r.byte(), Kind: r.byte(), Class: r.byte(),
		SecCode: r.str(0x19), ShortName: r.str(0x1e), ISIN: r.str(0xc),
		Byte1: r.byte(), Int1: r.int32(), Flags: r.byte(), Int2: r.int32(),
	}
	if s.Kind == 4 || s.Kind == 5 {
		s.DerivFlags = r.byte()
		if s.DerivFlags&0xc != 0 {
			s.Underlying = r.str(0x19)
		}
	}
	s.Props = r.byte()
	for board := r.word(); r.err == nil && board != 0xffff; board = r.word() {
		p := Pit{Board: board, LotSize: r.int32(), LotDivider: r.int32(), MinStep: r.int32(),
			Decimals: r.byte(), Byte1: r.byte()}
		if raw := r.raw(8); raw != nil {
			p.PointCost = math.Float64frombits(binary.LittleEndian.Uint64(raw))
		}
		p.Text = r.str(0xc)
		p.Default = r.byte() != 0
		p.Int1 = r.int32()
		p.Currency = r.str(4)
		s.Pits = append(s.Pits, p)
	}
	s.Currency = r.str(4)
	if r.err == nil && r.Len() > 0 {
		s.Tail1, s.Tail2 = r.byte(), r.byte()
	}
	if r.err == nil && r.Len() > 0 {
		s.Tail3 = r.int32()
		if raw := r.raw(16); raw != nil {
			s.TimeZone, s.HasTimeZone = [16]byte(raw), true
		}
	}
	if r.err == nil && r.Len() > 0 {
		s.MIC, s.Ticker = r.str(5), r.str(0x19)
	}
	if r.err == nil && r.Len() > 0 {
		s.Tail4, s.Tail5 = r.int32(), r.int32()
		s.Text1, s.Text2 = r.str(0x19), r.str(6)
	}
	if r.err == nil && r.Len() > 0 {
		s.Tail6 = r.byte()
	}
	if r.err == nil && r.Len() > 0 {
		s.Extra = r.raw(r.Len())
	}
	if r.err != nil {
		return s, fmt.Errorf("R_SEC_NEW: %w", r.err)
	}
	return s, nil
}

// DefaultPit returns the pit flagged as default, or the first one.
func (s SecNew) DefaultPit() (Pit, bool) {
	for _, p := range s.Pits {
		if p.Default {
			return p, true
		}
	}
	if len(s.Pits) > 0 {
		return s.Pits[0], true
	}
	return Pit{}, false
}
