package model

import "time"

// Candle is one OHLCV candle of <candles>.
type Candle struct {
	Time         time.Time
	Open         Decimal
	Close        Decimal
	High         Decimal
	Low          Decimal
	Volume       int64
	OpenInterest *int64 // present only when the record carried the extra value (I)
}

// Candles is one <candles> callback of gethistorydata. Items are in the DLL
// order (chronological: oldest first); Status is the PDF 4.1 status (0..3).
type Candles struct {
	Symbol Symbol
	SecID  uint16
	Period int
	Status int
	Items  []Candle
}

func (*Candles) isEvent() {}
