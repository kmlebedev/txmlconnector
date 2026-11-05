package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// hintsBars is how much history Hints asks from the feed.
const hintsBars = 200

// Hints computes technical hints for one instrument: SMA20/50, RSI14, ATR14,
// annualized volatility, trend, support/resistance and market breadth.
func (e *Engine) Hints(ctx context.Context, symbol string, periodSec int) (MarketHints, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	symbol = e.resolveSymbol(symbol)
	if symbol == "" {
		return MarketHints{}, errors.New("agent: symbol is required")
	}
	period := e.normalizePeriod(periodSec)
	bars, err := e.data.History(ctx, symbol, period, hintsBars)
	if err != nil {
		return MarketHints{}, fmt.Errorf("agent: history for %s: %w", symbol, err)
	}
	if len(bars) == 0 {
		return MarketHints{}, fmt.Errorf("agent: no history for %s", symbol)
	}
	h := MarketHints{
		Symbol:    symbol,
		PeriodSec: period,
		AsOf:      bars[len(bars)-1].Time,
		Breadth:   e.breadth(),
	}

	closes := make([]float64, len(bars))
	for i, bar := range bars {
		closes[i] = bar.Close
	}

	h.SMA20 = meanLast(closes, 20)
	h.SMA50 = meanLast(closes, 50)
	h.RSI14 = rsi(closes, 14)
	h.ATR14 = atr(bars, 14)
	h.VolatilityPct = volatilityPct(closes, period, 50)

	last := closes[len(closes)-1]
	switch {
	case last > h.SMA20 && h.SMA20 >= h.SMA50:
		h.Trend = "up"
	case last < h.SMA20 && h.SMA20 <= h.SMA50:
		h.Trend = "down"
	default:
		h.Trend = "flat"
	}
	window := bars
	if len(window) > 20 {
		window = window[len(window)-20:]
	}
	h.Support = window[0].Low
	h.Resistance = window[0].High
	for _, bar := range window[1:] {
		if bar.Low < h.Support {
			h.Support = bar.Low
		}
		if bar.High > h.Resistance {
			h.Resistance = bar.High
		}
	}
	h.Note = fmt.Sprintf("расчёт по %d барам, период %s; данные терминала, не рекомендация",
		len(bars), periodLabel(period))
	if len(bars) < 50 {
		h.Note += "; истории мало для надёжных сигналов"
	}
	return h, nil
}

// breadth counts advancers/decliners/unchanged across the data universe.
func (e *Engine) breadth() map[string]int {
	b := map[string]int{"advancers": 0, "decliners": 0, "unchanged": 0}
	for _, ins := range e.data.Instruments() {
		switch {
		case ins.ChangePct > 0.0001:
			b["advancers"]++
		case ins.ChangePct < -0.0001:
			b["decliners"]++
		default:
			b["unchanged"]++
		}
	}
	return b
}

// meanLast returns the mean of the last n values (all values when shorter).
func meanLast(values []float64, n int) float64 {
	if len(values) == 0 {
		return 0
	}
	if n <= 0 || n > len(values) {
		n = len(values)
	}
	sum := 0.0
	for _, v := range values[len(values)-n:] {
		sum += v
	}
	return sum / float64(n)
}

// rsi is the classic 14-period relative strength index over close prices.
func rsi(closes []float64, period int) float64 {
	if period <= 0 {
		period = 14
	}
	if len(closes) < period+1 {
		return 50
	}
	var gain, loss float64
	start := len(closes) - period
	for i := start; i < len(closes); i++ {
		diff := closes[i] - closes[i-1]
		if diff >= 0 {
			gain += diff
		} else {
			loss -= diff
		}
	}
	avgGain := gain / float64(period)
	avgLoss := loss / float64(period)
	if avgGain == 0 && avgLoss == 0 {
		return 50
	}
	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs)
}

