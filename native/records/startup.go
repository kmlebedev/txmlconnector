package records

import (
	"fmt"

	"golang.org/x/text/encoding/charmap"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// cp1251 converts a server string to UTF-8.
func cp1251(s string) string {
	out, err := charmap.Windows1251.NewDecoder().String(s)
	if err != nil {
		return s
	}
	return out
}

// reader wraps wire.Reader with the DLL read order helpers: the first error
// sticks and all later reads return zero values, so a decoder can read the
// whole record and check r.err once.
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

func (r *reader) int64() int64 {
	if r.err != nil {
		return 0
	}
	var v int64
	v, r.err = r.Int64()
	return v
}

func (r *reader) raw(n int) []byte {
	if r.err != nil {
		return nil
	}
	var v []byte
	v, r.err = r.Raw(n)
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
func ParseStartup(p []byte) (*model.Startup, error) {
	r := &reader{Reader: wire.NewReader(p)}
	s := &model.Startup{}
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		m := model.Market{ID: id, Code: r.str(10), Name: r.str(0x18)}
		for i := range m.Raw {
			m.Raw[i] = r.byte()
		}
		s.Markets = append(s.Markets, m)
	}
	s.MarketsTail = r.word()
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		s.BoardTypes = append(s.BoardTypes, model.BoardType{ID: id, Name: r.str(0x20), Text: r.str(8), Raw: r.byte()})
	}
	for num := r.word(); r.err == nil && num != 0xffff; num = r.word() {
		s.Boards = append(s.Boards, model.Board{
			Num: num, Market: r.byte(), Code: r.str(7), Name: r.str(0x1e),
			Word: r.word(), Byte1: r.byte(), Text: r.str(8), Byte2: r.byte(), Type: r.byte(),
		})
	}
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		t := model.SecType{ID: id, ShortName: r.str(10), Name: r.str(0x18)}
		for i := range t.Raw {
			t.Raw[i] = r.byte()
		}
		s.SecTypes = append(s.SecTypes, t)
	}
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		s.CandleKinds = append(s.CandleKinds, model.CandleKind{ID: id, Name: r.str(12), Period: r.int32(), Extra: r.int32()})
	}
	for id := r.byte(); r.err == nil && id != 0xff; id = r.byte() {
		s.Columns = append(s.Columns, model.SymbolColumn{ID: id, Group: r.byte(), Title: r.str(0x20), Code: r.str(0x10)})
	}
	if r.err != nil {
		return nil, fmt.Errorf("RQ_STARTUP: %w", r.err)
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("RQ_STARTUP: %d trailing bytes", r.Len())
	}
	return s, nil
}
