package model

// SecPosition is one <sec_position> of the <positions> stream (PDF 4.12). The
// money fields are the register values of R_CLIENT_ASSET (O: position-sec).
type SecPosition struct {
	Client    string
	Union     string
	SecID     uint16
	Market    int
	ShortName string
	SecCode   string
	SaldoIn   int64
	SaldoMin  int64
	Bought    int64
	Sold      int64
	Saldo     int64
	OrdBuy    int64
	OrdSell   int64
}

// FortsPosition is one <forts_position> of the RQ_CLIENT_HOLDINGS reply (O).
type FortsPosition struct {
	Client        string
	Union         string
	SecID         uint16
	SecCode       string
	Market        int
	StartNet      int64
	OpenBuys      int64
	OpenSells     int64
	TotalNet      int64
	TodayBuy      int64
	TodaySell     int64
	OptMargin     Decimal
	VarMargin     Decimal
	ExpirationPos int64
}

// FortsMoney is the <forts_money> callback of the FORTS reply.
type FortsMoney struct {
	Client    string
	Union     string
	Market    int
	ShortName string
	Blocked   Decimal
	VarMargin Decimal
	Current   Decimal
	Free      Decimal
}

// The remaining PDF 4.12 structs are modelled for the API but not rendered by
// the oracle fixtures yet (I).
type FortsCollaterals struct {
	Client string
	Union  string
	Market int
}

type SpotLimit struct {
	Client string
	Union  string
	Market int
}

type UnitedLimits struct {
	Client string
	Union  string
}

func (*SecPosition) isEvent()      {}
func (*FortsPosition) isEvent()    {}
func (*FortsMoney) isEvent()       {}
func (*FortsCollaterals) isEvent() {}
func (*SpotLimit) isEvent()        {}
func (*UnitedLimits) isEvent()     {}
