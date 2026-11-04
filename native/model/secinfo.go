package model

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
