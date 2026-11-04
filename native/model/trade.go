package model

import "time"

// Trade is one trade of the market feed subscribed with <alltrades>.
type Trade struct {
	SecID   uint16
	Board   string // board code from the RQ_STARTUP dictionary
	SecCode string
	TradeNo int64
	// Time is the trade instant in UTC: the server wall clock shifted by the
	// server time-zone bias from RQ_TIME.
	Time time.Time
	// WallTime is the server wall clock in seconds, the value the DLL prints
	// in <time>; it falls back to the trading day when the record had none.
	WallTime int64
	// Millis is the milliseconds of <time> from the wire; 1000 means the
	// record carried none.
	Millis          uint16
	Price           Decimal
	Quantity        int64
	BuySell         byte // 'B', 'S' or 0
	Period          byte // 'O', 'N', 'C' or 0
	OpenInterest    int64
	HasOpenInterest bool
}
