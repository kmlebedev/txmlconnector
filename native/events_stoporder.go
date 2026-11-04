package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// R_STOP (0x54): conditional/stop order updates are rendered in the <orders>
// envelope (O).
func init() { registerDecoder(records.TagStopOrder, decodeStopOrder) }

func decodeStopOrder(c *Client, body []byte) ([]model.Event, error) {
	raw, err := records.ParseStopOrder(body)
	if err != nil {
		// A valid but not-recovered update (status block/high mask bits) must
		// not abort the poll: drop it (I) and keep the session alive.
		return nil, nil
	}
	clientCode := ""
	if raw.HasClient {
		if raw.ClientID < 0 {
			return nil, nil
		}
		matched := false
		for _, id := range c.accountOrder {
			if state := c.accounts[id]; state != nil && state.raw.ID == raw.ClientID {
				clientCode = state.account.ID
				matched = true
				break
			}
		}
		if !matched {
			return nil, nil // foreign/unknown client: the DLL ignores the update
		}
	}
	if raw.HasSec && raw.SecID == 0xffff {
		return nil, nil
	}
	ev := raw.Event(c.catalog)
	ev.Client = clientCode
	ev.Author = c.cfg.Login
	return []model.Event{&ev}, nil
}
