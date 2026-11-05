package native

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// The portfolio command request/response layouts (and the <portfolios> element
// schema) are not recovered yet (portfolios.md: I). The methods return an
// explicit error instead of fabricating data; the D reply parsers in
// native/records are ready for the oracle fixtures.
var errPortfolioLayout = fmt.Errorf("layout not recovered (I)")

// PortfolioTPlus is get_portfolio (RQ_FOND_PORTFOLIO 86): layout I.
func (c *Client) PortfolioTPlus(ctx context.Context, client string) (model.PortfolioTPlus, error) {
	return model.PortfolioTPlus{}, fmt.Errorf("transaq: get_portfolio %w", errPortfolioLayout)
}

// PortfolioMCT is get_portfolio_mct: layout I.
func (c *Client) PortfolioMCT(ctx context.Context, client string) (model.PortfolioMCT, error) {
	return model.PortfolioMCT{}, fmt.Errorf("transaq: get_portfolio_mct %w", errPortfolioLayout)
}

// MCPortfolio is get_mc_portfolio: layout I.
func (c *Client) MCPortfolio(ctx context.Context, req model.MCPortfolioRequest) (model.MCPortfolio, error) {
	return model.MCPortfolio{}, fmt.Errorf("transaq: get_mc_portfolio %w", errPortfolioLayout)
}

// UnitedPortfolio is get_united_portfolio: layout I.
func (c *Client) UnitedPortfolio(ctx context.Context, client, union string) (model.UnitedPortfolio, error) {
	return model.UnitedPortfolio{}, fmt.Errorf("transaq: get_united_portfolio %w", errPortfolioLayout)
}

// UnitedEquity is get_united_equity: layout I.
func (c *Client) UnitedEquity(ctx context.Context, union string) (model.UnitedEquity, error) {
	return model.UnitedEquity{}, fmt.Errorf("transaq: get_united_equity %w", errPortfolioLayout)
}

// UnitedGO is get_united_go: layout I.
func (c *Client) UnitedGO(ctx context.Context, union string) (model.UnitedGO, error) {
	return model.UnitedGO{}, fmt.Errorf("transaq: get_united_go %w", errPortfolioLayout)
}
