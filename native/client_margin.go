package native

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// The T23 commands are a LOCAL render: no RQ is sent (margin.md, D/O). The
// identity fields come from the session catalog, <marketable> from the
// R_SEC_MKT_PERMIT bit. The maxbuy/maxsell formula and the permission mask/rate
// store are I (task-52): the DLL keeps price/rate inputs in internal stores that
// RQ_MAIN does not expose, so the values stay 0/false until RE delivers them.

func (c *Client) accountByCode(code string) (model.ClientAccount, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range c.accountOrder {
		state := c.accounts[id]
		if state != nil && state.account.ID == code && !state.account.Removed {
			return state.account, true
		}
	}
	return model.ClientAccount{}, false
}

func (c *Client) accountByUnion(union string) (model.ClientAccount, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range c.accountOrder {
		state := c.accounts[id]
		if state != nil && state.account.Union == union && !state.account.Removed {
			return state.account, true
		}
	}
	return model.ClientAccount{}, false
}

// marginRows resolves the catalog identity of the requested securities and
// fills the rendered rows. The maxbuy/maxsell values are I (see above).
func (c *Client) marginRows(symbols ...model.Symbol) ([]model.MaxBuySell, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return nil, fmt.Errorf("transaq: client is not connected")
	}
	rows := make([]model.MaxBuySell, 0, len(symbols))
	for _, symbol := range symbols {
		sec, _, ok := c.catalog.Lookup(symbol.Board, symbol.SecCode)
		if !ok {
			return nil, fmt.Errorf("Инструмент не найден ('seccode'=%s, 'board='%s)", symbol.SecCode, symbol.Board)
		}
		rows = append(rows, model.MaxBuySell{SecID: sec.SecID, Market: int(sec.Market), SecCode: sec.SecCode})
	}
	return rows, nil
}

// MaxBuySellTPlus is get_max_buy_sell_tplus (local render, margin.md).
func (c *Client) MaxBuySellTPlus(ctx context.Context, client string, symbols ...model.Symbol) ([]model.MaxBuySell, error) {
	_ = ctx
	if client == "" {
		return nil, fmt.Errorf("Client not specified.")
	}
	account, ok := c.accountByCode(client)
	if !ok {
		return nil, fmt.Errorf("Wrong client specifited.")
	}
	if account.Type == "mct" {
		return nil, fmt.Errorf("Данная команда не может быть выполнена для MCT клиента")
	}
	return c.marginRows(symbols...)
}

// MaxBuySell is get_max_buy_sell (local render, margin.md). An MCT client is
// accepted and renders zero values (O).
func (c *Client) MaxBuySell(ctx context.Context, client, union string, symbols ...model.Symbol) ([]model.MaxBuySell, error) {
	_ = ctx
	if client == "" && union == "" {
		return nil, fmt.Errorf("Parameters \"union\" and \"client\" should not be empty")
	}
	if client != "" {
		if _, ok := c.accountByCode(client); !ok {
			return nil, fmt.Errorf("{8} Неверное значение параметра 'szClient'.")
		}
	} else if _, ok := c.accountByUnion(union); !ok {
		return nil, fmt.Errorf("{8} Неверное значение параметра 'szUnion'.")
	}
	return c.marginRows(symbols...)
}

// ClientSecPermissions is get_cln_sec_permissions (local render, margin.md).
// tradeable/longable/shortable and the rates are I; marketable is the
// R_SEC_MKT_PERMIT flag of the catalog security.
func (c *Client) ClientSecPermissions(ctx context.Context, client string, sym model.Symbol) (model.SecPermissions, error) {
	_ = ctx
	account, ok := c.accountByCode(client)
	if !ok {
		return model.SecPermissions{}, fmt.Errorf("Неизвестный код клиента")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return model.SecPermissions{}, fmt.Errorf("transaq: client is not connected")
	}
	sec, board, ok := c.catalog.Lookup(sym.Board, sym.SecCode)
	if !ok {
		return model.SecPermissions{}, fmt.Errorf("Инструмент не найден ('seccode'=%s, 'board='%s)", sym.SecCode, sym.Board)
	}
	marketable := false
	if mut, ok := c.catalog.Security(sec.SecID); ok {
		marketable = mut.Flags&4 != 0
	}
	return model.SecPermissions{
		Client: account.ID, Union: account.Union,
		SecID: sec.SecID, SecCode: sec.SecCode, Market: int(sec.Market), Board: board.Code,
		Marketable: marketable,
		// I: the permission mask and rates of FUN_18001a700 are not in RQ_MAIN.
		RegRateLong:  model.Decimal{Units: 0, Scale: 2},
		RegRateShort: model.Decimal{Units: 0, Scale: 2},
	}, nil
}

// ClientSecPermissionsUnion resolves a union to its first account and renders
// the permissions form (the command accepts client and/or union).
func (c *Client) ClientSecPermissionsUnion(ctx context.Context, union string, sym model.Symbol) (model.SecPermissions, error) {
	account, ok := c.accountByUnion(union)
	if !ok {
		return model.SecPermissions{}, fmt.Errorf("Неизвестный код юниона")
	}
	perm, err := c.ClientSecPermissions(ctx, account.ID, sym)
	if err != nil {
		return model.SecPermissions{}, err
	}
	perm.Union = union
	return perm, nil
}
