package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// The <quotes> order book feed: R_QUOTES records (tag 0x07).
func init() { registerDecoder(records.TagQuotes, decodeQuotes) }

func decodeQuotes(c *Client, body []byte) ([]model.Event, error) {
	q, err := records.ParseQuotes(body)
	if err != nil {
		return nil, err
	}
	ev := q.Event(c.catalog)
	return []model.Event{&ev}, nil
}
