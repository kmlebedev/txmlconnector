package xmlcompat

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// Options selects the optional forms of the DLL output.
type Options struct {
	// Milliseconds renders <time> with the .fff suffix, as a connection opened
	// with <milliseconds>true</milliseconds> does.
	Milliseconds bool
	// UTCTime renders <time> in UTC instead of the server wall clock, as a
	// connection opened with <utc_time>true</utc_time> does.
	UTCTime bool
	// MicexRegisters renders the <register> tag of a money position, as a
	// connection opened with <micex_registers>true</micex_registers> does.
	MicexRegisters bool
}

// Render returns the XML message the DLL emits for an event, and false when the
// event has no XML form. It is the single entry point of the XML layer: each
// record family adds its case here on top of its decoder in
// native/events_<family>.go (tasks 6-28).
func Render(c *records.Catalog, ev model.Event, opt Options) (string, bool) {
	switch e := ev.(type) {
	case *model.Trade:
		return AllTradesXMLOptions([]model.Trade{*e}, opt), true
	case *model.ClientAccount:
		return ClientXML(*e), true
	case *model.MoneyPosition:
		return PositionsXML([]model.MoneyPosition{*e}, opt.MicexRegisters), true
	case *model.Union:
		return UnionXML(*e), true
	case *model.SecInfoUpdate:
		return SecInfoUpdateXML(*e), true
	case *model.Quotation:
		return QuotationXML(*e), true
	case *model.Quotes:
		return QuotesXML(*e), true
	case *model.SecInfo:
		return SecInfoXML(*e), true
	case *model.Candles:
		return CandlesXML(*e), true
	}
	return "", false
}

// AllTradesXMLOptions renders one <alltrades> batch with the optional forms of
// the connection. UTCTime replaces the DLL server wall clock with the UTC
// instant of each trade; the .fff suffix keeps the "no milliseconds" sentinel.
func AllTradesXMLOptions(trades []model.Trade, opt Options) string {
	if opt.UTCTime {
		adjusted := make([]model.Trade, len(trades))
		copy(adjusted, trades)
		for i := range adjusted {
			if adjusted[i].Millis < 1000 {
				adjusted[i].Millis = uint16(adjusted[i].Time.Nanosecond() / int(time.Millisecond))
			}
			adjusted[i].WallTime = adjusted[i].Time.Unix()
		}
		trades = adjusted
	}
	return AllTradesXML(trades, opt.Milliseconds)
}

// ClientXML renders the <client> callback of R_CLN_NEW (FUN_1800f7b40): the
// account attributes in the DLL order, or the removal form.
func ClientXML(a model.ClientAccount) string {
	if a.Removed {
		return "<client id=" + quoteAttr(a.ID) + " remove=" + quoteAttr("true") + "/>"
	}
	var b strings.Builder
	b.WriteString("<client id=" + quoteAttr(a.ID) + " remove=" + quoteAttr("false") + ">")
	b.WriteString("<market>" + strconv.Itoa(a.Market) + "</market>")
	b.WriteString("<currency>" + escape(a.Currency) + "</currency>")
	b.WriteString("<type>" + escape(a.Type) + "</type>")
	b.WriteString("</client>")
	return b.String()
}

