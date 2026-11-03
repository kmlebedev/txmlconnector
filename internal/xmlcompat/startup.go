// Package xmlcompat decodes TRANSAQ server payloads into the XML structures
// the vendor DLL emits to callbacks (see client/commands), so a native backend
// can serve existing XML consumers unchanged.
package xmlcompat

import (
	"encoding/xml"
	"fmt"
	"slices"

	"golang.org/x/text/encoding/charmap"

	"github.com/kmlebedev/txmlconnector/client/commands"
	"github.com/kmlebedev/txmlconnector/internal/wire"
)

// Market is one record of the markets section (FUN_1803953e0 "ReadMarkets").
type Market struct {
	ID   byte
	Code string // emitted as the <market> text
	Name string
	Raw  [4]byte // meaning unknown
}

// BoardType is one record of FUN_180395820 ("BoardTypesRequest").
type BoardType struct {
	ID   byte
	Name string
	Text string
	Raw  byte
}

// Board is one record of FUN_180395b40 ("ReadBoards").
type Board struct {
	Num    uint16
	Market byte
	Code   string
	Name   string
	Word   uint16 // meaning unknown
	Byte1  byte
	Text   string
	Byte2  byte
	Type   byte // <type>: 0 FORTS, 1 T+, 2 T0
}

// SecType is one record of FUN_180395f70 ("SecTypeRequest").
type SecType struct {
	ID        byte
	ShortName string
	Name      string
	Raw       [3]byte
}

// CandleKind is one record of FUN_1803b88b0 ("RQ_CANDLE_KIND").
type CandleKind struct {
	ID     byte
	Name   string
	Period int32 // seconds
	Extra  int32 // meaning unknown (259200 for the weekly kind)
}

// SymbolColumn is one record of FUN_1803b7ee0 ("RQ_SYMBOL_PARAM").
type SymbolColumn struct {
	ID    byte
	Group byte
	Title string
	Code  string
}

// Startup is the RQ_STARTUP reply: six sections, each terminated by -1
// (0xff, or 0xffff for boards), read in the order of FUN_180357e00.
type Startup struct {
	Markets     []Market
	MarketsTail uint16 // word after the markets terminator, stored at +0x6fc
	BoardTypes  []BoardType
	Boards      []Board
	SecTypes    []SecType
	CandleKinds []CandleKind
	Columns     []SymbolColumn
}

// cp1251 converts a server string to UTF-8.
func cp1251(s string) string {
	out, err := charmap.Windows1251.NewDecoder().String(s)
	if err != nil {
		return s
	}
	return out
}

type reader struct {
	*wire.Reader
	err error
}

func (r *reader) byte() byte {
	if r.err != nil {
		return 0
	}
	var v byte
	v, r.err = r.Byte()
	return v
}

func (r *reader) word() uint16 {
	if r.err != nil {
		return 0
	}
	var v uint16
	v, r.err = r.Uint16()
	return v
}

func (r *reader) int32() int32 {
	if r.err != nil {
		return 0
	}
	var v int32
	v, r.err = r.Int32()
	return v
}

func (r *reader) str(max int) string {
	if r.err != nil {
		return ""
	}
	var v string
	v, r.err = r.String(max)
	return cp1251(v)
}

// ParseStartup decodes an RQ_STARTUP reply payload.
func ParseStartup(p []byte) (*Startup, error) {
	r := &reader{Reader: wire.NewReader(p)}
	s := &Startup{}
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		m := Market{ID: id, Code: r.str(10), Name: r.str(0x18)}
		for i := range m.Raw {
			m.Raw[i] = r.byte()
		}
		s.Markets = append(s.Markets, m)
	}
	s.MarketsTail = r.word()
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		s.BoardTypes = append(s.BoardTypes, BoardType{ID: id, Name: r.str(0x20), Text: r.str(8), Raw: r.byte()})
	}
	for num := r.word(); r.err == nil && num != 0xffff; num = r.word() {
		s.Boards = append(s.Boards, Board{
			Num: num, Market: r.byte(), Code: r.str(7), Name: r.str(0x1e),
			Word: r.word(), Byte1: r.byte(), Text: r.str(8), Byte2: r.byte(), Type: r.byte(),
		})
	}
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		t := SecType{ID: id, ShortName: r.str(10), Name: r.str(0x18)}
		for i := range t.Raw {
			t.Raw[i] = r.byte()
		}
		s.SecTypes = append(s.SecTypes, t)
	}
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		s.CandleKinds = append(s.CandleKinds, CandleKind{ID: id, Name: r.str(12), Period: r.int32(), Extra: r.int32()})
	}
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		s.Columns = append(s.Columns, SymbolColumn{ID: id, Group: r.byte(), Title: r.str(0x20), Code: r.str(0x10)})
	}
	if r.err != nil {
		return nil, fmt.Errorf("RQ_STARTUP: %w", r.err)
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("RQ_STARTUP: %d trailing bytes", r.Len())
	}
	return s, nil
}

// MarketsXML returns <markets> as the DLL emits it: ordered by id.
func (s *Startup) MarketsXML() commands.Markets {
	var out commands.Markets
	for _, m := range s.Markets {
		out.Items = append(out.Items, commands.Market{ID: int(m.ID), Name: m.Code})
	}
	slices.SortFunc(out.Items, func(a, b commands.Market) int { return a.ID - b.ID })
	return out
}

// BoardsXML returns <boards> in wire order. The DLL reports FORTS options
// (market 5) under FORTS (market 4) (FUN_1800c07f0).
func (s *Startup) BoardsXML() commands.Boards {
	var out commands.Boards
	for _, b := range s.Boards {
		market := int(b.Market)
		if market == 5 {
			market = 4
		}
		out.Items = append(out.Items, commands.Board{ID: b.Code, Name: b.Name, Market: market, Type: int(b.Type)})
	}
	return out
}

// CandleKindsXML returns <candlekinds> in wire order.
func (s *Startup) CandleKindsXML() commands.CandleKinds {
	var out commands.CandleKinds
	for _, k := range s.CandleKinds {
		out.Items = append(out.Items, commands.CandleKind{ID: int(k.ID), Period: int(k.Period), Name: k.Name})
	}
	return out
}

// Marshal renders a structure as a callback message.
func Marshal(v any) (string, error) {
	b, err := xml.Marshal(v)
	return string(b), err
}
