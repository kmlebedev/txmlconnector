package model

import "time"

// Tick is one <tick> of subscribe_ticks. Period/BuySell are filled from the
// single flag byte of the D reply (semantics I); OpenInterest is set when a
// later RE pass recovers it (I, the field is not in the D layout).
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
