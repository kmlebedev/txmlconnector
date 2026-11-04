package model

// The options records (0x6f/0x71/0x78) are D-only: the XML element order/meaning
// is I until the S series / a faked >=0x45 server (options.md). The modelled
// fields are the ones the D layout reads; MatDate/LotSize semantics are I.
type OptionFamily struct {
	SecID   uint16
	OptCode string
	MatDate int64 // first L of R_FAMILY: I (probably mat date/lot size)
	LotSize int64 // second L of R_FAMILY: I
}

func (*OptionFamily) isEvent() {}

// EndOfFamily is R_END_OF_FAMILY (0x71): the DLL builder emits <options_failed>
// for errors (trigger semantics I).
type EndOfFamily struct {
	SecID   uint16
	OptCode string
	Code    int64
	Text    string
}

func (*EndOfFamily) isEvent() {}

// Strike is one R_STRIKES item: Z26 opt_code, B cp, L price (D).
type Strike struct {
	OptCode string
	CP      byte
	Price   int64
}

// OptionStrikes is R_STRIKES (0x78): W secid, L family id, strikes, final B.
type OptionStrikes struct {
	SecID    uint16
	FamilyID int64
	Items    []Strike
	Final    byte
}

func (*OptionStrikes) isEvent() {}

// OptionsRequest is the get_options input (PDF 3.37): a list of option codes.
type OptionsRequest struct {
	OptCodes []string
}

// Option is one option from the 151/150 reply stream (D form is I); only the
// identity is modelled for now.
type Option struct {
	OptCode string
}

// OptionsError is the PDF 4.40 <options_failed> error.
type OptionsError struct {
	Code    int
	Message string
}

func (e *OptionsError) Error() string { return e.Message }
