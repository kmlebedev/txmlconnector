package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// The <orders> feed: R_ORD_NEW (0x03) creates an order, R_ORD_MODIFY (0x10)
// updates it and the DLL renders the resulting order in one <orders> callback
// (orders.md, O).
func init() {
	registerDecoder(records.TagOrderNew, decodeOrderNew)
	registerDecoder(records.TagOrderModify, decodeOrderModify)
}

func decodeOrderNew(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseOrderNew(body)
	if err != nil {
		return nil, err
	}
	order := parsed.Order
	if c.catalog != nil {
		if sec, ok := c.catalog.Security(order.SecID); ok {
			order.SecCode = sec.SecCode
		}
		if board, ok := c.catalog.Board(parsed.BoardID); ok {
			order.Board = board.Code
		}
	}
	c.storeOrderLocked(order)
	return []model.Event{&order}, nil
}

func decodeOrderModify(c *Client, body []byte) ([]model.Event, error) {
	mod, err := records.ParseOrderModify(body)
	if err != nil {
		return nil, err
	}
	order := c.applyOrderModifyLocked(mod)
	if order == nil {
		// The DLL logs "!!!NOT FOUND!!!" and drops the update (D).
		return nil, nil
	}
	updated := *order
	return []model.Event{&updated}, nil
}
