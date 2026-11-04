package model

// ClientTrade is one <trade> of the client <trades> feed (PDF 4.11). The
// fields follow the oracle-confirmed R_TRD render (orders.md); the mask-selected
// values are not rendered here (I).
type ClientTrade struct {
	SecID      uint16
	Board      string
	SecCode    string
	TradeNo    uint64
	OrderNo    uint64
	Client     string
	Union      string
	BrokerRef  string
	SettleCode string
	TradeType  byte
	BuySell    byte
	Time       int64 // seconds of day
	Price      Decimal
	Value      Decimal
	Comission  Decimal
	Quantity   int64
	Items      int64
	CurrentPos int64
	Yield      Decimal
	AccruedInt Decimal
	Bypass     byte
}

func (*ClientTrade) isEvent() {}
