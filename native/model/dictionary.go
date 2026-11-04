package model

// Dictionary types decoded from the RQ_STARTUP reply. They are plain data: the
// XML representation of the callbacks lives in xmlcompat and client/commands.

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

// BoardByNum finds a board dictionary record by its wire number.
func (s *Startup) BoardByNum(num uint16) (Board, bool) {
	for _, b := range s.Boards {
		if b.Num == num {
			return b, true
		}
	}
	return Board{}, false
}

// BoardCode returns the board code, or "" when the board is unknown.
func (s *Startup) BoardCode(num uint16) string {
	if b, ok := s.BoardByNum(num); ok {
		return b.Code
	}
	return ""
}

// SecTypeByID finds a security type dictionary record by its id.
func (s *Startup) SecTypeByID(id byte) (SecType, bool) {
	for _, t := range s.SecTypes {
		if t.ID == id {
			return t, true
		}
	}
	return SecType{}, false
}
