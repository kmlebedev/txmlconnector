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
	// Warnings reports data that cannot be trusted as typed. The only warning
	// today is "unknown candle period N": the wire time unit comes from
	// <candlekinds>, so without the dictionary the candle Time is the zero
	// instant instead of a silently guessed one (R8).
	Warnings []string
	// ZoneBias is the server time-zone bias (UTC = wall + bias). The items keep
	// the UTC instants; the DLL renders the wall clock, so the renderer adds
	// -ZoneBias to the date string.
	ZoneBias time.Duration
}

func (*Candles) isEvent() {}
