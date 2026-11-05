package xmlcompat

import (
	"fmt"
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
	case *model.Order:
		return OrdersXML([]model.Order{*e}), true
	case *model.ClientTrade:
		return TradesXML([]model.ClientTrade{*e}), true
	case *model.MarketOrd:
		return MarketOrdXML(*e), true
	case *model.NewsHeader:
		return NewsHeaderXML(*e), true
	case *model.Messages:
		return MessagesXML(e.Items), true
	case *model.OptionFamily:
		return OptionFamiliesXML([]model.OptionFamily{*e}), true
	case *model.OptionStrikes:
		return FamilyStrikesXML(*e), true
	case *model.EndOfFamily:
		return OptionsFailedXML(*e), true
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
		// The items are UTC; the DLL prints the server wall clock.
		wall := candle.Time.Add(-candles.ZoneBias)
		b.WriteString("<candle date=" + quoteAttr(wall.Format("02.01.2006 15:04:05")))
		b.WriteString(" open=" + quoteAttr(candle.Open.String()))
		b.WriteString(" close=" + quoteAttr(candle.Close.String()))
		b.WriteString(" high=" + quoteAttr(candle.High.String()))
		b.WriteString(" low=" + quoteAttr(candle.Low.String()))
		b.WriteString(" volume=" + quoteAttr(strconv.FormatInt(candle.Volume, 10)) + "/> ")
	}
	b.WriteString("</candles>")
	return b.String()
}

// OrdersXML renders one <orders> callback of the order feed (D+O form of
// docs/protocol/orders.md; the element order is the oracle-confirmed one).
func OrdersXML(orders []model.Order) string {
	var b strings.Builder
	b.WriteString("<orders>")
	for _, order := range orders {
		b.WriteString(OrderXML(order))
	}
	b.WriteString("</orders>")
	return b.String()
}

// OrderXML renders one <order transactionid> element.
func OrderXML(order model.Order) string {
	timeOfDay := func(sec int32) string {
		return fmt.Sprintf("%02d:%02d:%02d", sec/3600, (sec%3600)/60, sec%60)
	}
	var b strings.Builder
	b.WriteString("<order transactionid=" + quoteAttr(strconv.FormatUint(order.TransactionID, 10)) + ">")
	b.WriteString("<orderno>" + strconv.FormatUint(order.OrderNo, 10) + "</orderno>")
	b.WriteString("<secid>" + strconv.Itoa(int(order.SecID)) + "</secid>")
	b.WriteString("<union>" + escape(order.Union) + "</union>")
	b.WriteString("<board>" + escape(order.Board) + "</board>")
	b.WriteString("<seccode>" + escape(order.SecCode) + "</seccode>")
	b.WriteString("<client>" + escape(order.Client) + "</client>")
	b.WriteString("<status>" + orderStatusName(order.Status) + "</status>")
	b.WriteString("<buysell>" + escape(string(order.BuySell)) + "</buysell>")
	b.WriteString("<time>" + timeOfDay(order.Time) + "</time>")
	b.WriteString("<accepttime>" + timeOfDay(order.AcceptTime) + "</accepttime>")
	b.WriteString("<brokerref>" + escape(order.BrokerRef) + "</brokerref>")
	b.WriteString("<value>0.0</value><accruedint>0.0</accruedint><settlecode></settlecode>")
	b.WriteString("<balance>" + strconv.FormatInt(order.Balance, 10) + "</balance>")
	b.WriteString("<price>" + orderAmount(order.Price) + "</price>")
	b.WriteString("<quantity>" + strconv.FormatInt(order.Quantity, 10) + "</quantity>")
	b.WriteString("<hidden>0</hidden>")
	b.WriteString("<yield>" + orderAmount(order.Yield) + "</yield>")
	b.WriteString("<withdrawtime>0</withdrawtime>")
	b.WriteString("<condition>" + orderConditionName(order.Condition) + "</condition>")
	b.WriteString("<conditionvalue>" + orderAmount(order.ConditionValue) + "</conditionvalue>")
	b.WriteString("<validafter>" + timeOfDay(order.ValidAfter) + "</validafter>")
	b.WriteString("<validbefore>" + timeOfDay(order.ValidBefore) + "</validbefore>")
	b.WriteString("<maxcomission>0.0</maxcomission><within_pos>true</within_pos>")
	b.WriteString("<result>" + escape(order.Result) + "</result>")
	b.WriteString("</order>")
	return b.String()
}