// atr is the average true range over the last period bars.
func atr(bars []backtest.Bar, period int) float64 {
	if period <= 0 {
		period = 14
	}
	if len(bars) < 2 {
		return 0
	}
	from := len(bars) - period
	if from < 1 {
		from = 1
	}
	sum := 0.0
	count := 0
	for i := from; i < len(bars); i++ {
		prevClose := bars[i-1].Close
		tr := bars[i].High - bars[i].Low
		if v := math.Abs(bars[i].High - prevClose); v > tr {
			tr = v
		}
		if v := math.Abs(bars[i].Low - prevClose); v > tr {
			tr = v
		}
		sum += tr
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// volatilityPct is the annualized standard deviation of bar returns, in
// percent. Annualization uses the freezing contract's convention
// sqrt(365*24*3600/periodSec).
func volatilityPct(closes []float64, periodSec, window int) float64 {
	if len(closes) < 3 {
		return 0
	}
	if window <= 0 || window > len(closes)-1 {
		window = len(closes) - 1
	}
	returns := make([]float64, 0, window)
	for i := len(closes) - window; i < len(closes); i++ {
		if closes[i-1] == 0 {
			continue
		}
		returns = append(returns, closes[i]/closes[i-1]-1)
	}
	if len(returns) < 2 {
		return 0
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))
	variance := 0.0
	for _, r := range returns {
		d := r - mean
		variance += d * d
	}
	variance /= float64(len(returns) - 1)
	annualization := 365 * 24 * 3600 / float64(periodSec)
	return math.Sqrt(variance) * math.Sqrt(annualization) * 100
}

// periodLabel renders a candle period for Russian notes.
func periodLabel(periodSec int) string {
	switch periodSec {
	case 60:
		return "1м"
	case 300:
		return "5м"
	case 900:
		return "15м"
	case 3600:
		return "1ч"
	case 86400:
		return "день"
	default:
		return fmt.Sprintf("%ds", periodSec)
	}
}

// wordIn reports whether the lowercase text contains word as a whole
// letter/digit token (underscore- and punctuation-separated words count).
func wordIn(text, word string) bool {
	if word == "" {
		return false
	}
	for from := 0; from <= len(text)-len(word); {
		idx := strings.Index(text[from:], word)
		if idx < 0 {
			return false
		}
		pos := from + idx
		before := pos == 0 || !isWordByte(text[pos-1])
		afterPos := pos + len(word)
		after := afterPos >= len(text) || !isWordByte(text[afterPos])
		if before && after {
			return true
		}
		from = pos + 1
	}
	return false
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 0x80
}

// parsePeriodSec parses Russian/English period words from a lowercase message.
func parsePeriodSec(text string) (int, bool) {
	switch {
	case hasAny(text, "15м", "15м.", "15 m", "15m", "15 мин", "15-мин", "15-минут"):
		return 900, true
	case hasAny(text, "5м", "5м.", "5 m", "5m", "5 мин", "5-мин", "5-минут"):
		return 300, true
	case hasAny(text, "1ч", "1 ч", "1h", "1 h", "часовой", "часовик", "часов"):
		return 3600, true
	case wordIn(text, "день") || wordIn(text, "дня") || wordIn(text, "дней") ||
		hasAny(text, "днев", "1д", "1 d", "1d", "day", "daily"):
		return 86400, true
	case hasAny(text, "1м", "1м.", "1 m", "1m", "1 мин", "1-мин", "1-минут"):
		return 60, true
	}
	return 0, false
}

// parseCountRange returns how many bars the user asked for ("за месяц",
// "за неделю", "за год"), clamped to the tool limit.
func parseCountRange(text string, periodSec int) int {
	if periodSec <= 0 {
		periodSec = defaultPeriodSec
	}
	days := 0
	switch {
	case hasAny(text, "за месяц", "месяц", "month", "30 дней"):
		days = 30
	case hasAny(text, "за неделю", "неделю", "week", "7 дней"):
		days = 7
	case hasAny(text, "за год", "год", "year", "12 месяц"):
		days = 365
	case hasAny(text, "за день", "сегодня", "today", "day"):
		days = 1
	}
	if days == 0 {
		if periodSec == 86400 {
			return 365
		}
		return defaultMaxBars
	}
	count := days * 86400 / periodSec
	if count < 20 {
		count = 20
	}
	if count > defaultMaxBars {
		count = defaultMaxBars
	}
	return count
}

// asOfTime is a small helper for logs: nil time is replaced with now.
func asOfTime(t time.Time, now time.Time) time.Time {
	if t.IsZero() {
		return now
	}
	return t
}
