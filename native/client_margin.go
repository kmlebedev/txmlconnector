package native

import (
	"context"
	"fmt"
	"math"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// The T23 commands are a LOCAL render: no RQ is sent (margin.md, D/O).
//
// maxbuy/maxsell follow the RE task-52 formula: a price P per security comes
// from R_SEC_RATE (0x0a, cost of the lot in kopecks), the money comes from the
// money R_CLIENT_ASSET open_balance and the positions from the security
// R_CLIENT_ASSET open_balance / lot size. The default rate is the demo 0.1428
// (I: per-client rates and the P initializer are not recovered yet).

// marginRate is the default credit rate R of the task-52 formula (demo O).
const marginRate = 0.1428

// marginOther is one cross-security position of the formula.
type marginOther struct {
	position float64 // open_balance in units (G)
	value    float64 // V in kopecks
	lotSize  float64
}

// marginMaxBuySell is the exact task-52 formula (margin.md, D+O):
//
//	lot_value_i = V_i * lotsize_i
//	lots_i      = pos_i / lotsize_i
//	q0          = money_units / (R*self_lot_value)
//	cross       = Σ others lots_i*lot_value_i*(1-R)/(R*self_lot_value)
//	maxbuy      = trunc(q0+cross+lots_self*(1-R)/R)
//	maxsell     = trunc(q0+cross+lots_self*(1+R)/R)
//
// The short branch (O): when the sell expression is negative, maxsell=0 and
// maxbuy=|pos_self| (in G units; lotsize!=1 is I). R is the demo long rate
// 0.1428; the store/record that supplies R is not recovered yet (I).
func marginMaxBuySell(moneyUnits, selfPos, selfValue, selfLotSize float64, others []marginOther) (int64, int64) {
	selfLotValue := selfValue * selfLotSize
	if selfLotValue <= 0 || selfLotSize <= 0 {
		return 0, 0
	}
	lotsSelf := selfPos / selfLotSize
	q0 := moneyUnits / (marginRate * selfLotValue)
	cross := 0.0
	for _, other := range others {
		if other.value <= 0 || other.lotSize <= 0 {
			continue
		}
		lotValue := other.value * other.lotSize
		lots := other.position / other.lotSize
		cross += lots * lotValue * (1 - marginRate) / (marginRate * selfLotValue)
	}
	t := q0 + cross
	buy := t + lotsSelf*(1-marginRate)/marginRate
	sell := t + lotsSelf*(1+marginRate)/marginRate
	if sell < 0 {
		// O: short branch — maxsell 0, maxbuy=|pos_self| in G units.
		return int64(math.Trunc(math.Abs(selfPos))), 0
	}
	return int64(math.Trunc(buy)), int64(math.Trunc(sell))
}

func (c *Client) accountByCode(code string) (model.ClientAccount, int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range c.accountOrder {
		state := c.accounts[id]
		if state != nil && state.account.ID == code && !state.account.Removed {
			return state.account, id, true
		}
	}
	return model.ClientAccount{}, 0, false
}

func (c *Client) accountByUnion(union string) (model.ClientAccount, int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range c.accountOrder {
		state := c.accounts[id]
		if state != nil && state.account.Union == union && !state.account.Removed {
			return state.account, id, true
		}
	}
	return model.ClientAccount{}, 0, false
}

// marginRows resolves the catalog identity and computes the two values from the
// loaded stores. zero=true is the MCT behavior of get_max_buy_sell (O).
func (c *Client) marginRows(account model.ClientAccount, rawID int64, zero bool, symbols ...model.Symbol) ([]model.MaxBuySell, error) {
	_ = account
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return nil, fmt.Errorf("transaq: client is not connected")
	}
	lotSize := func(secID uint16) float64 {
		if sec, ok := c.catalog.Security(secID); ok {
			for _, pit := range sec.Pits {
				if (pit.Default || len(sec.Pits) == 1) && pit.LotSize > 0 {
					return float64(pit.LotSize)
				}
			}
		}
		return 1
	}
	rate := func(secID uint16) float64 {
		if c.marginRates != nil && c.marginRates[secID] > 0 {
			return float64(c.marginRates[secID])
		}
		return 0
	}
	moneyUnits := 0.0
	if state := c.positions[positionKey{client: rawID, sym: 0xffff}]; state != nil {
		moneyUnits = float64(state.pos.SaldoIn.Units) // open_balance in kopecks
	}
	rows := make([]model.MaxBuySell, 0, len(symbols))
	for _, symbol := range symbols {
		sec, _, ok := c.catalog.Lookup(symbol.Board, symbol.SecCode)
		if !ok {
			return nil, fmt.Errorf("Инструмент не найден ('seccode'=%s, 'board='%s)", symbol.SecCode, symbol.Board)
		}
		row := model.MaxBuySell{SecID: sec.SecID, Market: int(sec.Market), SecCode: sec.SecCode}
		if !zero {
			if rate(sec.SecID) <= 0 {
				return nil, fmt.Errorf("rate is not known for secid %d (I)", sec.SecID)
			}
			selfPos := 0.0
			if pos, ok := c.secPositions[positionKey{client: rawID, sym: sec.SecID}]; ok {
				selfPos = float64(pos.SaldoIn) // open_balance in units
			}
			var others []marginOther
			for key, pos := range c.secPositions {
				if key.client != rawID || key.sym == sec.SecID {
					continue
				}
				others = append(others, marginOther{
					position: float64(pos.SaldoIn),
					value:    rate(key.sym),
					lotSize:  lotSize(key.sym),
				})
			}
			row.MaxBuy, row.MaxSell = marginMaxBuySell(
				moneyUnits,
				selfPos,
				rate(sec.SecID),
				lotSize(sec.SecID),
				others,
			)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// MaxBuySellTPlus is get_max_buy_sell_tplus (local render, margin.md).
func (c *Client) MaxBuySellTPlus(ctx context.Context, client string, symbols ...model.Symbol) ([]model.MaxBuySell, error) {
	_ = ctx
	if client == "" {
		return nil, fmt.Errorf("Client not specified.")
	}
	account, rawID, ok := c.accountByCode(client)
	if !ok {
		return nil, fmt.Errorf("Wrong client specifited.")
	}
	if account.Type == "mct" {
		return nil, fmt.Errorf("Данная команда не может быть выполнена для MCT клиента")
	}
	return c.marginRows(account, rawID, false, symbols...)
}

// MaxBuySell is get_max_buy_sell (local render, margin.md). An MCT client is
// accepted and renders zero values (O).
func (c *Client) MaxBuySell(ctx context.Context, client, union string, symbols ...model.Symbol) ([]model.MaxBuySell, error) {
	_ = ctx
	if client == "" && union == "" {
		return nil, fmt.Errorf("Parameters \"union\" and \"client\" should not be empty")
	}
	var (
		account model.ClientAccount
		rawID   int64
		ok      bool
	)
	if client != "" {
		account, rawID, ok = c.accountByCode(client)
		if !ok {
			return nil, fmt.Errorf("{8} Неверное значение параметра 'szClient'.")
		}
	} else {
		account, rawID, ok = c.accountByUnion(union)
		if !ok {
			return nil, fmt.Errorf("{8} Неверное значение параметра 'szUnion'.")
		}
	}
	return c.marginRows(account, rawID, account.Type == "mct", symbols...)
}

// ClientSecPermissions is get_cln_sec_permissions (local render, margin.md).
// tradeable/longable/shortable and the rates are I; marketable is the
// R_SEC_MKT_PERMIT flag of the catalog security.
func (c *Client) ClientSecPermissions(ctx context.Context, client string, sym model.Symbol) (model.SecPermissions, error) {
	_ = ctx
	account, _, ok := c.accountByCode(client)
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
	account, _, ok := c.accountByUnion(union)
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