// orderAmount prints the DLL order amount style: the Decimal trimmed, but with
// at least one fractional digit (100.00 -> 100.0, 5.55 -> 5.55, 0 -> 0.0).
func orderAmount(d model.Decimal) string {
	text := d.String()
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return text
}

// orderStatusName maps the DLL order status enum (only 1/2 are oracle-proven).
func orderStatusName(status int) string {
	switch status {
	case 0:
		return "none"
	case 1:
		return "watching"
	case 2:
		return "disabled"
	default:
		return strconv.Itoa(status)
	}
}

// orderConditionName maps the condition enum (Bid is oracle-confirmed).
func orderConditionName(condition int) string {
	switch condition {
	case 1:
		return "Bid"
	case 3:
		return "Ask"
	case 0:
		return ""
	default:
		return strconv.Itoa(condition)
	}
}

// TradesXML renders one client <trades> callback (PDF 4.11, oracle-confirmed
// order of elements in docs/protocol/orders.md).
func TradesXML(trades []model.ClientTrade) string {
	var b strings.Builder
	b.WriteString("<trades>")
	for _, trade := range trades {
		b.WriteString(TradeXML(trade))
	}
	b.WriteString("</trades>")
	return b.String()
}

// TradeXML renders one <trade> element.
func TradeXML(trade model.ClientTrade) string {
	timeOfDay := func(sec int64) string {
		return fmt.Sprintf("%02d:%02d:%02d", sec/3600, (sec%3600)/60, sec%60)
	}
	var b strings.Builder
	b.WriteString("<trade>")
	b.WriteString("<secid>" + strconv.Itoa(int(trade.SecID)) + "</secid>")
	b.WriteString("<tradeno>" + strconv.FormatUint(trade.TradeNo, 10) + "</tradeno>")
	b.WriteString("<orderno>" + strconv.FormatUint(trade.OrderNo, 10) + "</orderno>")
	b.WriteString("<board>" + escape(trade.Board) + "</board>")
	b.WriteString("<seccode>" + escape(trade.SecCode) + "</seccode>")
	b.WriteString("<client>" + escape(trade.Client) + "</client>")
	// The DLL prints the buysell byte as-is (the O fixture shows \x01).
	b.WriteString("<buysell>" + string([]byte{trade.BuySell}) + "</buysell>")
	b.WriteString("<union>" + escape(trade.Union) + "</union>")
	b.WriteString("<time>" + timeOfDay(trade.Time) + "</time>")
	b.WriteString("<brokerref>" + escape(trade.BrokerRef) + "</brokerref>")
	b.WriteString("<value>" + orderAmount(trade.Value) + "</value>")
	b.WriteString("<comission>" + orderAmount(trade.Comission) + "</comission>")
	b.WriteString("<price>" + orderAmount(trade.Price) + "</price>")
	b.WriteString("<quantity>" + strconv.FormatInt(trade.Quantity, 10) + "</quantity>")
	b.WriteString("<items>" + strconv.FormatInt(trade.Items, 10) + "</items>")
	b.WriteString("<yield>0.0</yield>")
	b.WriteString("<currentpos>" + strconv.FormatInt(trade.CurrentPos, 10) + "</currentpos>")
	b.WriteString("<accruedint>" + orderAmount(trade.AccruedInt) + "</accruedint>")
	b.WriteString("<tradetype>" + escape(string(trade.TradeType)) + "</tradetype>")
	b.WriteString("<settlecode>" + escape(trade.SettleCode) + "</settlecode>")
	b.WriteString("<bypass>" + strconv.Itoa(int(trade.Bypass)) + "</bypass>")
	b.WriteString("</trade>")
	return b.String()
}

