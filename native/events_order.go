package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// The <orders> feed: R_ORD_NEW (0x03) creates an order, R_ORD_MODIFY (0x10)
// updates it and the DLL renders the resulting order (orders.md, O). Orders of
// other numeric clients are dropped; when the R_CLN_NEW store is not loaded yet
// (the load record order is not guaranteed) the order is buffered and resolved
// when the client record arrives.
func init() {
	registerDecoder(records.TagOrderNew, decodeOrderNew)
	registerDecoder(records.TagOrderModify, decodeOrderModify)
}

func decodeOrderNew(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseOrderNew(body)
	if err != nil {
		return nil, err
	}
	if parsed.Short {
		// Cancelled/removed/denied/rejected short bodies are skipped safely (I);
		// the status-dependent D branches are not recovered yet.
		return nil, nil
	}
	_, known, match := c.orderClientMatchLocked(parsed.ClientID)
	if !known {
		c.pendingOrders = append(c.pendingOrders, parsed)
		return nil, nil
	}
	if !match {
		return nil, nil // foreign client: the DLL drops the order silently (O)
	}
	order := c.buildOrderEventLocked(parsed)
	c.storeOrderLocked(order)
	return []model.Event{&order}, nil
}

func decodeOrderModify(c *Client, body []byte) ([]model.Event, error) {
	mod, err := records.ParseOrderModify(body)
	if err != nil {
		return nil, err
	}
	if mod.Short {
		return nil, nil
	}
	order := c.applyOrderModifyLocked(mod)
	if order == nil {
		// The DLL logs "!!!NOT FOUND!!!" and drops the update (D).
		return nil, nil
	}
	updated := *order
	return []model.Event{&updated}, nil
}
