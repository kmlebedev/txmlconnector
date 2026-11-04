package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// The margin-permission feed: R_SEC_MKT_PERMIT (0x0e) is consumed by the record
// splitter as an attribute record (W secid + B val) and mutated into the
// catalog flags; the event is rendered as <marketord .../> (margin.md, O).
func init() {
	registerDecoder(records.TagSecMktPermit, decodeSecMktPermit)
}

// unknownMarketOrdSecCode is the 12-question-mark placeholder the DLL uses when
// the secid is not in the catalog (margin.md, D literal).
const unknownMarketOrdSecCode = "????????????"

func decodeSecMktPermit(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseSecMktPermit(body)
	if err != nil {
		return nil, err
	}
	event := model.MarketOrd{SecID: parsed.SecID, Permit: parsed.Permit}
	event.SecCode = unknownMarketOrdSecCode
	if c.catalog != nil {
		if sec, ok := c.catalog.Security(parsed.SecID); ok {
			event.SecCode = sec.SecCode
		}
	}
	return []model.Event{&event}, nil
}
