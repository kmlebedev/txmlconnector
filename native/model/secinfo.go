package model

import "time"

// SecInfoUpdate is one <sec_info_upd> callback: the changed R_SEC_PARAM (0x5e)
// fields of a security. Pointers follow PDF 4.22: only the fields changed by this
// record are present.
type SecInfoUpdate struct {
	Symbol Symbol
	SecID  uint16
	Market int
	// Kind is the security type from the catalog (4 futures, 5 options): the
	// DLL selects the FUT or OPT form of the callback with it.
	Kind byte
	// FUT form.
	BuyDeposit  *Decimal
	SellDeposit *Decimal
	MinPrice    *Decimal
	MaxPrice    *Decimal
	// OPT form.
	BgoC   *Decimal
	BgoNC  *Decimal
	BgoBuy *Decimal
	// Both forms.
	PointCost *Decimal
}

func (*SecInfoUpdate) isEvent() {}

// SecInfo is one <sec_info> callback (PDF 4.21): the catalog data of a security
// plus the RQ_SEC_SPEC cache. Pointers: the field is present/known; the DLL
// skips empty and zero fields when rendering.
type SecInfo struct {
	Symbol Symbol
	SecID  uint16
	// Catalog identity.
	SecName string
	SecCode string
	Market  int
	PName   string
	// Spec fields (RQ_SEC_SPEC, table in docs/protocol/dictionaries.md).
	MatDate       *time.Time
	ClearingPrice *Decimal
	MinPrice      *Decimal
	MaxPrice      *Decimal
	BuyDeposit    *Decimal
	SellDeposit   *Decimal
	BgoC          *Decimal
	BgoNC         *Decimal
	BgoBuy        *Decimal
	CouponValue   *Decimal
	CouponDate    *time.Time
	CouponPeriod  *int64
	FaceValue     *Decimal
	PutCall       *string
	OptType       *string
	AccruedInt    *Decimal
	PointCost     *Decimal
	LotVolume     *int64
	ISIN          string
	RegNumber     string
	BuybackPrice  *Decimal
	BuybackDate   *time.Time
	CurrencyID    string
}

func (*SecInfo) isEvent() {}
