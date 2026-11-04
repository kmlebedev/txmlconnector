package xmlcompat

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/client/commands"
	"github.com/kmlebedev/txmlconnector/native/wire"
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

// SecurityFlags is the per-security derived state the DLL uses for
// <security>: the opmask byte, the tradability gates and quotestype.
// The model is recovered from FUN_180398640 (record parse and opmask seed),
// FUN_1801dc810/FUN_1801dcb20/FUN_1803035c0/FUN_1801dcf80 (trading checks),
// FUN_1801dfaa0/FUN_1801e0080 (quotestype) and FUN_18011f4a0 (output), and
// matches the DLL on all 499 demo-server instruments of the snapshot fixture.
type SecurityFlags struct {
	OpMask    byte // opmask byte: ioc 0x01, fok 0x02, bymarket 0x04, nosplit 0x08, usecredit 0x10
	Trading   bool // symbols&3==3, not an index, not MCT DCF, futures/options not blocked
	Active    bool // Trading and the security has a default pit
	ByMarket  bool // Trading and both opmask and the record byte carry the market-permit bit
	QuotesTyp byte // 0 - none, 1 - OrderBook, 2 - Level2
}

// SecurityFlags derives the DLL state of a record. DefaultPit and board
// lookups follow the DLL: the pit flagged default, else the first one; the
// quotestype gate reads the Byte2 of that pit's board from RQ_STARTUP.
func (st *Startup) SecurityFlags(s SecNew) SecurityFlags {
	f := SecurityFlags{}
	if t, ok := findSecType(st.SecTypes, s.Kind); ok && len(t.Raw) > 1 {
		f.OpMask = t.Raw[1]
	}
	if s.Props&2 != 0 {
		f.OpMask |= 1
	}
	if s.Props&4 != 0 {
		f.OpMask |= 2
	}
	pit, hasPit := s.DefaultPit()
	f.Trading = s.Flags&3 == 3 && s.Kind != 7 &&
		!(s.Market == 0x0e && s.Tail2 == 7) &&
		!((s.Kind == 4 || s.Kind == 5) && s.DerivFlags&4 != 0 && s.DerivFlags&8 == 0)
	f.Active = hasPit && f.Trading
	f.ByMarket = f.Trading && f.OpMask&4 != 0 && s.Flags&4 != 0
	// quotestype: the default board must allow quotes (RQ_STARTUP boards
	// Byte2&2), indices and swaps report 0, MCT takes the value from the
	// record tail, market 12 reports Level2, everything else OrderBook.
	if s.Kind != 0x11 && hasPit {
		if b, ok := st.boardByNum(pit.Board); ok && b.Byte2&2 != 0 {
			switch {
			case s.Market == 0x0e:
				f.QuotesTyp = s.Tail1
			case s.Kind == 7:
				f.QuotesTyp = 0
			case s.Market == 12:
				f.QuotesTyp = 2
			default:
				f.QuotesTyp = 1
			}
		}
	}
	return f
}

func findSecType(list []SecType, id byte) (SecType, bool) {
	for _, t := range list {
		if t.ID == id {
			return t, true
		}
	}
	return SecType{}, false
}

func (st *Startup) boardByNum(num uint16) (Board, bool) {
	for _, b := range st.Boards {
		if b.Num == num {
			return b, true
		}
	}
	return Board{}, false
}

// secTZN returns the security time zone name and whether the DLL would print
// the <sec_tz> element for this record.
func (st *Startup) secTZN(s SecNew) string {
	if !s.HasTimeZone {
		return ""
	}
	name, _ := TimeZoneName(s.TimeZone)
	return name
}

// SecurityXML renders one <security> exactly in the DLL's snapshot-chunk
// element order (verified byte-for-byte against the load callbacks).
func (st *Startup) SecurityXML(s SecNew) string {
	return st.securityXML(s, false)
}

// SecurityDumpXML renders one <security> in the order of the get_securities
// dump writer FUN_18011f4a0: sec_tz, board, seccode, instrclass, currency,
// shortname, decimals, market, minstep…, point_cost, opmask, MIC, ticker,
// sectype, quotestype, currencyid — different from the load-chunk writer.
func (st *Startup) SecurityDumpXML(s SecNew) string {
	return st.securityXML(s, true)
}

