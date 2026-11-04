package native

import (
	"context"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
)

// GetFortsPositions runs get_forts_positions: the FORTS positions and money come
// in the RQ_CLIENT_HOLDINGS (42) direct reply, not through RQ_MAIN (O).
func (c *Client) GetFortsPositions(ctx context.Context, client string) ([]model.FortsPosition, *model.FortsMoney, error) {
	c.mu.Lock()
	numericID, ok := c.clientNumericIDLocked(client, "")
	catalog := c.catalog
	c.mu.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("transaq: unknown client %q", client)
	}
	conn := c.connection()
	if conn == nil {
		return nil, nil, errors.New("transaq: client is not connected")
	}
	payload, err := conn.Request(ctx, session.RqClientHoldings, nil)
	if err != nil {
		return nil, nil, err
	}
	holdings, err := records.ParseClientHoldings(payload)
	if err != nil {
		return nil, nil, err
	}
	var positions []model.FortsPosition
	var varMargin int64
	market := 4 // when there are no FORTS positions the money is still FORTS
	for _, h := range holdings {
		if h.ClientID != numericID {
			continue
		}
		var sec records.SecNew
		if catalog != nil {
			if found, ok := catalog.Security(h.SecID); ok {
				sec = found
				market = int(sec.Market)
			}
		}
		positions = append(positions, model.FortsPosition{
			// The oracle renders <forts_position><client></client> empty even
			// for the matching session client (I); forts_money carries the code.
			Client: "", SecID: h.SecID, SecCode: sec.SecCode, Market: int(sec.Market),
			StartNet: h.StartNet, OpenBuys: h.OpenBuys, OpenSells: h.OpenSells, TotalNet: h.TotalNet,
			TodayBuy: h.TodayBuy, TodaySell: h.TodaySell,
			OptMargin: model.Decimal{}, VarMargin: model.Decimal{Units: h.VarMargin, Scale: 2},
			ExpirationPos: 0,
		})
		varMargin += h.VarMargin
	}
	money := &model.FortsMoney{
		Client: clientName(c, numericID), Market: market, ShortName: "Деньги FORTS",
		Blocked: model.Decimal{}, VarMargin: model.Decimal{Units: varMargin, Scale: 2},
		Current: model.Decimal{}, Free: model.Decimal{},
	}
	return positions, money, nil
}

// clientName maps the numeric id back to the client code.
func clientName(c *Client, numericID int64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range c.accountOrder {
		if state := c.accounts[id]; state != nil && state.raw.ID == numericID {
			return state.account.ID
		}
	}
	return ""
}
