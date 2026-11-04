package xmlcompat

import (
	"slices"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// Attribute order in the DLL output, fixed by FUN_18011f4a0 and verified
// byte-for-byte against the demo-server capture (see TestSecurityXMLBytes):
// sec_tz, seccode, instrclass, currency, board, shortname, decimals, market,
// sectype, opmask, minstep, lotsize, lotdivider, point_cost, quotestype,
// MIC, ticker, currencyid.

// yesNo renders an opmask flag the way the DLL does.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// secTZN returns the security time zone name and whether the DLL would print
// the <sec_tz> element for this record.
func secTZN(s records.SecNew) string {
	if !s.HasTimeZone {
		return ""
	}
	name, _ := records.TimeZoneName(s.TimeZone)
	return name
}

// SecurityXML renders one <security> exactly in the DLL's snapshot-chunk
// element order (verified byte-for-byte against the load callbacks).
func SecurityXML(st *model.Startup, s records.SecNew) string {
	return securityXML(st, s, false)
}

// SecurityDumpXML renders one <security> in the order of the get_securities
// dump writer FUN_18011f4a0: sec_tz, board, seccode, instrclass, currency,
// shortname, decimals, market, minstep..., point_cost, opmask, MIC, ticker,
// sectype, quotestype, currencyid - different from the load-chunk writer.
func SecurityDumpXML(st *model.Startup, s records.SecNew) string {
	return securityXML(st, s, true)
}

func securityXML(st *model.Startup, s records.SecNew, dump bool) string {
	f := DeriveSecurityFlags(st, s)
	var tz, seccode, instr, currency, board, shortname, decimals, market, sectype, opmask, pitBlock, quotes, mic, ticker, currencyid string
	if t := secTZN(s); t != "" {
		tz = "<sec_tz><![CDATA[" + t + "]]></sec_tz>"
	}
	seccode = tagText("seccode", s.SecCode)
	switch {
	case s.Class != 0:
		instr = string(rune(s.Class))
	case kindClass[s.Kind] != 0:
		instr = string(rune(kindClass[s.Kind]))
	}
	instr = tagText("instrclass", instr)
	cur := s.Currency
	if cur == "" && s.Market == marketMICEX {
		cur = "RUR"
	}
	if cur != "" {
		currency = tagText("currency", cur)
	}
	pit, hasPit := s.DefaultPit()
	boardCode := ""
	if hasPit {
		boardCode = st.BoardCode(pit.Board)
	}
	board = tagText("board", boardCode)
	shortname = tagText("shortname", s.ShortName)
	dec := 0
	if hasPit {
		dec = int(pit.Decimals)
	}
	decimals = tagInt("decimals", dec)
	mktVal := int(s.Market)
	if mktVal == marketFORTSOption {
		mktVal = marketFORTS
	}
	market = tagInt("market", mktVal)
	sectype = tagText("sectype", sectypeNames[s.Kind])
	if f.Active {
		opmask = `<opmask usecredit="` + yesNo(f.OpMask&0x10 != 0) +
			`" bymarket="` + yesNo(f.ByMarket) +
			`" nosplit="` + yesNo(f.OpMask&8 != 0) +
			`" fok="` + yesNo(f.OpMask&2 != 0) +
			`" ioc="` + yesNo(f.OpMask&1 != 0) +
			`" immorcancel="` + yesNo(f.OpMask&2 != 0) +
			`" cancelbalance="` + yesNo(f.OpMask&1 != 0) + `"/>`
	}
	if hasPit {
		lot := int(pit.LotSize)
		if s.Market == marketFORTS || s.Market == marketFORTSOption {
			lot = 1
		}
		var pb strings.Builder
		pb.WriteString(tagText("minstep", FormatMinStep(float64(pit.MinStep)/pow10(dec), dec)))
		pb.WriteString(tagInt("lotsize", lot))
		pb.WriteString(tagInt("lotdivider", int(pit.LotDivider)))
		pb.WriteString(tagText("point_cost", FormatPointCost(pit.PointCost)))
		pitBlock = pb.String()
	}
	quotes = tagInt("quotestype", int(f.QuotesTyp))
	if s.MIC != "" {
		mic = tagText("MIC", s.MIC)
	}
	if s.Ticker != "" {
		ticker = tagText("ticker", s.Ticker)
	}
	if cid := currencyID(pit, cur, hasPit); cid != "" {
		currencyid = tagText("currencyid", cid)
	}
	var b strings.Builder
	b.WriteString(`<security secid="` + strconv.Itoa(int(s.SecID)) +
		`" active="` + strconv.FormatBool(f.Active) + `">`)
	b.WriteString(tz)
	if dump {
		b.WriteString(board + seccode + instr + currency + shortname + decimals + market)
		b.WriteString(pitBlock + opmask + mic + ticker + sectype + quotes + currencyid)
	} else {
		b.WriteString(seccode + instr + currency + board + shortname + decimals + market)
		b.WriteString(sectype + opmask + pitBlock + quotes + mic + ticker + currencyid)
	}
	b.WriteString("</security>")
	return b.String()
}

// currencyID resolves the settlement currency of the emitted security: the
// default board's, else the security currency, RUR reported as RUB (verified
// on 17 176 demo-server instruments).
func currencyID(pit records.Pit, cur string, hasPit bool) string {
	id := ""
	if hasPit {
		id = pit.Currency
	}
	if id == "" {
		id = cur
	}
	if id == "RUR" {
		id = "RUB"
	}
	return id
}

func writeText(b *strings.Builder, tag, value string) {
	b.WriteString(tagText(tag, value))
}

func writeInt(b *strings.Builder, tag string, value int) {
	b.WriteString(tagInt(tag, value))
}

func tagText(tag, value string) string {
	return "<" + tag + ">" + xmlEscape(value) + "</" + tag + ">"
}

func tagInt(tag string, value int) string {
	return "<" + tag + ">" + strconv.Itoa(value) + "</" + tag + ">"
}

// xmlEscape matches the DLL's entity table in .rdata (0x180829378):
// & -> &amp;, < -> &lt;, > -> &gt;, ' -> &apos;, " -> &quot;. Go's
// xml.EscapeText writes &#39; for the apostrophe, which is not what the DLL
// emits (verified on the live 17 199-instrument dump).
func xmlEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\'':
			b.WriteString("&apos;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// SecuritiesXML renders a list of records as the DLL emits it during load.
func SecuritiesXML(st *model.Startup, secs []records.SecNew) string {
	var b strings.Builder
	b.WriteString("<securities>")
	for _, s := range secs {
		b.WriteString(SecurityXML(st, s))
	}
	b.WriteString("</securities>")
	return b.String()
}

// SecuritiesDumpXML renders a list in the get_securities dump order: the DLL
// iterates its catalog grouped by the record market byte (ascending) and
// sorted by seccode byte order within a group (verified on the
// 17 199-instrument live dump).
func SecuritiesDumpXML(st *model.Startup, secs []records.SecNew) string {
	sorted := slices.Clone(secs)
	slices.SortStableFunc(sorted, func(a, b records.SecNew) int {
		if a.Market != b.Market {
			return int(a.Market) - int(b.Market)
		}
		return strings.Compare(a.SecCode, b.SecCode)
	})
	var b strings.Builder
	b.WriteString("<securities>")
	for _, s := range sorted {
		b.WriteString(SecurityDumpXML(st, s))
	}
	b.WriteString("</securities>")
	return b.String()
}

// PitsXML renders the <pits> chunk for the given securities: the default pit
// of each first, then the remaining pits in record order - the order the DLL
// emits (verified byte-for-byte on the live 35-chunk snapshot; every
// multi-pit demo security has its default as the second record pit, so
// "default first" and "board number descending" agree on all observed data).
func PitsXML(st *model.Startup, secs []records.SecNew) string {
	var b strings.Builder
	b.WriteString("<pits>")
	for _, s := range secs {
		ordered := s.Pits
		for i, p := range s.Pits {
			if p.Default && i != 0 {
				ordered = append([]records.Pit{}, s.Pits...)
				ordered = append([]records.Pit{s.Pits[i]}, append(ordered[:i], ordered[i+1:]...)...)
				break
			}
		}
		for _, p := range ordered {
			board, ok := st.BoardByNum(p.Board)
			if !ok {
				continue
			}
			b.WriteString(`<pit seccode="` + xmlAttr(s.SecCode) + `" board ="` + xmlAttr(board.Code) + `">`)
			market := int(board.Market)
			if market == marketFORTSOption {
				market = marketFORTS
			}
			writeInt(&b, "market", market)
			cid := p.Currency
			if cid == "" {
				cid = s.Currency
				if cid == "" && board.Market == marketMICEX {
					cid = "RUR"
				}
			}
			if cid == "RUR" {
				cid = "RUB"
			}
			if cid != "" {
				writeText(&b, "currencyid", cid)
			}
			dec := int(p.Decimals)
			writeInt(&b, "decimals", dec)
			writeText(&b, "minstep", FormatMinStep(float64(p.MinStep)/pow10(dec), dec))
			lot := int(p.LotSize)
			if board.Market == marketFORTS || board.Market == marketFORTSOption {
				lot = 1
			}
			writeInt(&b, "lotsize", lot)
			writeInt(&b, "lotdivider", int(p.LotDivider))
			writeText(&b, "point_cost", FormatPointCost(p.PointCost))
			b.WriteString("</pit>")
		}
	}
	b.WriteString("</pits>")
	return b.String()
}

func xmlAttr(s string) string { return xmlEscape(s) }
