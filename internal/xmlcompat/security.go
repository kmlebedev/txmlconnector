package xmlcompat

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/client/commands"
	"github.com/kmlebedev/txmlconnector/internal/wire"
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

func (r *reader) raw(n int) []byte {
	if r.err != nil {
		return nil
	}
	var v []byte
	v, r.err = r.Raw(n)
	return v
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

var (
	sectypeNames = map[byte]string{0: "SHARE", 1: "BOND", 4: "FUT", 5: "OPT", 7: "IDX", 14: "MCT", 15: "ETS_CURRENCY", 16: "ETS_SWAP"}
	kindClass    = map[byte]byte{0: 'E', 1: 'B', 4: 'F', 5: 'O', 7: 'I', 15: 'C', 16: 'S'}
)

const (
	marketMICEX       = 1
	marketFORTS       = 4
	marketFORTSOption = 5
)

// Security converts an R_SEC_NEW record to <security> using the board
// dictionary from RQ_STARTUP. Fields verified against the DLL on all 17 176
// demo-server instruments: secid, seccode, shortname, instrclass, board,
// market, decimals, minstep, lotsize, lotdivider, point_cost, sectype,
// currency, currencyid. active, opmask, quotestype, sec_tz, MIC and ticker
// are not derived yet and are left empty.
func (st *Startup) Security(s SecNew) commands.Security {
	out := commands.Security{
		SecId:     int(s.SecID),
		SecCode:   s.SecCode,
		ShortName: s.ShortName,
		Market:    int(s.Market),
		SecType:   sectypeNames[s.Kind],
		Currency:  s.Currency,
	}
	if out.Market == marketFORTSOption {
		out.Market = marketFORTS
	}
	switch {
	case s.Class != 0:
		out.InstrClass = string(rune(s.Class))
	case kindClass[s.Kind] != 0:
		out.InstrClass = string(rune(kindClass[s.Kind]))
	}
	if out.Currency == "" && s.Market == marketMICEX {
		out.Currency = "RUR"
	}
	p, ok := s.DefaultPit()
	if !ok {
		return out
	}
	out.Board = st.boardCode(p.Board)
	out.Decimals = int(p.Decimals)
	out.MinStep = float64(p.MinStep) / math.Pow10(int(p.Decimals))
	out.LotSize = int(p.LotSize)
	if s.Market == marketFORTS || s.Market == marketFORTSOption {
		out.LotSize = 1
	}
	out.LotDivider = int(p.LotDivider)
	out.PointCost = p.PointCost
	// Settlement currency of the default board, else the security currency
	// (already defaulted to RUR on MICEX); the DLL reports RUR as RUB.
	out.CurrencyID = p.Currency
	if out.CurrencyID == "" {
		out.CurrencyID = out.Currency
	}
	if out.CurrencyID == "RUR" {
		out.CurrencyID = "RUB"
	}
	return out
}

func (st *Startup) boardCode(num uint16) string {
	for _, b := range st.Boards {
		if b.Num == num {
			return b.Code
		}
	}
	return ""
}

// FormatMinStep renders minstep as the DLL does: fixed to the price decimals
// with trailing zeros removed, keeping one digit after the point.
func FormatMinStep(v float64, decimals int) string {
	if decimals <= 0 {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	s := strings.TrimRight(strconv.FormatFloat(v, 'f', decimals, 64), "0")
	if strings.HasSuffix(s, ".") {
		s += "0"
	}
	return s
}

// FormatPointCost renders point_cost as the DLL does (C++ stream default,
// six significant digits).
func FormatPointCost(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}
