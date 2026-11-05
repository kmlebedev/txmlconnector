package native

import (
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// Text messages: R_MSG (0x01) is an unsized record; all its records of one
// RQ_MAIN reply are batched into ONE <messages> callback (messages.md, O).
func init() {
	registerDecoder(records.TagMessage, decodeMessage)
}

func decodeMessage(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseRMsg(body)
	if err != nil {
		return nil, err
	}
	// Decoders run under Client.mu; the RQ_TIME bias converts the server wall
	// seconds to UTC (UTC = wall + bias, messages.md D).
	wall := time.Unix(parsed.Date, 0).UTC()
	instant := wall.Add(c.bias).UTC()
	message := model.Message{
		Time:     instant,
		WallTime: wall,
		Urgent:   parsed.Flags&1 != 0 || parsed.Flags&8 != 0,
		From:     parsed.From,
		Text:     parsed.Text,
		Flags:    parsed.Flags,
	}
	return []model.Event{&message}, nil
}
