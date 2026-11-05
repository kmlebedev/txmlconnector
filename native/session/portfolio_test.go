package session

import "testing"

// TestPortfolioRequestTypes pins the D request type ids (docs/protocol/
// connect.md, portfolios.md). Payload layouts are I.
func TestPortfolioRequestTypes(t *testing.T) {
	if RqFondPortfolio != 86 || RqMarginPosList != 85 || RqMarginPos != 84 || RqGetRpsRepo != 82 {
		t.Fatalf("portfolio request ids = %d/%d/%d/%d, want 86/85/84/82",
			RqFondPortfolio, RqMarginPosList, RqMarginPos, RqGetRpsRepo)
	}
}
