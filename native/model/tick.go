package model

import "time"

// Tick is one <tick> of subscribe_ticks (S: RQ_MAIN 0x5a). Board is the board
// code resolved from the catalog (also stored in Symbol.Board). Period/BuySell
// are the D fields; OpenInterest is not in the S layout (I).
type Tick struct {
	Symbol       Symbol
	Board        string
	SecID        uint16
	TradeNo      int64
	Time         time.Time
	Price        Decimal
	Quantity     int64
	Period       byte
	BuySell      byte
	OpenInterest *int64
}

func (*Tick) isEvent() {}

// Ticks is one 0x5a record batch: one RQ_MAIN record can carry several ticks
// and the connector renders one <ticks> callback for the batch (S).
type Ticks struct {
	Items []Tick
}

func (*Ticks) isEvent() {}