// SecPositionXML renders one <positions><sec_position> callback (O).
func SecPositionXML(pos model.SecPosition) string {
	var b strings.Builder
	b.WriteString("<positions><sec_position>")
	b.WriteString("<client>" + escape(pos.Client) + "</client>")
	b.WriteString("<union>" + escape(pos.Union) + "</union>")
	b.WriteString("<secid>" + strconv.Itoa(int(pos.SecID)) + "</secid>")
	b.WriteString("<market>" + strconv.Itoa(pos.Market) + "</market>")
	b.WriteString("<shortname>" + escape(pos.ShortName) + "</shortname>")
	b.WriteString("<seccode>" + escape(pos.SecCode) + "</seccode>")
	b.WriteString("<saldoin>" + strconv.FormatInt(pos.SaldoIn, 10) + "</saldoin>")
	b.WriteString("<saldomin>" + strconv.FormatInt(pos.SaldoMin, 10) + "</saldomin>")
	b.WriteString("<bought>" + strconv.FormatInt(pos.Bought, 10) + "</bought>")
	b.WriteString("<sold>" + strconv.FormatInt(pos.Sold, 10) + "</sold>")
	b.WriteString("<saldo>" + strconv.FormatInt(pos.Saldo, 10) + "</saldo>")
	b.WriteString("<ordbuy>" + strconv.FormatInt(pos.OrdBuy, 10) + "</ordbuy>")
	b.WriteString("<ordsell>" + strconv.FormatInt(pos.OrdSell, 10) + "</ordsell>")
	b.WriteString("</sec_position></positions>")
	return b.String()
}

// FortsPositionsXML renders the get_forts_positions callback: one
// <forts_position> per record and the aggregated <forts_money> (O).
func FortsPositionsXML(positions []model.FortsPosition, money *model.FortsMoney) string {
	var b strings.Builder
	b.WriteString("<positions>")
	for _, pos := range positions {
		b.WriteString("<forts_position>")
		b.WriteString("<client>" + escape(pos.Client) + "</client>")
		b.WriteString("<secid>" + strconv.Itoa(int(pos.SecID)) + "</secid>")
		b.WriteString("<union>" + escape(pos.Union) + "</union>")
		b.WriteString("<seccode>" + escape(pos.SecCode) + "</seccode>")
		b.WriteString("<markets><market>" + strconv.Itoa(pos.Market) + "</market></markets>")
		b.WriteString("<startnet>" + strconv.FormatInt(pos.StartNet, 10) + "</startnet>")
		b.WriteString("<openbuys>" + strconv.FormatInt(pos.OpenBuys, 10) + "</openbuys>")
		b.WriteString("<opensells>" + strconv.FormatInt(pos.OpenSells, 10) + "</opensells>")
		b.WriteString("<totalnet>" + strconv.FormatInt(pos.TotalNet, 10) + "</totalnet>")
		b.WriteString("<todaybuy>" + strconv.FormatInt(pos.TodayBuy, 10) + "</todaybuy>")
		b.WriteString("<todaysell>" + strconv.FormatInt(pos.TodaySell, 10) + "</todaysell>")
		b.WriteString("<optmargin>" + orderAmount(pos.OptMargin) + "</optmargin>")
		b.WriteString("<varmargin>" + orderAmount(pos.VarMargin) + "</varmargin>")
		b.WriteString("<expirationpos>" + strconv.FormatInt(pos.ExpirationPos, 10) + "</expirationpos>")
		b.WriteString("</forts_position>")
	}
	if money != nil {
		b.WriteString("<forts_money>")
		b.WriteString("<client>" + escape(money.Client) + "</client>")
		b.WriteString("<union>" + escape(money.Union) + "</union>")
		b.WriteString("<markets><market>" + strconv.Itoa(money.Market) + "</market></markets>")
		b.WriteString("<shortname>" + escape(money.ShortName) + "</shortname>")
		b.WriteString("<blocked>" + orderAmount(money.Blocked) + "</blocked>")
		b.WriteString("<varmargin>" + orderAmount(money.VarMargin) + "</varmargin>")
		b.WriteString("<current>" + orderAmount(money.Current) + "</current>")
		b.WriteString("<free>" + orderAmount(money.Free) + "</free>")
		b.WriteString("</forts_money>")
	}
	b.WriteString("</positions>")
	return b.String()
}

