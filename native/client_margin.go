package native

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// Task 23 interfaces (plan): the RQ_MARGIN_POS/LIST request and reply layouts and
// the XML forms of maxbuysell/cln_sec_permissions/leverage/marketordallowed are
// not recovered yet (portfolios.md, I). The methods return the explicit error
// instead of fabricating values; records/model are ready for the oracle tables.
var errMarginLayout = fmt.Errorf("layout not recovered (I)")

// MaxBuySellTPlus is get_max_buy_sell_tplus: layout I.
func (c *Client) MaxBuySellTPlus(ctx context.Context, client string, symbols ...model.Symbol) ([]model.MaxBuySell, error) {
	return nil, fmt.Errorf("transaq: get_max_buy_sell_tplus %w", errMarginLayout)
}

// MaxBuySell is get_max_buy_sell: layout I.
func (c *Client) MaxBuySell(ctx context.Context, client, union string, symbols ...model.Symbol) ([]model.MaxBuySell, error) {
	return nil, fmt.Errorf("transaq: get_max_buy_sell %w", errMarginLayout)
}

// ClientSecPermissions is get_cln_sec_permissions: layout I.
func (c *Client) ClientSecPermissions(ctx context.Context, client string, sym model.Symbol) (model.SecPermissions, error) {
	return model.SecPermissions{}, fmt.Errorf("transaq: get_cln_sec_permissions %w", errMarginLayout)
}
