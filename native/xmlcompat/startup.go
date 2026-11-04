// Package xmlcompat renders the records decoded by native/records as the XML
// messages the vendor DLL emits to callbacks (see client/commands), so a
// native backend can serve existing XML consumers unchanged. It is the only
// package that knows the DLL's XML shape; binary decoding lives in
// native/records, the domain types in native/model.
package xmlcompat

import (
	"encoding/xml"
	"slices"

	"github.com/kmlebedev/txmlconnector/client/commands"
	"github.com/kmlebedev/txmlconnector/native/model"
)

// MarketsXML returns <markets> as the DLL emits it: ordered by id.
func MarketsXML(st *model.Startup) commands.Markets {
	var out commands.Markets
	for _, m := range st.Markets {
		out.Items = append(out.Items, commands.Market{ID: int(m.ID), Name: m.Code})
	}
	slices.SortFunc(out.Items, func(a, b commands.Market) int { return a.ID - b.ID })
	return out
}

// BoardsXML returns <boards> in wire order. The DLL reports FORTS options
// (market 5) under FORTS (market 4) (FUN_1800c07f0).
func BoardsXML(st *model.Startup) commands.Boards {
	var out commands.Boards
	for _, b := range st.Boards {
		market := int(b.Market)
		if market == marketFORTSOption {
			market = marketFORTS
		}
		out.Items = append(out.Items, commands.Board{ID: b.Code, Name: b.Name, Market: market, Type: int(b.Type)})
	}
	return out
}

// CandleKindsXML returns <candlekinds> in wire order.
func CandleKindsXML(st *model.Startup) commands.CandleKinds {
	var out commands.CandleKinds
	for _, k := range st.CandleKinds {
		out.Items = append(out.Items, commands.CandleKind{ID: int(k.ID), Period: int(k.Period), Name: k.Name})
	}
	return out
}

// Marshal renders a structure as a callback message.
func Marshal(v any) (string, error) {
	b, err := xml.Marshal(v)
	return string(b), err
}