func (st *Startup) securityXML(s SecNew, dump bool) string {
	f := st.SecurityFlags(s)
	var tz, seccode, instr, currency, board, shortname, decimals, market, sectype, opmask, pitBlock, quotes, mic, ticker, currencyid string
	if t := st.secTZN(s); t != "" {
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
		boardCode = st.boardCode(pit.Board)
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
	if cid := st.currencyID(pit, cur, hasPit); cid != "" {
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
func (st *Startup) currencyID(pit Pit, cur string, hasPit bool) string {
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

func pow10(n int) float64 {
	v := 1.0
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
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
func (st *Startup) SecuritiesXML(secs []SecNew) string {
	var b strings.Builder
	b.WriteString("<securities>")
	for _, s := range secs {
		b.WriteString(st.SecurityXML(s))
	}
	b.WriteString("</securities>")
	return b.String()
}

// SecuritiesDumpXML renders a list in the get_securities dump order: the DLL
// iterates its catalog grouped by the record market byte (ascending) and
// sorted by seccode byte order within a group (verified on the
// 17 199-instrument live dump).
func (st *Startup) SecuritiesDumpXML(secs []SecNew) string {
	sorted := slices.Clone(secs)
	slices.SortStableFunc(sorted, func(a, b SecNew) int {
		if a.Market != b.Market {
			return int(a.Market) - int(b.Market)
		}
		return strings.Compare(a.SecCode, b.SecCode)
	})
	var b strings.Builder
	b.WriteString("<securities>")
	for _, s := range sorted {
		b.WriteString(st.SecurityDumpXML(s))
	}
	b.WriteString("</securities>")
	return b.String()
}

// PitsXML renders the <pits> chunk for the given securities: the default pit
// of each first, then the remaining pits in record order — the order the DLL
// emits (verified byte-for-byte on the live 35-chunk snapshot; every
// multi-pit demo security has its default as the second record pit, so
// "default first" and "board number descending" agree on all observed data).
func (st *Startup) PitsXML(secs []SecNew) string {
	var b strings.Builder
	b.WriteString("<pits>")
	for _, s := range secs {
		ordered := s.Pits
		for i, p := range s.Pits {
			if p.Default && i != 0 {
				ordered = append([]Pit{}, s.Pits...)
				ordered = append([]Pit{s.Pits[i]}, append(ordered[:i], ordered[i+1:]...)...)
				break
			}
		}
		for _, p := range ordered {
			board, ok := st.boardByNum(p.Board)
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

// Apply updates derived state for record tags that mutate a security: the
// attribute tags R_SEC_ZALOG/OBLIG/ONOFF/MKT_PERMIT rewrite bytes of the
// record the same way FUN_180264150 does, so later <securities> output
// reflects them.
func (c *Catalog) ApplyMutations(rec Record) (bool, error) {
	switch rec.Tag {
	case TagSecNew, TagDate:
		return c.Apply(rec)
	}
	if !isAttrTag(rec.Tag) {
		return false, nil
	}
	if len(rec.Body) < 3 {
		return false, fmt.Errorf("attribute record %#02x: short body", rec.Tag)
	}
	r := wire.NewReader(rec.Body)
	id, _ := r.Uint16()
	v, _ := r.Byte()
	s, ok := c.secs[id]
	if !ok {
		return true, nil // the DLL logs Unknown secid and skips
	}
	switch rec.Tag {
	case 0x0d: // R_SEC_ONOFF: low two bits of the flags byte
		s.Flags = s.Flags&0xfc | v
		c.secs[id] = s
	case 0x0e: // R_SEC_MKT_PERMIT: market-permission bit
		if v != 0 {
			s.Flags |= 4
		} else {
			s.Flags &^= 4
		}
		c.secs[id] = s
	}
	return true, nil
}

func isAttrTag(tag byte) bool { return attrTags[tag] != "" }

// List returns the known securities in R_SEC_NEW arrival order.
func (c *Catalog) List() []SecNew {
	out := make([]SecNew, 0, len(c.order))
	for _, id := range c.order {
		if s, ok := c.secs[id]; ok {
			out = append(out, s)
		}
	}
	return out
}

// SecuritiesDumpXML renders the whole catalog for get_securities (dump order).
func (c *Catalog) SecuritiesDumpXML() string {
	return c.startup.SecuritiesDumpXML(c.List())
}

func xmlAttr(s string) string { return xmlEscape(s) }

// update commands.Security with the derived fields so parsed consumers see
// the same values the DLL callbacks carry.
func (st *Startup) applyDerived(sec *commands.Security, s SecNew, f SecurityFlags) {
	sec.Active = strconv.FormatBool(f.Active)
	sec.QuotesType = int(f.QuotesTyp)
	sec.MIC = s.MIC
	sec.Ticker = s.Ticker
	if !f.Active {
		return
	}
	sec.OpMask = commands.OpMask{
		UseCredit:     yesNo(f.OpMask&0x10 != 0),
		ByMarket:      yesNo(f.ByMarket),
		NoSplit:       yesNo(f.OpMask&8 != 0),
		Fok:           yesNo(f.OpMask&2 != 0),
		Ioc:           yesNo(f.OpMask&1 != 0),
		ImmOrCancel:   yesNo(f.OpMask&2 != 0),
		CancelBalance: yesNo(f.OpMask&1 != 0),
	}
}
