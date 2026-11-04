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

// StopLoss are the stop-loss parameters of newstoporder (PDF 3.10); the oracle
// payload carries activationprice/orderprice/quantity for this leg.
type StopLoss struct {
	ActivationPrice Decimal
	OrderPrice      Decimal
	Quantity        int64
}

// TakeProfit are the take-profit parameters; the oracle payload carries its
// activationprice (and a quantity byte, I).
type TakeProfit struct {
	ActivationPrice Decimal
	Quantity        int64
}

// NewStopOrder is the newstoporder command (PDF 3.10).
type NewStopOrder struct {
	Client        string
	Union         string
	Symbol        Symbol
	Quantity      int64
	BuySell       byte
	StopLoss      StopLoss
	TakeProfit    TakeProfit
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
