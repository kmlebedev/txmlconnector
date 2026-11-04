package model

// Quote is one price level of an order book snapshot (PDF 4.9). Buy/Sell hold
// the level quantity of their side; -1 marks a level deletion (PDF 4.9: buy/sell
// = -1, no separate flag). Yield is the level yield when the mode carries one;
// nil renders the DLL default 0.
type Quote struct {
	Symbol Symbol
	SecID  uint16
	Price  Decimal
	Source string
	Yield  *int
	Buy    int64
	Sell   int64
}

// Quotes is one <quotes> callback: the whole order book snapshot of a security
// on one board, levels in wire order (buy levels, then sell levels).
type Quotes struct {
	Symbol Symbol
	SecID  uint16
	Board  string
	Levels []Quote
}

func (*Quotes) isEvent() {}
