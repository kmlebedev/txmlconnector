package records

import (
	"fmt"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Catalog resolves session-local ids from RQ_MAIN records: board numbers
// from RQ_STARTUP and securities from R_SEC_NEW. secid is stable only within
// one connection (TXmlConnector.pdf, p. 13), so a catalog belongs to a session.
type Catalog struct {
	startup   *model.Startup
	boards    map[uint16]model.Board
	codes     map[string]model.Board
	secs      map[uint16]SecNew
	symbols   map[symbol]uint16
	order     []uint16 // R_SEC_NEW arrival order, the order the DLL reports
	delivered int      // how many of order the last <securities> chunk already emitted
	tradeDate int64
}

type symbol struct{ board, seccode string }

// DrainPending returns the securities added since the last call: the chunk
// the DLL emits in one <securities> callback per RQ_MAIN poll.
func (c *Catalog) DrainPending() []SecNew {
	if c.delivered >= len(c.order) {
		return nil
	}
	out := make([]SecNew, 0, len(c.order)-c.delivered)
	for _, id := range c.order[c.delivered:] {
		if s, ok := c.secs[id]; ok {
			out = append(out, s)
		}
	}
	c.delivered = len(c.order)
	return out
}

// NewCatalog builds a catalog from the RQ_STARTUP dictionaries.
func NewCatalog(st *model.Startup) *Catalog {
	c := &Catalog{
		startup: st,
		boards:  map[uint16]model.Board{},
		codes:   map[string]model.Board{},
		secs:    map[uint16]SecNew{},
		symbols: map[symbol]uint16{},
	}
	for _, b := range st.Boards {
		c.boards[b.Num] = b
		c.codes[b.Code] = b
	}
	return c
}

// Startup returns the dictionaries the catalog was built from.
func (c *Catalog) Startup() *model.Startup { return c.startup }

// Apply records what the catalog tracks (R_SEC_NEW, R_DATE) and reports
// whether the record was used.
func (c *Catalog) Apply(rec Record) (bool, error) {
	switch rec.Tag {
	case TagSecNew:
		s, err := ParseSecNew(rec.Body)
		if err != nil {
			return false, err
		}
		c.AddSecurity(s)
		return true, nil
	case TagDate:
		d, err := TradeDate(rec)
		if err != nil {
			return false, err
		}
		c.tradeDate = d
		return true, nil
	}
	return false, nil
}

// AddSecurity registers a security under every board it has parameters for.
func (c *Catalog) AddSecurity(s SecNew) {
	if _, seen := c.secs[s.SecID]; !seen {
		c.order = append(c.order, s.SecID)
	}
	c.secs[s.SecID] = s
	for _, p := range s.Pits {
		if b, ok := c.boards[p.Board]; ok {
			c.symbols[symbol{b.Code, s.SecCode}] = s.SecID
		}
	}
}

// Len returns the number of securities.
func (c *Catalog) Len() int { return len(c.secs) }

// TradeDate is the R_DATE value: Unix seconds at 00:00 of the trading day.
func (c *Catalog) TradeDate() int64 { return c.tradeDate }

// Security returns a security by its session-local id.
func (c *Catalog) Security(secid uint16) (SecNew, bool) {
	s, ok := c.secs[secid]
	return s, ok
}

// Board returns a board dictionary record by its wire number.
func (c *Catalog) Board(num uint16) (model.Board, bool) {
	b, ok := c.boards[num]
	return b, ok
}

// BoardByCode returns a board dictionary record by its code.
func (c *Catalog) BoardByCode(code string) (model.Board, bool) {
	b, ok := c.codes[code]
	return b, ok
}

// Lookup finds a security by board code and seccode.
func (c *Catalog) Lookup(board, seccode string) (SecNew, model.Board, bool) {
	id, ok := c.symbols[symbol{board, seccode}]
	if !ok {
		return SecNew{}, model.Board{}, false
	}
	return c.secs[id], c.codes[board], true
}

// Decimals returns the price decimals of a security on a board: the pit for
// that board, else the default pit.
func (c *Catalog) Decimals(secid, board uint16) (byte, bool) {
	s, ok := c.secs[secid]
	if !ok {
		return 0, false
	}
	for _, p := range s.Pits {
		if p.Board == board {
			return p.Decimals, true
		}
	}
	if p, ok := s.DefaultPit(); ok {
		return p.Decimals, true
	}
	return 0, false
}

// List returns the known securities in R_SEC_NEW arrival order.
func (c *Catalog) List() []SecNew {
	out := make([]SecNew, 0, len(c.order))
	for _, id := range c.order {
		if s, ok := c.secs[id]; ok {
			out = append(out, s)
		}
	}
	return out
}

// Trade converts a decoded R_ALLTRADE record to the typed model trade. It
// resolves the security and board names and the price decimals from the
// catalog and the trade instant from the RQ_TIME bias: bias is the server
// time-zone shift, UTC = wall clock + bias. The wall clock itself is kept in
// WallTime for the DLL-compatible <time> rendering.
func (c *Catalog) Trade(a AllTrade, bias time.Duration) model.Trade {
	wall := a.Time
	if wall == 0 {
		wall = c.tradeDate
	}
	ts := time.Unix(wall, 0).Add(bias)
	if a.Millis < 1000 {
		ts = ts.Add(time.Duration(a.Millis) * time.Millisecond)
	}
	t := model.Trade{
		SecID:    a.SecID,
		TradeNo:  a.TradeNo,
		Time:     ts.UTC(),
		WallTime: wall,
		Millis:   a.Millis,
		Price:    model.Decimal{Units: a.Price},
		Quantity: a.Quantity,
		BuySell:  a.BuySell,
		Period:   a.Period,
	}
	if s, ok := c.secs[a.SecID]; ok {
		t.SecCode = s.SecCode
	}
	if b, ok := c.boards[a.Board]; ok {
		t.Board = b.Code
	}
	t.Price.Scale, _ = c.Decimals(a.SecID, a.Board)
	if a.Flags&AllTradeOpenInterest != 0 {
		t.OpenInterest, t.HasOpenInterest = a.OpenInt, true
	}
	return t
}

// ApplyMutations updates derived state for record tags that mutate a
// security: the attribute tags R_SEC_ZALOG/OBLIG/ONOFF/MKT_PERMIT rewrite
// bytes of the record the same way FUN_180264150 does, so later
// <securities> output reflects them.
func (c *Catalog) ApplyMutations(rec Record) (bool, error) {
	switch rec.Tag {
	case TagSecNew, TagDate:
		return c.Apply(rec)
	}
	if !isAttrTag(rec.Tag) {
		return false, nil
	}
	if len(rec.Body) < 3 {
		return false, fmt.Errorf("attribute record %#02x: short body", rec.Tag)
	}
	r := wire.NewReader(rec.Body)
	id, _ := r.Uint16()
	v, _ := r.Byte()
	s, ok := c.secs[id]
	if !ok {
		return true, nil // the DLL logs Unknown secid and skips
	}
	switch rec.Tag {
	case 0x0d: // R_SEC_ONOFF: low two bits of the flags byte
		s.Flags = s.Flags&0xfc | v
		c.secs[id] = s
	case 0x0e: // R_SEC_MKT_PERMIT: market-permission bit
		if v != 0 {
			s.Flags |= 4
		} else {
			s.Flags &^= 4
		}
		c.secs[id] = s
	}
	return true, nil
}

func isAttrTag(tag byte) bool { return attrTags[tag] != "" }
