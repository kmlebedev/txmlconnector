package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// gethistorydata candles: R_HISTORY records (0x09). The parts are collected by
// Client.History; Run must not emit them (the DLL only delivers the combined
// <candles> callback).
func init() { registerDecoder(records.TagCandles, decodeCandles) }

func decodeCandles(c *Client, body []byte) ([]model.Event, error) {
	part, err := records.ParseCandles(body)
	if err != nil {
		return nil, err
	}
	c.addHistoryPart(part)
	return nil, nil
}
