package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NewOrder is a neworder command (PDF 3.8).
type NewOrder struct {
	Client    string
	Union     string
	Symbol    Symbol
	Price     Decimal
	Hidden    int64
	Quantity  int64
	BuySell   byte
	ByMarket  bool
	UseCredit bool
	NoSplit   bool
	Unfilled  string // PutInQueue|FOK|IOC
	ExpDate   *time.Time
	BrokerRef string
}

// Order is one <order> of the <orders> feed (PDF 4.10). Only the fields the
// DLL render shows are typed; the rest of the 52-read body is I (orders.md).
type Order struct {
	TransactionID  uint64
	OrderNo        uint64
	SecID          uint16
	Board          string
	SecCode        string
	Union          string
	Client         string
	Status         int
	BuySell        byte
	Time           int32 // seconds of day
	AcceptTime     int32 // seconds of day
	BrokerRef      string
	Balance        int64
	Price          Decimal
	Quantity       int64
	Yield          Decimal
	Condition      int
	ConditionValue Decimal
	ValidAfter     int32
	ValidBefore    int32
	Hidden         int64
	Result         string
}

func (*Order) isEvent() {}

// ParseDecimal parses a plain decimal command value (price/yield): the scale is
// the number of fractional digits (100.00 -> Units 10000, Scale 2).
func ParseDecimal(text string) (Decimal, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Decimal{}, fmt.Errorf("empty decimal")
	}
	neg := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	whole, frac, _ := strings.Cut(text, ".")
	if whole == "" {
		whole = "0"
	}
	all := whole + frac
	units, err := strconv.ParseInt(all, 10, 64)
	if err != nil {
		return Decimal{}, fmt.Errorf("decimal %q: %w", text, err)
	}
	if neg {
		units = -units
	}
	return Decimal{Units: units, Scale: uint8(len(frac))}, nil
}
