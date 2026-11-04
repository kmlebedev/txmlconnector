package xmlcompat

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// tradeTimeLayout is the DLL <time> format; milliseconds are added when the
// connection was opened with <milliseconds>true</milliseconds>.
const tradeTimeLayout = "02.01.2006 15:04:05"

// FormatTradeTime renders server wall-clock seconds and milliseconds.
func FormatTradeTime(seconds int64, millis uint16, withMillis bool) string {
	t := time.Unix(seconds, 0).UTC()
	if !withMillis {
		return t.Format(tradeTimeLayout)
	}
	if millis >= 1000 {
		millis = 0
	}
	return t.Format(tradeTimeLayout) + fmt.Sprintf(".%03d", millis)
}

// AllTradesXML renders trades as the DLL's <alltrades> message. A trade
// without a wire time gets 00:00 of the trading day from the catalog when it
// was decoded, so the wall clock in model.Trade is already resolved.
func AllTradesXML(trades []model.Trade, withMillis bool) string {
	var b strings.Builder
	b.WriteString("<alltrades>")
	for _, t := range trades {
		b.WriteString(`<trade secid="` + strconv.Itoa(int(t.SecID)) + `">`)
		b.WriteString("<tradeno>" + strconv.FormatInt(t.TradeNo, 10) + "</tradeno>")
		b.WriteString("<board>" + escape(t.Board) + "</board>")
		b.WriteString("<time>" + FormatTradeTime(t.WallTime, t.Millis, withMillis) + "</time>")
		b.WriteString("<price>" + t.Price.String() + "</price>")
		b.WriteString("<quantity>" + strconv.FormatInt(t.Quantity, 10) + "</quantity>")
		if t.BuySell == 'B' || t.BuySell == 'S' {
			b.WriteString("<buysell>" + string(rune(t.BuySell)) + "</buysell>")
		} else {
			b.WriteString("<buysell/>")
		}
		if t.HasOpenInterest {
			b.WriteString("<openinterest>" + strconv.FormatInt(t.OpenInterest, 10) + "</openinterest>")
		}
		b.WriteString("<seccode>" + escape(t.SecCode) + "</seccode>")
		if t.Period != 0 {
			b.WriteString("<period>" + escape(string(rune(t.Period))) + "</period>")
		} else {
			b.WriteString("<period/>")
		}
		b.WriteString("</trade>")
	}
	b.WriteString("</alltrades>")
	return b.String()
}

func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
