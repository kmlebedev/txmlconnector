package model

import "time"

// NewCondOrder is the newcondorder command (PDF 3.9).
type NewCondOrder struct {
	Client      string
	Union       string
	Symbol      Symbol
	Price       Decimal
	Quantity    int64
	BuySell     byte
	CondType    string
	CondValue   Decimal
	ValidAfter  *time.Time
	ValidBefore *time.Time
	BrokerRef   string
}

// NewStopOrder is the newstoporder command (PDF 3.10).
type NewStopOrder struct {
	Client        string
	Union         string
	Symbol        Symbol
	Quantity      int64
	BuySell       byte
	StopLoss      Decimal
	TakeProfit    Decimal
	LinkedOrderNo uint64
	BrokerRef     string
}

// StopOrder is one <stoporder> of the <orders> callback (PDF 4.10, R_STOP 0x54;
// only the oracle-rendered fields are typed, the rest is I).
type StopOrder struct {
	TransactionID uint64
	SecID         uint16
	Board         string
	SecCode       string
	Union         string
	Client        string
	BuySell       byte
	Author        string
	Status        int
	WithdrawTime  int64
	Result        string
}

func (*StopOrder) isEvent() {}