// ClientLimitsXML renders <clientlimits client="…"> (limits.md, D): the DLL
// emits only non-zero fields in this fixed order, keeps the </cpblused> typo of
// the cbplused element and prints the money fields with two decimals, coverage
// raw and liquidity_c with five decimals.
func ClientLimitsXML(l model.ClientLimits) string {
	var b strings.Builder
	b.WriteString("<clientlimits client=" + quoteAttr(l.Client) + ">")
	money := func(name string, d model.Decimal, typo bool) {
		if d.Units == 0 {
			return
		}
		closeTag := name
		if typo {
			closeTag = "cpblused"
		}
		b.WriteString("<" + name + ">" + fixedDecimal(d) + "</" + closeTag + ">")
	}
	money("cbplimit", l.CBPLimit, false)
	money("cbplused", l.CBPLUsed, true)
	money("cbplplanned", l.CBPLPlanned, false)
	money("fob_varmargin", l.FobVarMargin, false)
	if l.Coverage != 0 {
		b.WriteString("<coverage>" + strconv.Itoa(int(l.Coverage)) + "</coverage>")
	}
	if l.LiquidityC.Units != 0 {
		b.WriteString("<liquidity_c>" + fixedDecimal(l.LiquidityC) + "</liquidity_c>")
	}
	for _, field := range []struct {
		name string
		d    model.Decimal
	}{
		{"profit", l.Profit}, {"money_current", l.MoneyCurrent}, {"money_reserve", l.MoneyReserve},
		{"money_free", l.MoneyFree}, {"options_premium", l.OptionsPremium}, {"exchange_fee", l.ExchangeFee},
		{"forts_varmargin", l.FortsVarMargin}, {"varmargin", l.VarMargin}, {"pclmargin", l.PCLMargin},
		{"options_vm", l.OptionsVM}, {"spot_buy_limit", l.SpotBuyLimit}, {"used_spot_buy_limit", l.UsedSpotBuyLimit},
		{"collat_current", l.CollatCurrent}, {"collat_blocked", l.CollatBlocked}, {"collat_free", l.CollatFree},
	} {
		money(field.name, field.d, false)
	}
	b.WriteString("</clientlimits>")
	return b.String()
}

// UnitedLimitsXML renders the D-only <united_limits union="…"> callback.
func UnitedLimitsXML(l model.UnitedLimits) string {
	var b strings.Builder
	b.WriteString("<united_limits union=" + quoteAttr(l.Union) + ">")
	for _, field := range []struct {
		name string
		d    model.Decimal
	}{
		{"open_equity", l.OpenEquity}, {"equity", l.Equity}, {"requirements", l.Requirements},
		{"free", l.Free}, {"vm", l.VM}, {"finres", l.FinRes}, {"go", l.GO},
	} {
		if field.d.Units == 0 {
			continue
		}
		b.WriteString("<" + field.name + ">" + fixedDecimal(field.d) + "</" + field.name + ">")
	}
	b.WriteString("</united_limits>")
	return b.String()
}

// StopOrdersXML renders the R_STOP callback: the DLL uses the <orders> envelope
// with one <stoporder transactionid> element (orders.md, O).
func StopOrdersXML(orders []model.StopOrder) string {
	var b strings.Builder
	b.WriteString("<orders>")
	for _, order := range orders {
		b.WriteString("<stoporder transactionid=" + quoteAttr(strconv.FormatUint(order.TransactionID, 10)) + ">")
		// Only the fields the record actually carries (mask-aware): a status-only
		// update must not print a NUL buysell or a zero secid (D).
		if order.HasSec {
			b.WriteString("<secid>" + strconv.Itoa(int(order.SecID)) + "</secid>")
		}
		b.WriteString("<union>" + escape(order.Union) + "</union>")
		if order.HasBoard {
			b.WriteString("<board>" + escape(order.Board) + "</board>")
		}
		if order.HasSec {
			b.WriteString("<seccode>" + escape(order.SecCode) + "</seccode>")
		}
		if order.HasClient {
			b.WriteString("<client>" + escape(order.Client) + "</client>")
		}
		if order.HasBuySell {
			b.WriteString("<buysell>" + escape(string(order.BuySell)) + "</buysell>")
		}
		b.WriteString("<author>" + escape(order.Author) + "</author>")
		b.WriteString("<status>" + stopOrderStatusName(order.Status) + "</status>")
		b.WriteString("<withdrawtime>" + strconv.FormatInt(order.WithdrawTime, 10) + "</withdrawtime>")
		b.WriteString("<result>" + escape(order.Result) + "</result>")
		b.WriteString("</stoporder>")
	}
	b.WriteString("</orders>")
	return b.String()
}

// stopOrderStatusName maps the R_STOP status codes (only none is O-confirmed).
func stopOrderStatusName(status int) string {
	switch status {
	case 0:
		return "none"
	default:
		return strconv.Itoa(status)
	}
}

