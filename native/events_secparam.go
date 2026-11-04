package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// <sec_info_upd> from R_SEC_PARAM (tag 0x5e).
func init() { registerDecoder(records.TagSecParam, decodeSecParam) }

// decodeSecParam turns one R_SEC_PARAM body into a typed event. The S-rule from
// the E07 capture: only a record with mask bit 0x8 set makes an event; the rest
// are muted updates.
func decodeSecParam(c *Client, body []byte) ([]model.Event, error) {
	p, err := records.ParseSecParam(body)
	if err != nil {
		return nil, err
	}
	if p.Mask&0x8 == 0 {
		return nil, nil
	}
	if c.catalog == nil {
		return nil, nil
	}
	sec, ok := c.catalog.Security(p.SecID)
	if !ok {
		// The DLL always knows the security from the snapshot; skipping an
		// unknown secid is safer than aborting the poll.
		return nil, nil
	}
	update := p.SecInfoUpdate(sec)
	return []model.Event{&update}, nil
}
