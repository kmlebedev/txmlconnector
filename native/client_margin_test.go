package native

import (
	"context"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// TestMarginInterfacesAreExplicitI: the T23 request/reply and XML layouts are
// not recovered yet, so the Client methods must return the explicit error
// instead of fabricated values (plan task 23, Step 1 still pending RE).
func TestMarginInterfacesAreExplicitI(t *testing.T) {
	c := &Client{}
	if _, err := c.MaxBuySellTPlus(context.Background(), "TESTCLIEN"); err == nil || !strings.Contains(err.Error(), "layout not recovered (I)") {
		t.Fatalf("MaxBuySellTPlus err = %v", err)
	}
	if _, err := c.MaxBuySell(context.Background(), "TESTCLIEN", "TESTUNION"); err == nil || !strings.Contains(err.Error(), "layout not recovered (I)") {
		t.Fatalf("MaxBuySell err = %v", err)
	}
	if _, err := c.ClientSecPermissions(context.Background(), "TESTCLIEN", model.Symbol{Board: "TQBR", SecCode: "SBER"}); err == nil || !strings.Contains(err.Error(), "layout not recovered (I)") {
		t.Fatalf("ClientSecPermissions err = %v", err)
	}
}
