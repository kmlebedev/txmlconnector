package model

// QuotationFields are the changed <quotation> fields of one R_QUOTATION event.
// Pointers: nil means the field was not in this update (PDF 4.7, only changed
// fields are sent). Prices are Decimal with the decimals of the security mode.
type QuotationFields struct {
	// Fields confirmed on the demo wire (E07/r9, docs/protocol/alltrades.md).
	BidDepth              *int64
	NumBids               *int64
	OfferDepth            *int64
	NumOffers             *int64
	BidDepthT             *int64
	OfferDepthT           *int64
	Bid                   *Decimal
	Offer                 *Decimal
	NumTrades             *int64
	VolToday              *int64
	OpenPositions         *int64
	Quantity              *int64
	Time                  *string
	Last                  *Decimal
	Change                *Decimal
	PriceMinusPrevWaPrice *Decimal
	ValToday              *Decimal
	WaPrice               *Decimal
	HighBid               *Decimal
	LowOffer              *Decimal
	High                  *Decimal
	Low                   *Decimal
	Open                  *Decimal
	ClosePrice            *Decimal
	Status                *string
	TradingStatus         *string
	LCurrentPrice         *Decimal
	BuyDeposit            *Decimal
	SellDeposit           *Decimal
	// The DLL renders point_cost from the security catalog at the start of the
	// callback (FUN_1800f7b40); it is not carried by the R_QUOTATION record.
	PointCost *Decimal
	// PDF 4.7 fields not observed on the demo wire yet: the mask bits are not
	// recovered, so they stay nil until task 12/RE-1 maps them.
	AccruedIntValue  *Decimal
	DeltaPositions   *int64
	Yield            *Decimal
	YieldAtWaPrice   *Decimal
	MarketPriceToday *Decimal
	CloseYield       *Decimal
	Volatility       *Decimal
	TheoreticalPrice *Decimal
	BgoBuy           *Decimal
}

// Quotation is one <quotation> callback: the changed fields of one security on
// one board. The board is part of the identity because the same security can
// have different price decimals in different modes (HYDR TQBR 4 vs MCT 2).
type Quotation struct {
	Symbol Symbol
	SecID  uint16
	Board  string
	Fields QuotationFields
}

func (*Quotation) isEvent() {}
