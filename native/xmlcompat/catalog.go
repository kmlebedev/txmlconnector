package xmlcompat

// Catalog resolves session-local ids from RQ_MAIN records: board numbers
// from RQ_STARTUP and securities from R_SEC_NEW. secid is stable only within
// one connection (TXmlConnector.pdf, p. 13), so a catalog belongs to a session.
type Catalog struct {
	startup   *Startup
	boards    map[uint16]Board
	codes     map[string]Board
	secs      map[uint16]SecNew
	symbols   map[symbol]uint16
	tradeDate int64
}

type symbol struct{ board, seccode string }

func NewCatalog(st *Startup) *Catalog {
	c := &Catalog{
		startup: st,
		boards:  map[uint16]Board{},
		codes:   map[string]Board{},
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
func (c *Catalog) Startup() *Startup { return c.startup }

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

func (c *Catalog) Security(secid uint16) (SecNew, bool) {
	s, ok := c.secs[secid]
	return s, ok
}

func (c *Catalog) Board(num uint16) (Board, bool) {
	b, ok := c.boards[num]
	return b, ok
}

func (c *Catalog) BoardByCode(code string) (Board, bool) {
	b, ok := c.codes[code]
	return b, ok
}

// Lookup finds a security by board code and seccode.
func (c *Catalog) Lookup(board, seccode string) (SecNew, Board, bool) {
	id, ok := c.symbols[symbol{board, seccode}]
	if !ok {
		return SecNew{}, Board{}, false
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
