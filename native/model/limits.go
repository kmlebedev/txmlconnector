package model

// ClientLimits is the <clientlimits> data of RQ_CLIENT_LIMITS (PDF 4.13,
// limits.md). Money fields are stored with Scale 2 (the DLL divides by 100),
// coverage is the raw W value, liquidity_c has Scale 5.
type ClientLimits struct {
	ClientID uint64
	Client   string
	Union    string

	CBPLimit     Decimal
	CBPLUsed     Decimal
	CBPLPlanned  Decimal
	FobVarMargin Decimal
	Coverage     uint16
	LiquidityC   Decimal

	Profit           Decimal
	MoneyCurrent     Decimal
	MoneyReserve     Decimal
	MoneyFree        Decimal
	OptionsPremium   Decimal
	ExchangeFee      Decimal
	FortsVarMargin   Decimal
	VarMargin        Decimal
	PCLMargin        Decimal
	OptionsVM        Decimal
	SpotBuyLimit     Decimal
	UsedSpotBuyLimit Decimal
	CollatCurrent    Decimal
	CollatBlocked    Decimal
	CollatFree       Decimal
}

// UnitedLimits is the <united_limits> callback (D, limits.md; no oracle case).
type UnitedLimits struct {
	Union        string
	OpenEquity   Decimal
	Equity       Decimal
	Requirements Decimal
	Free         Decimal
	VM           Decimal
	FinRes       Decimal
	GO           Decimal
}

func (*ClientLimits) isEvent() {}
func (*UnitedLimits) isEvent() {}
