package backtest

import (
	"math"
	"testing"
	"time"
)

// trendIDs are the trend-following strategies of plan Task 15.
var trendIDs = []string{"trend_tsmom", "trend_keltner", "trend_supertrend", "trend_maribbon"}

// trendBars is a deterministic uptrend with tradable pullbacks: a slow drift
// plus a sine wave. Trend followers should open and close positions on it.
func trendBars(n int) []Bar {
	closes := make([]float64, n)
	for i := range closes {
		closes[i] = 100 + 0.25*float64(i) + 6*math.Sin(float64(i)/9.0)
	}
	bars := closeSeries(closes)
	// Give every bar a non-empty range so ATR-based strategies have data.
	for i := range bars {
		if bars[i].High == bars[i].Low {
			bars[i].High += 0.5
			bars[i].Low -= 0.5
		}
	}
	return bars
}

// flatBars is the opposite fixture: no trend and no range, so trend followers
// must stay out.
func flatBars(n int) []Bar { return closeSeries(constCloses(100, n)) }

func runTrend(t *testing.T, id string, bars []Bar) Result {
	t.Helper()
	res, err := Run(Request{
		Symbol:      "TEST",
		PeriodSec:   86400,
		Bars:        bars,
		Strategy:    id,
		InitialCash: 1_000_000,
	})
	if err != nil {
		t.Fatalf("Run(%s): %v", id, err)
	}
	return res
}

// TestTrendStrategiesOnTrendAndFlat is the plan Task 15 strategy test: every
// trend strategy must trade on a trending series and stay out of a flat one.
func TestTrendStrategiesOnTrendAndFlat(t *testing.T) {
	trend := trendBars(400)
	flat := flatBars(400)
	for _, id := range trendIDs {
		strategy, err := NewStrategy(id, nil)
		if err != nil {
			t.Fatalf("NewStrategy(%q): %v", id, err)
		}
		if strategy.ID() != id {
			t.Fatalf("ID() = %q, want %q", strategy.ID(), id)
		}
		if len(strategy.Params()) == 0 {
			t.Fatalf("%s: Params() is empty", id)
		}

		onTrend := runTrend(t, id, trend)
		if onTrend.Metrics.Trades == 0 {
			t.Fatalf("%s: no trades on the trending fixture (warnings: %v)", id, onTrend.Warnings)
		}
		onFlat := runTrend(t, id, flat)
		if onFlat.Metrics.Trades > 1 {
			t.Fatalf("%s: %d trades on the flat fixture, want at most 1", id, onFlat.Metrics.Trades)
		}
	}
}

// TestTrendStrategiesNoLookahead is the OnBar-level causality invariant for the
// new trend strategies: the action on bar i must not depend on later bars.
func TestTrendStrategiesNoLookahead(t *testing.T) {
	base := trendBars(160)
	poisoned := make([]Bar, len(base))
	copy(poisoned, base)
	for i := 81; i < len(poisoned); i++ {
		factor := 3.0
		poisoned[i] = Bar{
			Time:   base[i].Time,
			Open:   base[i].Open * factor,
			High:   base[i].High * factor,
			Low:    base[i].Low * factor,
			Close:  base[i].Close * factor,
			Volume: base[i].Volume,
		}
	}
	for _, id := range trendIDs {
		strategy, err := NewStrategy(id, nil)
		if err != nil {
			t.Fatalf("NewStrategy(%q): %v", id, err)
		}
		for i := 0; i <= 80; i++ {
			got := strategy.OnBar(base, i)
			changed := strategy.OnBar(poisoned, i)
			if got != changed {
				t.Fatalf("%s: OnBar(%d) depends on future bars: %v vs %v", id, i, got, changed)
			}
		}
	}
}

// TestTrendStrategyParamsNormalize checks the catalog defaults and clamping of
// the new strategies.
func TestTrendStrategyParamsNormalize(t *testing.T) {
	cases := []struct {
		id     string
		params map[string]float64
		want   map[string]float64
	}{
		{"trend_tsmom", nil, map[string]float64{"lookback": 20, "thresholdPct": 0}},
		{"trend_tsmom", map[string]float64{"lookback": 1, "thresholdPct": 100}, map[string]float64{"lookback": 2, "thresholdPct": 50}},
		{"trend_keltner", nil, map[string]float64{"period": 20, "multiplier": 2}},
		{"trend_keltner", map[string]float64{"period": 1, "multiplier": 0}, map[string]float64{"period": 2, "multiplier": 0.5}},
		{"trend_supertrend", nil, map[string]float64{"period": 10, "multiplier": 3}},
		{"trend_supertrend", map[string]float64{"period": 100000, "multiplier": 100}, map[string]float64{"period": 100, "multiplier": 10}},
		{"trend_maribbon", nil, map[string]float64{"fast": 5, "slow": 40}},
		{"trend_maribbon", map[string]float64{"fast": 80, "slow": 20}, map[string]float64{"fast": 19, "slow": 20}},
	}
	for _, tc := range cases {
		strategy, err := NewStrategy(tc.id, tc.params)
		if err != nil {
			t.Fatalf("NewStrategy(%q, %v): %v", tc.id, tc.params, err)
		}
		assertParams(t, strategy.Params(), tc.want)
	}
}

// TestTrendStrategiesJSONShape keeps the catalog contract: every new strategy
// must be serializable and carry non-empty info.
func TestTrendStrategiesJSONShape(t *testing.T) {
	infos := Strategies()
	byID := make(map[string]StrategyInfo, len(infos))
	for _, info := range infos {
		byID[info.ID] = info
	}
	for _, id := range trendIDs {
		info, ok := byID[id]
		if !ok {
			t.Fatalf("strategy %q is missing from the catalog", id)
		}
		if info.Name == "" || info.Description == "" || len(info.Params) == 0 {
			t.Fatalf("strategy %q has incomplete info: %+v", id, info)
		}
		for _, param := range info.Params {
			if param.Key == "" || param.Title == "" || param.Step <= 0 || param.Min > param.Max ||
				param.Default < param.Min || param.Default > param.Max {
				t.Fatalf("strategy %q param %+v is invalid", id, param)
			}
		}
	}
	_ = time.Now
}
