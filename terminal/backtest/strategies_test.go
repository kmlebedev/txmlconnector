package backtest

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestStrategiesCatalog(t *testing.T) {
	wantDefaults := map[string]map[string]float64{
		"sma_cross":         {"fast": 10, "slow": 30},
		"rsi_reversion":     {"period": 14, "oversold": 30, "overbought": 55},
		"donchian_breakout": {"period": 20},
		"momentum":          {"lookback": 20, "thresholdPct": 1.0},
	}
	infos := Strategies()
	if len(infos) != len(wantDefaults) {
		t.Fatalf("Strategies() returned %d strategies, want %d", len(infos), len(wantDefaults))
	}
	seen := make(map[string]bool, len(infos))
	for _, info := range infos {
		if info.ID == "" || info.Name == "" || info.Description == "" {
			t.Fatalf("strategy %+v has an empty id/name/description", info)
		}
		if seen[info.ID] {
			t.Fatalf("duplicate strategy id %q", info.ID)
		}
		seen[info.ID] = true
		want, ok := wantDefaults[info.ID]
		if !ok {
			t.Fatalf("unexpected strategy id %q", info.ID)
		}
		got := make(map[string]float64, len(info.Params))
		for _, p := range info.Params {
			if p.Key == "" || p.Title == "" {
				t.Fatalf("%s: param %+v has an empty key/title", info.ID, p)
			}
			if p.Min > p.Max {
				t.Fatalf("%s: param %q has min %v > max %v", info.ID, p.Key, p.Min, p.Max)
			}
			if p.Default < p.Min || p.Default > p.Max {
				t.Fatalf("%s: param %q default %v outside [%v, %v]", info.ID, p.Key, p.Default, p.Min, p.Max)
			}
			if p.Step <= 0 {
				t.Fatalf("%s: param %q step %v must be positive", info.ID, p.Key, p.Step)
			}
			got[p.Key] = p.Default
		}
		assertParams(t, got, want)
	}
	for id := range wantDefaults {
		if !seen[id] {
			t.Fatalf("strategy %q is missing from the catalog", id)
		}
	}
}

func TestNewStrategyDefaults(t *testing.T) {
	cases := map[string]map[string]float64{
		"sma_cross":         {"fast": 10, "slow": 30},
		"rsi_reversion":     {"period": 14, "oversold": 30, "overbought": 55},
		"donchian_breakout": {"period": 20},
		"momentum":          {"lookback": 20, "thresholdPct": 1.0},
	}
	for id, want := range cases {
		strategy, err := NewStrategy(id, nil)
		if err != nil {
			t.Fatalf("NewStrategy(%q): %v", id, err)
		}
		if strategy.ID() != id {
			t.Fatalf("ID() = %q, want %q", strategy.ID(), id)
		}
		assertParams(t, strategy.Params(), want)

		// Params() must hand out a copy: callers cannot mutate the strategy.
		params := strategy.Params()
		params["bogus"] = 1
		if _, ok := strategy.Params()["bogus"]; ok {
			t.Fatalf("%s: Params() returns the internal map", id)
		}
	}
}

func TestNewStrategyUnknown(t *testing.T) {
	for _, id := range []string{"", "no_such_strategy"} {
		_, err := NewStrategy(id, nil)
		if err == nil {
			t.Fatalf("NewStrategy(%q) = nil error, want error", id)
		}
		if !strings.Contains(err.Error(), "unknown strategy") {
			t.Fatalf("NewStrategy(%q) error = %q, want it to mention unknown strategy", id, err)
		}
	}
}

func TestNewStrategyClampsParams(t *testing.T) {
	cases := []struct {
		name   string
		id     string
		params map[string]float64
		want   map[string]float64
	}{
		{
			name:   "sma fast above slow is clamped below slow",
			id:     "sma_cross",
			params: map[string]float64{"fast": 50, "slow": 20},
			want:   map[string]float64{"fast": 19, "slow": 20},
		},
		{
			name:   "sma period bounds",
			id:     "sma_cross",
			params: map[string]float64{"fast": 1, "slow": 100000},
			want:   map[string]float64{"fast": 2, "slow": 500},
		},
		{
			name:   "rsi crossed thresholds are separated",
			id:     "rsi_reversion",
			params: map[string]float64{"period": 1, "oversold": 80, "overbought": 20},
			want:   map[string]float64{"period": 2, "oversold": 19, "overbought": 20},
		},
		{
			name:   "donchian period below minimum",
			id:     "donchian_breakout",
			params: map[string]float64{"period": 0},
			want:   map[string]float64{"period": 2},
		},
		{
			name:   "momentum lookback above maximum",
			id:     "momentum",
			params: map[string]float64{"lookback": 100000, "thresholdPct": 100},
			want:   map[string]float64{"lookback": 500, "thresholdPct": 50},
		},
		{
			name:   "non-finite params fall back to defaults",
			id:     "momentum",
			params: map[string]float64{"lookback": math.NaN(), "thresholdPct": math.Inf(1)},
			want:   map[string]float64{"lookback": 20, "thresholdPct": 1.0},
		},
		{
			name:   "unknown params are ignored",
			id:     "donchian_breakout",
			params: map[string]float64{"period": 30, "mystery": 42},
			want:   map[string]float64{"period": 30},
		},
		{
			name:   "fractional period is rounded",
			id:     "sma_cross",
			params: map[string]float64{"fast": 10.4, "slow": 30.6},
			want:   map[string]float64{"fast": 10, "slow": 31},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strategy, err := NewStrategy(tc.id, tc.params)
			if err != nil {
				t.Fatalf("NewStrategy(%q): %v", tc.id, err)
			}
			assertParams(t, strategy.Params(), tc.want)
		})
	}
}

