package model

// MaxBuySell is one max-buy/max-sell record (PDF 4.32/4.35; the RQ_MARGIN_POS*
// reply layout is not recovered yet, portfolios.md/margin - I). No fields are
// fabricated until RE delivers the field table.
type MaxBuySell struct{}

// SecPermissions are the client security permissions (PDF 4.37; layout I).
type SecPermissions struct{}

// Leverage is the PDF 4.14 leverage notification (RQ_MAIN event; layout I).
type Leverage struct{}

// MarketOrdAllowed is the PDF 4.15 market-order-allowed notification (I).
type MarketOrdAllowed struct{}

func (*Leverage) isEvent()         {}
func (*MarketOrdAllowed) isEvent() {}
