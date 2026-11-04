package model

// Security is the typed form of one <security> message: the R_SEC_NEW record
// with the fields the DLL derives from the RQ_STARTUP dictionaries. The XML
// representation stays in client/commands; the emitter is in xmlcompat.
type Security struct {
	SecID      int
	Active     bool
	SecCode    string
	InstrClass string
	Board      string
	Market     int
	ShortName  string
	Decimals   int
	MinStep    float64
	LotSize    int
	LotDivider int
	PointCost  float64
	OpMask     SecurityOpMask
	SecType    string
	SecTZ      string // Windows time zone name of the security
	QuotesType int    // 0 - none, 1 - OrderBook, 2 - Level2
	MIC        string
	Ticker     string
	Currency   string
	CurrencyID string
}

// SecurityOpMask is the order-permission set the DLL reports in <opmask>.
type SecurityOpMask struct {
	UseCredit     bool
	ByMarket      bool
	NoSplit       bool
	Fok           bool
	Ioc           bool
	ImmOrCancel   bool
	CancelBalance bool
}
