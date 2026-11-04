package native

import (
	"context"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// TestPortfolioLayoutsAreExplicitI: the command request/response layouts that
// the RE doc marks I must return the explicit error, never fabricated data.
func TestPortfolioLayoutsAreExplicitI(t *testing.T) {
	c := &Client{}
	checks := []struct {
		name string
		call func() error
	}{
		{"PortfolioTPlus", func() error { _, err := c.PortfolioTPlus(context.Background(), "TESTCLIEN"); return err }},
		{"PortfolioMCT", func() error { _, err := c.PortfolioMCT(context.Background(), "TESTCLIEN"); return err }},
		{"MCPortfolio", func() error {
			_, err := c.MCPortfolio(context.Background(), model.MCPortfolioRequest{Client: "TESTCLIEN"})
			return err
		}},
		{"UnitedPortfolio", func() error { _, err := c.UnitedPortfolio(context.Background(), "TESTCLIEN", "TESTUNION"); return err }},
		{"UnitedEquity", func() error { _, err := c.UnitedEquity(context.Background(), "TESTUNION"); return err }},
		{"UnitedGO", func() error { _, err := c.UnitedGO(context.Background(), "TESTUNION"); return err }},
	}
	for _, check := range checks {
		err := check.call()
		if err == nil || !strings.Contains(err.Error(), "layout not recovered (I)") {
			t.Fatalf("%s err = %v, want the explicit I error", check.name, err)
		}
	}
}