// PositionsXML renders the <positions> wrapper the DLL emits at the end of the
// load: task 7 renders the money positions only (paper and FORTS positions come
// with task 20). The <register> tag appears only with <micex_registers>.
func PositionsXML(positions []model.MoneyPosition, micexRegisters bool) string {
	var b strings.Builder
	b.WriteString("<positions>")
	for _, p := range positions {
		b.WriteString("<money_position>")
		b.WriteString("<currency>" + escape(p.Currency) + "</currency>")
		b.WriteString("<client>" + escape(p.Client) + "</client>")
		b.WriteString("<union>" + escape(p.Union) + "</union>")
		b.WriteString("<markets><market>" + strconv.Itoa(p.Market) + "</market></markets>")
		b.WriteString("<asset>" + escape(p.Asset) + "</asset>")
		b.WriteString("<shortname>" + escape(p.ShortName) + "</shortname>")
		if micexRegisters && p.Register != "" {
			b.WriteString("<register>" + escape(p.Register) + "</register>")
		}
		b.WriteString("<saldoin>" + p.SaldoIn.String() + "</saldoin>")
		b.WriteString("<bought>" + p.Bought.String() + "</bought>")
		b.WriteString("<sold>" + p.Sold.String() + "</sold>")
		b.WriteString("<saldo>" + p.Saldo.String() + "</saldo>")
		b.WriteString("<ordbuy>" + p.OrdBuy.String() + "</ordbuy>")
		b.WriteString("<ordbuycond>" + p.OrdBuyCond.String() + "</ordbuycond>")
		b.WriteString("<comission>" + p.Comission.String() + "</comission>")
		b.WriteString("</money_position>")
	}
	b.WriteString("</positions>")
	return b.String()
}

// OvernightXML renders <overnight status="true|false"/>; the DLL puts it before
// every server_status callback.
func OvernightXML(status bool) string {
	if status {
		return "<overnight status=" + quoteAttr("true") + "/>"
	}
	return "<overnight status=" + quoteAttr("false") + "/>"
}

// UnionXML renders the <union> callback of R_UNION. The live case was not
// captured (the demo union is empty), so the shape follows the DLL literal and
// is marked inferred (docs/protocol/connect.md).
func UnionXML(u model.Union) string {
	return "<union id=" + quoteAttr(u.ID) + " remove=" + quoteAttr(strconv.FormatBool(u.Removed)) + "/>"
}

// quoteAttr renders an XML attribute value with the surrounding quotes.
func quoteAttr(s string) string {
	return "\"" + escape(s) + "\""
}

// SecInfoUpdateXML renders one <sec_info_upd> callback (FUN_180107800): the
// security identity and the changed fields in the DLL order. The FUT form is
// selected for futures, the OPT form for options (the DLL's type switch).
func SecInfoUpdateXML(u model.SecInfoUpdate) string {
	var b strings.Builder
	b.WriteString("<sec_info_upd><secid>" + strconv.Itoa(int(u.SecID)) + "</secid>")
	b.WriteString("<seccode>" + escape(u.Symbol.SecCode) + "</seccode>")
	b.WriteString("<market>" + strconv.Itoa(u.Market) + "</market>")
	fut := u.Kind == 4 || u.BuyDeposit != nil || u.SellDeposit != nil || u.MinPrice != nil || u.MaxPrice != nil
	if fut {
		if u.BuyDeposit != nil {
			b.WriteString("<buy_deposit>" + moneyHundredths(*u.BuyDeposit) + "</buy_deposit>")
		}
		if u.SellDeposit != nil {
			b.WriteString("<sell_deposit>" + moneyHundredths(*u.SellDeposit) + "</sell_deposit>")
		}
		if u.PointCost != nil {
			b.WriteString("<point_cost>" + u.PointCost.String() + "</point_cost>")
		}
		if u.MinPrice != nil {
			b.WriteString("<minprice>" + u.MinPrice.String() + "</minprice>")
		}
		if u.MaxPrice != nil {
			b.WriteString("<maxprice>" + u.MaxPrice.String() + "</maxprice>")
		}
	} else {
		if u.BgoC != nil {
			b.WriteString("<bgo_c>" + moneyHundredths(*u.BgoC) + "</bgo_c>")
		}
		if u.BgoNC != nil {
			b.WriteString("<bgo_nc>" + moneyHundredths(*u.BgoNC) + "</bgo_nc>")
		}
		if u.BgoBuy != nil {
			b.WriteString("<bgo_buy>" + moneyHundredths(*u.BgoBuy) + "</bgo_buy>")
		}
		if u.PointCost != nil {
			b.WriteString("<point_cost>" + u.PointCost.String() + "</point_cost>")
		}
	}
	b.WriteString("</sec_info_upd>")
	return b.String()
}

