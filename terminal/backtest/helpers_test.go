package backtest

import (
	"math"
	"strings"
	"testing"
	"time"
)

var testStart = time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)

const testBarPeriod = 300 * time.Second

// closeSeries builds bars with open equal to the previous close (no gaps) and
// high/low equal to max/min of open and close. It is the base fixture for all
// deterministic tests.
func closeSeries(closes []float64) []Bar {
	bars := make([]Bar, len(closes))
	for i, c := range closes {
		open := c
		if i > 0 {
			open = closes[i-1]
		}
		high, low := open, c
		if c > open {
			high = c
		} else {
			low = c
		}
		bars[i] = Bar{
			Time:   testStart.Add(time.Duration(i) * testBarPeriod),
			Open:   open,
			High:   high,
			Low:    low,
			Close:  c,
			Volume: 1000 + int64(i),
		}
	}
	return bars
}

// wavyBars returns a deterministic oscillating series that produces several
// SMA crosses.
func wavyBars(n int) []Bar {
	closes := make([]float64, n)
	for i := range closes {
		closes[i] = 100 + 8*math.Sin(float64(i)/5.0) + 0.05*float64(i)
	}
	return closeSeries(closes)
}

func constCloses(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func trendCloses(start, step float64, n int) []float64 {
	out := make([]float64, n)
	v := start
	for i := range out {
		out[i] = v
		v += step
	}
	return out
}

// knownCrossCloses is the hand-checked SMA(2/3) fixture: it opens a long at the
// close of bar 5 (filled at open of bar 6) and closes it at the close of bar 12
// (filled at open of bar 13).
func knownCrossCloses() []float64 {
	return []float64{100, 100, 100, 100, 100, 101, 103, 105, 107, 109, 111, 110, 105, 104}
}

func assertClose(t *testing.T, what string, got, want float64) {
	t.Helper()
	tol := 1e-9 * math.Max(1, math.Abs(want))
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.10f, want %.10f", what, got, want)
	}
}

func assertParams(t *testing.T, got, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("params = %v, want %v", got, want)
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Fatalf("param %q missing in %v", key, got)
		}
		if math.Abs(gotValue-wantValue) > 1e-9 {
			t.Fatalf("param %q = %v, want %v", key, gotValue, wantValue)
		}
	}
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// referenceSharpe recomputes the documented Sharpe formula independently of the
// implementation: mean/std of per-bar equity returns, scaled by sqrt(annualization).
func referenceSharpe(equity []float64, periodSec int) float64 {
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
	return mean / std * math.Sqrt(annualization)
}
