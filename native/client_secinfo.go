package native

import (
	"context"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// SecuritiesInfo renders the <sec_info> data of the given symbols from the local
// catalog and the RQ_SEC_SPEC cache, exactly like the DLL command
// get_securities_info (it does not touch the server). The cache is filled by the
// RQ_SEC_SPEC request the client sends after every subscribe.
func (c *Client) SecuritiesInfo(ctx context.Context, symbols ...model.Symbol) ([]model.SecInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return nil, errors.New("transaq: client is not connected")
	}
	infos := make([]model.SecInfo, 0, len(symbols))
	for _, symbol := range symbols {
		// Poll writes the catalog under c.mu, so all reads are guarded too.
		sec, _, ok := c.catalog.Lookup(symbol.Board, symbol.SecCode)
		if !ok {
			return nil, fmt.Errorf("transaq: unknown security %s", symbol)
		}
		spec, ok := c.secSpecs[sec.SecID]
		if !ok {
			// The DLL throws "GetSecSpec returned null" here (I, D).
			return nil, fmt.Errorf("transaq: sec spec for %s is not loaded", symbol)
		}
		infos = append(infos, spec.Info(sec))
	}
	return infos, nil
}

// SymbolByMarket resolves the <security> form of get_securities_info: the DLL
// looks the security up by market id and code in the local catalog.
func (c *Client) SymbolByMarket(market int, secCode string) (model.Symbol, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return model.Symbol{}, false
	}
	for _, sec := range c.catalog.List() {
		if int(sec.Market) != market || sec.SecCode != secCode {
			continue
		}
		pit, ok := sec.DefaultPit()
		if !ok {
			return model.Symbol{}, false
		}
		board, ok := c.catalog.Board(pit.Board)
		if !ok {
			return model.Symbol{}, false
		}
		return model.Symbol{Board: board.Code, SecCode: sec.SecCode}, true
	}
	return model.Symbol{}, false
}

// SymbolBySecID resolves the <secid> form of get_securities_info.
func (c *Client) SymbolBySecID(secID uint16) (model.Symbol, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return model.Symbol{}, false
	}
	sec, ok := c.catalog.Security(secID)
	if !ok {
		return model.Symbol{}, false
	}
	pit, ok := sec.DefaultPit()
	if !ok {
		return model.Symbol{}, false
	}
	board, ok := c.catalog.Board(pit.Board)
	if !ok {
		return model.Symbol{}, false
	}
	return model.Symbol{Board: board.Code, SecCode: sec.SecCode}, true
}

// loadSecSpecs requests the spec of the touched securities (the DLL sends
// RQ_SEC_SPEC after every subscribe, in the reverse order of the XML elements)
// and stores the replies in the cache.
func (c *Client) loadSecSpecs(ctx context.Context, ids []uint16) error {
	if len(ids) == 0 {
		return nil
	}
	conn := c.connection()
	if conn == nil {
		return errors.New("transaq: client is not connected")
	}
	payload, err := conn.SecSpec(ctx, ids)
	if err != nil {
		return err
	}
	specs, err := records.ParseSecSpec(payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.secSpecs == nil {
		c.secSpecs = map[uint16]records.SecSpec{}
	}
	for _, spec := range specs {
		c.secSpecs[spec.SecID] = spec
	}
	c.mu.Unlock()
	return nil
}