// moneyHundredths renders a Scale-2 money value with exactly two decimals, as
// the DLL prints the sec_info_upd money fields (e.g. bgo_buy 17425.00): unlike
// model.Decimal.String it keeps the trailing zero that the callback carries.
func moneyHundredths(d model.Decimal) string {
	units := d.Units
	if d.Scale > 2 {
		units /= int64(math.Pow10(int(d.Scale - 2)))
	} else if d.Scale < 2 {
		units *= int64(math.Pow10(int(2 - d.Scale)))
	}
	neg := units < 0
	if neg {
		units = -units
	}
	whole, frac := units/100, units%100
	text := strconv.FormatInt(whole, 10) + "."
	if frac < 10 {
		text += "0"
	}
	text += strconv.FormatInt(frac, 10)
	if neg {
		return "-" + text
	}
	return text
}

// QuotationXML renders one <quotations> callback (FUN_1800f7b40): identity and
// the changed fields in the DLL's fixed order. point_cost comes first, followed
// by a separating space exactly as the DLL prints it, and only present fields
// are emitted.
func QuotationXML(q model.Quotation) string {
	var b strings.Builder
	b.WriteString("<quotations><quotation secid=" + quoteAttr(strconv.Itoa(int(q.SecID))) + ">")
	b.WriteString("<board>" + escape(q.Board) + "</board>")
	b.WriteString("<seccode>" + escape(q.Symbol.SecCode) + "</seccode>")
	f := q.Fields
	// The DLL prints point_cost only in the callbacks that carry the trading
	// status (the full updates); partial updates like a bid change omit it
	// (S: the 16 r9 callbacks, rule inferred as I).
	if f.PointCost != nil && (f.Status != nil || f.TradingStatus != nil) {
		b.WriteString("<point_cost>" + f.PointCost.String() + "</point_cost> ")
	}
	stringField := func(name string, v *string) {
		if v != nil {
			b.WriteString("<" + name + ">" + escape(*v) + "</" + name + ">")
		}
	}
	intField := func(name string, v *int64) {
		if v != nil {
			b.WriteString("<" + name + ">" + strconv.FormatInt(*v, 10) + "</" + name + ">")
		}
	}
	decField := func(name string, v *model.Decimal) {
		if v != nil {
			b.WriteString("<" + name + ">" + v.String() + "</" + name + ">")
		}
	}
	// The DLL prints the money fields with fixed digits: valtoday keeps three
	// decimals (1000 -> 1.000), the deposits keep two (7601.40, 7516.00).
	fixedField := func(name string, v *model.Decimal, scale int) {
		if v != nil {
			b.WriteString("<" + name + ">" + fixedMoney(*v, scale) + "</" + name + ">")
		}
	}
	stringField("status", f.Status)
	stringField("tradingstatus", f.TradingStatus)
	decField("last", f.Last)
	intField("quantity", f.Quantity)
	stringField("time", f.Time)
	decField("change", f.Change)
	decField("priceminusprevwaprice", f.PriceMinusPrevWaPrice)
	intField("openpositions", f.OpenPositions)
	decField("bid", f.Bid)
	intField("biddepth", f.BidDepth)
	intField("biddeptht", f.BidDepthT)
	intField("numbids", f.NumBids)
	decField("offer", f.Offer)
	intField("offerdepth", f.OfferDepth)
	intField("offerdeptht", f.OfferDepthT)
	intField("numoffers", f.NumOffers)
	decField("open", f.Open)
	decField("high", f.High)
	decField("low", f.Low)
	decField("highbid", f.HighBid)
	decField("lowoffer", f.LowOffer)
	intField("voltoday", f.VolToday)
	intField("numtrades", f.NumTrades)
	fixedField("valtoday", f.ValToday, 3)
	decField("waprice", f.WaPrice)
	decField("closeprice", f.ClosePrice)
	decField("lcurrentprice", f.LCurrentPrice)
	fixedField("buydeposit", f.BuyDeposit, 2)
	fixedField("selldeposit", f.SellDeposit, 2)
	b.WriteString("</quotation></quotations>")
	return b.String()
}

