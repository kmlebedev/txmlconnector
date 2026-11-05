package model

// MaxBuySell is one <security> item of <max_buy_sell[_tplus]>: the identity
// comes from the session catalog, maxbuy/maxsell are printed as integers
// (margin.md, D/O). The empirical value formula is I (RE task-52): the DLL
// keeps the price/rate inputs in internal stores that RQ_MAIN does not expose.
type MaxBuySell struct {
	SecID   uint16
	Market  int
	SecCode string
	MaxBuy  int64
	MaxSell int64
}

// SecPermissions is the <cln_sec_permissions> form. The permission mask source
// (FUN_18001a700) is not in the RQ_MAIN stores yet (margin.md, I): marketable
// is taken from R_SEC_MKT_PERMIT, the other rights/rates are I and stay false/0
// until task-52/RE delivers the store.
type SecPermissions struct {
	Client     string
	Union      string
	SecID      uint16
	SecCode    string
	Market     int
	Board      string
	Tradeable  bool
	Marketable bool
	Longable   bool
	Shortable  bool
	// The first D branch (O): regratelong=rate[2], regrateshort=rate[1] scaled
	// by 100 and printed with 2 decimals; the alternative branch is I.
	RegRateLong   Decimal
	RegRateShort  Decimal
	RiskRateLong  *Decimal
	ReserateLong  *Decimal
	RiskRateShort *Decimal
	ReserateShort *Decimal
}

// MarketOrd is the <marketord secid permit seccode/> callback of R_SEC_MKT_PERMIT
// (0x0e): the DLL emits it for every record, without deduplication (O).
type MarketOrd struct {
	SecID   uint16
	Permit  bool
	SecCode string
}

func (*MarketOrd) isEvent() {}

// Leverage is the PDF 4.14 credit-mode config: DLL 6.47 has no <leverage>
// literal and delivers the RQ_INQUIRE_UT (152) XML config instead (margin.md,
// D); kept as a typed placeholder until that request is implemented (I).
type Leverage struct{}

func (*Leverage) isEvent() {}

// SecRate is one R_SEC_RATE (0x0a) state/event: the price V in kopecks per unit
// (the lot value is V*lotsize; margin.md). It has no XML callback yet (I).
type SecRate struct {
	SecID uint16
	Value int64
}

func (*SecRate) isEvent() {}
