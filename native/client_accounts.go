package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// The end-of-load callbacks (task 7): R_CLN_NEW (0x02), R_CLIENT_ASSET (0x3b),
// R_OVERNIGHT (0x18) and R_UNION (0x68). 0x62/0x67/0x69 are state-only records
// and have no events.
func init() {
	registerDecoder(records.TagClientNew, decodeClientNew)
	registerDecoder(records.TagClientAsset, decodeClientAsset)
	registerDecoder(records.TagOvernight, decodeOvernight)
	registerDecoder(records.TagUnion, decodeUnion)
}

// accountState is the client store entry: the typed account plus the raw
// record fields the position rendering needs (market, union).
type accountState struct {
	account model.ClientAccount
	raw     records.ClientNew
}

// positionKey identifies a stored position: a client and a security (0xffff is
// the money asset).
type positionKey struct {
	client int64
	sym    uint16
}

// positionState is the stored position; task 7 keeps only money positions.
type positionState struct {
	client int64
	sym    uint16
	reg    byte
	pos    model.MoneyPosition
}

func (c *Client) resetAccountsLocked() {
	c.accounts = map[int64]*accountState{}
	c.accountOrder = nil
	c.positions = map[positionKey]*positionState{}
	c.positionOrder = nil
	c.unions = nil
	c.overnight = model.Overnight{}
}

func decodeClientNew(c *Client, body []byte) ([]model.Event, error) {
	raw, err := records.ParseClientNew(body)
	if err != nil {
		return nil, err
	}
	typ, _ := records.ClientTypeName(raw.Type)
	account := model.ClientAccount{
		ID:       raw.Code,
		Type:     typ,
		Currency: records.CurrencyName(raw.CurrencyID),
		Union:    raw.Union,
		Market:   int(raw.Market),
	}
	if c.accounts == nil {
		c.resetAccountsLocked()
	}
	if _, seen := c.accounts[raw.ID]; !seen {
		c.accountOrder = append(c.accountOrder, raw.ID)
	}
	c.accounts[raw.ID] = &accountState{account: account, raw: raw}
	return []model.Event{&account}, nil
}

func decodeClientAsset(c *Client, body []byte) ([]model.Event, error) {
	raw, err := records.ParseClientAsset(body)
	if err != nil {
		return nil, err
	}
	key := positionKey{client: raw.ClientID, sym: raw.SymID}
	if c.positions == nil {
		c.resetAccountsLocked()
	}
	state := c.positions[key]
	if state == nil {
		state = &positionState{client: raw.ClientID, sym: raw.SymID}
		c.positions[key] = state
		c.positionOrder = append(c.positionOrder, key)
	}
	if account := c.accounts[raw.ClientID]; account != nil {
		state.pos.Client = account.account.ID
		state.pos.Union = account.account.Union
		state.pos.Market = account.account.Market
	}
	if raw.SymID != 0xffff {
		// Paper and FORTS positions belong to task 20: keep the state but do
		// not render it as a money position yet.
		return nil, nil
	}
	var (
		open, bought, sold int64
		commission         int64
		currency           string
	)
	for _, reg := range raw.Regs {
		if state.reg == 0 {
			state.reg = reg.Reg
		}
		open += reg.OpenBalance
		bought += reg.Bought
		sold += reg.Sold
		commission += reg.Commission
		if reg.Currency != "" {
			currency = reg.Currency
		}
	}
	state.pos.Currency = currency
	state.pos.SaldoIn = moneyDecimal(open)
	state.pos.Bought = moneyDecimal(bought)
	state.pos.Sold = moneyDecimal(sold)
	state.pos.Saldo = moneyDecimal(open + bought - sold)
	state.pos.OrdBuy = moneyDecimal(0)
	state.pos.OrdBuyCond = moneyDecimal(0)
	state.pos.Comission = moneyDecimal(commission)
	state.pos.Asset, state.pos.ShortName = moneyAssetInfo(state.pos.Market, currency)
	state.pos.Register = registerName(state.reg)
	update := state.pos
	return []model.Event{&update}, nil
}

// moneyDecimal converts a wire money value to the DLL money scale (2): the
// Decimal printer then keeps at least one decimal digit (300000.0).
func moneyDecimal(units int64) model.Decimal {
	return model.Decimal{Units: units, Scale: 2}
}

// moneyAssetInfo is the <asset>/<shortname> pair of a money position. Only the
// demo case is confirmed (RUR on the MICEX market); the other DLL asset enum
// values are not recovered, so they render empty and are documented as open.
func moneyAssetInfo(clientMarket int, currency string) (asset, shortName string) {
	if currency == "RUR" {
		return "FOND_MICEX", "Деньги КЦБ ММВБ (RUR)"
	}
	return "", ""
}

// registerName is the micex register name by reg code (only T0 is confirmed).
func registerName(reg byte) string {
	if reg == 1 {
		return "T0"
	}
	return ""
}

func decodeOvernight(c *Client, body []byte) ([]model.Event, error) {
	status, err := records.ParseOvernight(body)
	if err != nil {
		return nil, err
	}
	c.overnight = model.Overnight{Status: status}
	return nil, nil
}

func decodeUnion(c *Client, body []byte) ([]model.Event, error) {
	name, err := records.ParseUnion(body)
	if err != nil {
		return nil, err
	}
	union := model.Union{ID: name}
	c.unions = append(c.unions, union)
	return []model.Event{&union}, nil
}

// Accounts returns the client accounts of the session in arrival order.
func (c *Client) Accounts() []model.ClientAccount {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.ClientAccount, 0, len(c.accountOrder))
	for _, id := range c.accountOrder {
		if state := c.accounts[id]; state != nil {
			out = append(out, state.account)
		}
	}
	return out
}

// MoneyPositions returns the money positions of the session in arrival order.
// Paper and FORTS positions are not implemented yet (task 20).
func (c *Client) MoneyPositions() []model.MoneyPosition {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.MoneyPosition, 0, len(c.positionOrder))
	for _, key := range c.positionOrder {
		if state := c.positions[key]; state != nil && state.sym == 0xffff {
			out = append(out, state.pos)
		}
	}
	return out
}

// Overnight returns the current R_OVERNIGHT flag (default false).
func (c *Client) Overnight() model.Overnight {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.overnight
}

// Unions returns the <union> records seen in the session.
func (c *Client) Unions() []model.Union {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]model.Union(nil), c.unions...)
}