// QuotesXML renders one <quotes> order book callback (FUN_1800f7b40): all levels
// of the snapshot in wire order (buy levels, then sell levels), each as
// <quote secid><board><seccode><price><yield><buy|sell>.
func QuotesXML(book model.Quotes) string {
	var b strings.Builder
	b.WriteString("<quotes>")
	for _, level := range book.Levels {
		b.WriteString("<quote secid=" + quoteAttr(strconv.Itoa(int(level.SecID))) + ">")
		b.WriteString("<board>" + escape(book.Board) + "</board>")
		b.WriteString("<seccode>" + escape(book.Symbol.SecCode) + "</seccode>")
		// The DLL builds <price> before the deletion branch, so even a deletion
		// level keeps its price (asm 0x1800faa27/0x1800faad6).
		b.WriteString("<price>" + level.Price.String() + "</price>")
		yield := 0
		if level.Yield != nil {
			yield = *level.Yield
		}
		b.WriteString("<yield>" + strconv.Itoa(yield) + "</yield>")
		switch {
		case level.Buy == -1:
			b.WriteString("<buy>-1</buy>")
		case level.Sell == -1:
			b.WriteString("<sell>-1</sell>")
		case level.Sell != 0:
			b.WriteString("<sell>" + strconv.FormatInt(level.Sell, 10) + "</sell>")
		default:
			b.WriteString("<buy>" + strconv.FormatInt(level.Buy, 10) + "</buy>")
		}
		b.WriteString("</quote>")
	}
	b.WriteString("</quotes>")
	return b.String()
}

// SecInfoXML renders one <sec_info> callback (FUN_180105990): the catalog and
// RQ_SEC_SPEC data in the DLL order, empty and zero fields skipped.
func SecInfoXML(info model.SecInfo) string {
	var b strings.Builder
	b.WriteString("<sec_info secid=" + quoteAttr(strconv.Itoa(int(info.SecID))) + ">")
	if info.SecName != "" {
		b.WriteString("<secname>" + escape(info.SecName) + "</secname>")
	}
	if info.SecCode != "" {
		b.WriteString("<seccode>" + escape(info.SecCode) + "</seccode>")
	}
	// The DLL skips empty/zero fields; market 0 is not a real market.
	if info.Market != 0 {
		b.WriteString("<market>" + strconv.Itoa(info.Market) + "</market>")
	}
	if info.PName != "" {
		b.WriteString("<pname>" + escape(info.PName) + "</pname>")
	}
	dateField := func(name string, v *time.Time) {
		if v != nil {
			b.WriteString("<" + name + ">" + v.Format("02.01.2006") + "</" + name + ">")
		}
	}
	decField := func(name string, v *model.Decimal) {
		if v != nil && v.Units != 0 {
			b.WriteString("<" + name + ">" + v.String() + "</" + name + ">")
		}
	}
	fixedField := func(name string, v *model.Decimal) {
		if v != nil && v.Units != 0 {
			b.WriteString("<" + name + ">" + fixedDecimal(*v) + "</" + name + ">")
		}
	}
	intField := func(name string, v *int64) {
		if v != nil {
			b.WriteString("<" + name + ">" + strconv.FormatInt(*v, 10) + "</" + name + ">")
		}
	}
	dateField("mat_date", info.MatDate)
	decField("clearing_price", info.ClearingPrice)
	decField("minprice", info.MinPrice)
	decField("maxprice", info.MaxPrice)
	if info.BgoC != nil || info.BgoNC != nil || info.BgoBuy != nil {
		decField("bgo_c", info.BgoC)
		decField("bgo_nc", info.BgoNC)
		decField("bgo_buy", info.BgoBuy)
	} else {
		fixedField("buy_deposit", info.BuyDeposit)
		fixedField("sell_deposit", info.SellDeposit)
	}
	fixedField("coupon_value", info.CouponValue)
	dateField("coupon_date", info.CouponDate)
	intField("coupon_period", info.CouponPeriod)
	fixedField("facevalue", info.FaceValue)
	if info.PutCall != nil {
		b.WriteString("<put_call>" + escape(*info.PutCall) + "</put_call>")
	}
	if info.OptType != nil {
		b.WriteString("<opt_type>" + escape(*info.OptType) + "</opt_type>")
	}
	fixedField("accruedint", info.AccruedInt)
	decField("point_cost", info.PointCost)
	intField("lot_volume", info.LotVolume)
	if info.ISIN != "" {
		b.WriteString("<isin>" + escape(info.ISIN) + "</isin>")
	}
	if info.RegNumber != "" {
		b.WriteString("<regnumber>" + escape(info.RegNumber) + "</regnumber>")
	}
	decField("buybackprice", info.BuybackPrice)
	dateField("buybackdate", info.BuybackDate)
	if info.CurrencyID != "" {
		b.WriteString("<currencyid>" + escape(info.CurrencyID) + "</currencyid>")
	}
	b.WriteString("</sec_info>")
	return b.String()
}

