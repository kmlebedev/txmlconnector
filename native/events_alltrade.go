package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// The <alltrades> feed: R_ALLTRADE records (tag 0x08).
func init() { registerDecoder(records.TagAllTrade, decodeAllTrade) }

// decodeAllTrade decodes one R_ALLTRADE body, drops the replays the DLL would
// drop and converts the record to the typed trade.
func decodeAllTrade(c *Client, body []byte) ([]model.Event, error) {
	raw, err := records.ParseAllTrade(body)
	if err != nil {
		return nil, err
	}
	if !c.dedup.Fresh(raw) {
		c.stats.Duplicates++
		return nil, nil
	}
	trade := c.catalog.Trade(raw, c.bias)
	c.stats.Trades++
	return []model.Event{&trade}, nil
}
