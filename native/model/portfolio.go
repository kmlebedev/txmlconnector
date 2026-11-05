package model

// PortfolioTPlus is the preliminary RQ_FOND_PORTFOLIO (86) reply: the D
// inventory reads 2 packed-G followed by 7 W (docs/protocol/portfolios.md, I).
// The field meanings and the <portfolio_tplus> schema are not recovered.
type PortfolioTPlus struct {
	RawG [2]int64
	RawW [7]uint16
}

// PortfolioMCT is a placeholder for get_portfolio_mct: its exchange/XML schema
// is not recovered (I), so no fields are fabricated.
type PortfolioMCT struct{}

// MCPortfolioRequest are the PDF 3.30 command attributes (client/union/
// currency/asset/money/depo/registers/maxbs); the request payload is I.
type MCPortfolioRequest struct {
	Client    string
	Union     string
	Currency  string
	Asset     string
	Money     bool
	Depo      bool
	Registers bool
	MaxBS     bool
}

// MCPortfolio is a placeholder for get_mc_portfolio (schema I).
type MCPortfolio struct{}

// UnitedPortfolio is a placeholder for get_united_portfolio (schema I).
type UnitedPortfolio struct{}

// UnitedEquity is a placeholder for get_united_equity (schema I).
type UnitedEquity struct{}

// UnitedGO is a placeholder for get_united_go (schema I).
type UnitedGO struct{}

// MarginPos is the preliminary RQ_MARGIN_POS (84) reply: the ~19 fields are
// read in the D order but their meanings are I.
type MarginPos struct {
	G1, G2         int64
	W1, W2, W3     uint16
	G3, G4         int64
	G5, G6         int64
	W4, W5, W6, W7 uint16
	B1             byte
	G7, G8, G9     int64
	RawTail        []byte
}
