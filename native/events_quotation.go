package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// The <quotations> feed: R_QUOTATION records (tag 0x06).
func init() { registerDecoder(records.TagQuotation, decodeQuotation) }

func decodeQuotation(c *Client, body []byte) ([]model.Event, error) {
	q, err := records.ParseQuotation(body)
	if err != nil {
		return nil, err
	}
	ev := q.Event(c.catalog)
	return []model.Event{&ev}, nil
}