// The portfolio XML literals below are the only D-known forms: the element
// schemas/field names are not recovered yet (portfolios.md, I), so these
// helpers emit the literal envelope without fabricating data and are NOT wired
// into Render until the oracle fixtures exist.

// PortfoliosXML wraps the portfolio callbacks in the <portfolios> literal.
func PortfoliosXML(inner string) string { return "<portfolios>" + inner + "</portfolios>" }

// PortfolioTPlusXML is the literal <portfolio_tplus/> skeleton (fields I).
func PortfolioTPlusXML() string { return "<portfolio_tplus/>" }

// UnitedPortfolioXML is the literal <united_portfolio/> skeleton (fields I).
func UnitedPortfolioXML() string { return "<united_portfolio/>" }

// portfolioCurrencyName maps the wire currency to the DLL <currency> literal.
// Only the demo RUR->RUB mapping is confirmed (I for the rest).
func portfolioCurrencyName(currency string) string {
	if currency == "RUR" {
		return "RUB"
	}
	return currency
}

// fixed2 renders a money Decimal with exactly two digits (the portfolio forms
// keep the trailing zeros: 300000.00).
func fixed2(d model.Decimal) string {
	return fixedDecimal(model.Decimal{Units: rescaleMoneyUnits(d), Scale: 2})
}

func rescaleMoneyUnits(d model.Decimal) int64 {
	units := d.Units
	if d.Scale > 2 {
		for i := uint8(2); i < d.Scale; i++ {
			units /= 10
		}
	}
	for i := d.Scale; i < 2; i++ {
		units *= 10
	}
	return units
}

// zero2 is the DLL zero for the derived portfolio numbers (I formulas).
var zero2 = model.Decimal{Units: 0, Scale: 2}

// PortfolioCurrencyXML renders one <portfolio_currency> of get_portfolio (O).
// equity/cover are rendered from the stored balance; the derived formulas are
// I (the demo fixture has all of them equal to the balance).
func PortfolioCurrencyXML(p model.MoneyPosition) string {
	var b strings.Builder
	b.WriteString("<portfolio_currency currency=" + quoteAttr(portfolioCurrencyName(p.Currency)) + ">")
	b.WriteString("<cross_rate>1</cross_rate>")
	b.WriteString("<open_balance>" + fixed2(p.SaldoIn) + "</open_balance>")
	b.WriteString("<balance>" + fixed2(p.Saldo) + "</balance>")
	b.WriteString("<equity>" + fixed2(p.Saldo) + "</equity>")
	b.WriteString("<cover>" + fixed2(p.Saldo) + "</cover>")
	b.WriteString("<init_req>" + fixed2(zero2) + "</init_req>")
	b.WriteString("<maint_req>" + fixed2(zero2) + "</maint_req>")
	b.WriteString("<unrealized_pnl>" + fixed2(zero2) + "</unrealized_pnl>")
	b.WriteString("</portfolio_currency>")
	return b.String()
}

