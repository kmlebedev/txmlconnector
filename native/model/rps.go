package model

// The RPS/REPO payloads are O-pinned only for the base fields (rps.md); the
// repo-term/rate/value/discount offsets are I and are sent as the oracle zeros.
type NewRPSOrder struct {
	Symbol   Symbol
	Client   string
	Union    string
	BuySell  byte
	Price    Decimal
	Quantity int64
}

type NewRepoOrder struct {
	Symbol    Symbol
	Client    string
	Union     string
	BuySell   byte
	Price     Decimal
	Quantity  int64
	RepoTerm  int64
	RepoRate  Decimal
	StartDisc Decimal
	LowerDisc Decimal
	UpperDisc Decimal
	Block     byte // 'Y'/'N'; default N
}

// RPSTradeForExecution is the PDF 5.7 event (<negdeal>); the fields are the
// D-literals, the exact XML order is I (no oracle capture).
type RPSTradeForExecution struct {
	TransactionID uint64
	SecID         uint16
	Union         string
	Client        string
	Board         string
	SecCode       string
	BuySell       byte
	Price         Decimal
	Volume        int64
	RepoTerm      int64
	RepoRate      Decimal
	RepoValue     Decimal
	Repo2Value    Decimal
}

func (*RPSTradeForExecution) isEvent() {}

// ExecutionReport is the PDF 5.8 event (<report>); D-literals only, XML I.
type ExecutionReport struct {
	TransactionID uint64
	ReportNo      int64
	ReportKind    int
	ReportType    int
	RepoTradeNo   int64
	RepoValue     Decimal
	Repo2Value    Decimal
}

func (*ExecutionReport) isEvent() {}
