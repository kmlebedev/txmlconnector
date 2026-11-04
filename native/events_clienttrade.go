package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// The client <trades> feed: R_TRD (0x04) is rendered only when the numeric
// client id matches the session's account (O: foreign trades are dropped).
func init() { registerDecoder(records.TagClientTrade, decodeClientTrade) }

func decodeClientTrade(c *Client, body []byte) ([]model.Event, error) {
	raw, err := records.ParseClientTrade(body)
	if err != nil {
		return nil, err
	}
	clientCode, union := "", ""
	for _, id := range c.accountOrder {
		state := c.accounts[id]
		if state != nil && state.raw.ID == raw.ClientID {
			clientCode = state.account.ID
			union = state.account.Union
			break
		}
	}
	if clientCode == "" {
		return nil, nil
	}
	ev := raw.Event(c.catalog)
	ev.Client = clientCode
	ev.Union = union
	return []model.Event{&ev}, nil
}