// MCPortfolioXML renders one <mc_portfolio client> with the nested
// <portfolio_currency>, the demo <money> block and its <value_part register>.
// The money name is the demo literal; balance_prc is 0.0000 in the fixture (I).
func MCPortfolioXML(client string, p model.MoneyPosition) string {
	var b strings.Builder
	b.WriteString("<mc_portfolio client=" + quoteAttr(client) + ">")
	b.WriteString("<open_equity>" + fixed2(p.Saldo) + "</open_equity>")
	b.WriteString("<equity>" + fixed2(p.Saldo) + "</equity>")
	b.WriteString("<pl>" + fixed2(zero2) + "</pl>")
	b.WriteString("<go>" + fixed2(zero2) + "</go>")
	b.WriteString("<cover>" + fixed2(p.Saldo) + "</cover>")
	b.WriteString("<init_req>" + fixed2(zero2) + "</init_req>")
	b.WriteString("<maint_req>" + fixed2(zero2) + "</maint_req>")
	b.WriteString("<unrealized_pnl>" + fixed2(zero2) + "</unrealized_pnl>")
	b.WriteString(PortfolioCurrencyXML(p))
	b.WriteString("<money name=" + quoteAttr("Фондовый рынок МБ") + " currency=" + quoteAttr(portfolioCurrencyName(p.Currency)) + ">")
	b.WriteString("<balance_prc>0.0000</balance_prc>")
	b.WriteString("<open_balance>" + fixed2(p.SaldoIn) + "</open_balance>")
	b.WriteString("<bought>" + fixed2(p.Bought) + "</bought>")
	b.WriteString("<sold>" + fixed2(p.Sold) + "</sold>")
	b.WriteString("<balance>" + fixed2(p.Saldo) + "</balance>")
	b.WriteString("<blocked>" + fixed2(zero2) + "</blocked>")
	b.WriteString("<estimated>" + fixed2(zero2) + "</estimated>")
	b.WriteString("<fee>" + fixed2(zero2) + "</fee>")
	b.WriteString("<vm>" + fixed2(zero2) + "</vm>")
	b.WriteString("<finres>" + fixed2(zero2) + "</finres>")
	b.WriteString("<cover>" + fixed2(p.Saldo) + "</cover>")
	b.WriteString("<value_part register=" + quoteAttr(p.Register) + ">")
	b.WriteString("<open_balance>" + fixed2(p.SaldoIn) + "</open_balance>")
	b.WriteString("<bought>" + fixed2(p.Bought) + "</bought>")
	b.WriteString("<sold>" + fixed2(p.Sold) + "</sold>")
	b.WriteString("<balance>" + fixed2(p.Saldo) + "</balance>")
	b.WriteString("<blocked>" + fixed2(zero2) + "</blocked>")
	b.WriteString("<estimated>" + fixed2(zero2) + "</estimated>")
	b.WriteString("</value_part></money></mc_portfolio>")
	return b.String()
}

// maxBuySellItemsXML renders the <security secid> items of both max_buy_sell
// forms: identity from the catalog, integer maxbuy/maxsell (margin.md, D/O).
func maxBuySellItemsXML(items []model.MaxBuySell) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString("<security secid=" + quoteAttr(strconv.Itoa(int(item.SecID))) + ">")
		b.WriteString("<market>" + strconv.Itoa(item.Market) + "</market>")
		b.WriteString("<seccode>" + escape(item.SecCode) + "</seccode>")
		b.WriteString("<maxbuy>" + strconv.FormatInt(item.MaxBuy, 10) + "</maxbuy>")
		b.WriteString("<maxsell>" + strconv.FormatInt(item.MaxSell, 10) + "</maxsell>")
		b.WriteString("</security>")
	}
	return b.String()
}

// MaxBuySellTPlusXML renders <max_buy_sell_tplus client="…"> (O).
func MaxBuySellTPlusXML(client string, items []model.MaxBuySell) string {
	return "<max_buy_sell_tplus client=" + quoteAttr(client) + ">" + maxBuySellItemsXML(items) + "</max_buy_sell_tplus>"
}

// MaxBuySellXML renders <max_buy_sell client="…"> or <max_buy_sell union="…">
// depending on which selector the command carries (O).
func MaxBuySellXML(client, union string, items []model.MaxBuySell) string {
	root := "<max_buy_sell"
	if client != "" {
		root += " client=" + quoteAttr(client)
	} else {
		root += " union=" + quoteAttr(union)
	}
	return root + ">" + maxBuySellItemsXML(items) + "</max_buy_sell>"
}

// fixed2Decimal renders a Decimal with exactly two digits.
func fixed2Decimal(d model.Decimal) string {
	if d.Scale != 2 {
		units := d.Units
		if d.Scale > 2 {
			for i := uint8(2); i < d.Scale; i++ {
				units /= 10
			}
		}
		for i := d.Scale; i < 2; i++ {
			units *= 10
		}
		d = model.Decimal{Units: units, Scale: 2}
	}
	return fixedDecimal(d)
}