// fixedMoney prints exactly scale digits after the point, re-scaling the
// decimal (the DLL keeps trailing zeros in the quotation money fields).
func fixedMoney(d model.Decimal, scale int) string {
	units := d.Units
	switch {
	case int(d.Scale) < scale:
		units *= pow10Int(scale - int(d.Scale))
	case int(d.Scale) > scale:
		units /= pow10Int(int(d.Scale) - scale)
	}
	return fixedDecimal(model.Decimal{Units: units, Scale: uint8(scale)})
}

func pow10Int(n int) int64 {
	v := int64(1)
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
}

// fixedDecimal prints exactly Scale digits after the point (the DLL keeps the
// trailing zeros of facevalue and the money fields, e.g. 1000.0000).
func fixedDecimal(d model.Decimal) string {
	neg := d.Units < 0
	units := d.Units
	if neg {
		units = -units
	}
	text := strconv.FormatInt(units, 10)
	if d.Scale > 0 {
		scale := int(d.Scale)
		for len(text) <= scale {
			text = "0" + text
		}
		text = text[:len(text)-scale] + "." + text[len(text)-scale:]
	}
	if neg {
		return "-" + text
	}
	return text
}

// CandlesXML renders one <candles> callback of gethistorydata (PDF 4.1): the
// DLL keeps a space after the opening tag and before the closing one, and the
// candles are chronological (oldest first).
func CandlesXML(candles model.Candles) string {
	var b strings.Builder
	b.WriteString("<candles secid=" + quoteAttr(strconv.Itoa(int(candles.SecID))) + " board=" + quoteAttr(candles.Symbol.Board))
	b.WriteString(" seccode=" + quoteAttr(candles.Symbol.SecCode) + " period=" + quoteAttr(strconv.Itoa(candles.Period)))
	b.WriteString(" status=" + quoteAttr(strconv.Itoa(candles.Status)) + "> ")
	for _, candle := range candles.Items {
		b.WriteString("<candle date=" + quoteAttr(candle.Time.Format("02.01.2006 15:04:05")))
		b.WriteString(" open=" + quoteAttr(candle.Open.String()))
		b.WriteString(" close=" + quoteAttr(candle.Close.String()))
		b.WriteString(" high=" + quoteAttr(candle.High.String()))
		b.WriteString(" low=" + quoteAttr(candle.Low.String()))
		b.WriteString(" volume=" + quoteAttr(strconv.FormatInt(candle.Volume, 10)) + "/> ")
	}
	b.WriteString("</candles>")
	return b.String()
}
