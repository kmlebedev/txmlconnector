package xmlcompat

import (
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
		return PositionsXML([]model.MoneyPosition{*e}, false), true
	case *model.Union:
		return UnionXML(*e), true
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