// ClnSecPermissionsXML renders <cln_sec_permissions>: client/union are
// attributes here (unlike the command where they are elements), the security is
// an element, then the rights and the rate branch (margin.md, D/O).
func ClnSecPermissionsXML(p model.SecPermissions) string {
	var b strings.Builder
	b.WriteString("<cln_sec_permissions")
	if p.Client != "" {
		b.WriteString(" client=" + quoteAttr(p.Client))
	}
	if p.Union != "" {
		b.WriteString(" union=" + quoteAttr(p.Union))
	}
	b.WriteString(" secid=" + quoteAttr(strconv.Itoa(int(p.SecID))) + ">")
	b.WriteString("<security><seccode>" + escape(p.SecCode) + "</seccode>")
	b.WriteString("<market>" + strconv.Itoa(p.Market) + "</market>")
	b.WriteString("<board>" + escape(p.Board) + "</board></security>")
	b.WriteString("<tradeable>" + strconv.FormatBool(p.Tradeable) + "</tradeable>")
	b.WriteString("<marketable>" + strconv.FormatBool(p.Marketable) + "</marketable>")
	b.WriteString("<longable>" + strconv.FormatBool(p.Longable) + "</longable>")
	b.WriteString("<shortable>" + strconv.FormatBool(p.Shortable) + "</shortable>")
	if p.RiskRateLong == nil {
		b.WriteString("<regratelong>" + fixed2Decimal(p.RegRateLong) + "</regratelong>")
		b.WriteString("<regrateshort>" + fixed2Decimal(p.RegRateShort) + "</regrateshort>")
	} else {
		if p.RiskRateLong != nil {
			b.WriteString("<riskrate_long>" + fixed2Decimal(*p.RiskRateLong) + "</riskrate_long>")
		}
		if p.ReserateLong != nil {
			b.WriteString("<reserate_long>" + fixed2Decimal(*p.ReserateLong) + "</reserate_long>")
		}
		if p.RiskRateShort != nil {
			b.WriteString("<riskrate_short>" + fixed2Decimal(*p.RiskRateShort) + "</riskrate_short>")
		}
		if p.ReserateShort != nil {
			b.WriteString("<reserate_short>" + fixed2Decimal(*p.ReserateShort) + "</reserate_short>")
		}
	}
	b.WriteString("</cln_sec_permissions>")
	return b.String()
}

// MarketOrdXML renders the <marketord secid permit seccode/> callback (O).
func MarketOrdXML(m model.MarketOrd) string {
	permit := "no"
	if m.Permit {
		permit = "yes"
	}
	return "<marketord secid=" + quoteAttr(strconv.Itoa(int(m.SecID))) + " permit=" + quoteAttr(permit) + " seccode=" + quoteAttr(m.SecCode) + "/>"
}

// cdata escapes the text of an XML CDATA section (the DLL wraps the news
// source/title/text in CDATA, news.md D/O).
func cdata(s string) string {
	return strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>")
}

// NewsHeaderXML renders <news_header> (news.md, D + O fixture). The O fixture
// prints the timestamp as UTC (the capture machine printed UTC); the exact
// time source of the DLL is I, so UTC keeps the fixture byte-exact.
func NewsHeaderXML(h model.NewsHeader) string {
	return "<news_header><id>" + strconv.FormatInt(h.ID, 10) + "</id><timestamp>" +
		h.Time.UTC().Format("02.01.2006 15:04:05") + "</timestamp><source><![CDATA[" + cdata(h.Source) +
		"]]></source><title><![CDATA[" + cdata(h.Title) + "]]></title></news_header>"
}

// NewsBodyXML renders <news_body> (D form; the emit mechanics are I).
func NewsBodyXML(b model.NewsBody) string {
	return "<news_body><id>" + strconv.FormatInt(b.ID, 10) + "</id><text><![CDATA[" + cdata(b.Text) + "]]></text></news_body>"
}

// MessageXML renders one <message> item (messages.md D/O): only HH:MM:SS of the
// WALL time (Time - bias; the machine-local conversion is I), Y/N urgency and
// CDATA text.
func MessageXML(m model.Message) string {
	urgent := "N"
	if m.Urgent {
		urgent = "Y"
	}
	wall := m.WallTime
	if wall.IsZero() {
		wall = m.Time
	}
	return "<message><date>" + wall.UTC().Format("15:04:05") + "</date><urgent>" + urgent +
		"</urgent><from>" + escape(m.From) + "</from><text><![CDATA[" + cdata(m.Text) + "]]></text></message>"
}

// MessagesXML renders the batch of one RQ_MAIN reply in record order (O).
func MessagesXML(messages []model.Message) string {
	var b strings.Builder
	b.WriteString("<messages>")
	for _, m := range messages {
		b.WriteString(MessageXML(m))
	}
	b.WriteString("</messages>")
	return b.String()
}