// TestStrategiesDoNotReadFutureBars is the OnBar-level no-lookahead invariant:
// the action on bar i must not depend on any bar after i.
func TestStrategiesDoNotReadFutureBars(t *testing.T) {
	base := wavyBars(80)
	poisoned := make([]Bar, len(base))
	copy(poisoned, base)
	for i := 41; i < len(poisoned); i++ {
		poisoned[i] = Bar{
			Time:   base[i].Time,
			Open:   500,
			High:   700,
			Low:    300,
			Close:  500,
			Volume: 1,
		}
	}
	for _, id := range []string{"sma_cross", "rsi_reversion", "donchian_breakout", "momentum"} {
		strategy, err := NewStrategy(id, nil)
		if err != nil {
			t.Fatalf("NewStrategy(%q): %v", id, err)
		}
		for i := 0; i <= 40; i++ {
			got := strategy.OnBar(base, i)
			changed := strategy.OnBar(poisoned, i)
			if got != changed {
				t.Fatalf("%s: OnBar(%d) depends on future bars: %v vs %v", id, i, got, changed)
			}
		}
	}
}

func TestSMACrossActions(t *testing.T) {
	strategy, err := NewStrategy("sma_cross", map[string]float64{"fast": 2, "slow": 3})
	if err != nil {
		t.Fatalf("NewStrategy: %v", err)
	}
	bars := closeSeries(knownCrossCloses())
	if got := strategy.OnBar(bars, 5); got != Buy {
		t.Fatalf("OnBar(5) = %v, want Buy", got)
	}
	if got := strategy.OnBar(bars, 12); got != Sell {
		t.Fatalf("OnBar(12) = %v, want Sell", got)
	}
	if got := strategy.OnBar(bars, 4); got != Hold {
		t.Fatalf("OnBar(4) = %v, want Hold (not enough history)", got)
	}
}

func TestRSIReversionActions(t *testing.T) {
	strategy, err := NewStrategy("rsi_reversion", nil)
	if err != nil {
		t.Fatalf("NewStrategy: %v", err)
	}
	down := closeSeries(trendCloses(200, -1, 40))
	up := closeSeries(trendCloses(100, 1, 40))
	if got := strategy.OnBar(down, 39); got != Buy {
		t.Fatalf("falling RSI action = %v, want Buy (oversold)", got)
	}
	if got := strategy.OnBar(up, 39); got != Sell {
		t.Fatalf("rising RSI action = %v, want Sell (overbought)", got)
	}
	if got := strategy.OnBar(down, 5); got != Hold {
		t.Fatalf("OnBar(5) = %v, want Hold (not enough history)", got)
	}
}

func TestDonchianBreakoutActions(t *testing.T) {
	strategy, err := NewStrategy("donchian_breakout", map[string]float64{"period": 3})
	if err != nil {
		t.Fatalf("NewStrategy: %v", err)
	}
	closes := []float64{10, 10.5, 10.2, 11.0, 10.3, 10.4, 10.4, 9.5}
	bars := make([]Bar, len(closes))
	for i, c := range closes {
		bars[i] = Bar{
			Time:   testStart.Add(time.Duration(i) * testBarPeriod),
			Open:   c,
			High:   c + 0.2,
			Low:    c - 0.2,
			Close:  c,
			Volume: 1,
		}
	}
	if got := strategy.OnBar(bars, 3); got != Buy {
		t.Fatalf("breakout above channel action = %v, want Buy", got)
	}
	if got := strategy.OnBar(bars, 7); got != Sell {
		t.Fatalf("breakdown below channel action = %v, want Sell", got)
	}
	if got := strategy.OnBar(bars, 2); got != Hold {
		t.Fatalf("OnBar(2) = %v, want Hold (not enough history)", got)
	}
}

func TestMomentumActions(t *testing.T) {
	strategy, err := NewStrategy("momentum", map[string]float64{"lookback": 2, "thresholdPct": 1.0})
	if err != nil {
		t.Fatalf("NewStrategy: %v", err)
	}
	up := closeSeries([]float64{100, 100, 100, 103})
	if got := strategy.OnBar(up, 3); got != Buy {
		t.Fatalf("positive momentum action = %v, want Buy", got)
	}
	down := closeSeries([]float64{100, 100, 100, 99})
	if got := strategy.OnBar(down, 3); got != Sell {
		t.Fatalf("negative momentum action = %v, want Sell", got)
	}
	flat := closeSeries([]float64{100, 100, 100, 100})
	if got := strategy.OnBar(flat, 3); got != Hold {
		t.Fatalf("flat momentum action = %v, want Hold", got)
	}
	if got := strategy.OnBar(up, 1); got != Hold {
		t.Fatalf("OnBar(1) = %v, want Hold (not enough history)", got)
	}
}
