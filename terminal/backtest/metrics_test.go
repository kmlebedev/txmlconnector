package backtest

import (
	"math"
	"testing"
	"time"
)

func TestComputeMetricsKnownPath(t *testing.T) {
	equity := []float64{100, 110, 105, 120, 90, 100}
	times := make([]time.Time, len(equity))
	for i := range times {
		times[i] = testStart.Add(time.Duration(i) * testBarPeriod)
	}
	trades := []Trade{
		{PnL: 50, PnLPct: 5},
		{PnL: -30, PnLPct: -3},
		{PnL: 20, PnLPct: 2},
	}
	m := computeMetrics(metricsInput{
		initialCash:  100,
		equity:       equity,
		times:        times,
		exposureBars: 3,
		trades:       trades,
		periodSec:    86400,
	})

	assertClose(t, "initial cash", m.InitialCash, 100)
	assertClose(t, "final equity", m.FinalEquity, 100)
	assertClose(t, "total return pct", m.TotalReturnPct, 0)
	// The worst peak-to-trough move is 120 -> 90 = -25%.
	assertClose(t, "max drawdown pct", m.MaxDrawdownPct, -25)
	assertClose(t, "win rate pct", m.WinRatePct, 2.0/3.0*100)
	assertClose(t, "profit factor", m.ProfitFactor, 70.0/30.0)
	assertClose(t, "avg trade pct", m.AvgTradePct, (5.0-3.0+2.0)/3.0)
	assertClose(t, "exposure pct", m.ExposurePct, 3.0/6.0*100)
	assertClose(t, "sharpe", m.Sharpe, referenceSharpe(equity, 86400))
	if m.Trades != 3 || m.Bars != 6 {
		t.Fatalf("trades/bars = %d/%d, want 3/6", m.Trades, m.Bars)
	}
	if !m.From.Equal(times[0]) || !m.To.Equal(times[len(times)-1]) {
		t.Fatalf("from/to = %v/%v, want %v/%v", m.From, m.To, times[0], times[len(times)-1])
	}
}

func TestComputeMetricsFlat(t *testing.T) {
	equity := []float64{1000, 1000, 1000}
	times := make([]time.Time, len(equity))
	m := computeMetrics(metricsInput{initialCash: 1000, equity: equity, times: times, periodSec: 60})
	if m.Trades != 0 || m.WinRatePct != 0 || m.ProfitFactor != 0 || m.AvgTradePct != 0 {
		t.Fatalf("trade metrics must be zero without trades, got %+v", m)
	}
	if m.MaxDrawdownPct != 0 || m.Sharpe != 0 || m.TotalReturnPct != 0 || m.ExposurePct != 0 {
		t.Fatalf("flat equity metrics must be zero, got %+v", m)
	}
}

func TestComputeMetricsNoLossesProfitFactorFinite(t *testing.T) {
	equity := []float64{100, 110}
	times := make([]time.Time, len(equity))
	trades := []Trade{{PnL: 10, PnLPct: 10}}
	m := computeMetrics(metricsInput{initialCash: 100, equity: equity, times: times, trades: trades, exposureBars: 1, periodSec: 86400})
	if !isFinite(m.ProfitFactor) {
		t.Fatalf("profit factor with no losses = %v, want a finite number (JSON must be valid)", m.ProfitFactor)
	}
	assertClose(t, "win rate pct", m.WinRatePct, 100)
	assertClose(t, "avg trade pct", m.AvgTradePct, 10)
}

func TestDrawdownSeries(t *testing.T) {
	got := drawdownSeries([]float64{100, 110, 105, 120, 90, 100})
	want := []float64{0, 0, (105.0/110 - 1) * 100, 0, -25, (100.0/120 - 1) * 100}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		assertClose(t, "drawdown", got[i], want[i])
	}
	if empty := drawdownSeries(nil); len(empty) != 0 {
		t.Fatalf("drawdownSeries(nil) = %v, want empty", empty)
	}
}

func TestSharpeAnnualization(t *testing.T) {
	equity := []float64{100, 101, 100.5, 102, 101.2, 103}
	daily := sharpeRatio(equity, 86400)
	hourly := sharpeRatio(equity, 3600)
	if daily == 0 || hourly == 0 {
		t.Fatalf("sharpe daily/hourly = %v/%v, want non-zero", daily, hourly)
	}
	// annualization = 365*24*3600/periodSec, so the hourly ratio is sqrt(24) times larger.
	assertClose(t, "annualization ratio", hourly/daily, math.Sqrt(24))
	assertClose(t, "sharpe", daily, referenceSharpe(equity, 86400))

	if got := sharpeRatio(equity, 0); got != 0 {
		t.Fatalf("sharpe with periodSec=0 = %v, want 0", got)
	}
	if got := sharpeRatio([]float64{100, 100, 100, 100}, 86400); got != 0 {
		t.Fatalf("sharpe of flat equity = %v, want 0", got)
	}
	if got := sharpeRatio(equity, -300); got != 0 {
		t.Fatalf("sharpe with negative periodSec = %v, want 0", got)
	}
}
