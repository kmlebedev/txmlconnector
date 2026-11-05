package backtest

import (
	"fmt"
	"math"
	"time"
)

// Sample-size and cost warning codes. They are stable machine-readable prefixes
// of the warnings in Result.Warnings and research.BatchRow.Warnings.
const (
	// WarningSmallSample marks a backtest whose sample is too small to be
	// treated as evidence (plan Review Focus 6).
	WarningSmallSample = "small_sample"
	// WarningNoCosts marks a run that explicitly used zero fees and slippage.
	WarningNoCosts = "no_costs"
)

// Sample-size thresholds of the research warnings.
const (
	// minSampleTrades is the minimum number of closed round trips.
	minSampleTrades = 30
	// minDailySample is the minimum daily history: 5 years.
	minDailySample = 5 * 365 * 24 * time.Hour
	// minIntradaySample is the minimum intraday history: 1 year.
	minIntradaySample = 365 * 24 * time.Hour
)

// SampleWarning returns the small_sample warning for a completed backtest, or
// "" when the sample is large enough. periodSec >= 86400 is treated as daily
// bars (at least 5 years are required), anything shorter as intraday bars (at
// least 1 year).
func SampleWarning(trades int, from, to time.Time, periodSec int) string {
	if trades < minSampleTrades {
		return fmt.Sprintf("%s: мало сделок: %d < %d — выборка ненадёжна",
			WarningSmallSample, trades, minSampleTrades)
	}
	required := minIntradaySample
	if periodSec >= 86400 {
		required = minDailySample
	}
	span := to.Sub(from)
	if span > 0 && span < required {
		return fmt.Sprintf("%s: история %.1f лет < %.0f лет — выборка ненадёжна",
			WarningSmallSample, span.Hours()/24/365, required.Hours()/24/365)
	}
	return ""
}

// NoCostsWarning returns the no_costs warning for a run that explicitly asked
// for zero fees and slippage, or "" otherwise. Explicit costs are the only way
// to get a true zero-cost backtest: without Request.ExplicitCosts a zero cost
// means "use the documented defaults" (see normalizeRequest).
func NoCostsWarning(feeBps, slippageBps float64, explicit bool) string {
	if !explicit || feeBps != 0 || slippageBps != 0 {
		return ""
	}
	return WarningNoCosts + ": комиссия и проскальзывание нулевые — расчёт без расходов"
}

// profitFactorCap is reported when there are winning trades but no losing ones:
// ProfitFactor is mathematically infinite there, but the JSON API requires a
// finite number.
const profitFactorCap = 1000.0

// metricsInput carries the raw series and trades the summary metrics are
// derived from.
type metricsInput struct {
	initialCash  float64
	equity       []float64
	times        []time.Time
	exposureBars int
	trades       []Trade
	periodSec    int
}

// drawdownSeries returns the per-bar drawdown from the running equity peak, in
// percent (0 at a new peak, negative below it).
func drawdownSeries(equity []float64) []float64 {
	out := make([]float64, len(equity))
	if len(equity) == 0 {
		return out
	}
	peak := equity[0]
	for i, value := range equity {
		if value > peak {
			peak = value
		}
		if peak > 0 {
			out[i] = (value/peak - 1) * 100
		}
	}
	return out
}

// sharpeRatio annualizes the mean/std of per-bar equity returns by
// sqrt(365*24*3600/periodSec). It returns 0 when the ratio is undefined
// (no period, too few returns, zero volatility).
func sharpeRatio(equity []float64, periodSec int) float64 {
	if periodSec <= 0 || len(equity) < 3 {
		return 0
	}
	returns := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		if equity[i-1] <= 0 {
			continue
		}
		returns = append(returns, equity[i]/equity[i-1]-1)
	}
	if len(returns) < 2 {
		return 0
	}
	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))
	var variance float64
	for _, r := range returns {
		d := r - mean
		variance += d * d
	}
	variance /= float64(len(returns))
	std := math.Sqrt(variance)
	if std == 0 {
		return 0
	}
	annualization := float64(365*24*3600) / float64(periodSec)
	sharpe := mean / std * math.Sqrt(annualization)
	if math.IsNaN(sharpe) || math.IsInf(sharpe, 0) {
		return 0
	}
	return sharpe
}

// computeMetrics derives the Metrics block from the equity path and closed
// trades. Percentage fields are in percent; MaxDrawdownPct is negative.
func computeMetrics(in metricsInput) Metrics {
	m := Metrics{
		InitialCash: in.initialCash,
		Bars:        len(in.equity),
	}
	if len(in.equity) == 0 {
		return m
	}
	m.FinalEquity = in.equity[len(in.equity)-1]
	if in.initialCash > 0 {
		m.TotalReturnPct = (m.FinalEquity/in.initialCash - 1) * 100
	}

	for _, drawdown := range drawdownSeries(in.equity) {
		if drawdown < m.MaxDrawdownPct {
			m.MaxDrawdownPct = drawdown
		}
	}
	m.Sharpe = sharpeRatio(in.equity, in.periodSec)

	m.Trades = len(in.trades)
	var wins, losses, pnlPctSum float64
	winners := 0
	for _, trade := range in.trades {
		pnlPctSum += trade.PnLPct
		switch {
		case trade.PnL > 0:
			winners++
			wins += trade.PnL
		case trade.PnL < 0:
			losses -= trade.PnL
		}
	}
	if n := len(in.trades); n > 0 {
		m.WinRatePct = float64(winners) / float64(n) * 100
		m.AvgTradePct = pnlPctSum / float64(n)
	}
	switch {
	case losses > 0:
		m.ProfitFactor = wins / losses
	case wins > 0:
		m.ProfitFactor = profitFactorCap
	}

	m.ExposurePct = float64(in.exposureBars) / float64(len(in.equity)) * 100

	if len(in.times) == len(in.equity) {
		m.From = in.times[0]
		m.To = in.times[len(in.times)-1]
	}
	return m
}
