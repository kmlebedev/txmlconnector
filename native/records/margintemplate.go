package records

import (
	"encoding/xml"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// marginRuleXML holds the attributes shared by <template>, <market>, <class>
// and <instr> (S: RQ_INQUIRE_UT on production, 2026-11-05).
type marginRuleXML struct {
	Tradeable  string `xml:"tradeable,attr"`
	Longable   string `xml:"longable,attr"`
	Shortable  string `xml:"shortable,attr"`
	Marketable string `xml:"marketable,attr"`
	Long       string `xml:"long,attr"`
	Short      string `xml:"short,attr"`
	RLong      string `xml:"Rlong,attr"`
	RShort     string `xml:"Rshort,attr"`
}

type marginTemplateXML struct {
	marginRuleXML
	ID      int    `xml:"id,attr"`
	Name    string `xml:"name,attr"`
	Markets []struct {
		marginRuleXML
		ID int `xml:"id,attr"`
	} `xml:"market-defaults>market"`
	Classes []struct {
		marginRuleXML
		Letter string `xml:"letter,attr"`
		Market int    `xml:"mkt,attr"`
	} `xml:"instrclass-defaults>class"`
	Instruments []struct {
		marginRuleXML
		SecCode string `xml:"seccode,attr"`
		Market  int    `xml:"mkt,attr"`
	} `xml:"instruments>instr"`
}

func (m marginRuleXML) rule() (model.MarginRule, error) {
	var r model.MarginRule
	r.Tradeable, r.Longable = m.Tradeable == "1", m.Longable == "1"
	r.Shortable, r.Marketable = m.Shortable == "1", m.Marketable == "1"
	for _, f := range []struct {
		dst *model.Decimal
		src string
	}{{&r.Long, m.Long}, {&r.Short, m.Short}, {&r.RLong, m.RLong}, {&r.RShort, m.RShort}} {
		if f.src == "" {
			continue // absent: 0, as the DLL prints reserate 0.00 (S)
		}
		d, err := model.ParseDecimal(f.src)
		if err != nil {
			return r, fmt.Errorf("margin template rate %q: %w", f.src, err)
		}
		*f.dst = d
	}
	return r, nil
}

// ParseMarginTemplate decodes the inflated RQ_INQUIRE_UT document (CP1251 XML).
func ParseMarginTemplate(doc []byte) (*model.MarginTemplate, error) {
	var x marginTemplateXML
	if err := xml.Unmarshal([]byte(DecodeCP1251(string(doc))), &x); err != nil {
		return nil, fmt.Errorf("margin template: %w", err)
	}
	t := &model.MarginTemplate{
		ID: x.ID, Name: x.Name,
		Markets:     map[int]model.MarginRule{},
		Classes:     map[model.MarginClassKey]model.MarginRule{},
		Instruments: map[model.MarginInstrumentKey]model.MarginRule{},
	}
	var err error
	if t.Default, err = x.rule(); err != nil {
		return nil, err
	}
	for _, m := range x.Markets {
		if t.Markets[m.ID], err = m.rule(); err != nil {
			return nil, err
		}
	}
	for _, c := range x.Classes {
		if t.Classes[model.MarginClassKey{Letter: c.Letter, Market: c.Market}], err = c.rule(); err != nil {
			return nil, err
		}
	}
	for _, i := range x.Instruments {
		if t.Instruments[model.MarginInstrumentKey{SecCode: i.SecCode, Market: i.Market}], err = i.rule(); err != nil {
			return nil, err
		}
	}
	return t, nil
}