// The option XML forms are D literals only (options.md): no O fixture exists,
// so the element meaning/order is I. MatDate is the R_FAMILY first L (semantics
// I; rendered empty when 0), LotSize is the second L (I).
func OptionFamiliesXML(families []model.OptionFamily) string {
	var b strings.Builder
	b.WriteString("<option_families>")
	for _, family := range families {
		b.WriteString("<family>")
		// MatDate is the raw R_FAMILY first L; its meaning/format is not
		// recovered (options.md I), so the element is intentionally empty rather
		// than formatting a possibly bogus Unix date.
		b.WriteString("<mat_date></mat_date>")
		b.WriteString("<lot_size>" + strconv.FormatInt(family.LotSize, 10) + "</lot_size>")
		b.WriteString("</family>")
	}
	b.WriteString("</option_families>")
	return b.String()
}

// FamilyStrikesXML renders the D literal <family_strikes> (order/attrs are I).
func FamilyStrikesXML(strikes model.OptionStrikes) string {
	var b strings.Builder
	b.WriteString("<family_strikes>")
	for _, strike := range strikes.Items {
		b.WriteString("<strike><opt_code>" + escape(strike.OptCode) + "</opt_code>")
		b.WriteString("<price>" + strconv.FormatInt(strike.Price, 10) + "</price>")
		b.WriteString("<cp>" + escape(string([]byte{strike.CP})) + "</cp></strike>")
	}
	b.WriteString("</family_strikes>")
	return b.String()
}

// OptionsFailedXML renders the D literal <options_failed> (trigger semantics I).
func OptionsFailedXML(end model.EndOfFamily) string {
	return "<options_failed><option opt_code=" + quoteAttr(end.OptCode) + " error=" + quoteAttr(end.Text) + "/></options_failed>"
}

// NegDealXML renders the D-literal <negdeal> (rps.md): no O capture exists, so
// the order/attribute details are I. It is NOT wired into Render: there is no
// RQ_MAIN record decoder for the PDF 5.7 event yet (I).
func NegDealXML(deal model.RPSTradeForExecution) string {
	var b strings.Builder
	b.WriteString("<negdeal transactionid=" + quoteAttr(strconv.FormatUint(deal.TransactionID, 10)) + ">")
	b.WriteString("<secid>" + strconv.Itoa(int(deal.SecID)) + "</secid>")
	b.WriteString("<union>" + escape(deal.Union) + "</union>")
	b.WriteString("<client>" + escape(deal.Client) + "</client>")
	b.WriteString("<board>" + escape(deal.Board) + "</board>")
	b.WriteString("<seccode>" + escape(deal.SecCode) + "</seccode>")
	b.WriteString("<buysell>" + escape(string([]byte{deal.BuySell})) + "</buysell>")
	b.WriteString("<price>" + deal.Price.String() + "</price>")
	b.WriteString("<volume>" + strconv.FormatInt(deal.Volume, 10) + "</volume>")
	b.WriteString("<repoterm>" + strconv.FormatInt(deal.RepoTerm, 10) + "</repoterm>")
	b.WriteString("<reporate>" + deal.RepoRate.String() + "</reporate>")
	b.WriteString("<repovalue>" + deal.RepoValue.String() + "</repovalue>")
	b.WriteString("<repo2value>" + deal.Repo2Value.String() + "</repo2value>")
	b.WriteString("</negdeal>")
	return b.String()
}

// ReportXML renders the D-literal <report> (PDF 5.8); O form is I and it is NOT
// wired into Render (no PDF 5.8 record decoder yet).
func ReportXML(report model.ExecutionReport) string {
	var b strings.Builder
	b.WriteString("<report transactionid=" + quoteAttr(strconv.FormatUint(report.TransactionID, 10)) + ">")
	b.WriteString("<reportno>" + strconv.FormatInt(report.ReportNo, 10) + "</reportno>")
	b.WriteString("<reportkind>" + strconv.Itoa(report.ReportKind) + "</reportkind>")
	b.WriteString("<reporttype>" + strconv.Itoa(report.ReportType) + "</reporttype>")
	b.WriteString("<repotradeno>" + strconv.FormatInt(report.RepoTradeNo, 10) + "</repotradeno>")
	b.WriteString("<repovalue>" + report.RepoValue.String() + "</repovalue>")
	b.WriteString("<repo2value>" + report.Repo2Value.String() + "</repo2value>")
	b.WriteString("</report>")
	return b.String()
}
