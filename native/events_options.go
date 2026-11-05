package native

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// Option RQ_MAIN records (0x6f/0x71/0x78) are D-only: their XML rendering is I
// until an S capture or a faked >=0x45 server (options.md). The decoders keep
// the state and emit typed events for the renderer.
func init() {
	registerDecoder(records.TagFamily, decodeOptionFamily)
	registerDecoder(records.TagEndOfFamily, decodeEndOfFamily)
	registerDecoder(records.TagStrikes, decodeOptionStrikes)
}

func decodeOptionFamily(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseFamily(body)
	if err != nil {
		return nil, err
	}
	family := model.OptionFamily{SecID: parsed.SecID, OptCode: parsed.OptCode, MatDate: parsed.First, LotSize: parsed.Second}
	if c.optionFamilies == nil {
		c.optionFamilies = map[uint16][]model.OptionFamily{}
	}
	c.optionFamilies[parsed.SecID] = append(c.optionFamilies[parsed.SecID], family)
	return []model.Event{&family}, nil
}

func decodeEndOfFamily(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseEndOfFamily(body)
	if err != nil {
		return nil, err
	}
	event := model.EndOfFamily{SecID: parsed.SecID, OptCode: parsed.OptCode, Code: parsed.Code, Text: parsed.Text}
	return []model.Event{&event}, nil
}

func decodeOptionStrikes(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseStrikes(body)
	if err != nil {
		return nil, err
	}
	strikes := model.OptionStrikes{SecID: parsed.SecID, FamilyID: parsed.FamilyID, Final: parsed.Final}
	for _, item := range parsed.Items {
		strikes.Items = append(strikes.Items, model.Strike{OptCode: item.OptCode, CP: item.CP, Price: item.Price})
	}
	if c.optionStrikes == nil {
		c.optionStrikes = map[[2]int64]model.OptionStrikes{}
	}
	c.optionStrikes[[2]int64{int64(parsed.SecID), parsed.FamilyID}] = strikes
	return []model.Event{&strikes}, nil
}
