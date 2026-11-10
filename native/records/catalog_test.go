package records

import (
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
)

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

func TestCatalogSince(t *testing.T) {
	c := NewCatalog(&model.Startup{})
	c.AddSecurity(SecNew{SecID: 1, SecCode: "A"})
	c.AddSecurity(SecNew{SecID: 2, SecCode: "B"})
	c.AddSecurity(SecNew{SecID: 1, SecCode: "A"}) // repeat: not a new arrival
	c.AddSecurity(SecNew{SecID: 3, SecCode: "C"})
	got := c.Since(1)
	if len(got) != 2 || got[0].SecID != 2 || got[1].SecID != 3 {
		t.Fatalf("Since(1) = %+v, want secids 2, 3", got)
	}
	if c.Since(3) != nil || c.Since(10) != nil {
		t.Fatal("Since past the end must be nil")
	}
	if len(c.Since(-1)) != 3 {
		t.Fatal("Since(-1) must return all")
	}
}
