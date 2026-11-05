package native

import (
	"sort"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// Option RQ_MAIN records (options.md, S on production 2026-11-05): the DLL
// collects R_FAMILY 0x6f per underlying and renders one <option_families> on
// R_END_OF_FAMILIES 0x70; R_STRIKES 0x78 renders one <family_strikes> once its
// final byte is set. Decoders run under the client lock.
func init() {
	registerDecoder(records.TagFamily, decodeOptionFamily)
	registerDecoder(records.TagEndOfFamilies, decodeEndOfFamilies)
	registerDecoder(records.TagEndOfFamily, decodeEndOfFamily)
	registerDecoder(records.TagStrikes, decodeOptionStrikes)
}

func decodeOptionFamily(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseFamily(body)
	if err != nil {
		return nil, err
	}
	id := int32(parsed.First)
	if c.optionFamilies == nil {
		c.optionFamilies = map[uint16]map[int32]model.OptionFamily{}
	}
	if c.optionFamilies[parsed.SecID] == nil {
		c.optionFamilies[parsed.SecID] = map[int32]model.OptionFamily{}
	}
	c.optionFamilies[parsed.SecID][id] = model.OptionFamily{
		SecID: parsed.SecID, FamilyID: id, MatDate: model.FamilyDate(id), LotSize: parsed.Second,
	}
	return nil, nil
}

// sortedFamiliesLocked returns the families of an underlying by date.
func (c *Client) sortedFamiliesLocked(secID uint16) []model.OptionFamily {
	var out []model.OptionFamily
	for _, f := range c.optionFamilies[secID] {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FamilyID < out[j].FamilyID })
	return out
}

// symbolLocked resolves a secid through the catalog (default pit board).
func (c *Client) symbolLocked(secID uint16) model.Symbol {
	if c.catalog == nil {
		return model.Symbol{}
	}
	sec, ok := c.catalog.Security(secID)
	if !ok {
		return model.Symbol{}
	}
	sym := model.Symbol{SecCode: sec.SecCode}
	if pit, ok := sec.DefaultPit(); ok {
		if board, ok := c.catalog.Board(pit.Board); ok {
			sym.Board = board.Code
		}
	}
	return sym
}

func decodeEndOfFamilies(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseEndOfFamilies(body)
	if err != nil {
		return nil, err
	}
	return []model.Event{&model.OptionFamilies{
		SecID: parsed.SecID, Underlying: c.symbolLocked(parsed.SecID),
		Families: c.sortedFamiliesLocked(parsed.SecID), Text: records.DecodeCP1251(parsed.Text),
	}}, nil
}

func decodeEndOfFamily(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseEndOfFamily(body)
	if err != nil {
		return nil, err
	}
	event := model.EndOfFamily{SecID: parsed.SecID, OptCode: parsed.OptCode, Code: parsed.Code, Text: records.DecodeCP1251(parsed.Text)}
	return []model.Event{&event}, nil
}

func decodeOptionStrikes(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseStrikes(body)
	if err != nil {
		return nil, err
	}
	key := [2]int64{int64(parsed.SecID), parsed.FamilyID}
	if c.optionStrikes == nil {
		c.optionStrikes = map[[2]int64]*model.OptionStrikes{}
	}
	acc := c.optionStrikes[key]
	if acc == nil {
		acc = &model.OptionStrikes{
			SecID: parsed.SecID, Underlying: c.symbolLocked(parsed.SecID),
			FamilyID: parsed.FamilyID, MatDate: model.FamilyDate(int32(parsed.FamilyID)),
		}
		c.optionStrikes[key] = acc
	}
	for _, item := range parsed.Items {
		acc.Items = append(acc.Items, model.Strike{OptCode: item.OptCode, CP: item.CP, Price: item.Price})
	}
	acc.Final = parsed.Final
	if parsed.Final == 0 {
		return nil, nil // more R_STRIKES follow (I: never seen live)
	}
	delete(c.optionStrikes, key)
	sort.SliceStable(acc.Items, func(i, j int) bool {
		if acc.Items[i].Price != acc.Items[j].Price {
			return acc.Items[i].Price < acc.Items[j].Price
		}
		return acc.Items[i].CP < acc.Items[j].CP // 'C' before 'P'
	})
	return []model.Event{acc}, nil
}
