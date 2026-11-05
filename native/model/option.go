package model

import "time"

// Options records, S-checked on production 2026-11-05 (options.md): R_FAMILY
// 0x6f lists the expirations of an underlying, R_END_OF_FAMILIES 0x70 closes
// the list, R_STRIKES 0x78 carries the strikes of one expiration.

// OptionFamily is one R_FAMILY record: the family id is the expiration date in
// days since 1970-01-01 (S: 20804 = 17.12.2026), also sent back in
// RQ_FAMILY_STRIKES.
type OptionFamily struct {
	SecID    uint16
	FamilyID int32
	MatDate  time.Time // UTC midnight of FamilyID
	LotSize  int64
}

// OptionFamilies is the list of an underlying, emitted on R_END_OF_FAMILIES
// like the DLL's single <option_families> callback; Families are sorted by
// date. Text is the R_END_OF_FAMILIES text (empty on success; non-empty is I).
type OptionFamilies struct {
	SecID      uint16
	Underlying Symbol
	Families   []OptionFamily
	Text       string
}

func (*OptionFamilies) isEvent() {}

// FamilyDate converts a family id to its expiration date.
func FamilyDate(id int32) time.Time { return time.Unix(int64(id)*86400, 0).UTC() }

// FamilyID converts an expiration date to the family id.
func FamilyID(date time.Time) int32 {
	d := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	return int32(d.Unix() / 86400)
}

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

// OptionStrikes is the strike list of one expiration (R_STRIKES 0x78: W secid,
// L family id, strikes, final B), emitted once final is set, sorted by price
// and then C before P like the DLL (S).
type OptionStrikes struct {
	SecID      uint16
	Underlying Symbol
	FamilyID   int64
	MatDate    time.Time
	Items      []Strike
	Final      byte
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

// OptionsError is the PDF 4.40 <options_failed> error. It is not wired into the
// command path yet: the XML trigger/fields are D-only (options.md I).
type OptionsError struct {
	Code    int
	Message string
}

func (e *OptionsError) Error() string { return e.Message }
