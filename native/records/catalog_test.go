package records

import "testing"

// TestCatalogApplyMutations checks the attribute-update state machine that
// rewrites the R_SEC_NEW bytes: R_SEC_MKT_PERMIT (0x0e) sets or clears the
// market-permission bit, R_SEC_ONOFF (0x0d) the low tradability bits.
func TestCatalogApplyMutations(t *testing.T) {
	c := NewCatalog(loadStartup(t))
	s := SecNew{SecID: 700, Kind: 0, Market: 1, SecCode: "TEST", Class: 'E', Flags: 0x0f,
		Pits: []Pit{{Board: 37, Decimals: 2, MinStep: 1, LotSize: 1, LotDivider: 1, PointCost: 1, Default: true}}}
	c.AddSecurity(s)
	body := []byte{byte(s.SecID >> 8), byte(s.SecID), 0x00}
	if _, err := c.ApplyMutations(Record{Tag: 0x0e, Body: body}); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Security(700); got.Flags&4 != 0 {
		t.Fatalf("R_SEC_MKT_PERMIT=0 must clear the permit bit: %#x", got.Flags)
	}
	if _, err := c.ApplyMutations(Record{Tag: 0x0d, Body: body}); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Security(700); got.Flags&3 != 0 {
		t.Fatalf("R_SEC_ONOFF=0 must clear the tradability bits: %#x", got.Flags)
	}
}
