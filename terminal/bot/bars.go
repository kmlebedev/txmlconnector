package bot

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// closedBars drops the trailing partial bars of a history series: a still
// forming candle is never used for a signal (Global Constraint).
func closedBars(bars []backtest.Bar) []backtest.Bar {
	end := len(bars)
	for end > 0 && bars[end-1].Partial {
		end--
	}
	return bars[:end]
}

// actionOn evaluates one compiled strategy on the last bar of the series.
func actionOn(strategy backtest.Strategy, bars []backtest.Bar) backtest.Action {
	if len(bars) == 0 {
		return backtest.Hold
	}
	return strategy.OnBar(bars, len(bars)-1)
}

// indicatorValues extracts the declared indicator values at the last bar. The
// rules strategy exposes them through Values (series.go); other strategies have
// no indicators, so the map stays empty.
func indicatorValues(strategy backtest.Strategy, bars []backtest.Bar) map[string]float64 {
	ruleStrategy, ok := strategy.(*rules.Strategy)
	if !ok || len(bars) == 0 {
		return nil
	}
	values := ruleStrategy.Values(bars, len(bars)-1)
	if len(values) == 0 {
		return nil
	}
	return values
}

// describeBar renders the compact journal message "close=… rsi=… signal=…".
func describeBar(close float64, indicators map[string]float64, signal string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "close=%.2f", close)
	names := make([]string, 0, len(indicators))
	for name := range indicators {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, " %s=%.4f", name, indicators[name])
	}
	fmt.Fprintf(&b, " signal=%s", signal)
	return b.String()
}

// paperPosition returns the quantity of one symbol in a paper account.
func paperPosition(positions []paper.Position, symbol string) int64 {
	for _, pos := range positions {
		if strings.EqualFold(pos.Symbol, symbol) {
			return pos.Qty
		}
	}
	return 0
}

// recentTrades returns the bot trade stamps newer than cutoff.
func (st *state) recentTrades(cutoff time.Time) []time.Time {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := st.tradeTimes[:0]
	for _, at := range st.tradeTimes {
		if at.After(cutoff) {
			out = append(out, at)
		}
	}
	st.tradeTimes = out
	return append([]time.Time(nil), out...)
}

// recordTrade stamps one paper round-trip start for the per-hour limit.
func (st *state) recordTrade(at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.tradeTimes = append(st.tradeTimes, at)
}

// appendJournal is the lock-free journal helper of the runtime.
func (st *state) appendJournal(entry JournalEntry) JournalEntry {
	return st.journal.append(entry)
}
