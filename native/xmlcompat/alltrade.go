package xmlcompat

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// AllTrade flag bits: optional fields of R_ALLTRADE, read in this order by
// FUN_1803bf700.
const (
	AllTradePeriod       uint16 = 0x0001 // B: <period> (O, N, C)
	AllTradeField2       uint16 = 0x0002 // L, then G after Field4 (not in <alltrades>)
	AllTradeField4       uint16 = 0x0004 // G (not in <alltrades>)
	AllTradeBuySell      uint16 = 0x0008 // B: <buysell> (B or S)
	AllTradeText         uint16 = 0x0010 // Z12 (not in <alltrades>)
	AllTradeBlock20      uint16 = 0x0020 // L L G G (not in <alltrades>)
	AllTradeOpenInterest uint16 = 0x0040 // L: <openinterest>
	AllTradeCurrency     uint16 = 0x0080 // Z4 (not in <alltrades>)
	AllTradeMillis       uint16 = 0x0100 // W: milliseconds of <time>, 1000 when absent
)

// AllTrade is an R_ALLTRADE record (tag 0x08): one trade of the market feed
// subscribed with <alltrades>. Prices are integers in units of 10^-decimals
// of the trade's board.
type AllTrade struct {
	SecID      uint16
	Board      uint16 // board number from RQ_STARTUP
	TradeNo    int64
	Time       int64 // seconds, server wall clock as Unix time; 0: not set
	Price      int64 // in units of 10^-decimals of Board
	Quantity   int64
	Flags      uint16
	Period     byte // 0 when absent
	BuySell    byte // 0 when absent
	OpenInt    int64
	Millis     uint16 // 1000 when absent
	Field2     int32
	Field2Long int64
	Field4     int64
	Text       string
	Block20    [4]int64
	Currency   string
}

// ParseAllTrade decodes an R_ALLTRADE body.
func ParseAllTrade(body []byte) (AllTrade, error) {
	r := &reader{Reader: wire.NewReader(body)}
	t := AllTrade{SecID: r.word(), Board: r.word(), TradeNo: r.int64(), Time: int64(r.int32()),
		Price: int64(r.int32()), Quantity: int64(r.int32()), Flags: r.word(), Millis: 1000}
	f := t.Flags
	if f&AllTradePeriod != 0 {
		t.Period = r.byte()
	}
	if f&AllTradeField2 != 0 {
		t.Field2 = r.int32()
	}
	if f&AllTradeField4 != 0 {
		t.Field4 = r.int64()
	}
	if f&AllTradeField2 != 0 {
		t.Field2Long = r.int64()
	}
	if f&AllTradeBuySell != 0 {
		t.BuySell = r.byte()
	}
	if f&AllTradeText != 0 {
		t.Text = r.str(0xc)
	}
	if f&AllTradeBlock20 != 0 {
		t.Block20 = [4]int64{int64(r.int32()), int64(r.int32()), r.int64(), r.int64()}
	}
	if f&AllTradeOpenInterest != 0 {
		t.OpenInt = int64(r.int32())
	}
	if f&AllTradeCurrency != 0 {
		t.Currency = r.str(4)
	}
	if f&AllTradeMillis != 0 {
		t.Millis = r.word()
	}
	if r.err != nil {
		return t, fmt.Errorf("R_ALLTRADE: %w", r.err)
	}
	return t, nil
}

func (r *reader) int64() int64 {
	if r.err != nil {
		return 0
	}
	var v int64
	v, r.err = r.Int64()
	return v
}

// TradeDedup drops trades whose number is not above the last one seen for
// the same security and board, as the DLL does ("old ALLTRADE No" in its log).
// It keeps replays after a reconnect out of the feed.
type TradeDedup struct {
	last map[[2]uint16]int64
}

// Fresh reports whether t is new and records it.
func (d *TradeDedup) Fresh(t AllTrade) bool {
	if d.last == nil {
		d.last = map[[2]uint16]int64{}
	}
	key := [2]uint16{t.SecID, t.Board}
	if last, ok := d.last[key]; ok && t.TradeNo <= last {
		return false
	}
	d.last[key] = t.TradeNo
	return true
}

// FormatPrice renders an integer price in units of 10^-decimals as the DLL
// does: trailing zeros removed, one digit kept after the point (305.0).
func FormatPrice(units int64, decimals int) string {
	neg := units < 0
	u := uint64(units)
	if neg {
		u = -u
	}
	digits := strconv.FormatUint(u, 10)
	if decimals > 0 {
		if len(digits) <= decimals {
			digits = strings.Repeat("0", decimals-len(digits)+1) + digits
		}
		whole, frac := digits[:len(digits)-decimals], strings.TrimRight(digits[len(digits)-decimals:], "0")
		if frac == "" {
			frac = "0"
		}
		digits = whole + "." + frac
	}
	if neg {
		return "-" + digits
	}
	return digits
}

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
// without time gets 00:00 of the trading day, as observed from the DLL.
func (c *Catalog) AllTradesXML(trades []AllTrade, withMillis bool) string {
	var b strings.Builder
	b.WriteString("<alltrades>")
	for _, t := range trades {
		seccode, board := "", ""
		if s, ok := c.Security(t.SecID); ok {
			seccode = s.SecCode
		}
		if bd, ok := c.Board(t.Board); ok {
			board = bd.Code
		}
		dec, _ := c.Decimals(t.SecID, t.Board)
		secs := t.Time
		if secs == 0 {
			secs = c.TradeDate()
		}
		b.WriteString(`<trade secid="` + strconv.Itoa(int(t.SecID)) + `">`)
		b.WriteString("<tradeno>" + strconv.FormatInt(t.TradeNo, 10) + "</tradeno>")
		b.WriteString("<board>" + escape(board) + "</board>")
		b.WriteString("<time>" + FormatTradeTime(secs, t.Millis, withMillis) + "</time>")
		b.WriteString("<price>" + FormatPrice(t.Price, int(dec)) + "</price>")
		b.WriteString("<quantity>" + strconv.FormatInt(t.Quantity, 10) + "</quantity>")
		if t.BuySell == 'B' || t.BuySell == 'S' {
			b.WriteString("<buysell>" + string(rune(t.BuySell)) + "</buysell>")
		} else {
			b.WriteString("<buysell/>")
		}
		if t.Flags&AllTradeOpenInterest != 0 {
			b.WriteString("<openinterest>" + strconv.FormatInt(t.OpenInt, 10) + "</openinterest>")
		}
		b.WriteString("<seccode>" + escape(seccode) + "</seccode>")
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
