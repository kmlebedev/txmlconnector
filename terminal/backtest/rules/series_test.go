package rules

import (
	"math"
	"testing"
)

// TestStrategySeriesMatchesOnBar proves the chart series (rules.Strategy.Series)
// has the documented shape, marks the warm-up with NaN and matches the values
// the incremental OnBar path computes.
func TestStrategySeriesMatchesOnBar(t *testing.T) {
	spec := loadFixture(t)
	bars := publicationBars()
	strategy := mustCompile(t, spec)
	series := strategy.Series(bars)

	for _, name := range []string{"rsi", "sma200"} {
		values, ok := series[name]
		if !ok {
			t.Fatalf("series %q is missing: %v", name, len(series))
		}
		if len(values) != len(bars) {
			t.Fatalf("series %q length = %d, want %d", name, len(values), len(bars))
		}
	}
	if !math.IsNaN(series["rsi"][13]) {
		t.Fatalf("rsi warm-up value = %v, want NaN", series["rsi"][13])
	}
	if math.IsNaN(series["rsi"][14]) {
		t.Fatalf("rsi[14] = NaN, want the first warm value")
	}
	if !math.IsNaN(series["sma200"][198]) {
		t.Fatalf("sma200 warm-up value = %v, want NaN", series["sma200"][198])
	}

	// The incremental path must agree with the independent series.
	incremental := mustCompile(t, spec)
	for i := range bars {
		incremental.prepare(bars, i)
		for name, values := range series {
			got, ok := incremental.inds[name].at(i)
			want := values[i]
			if !ok {
				if !math.IsNaN(want) {
					t.Fatalf("%s[%d]: incremental path has no value but series has %v", name, i, want)
				}
				continue
			}
			if math.IsNaN(want) || math.Abs(got-want) > 1e-12 {
				t.Fatalf("%s[%d]: incremental %.12f != series %.12f", name, i, got, want)
			}
		}
	}
}

// TestStrategySeriesDoesNotTouchState proves Series is side-effect free: the
// OnBar transcript before and after asking for the series is identical.
func TestStrategySeriesDoesNotTouchState(t *testing.T) {
	spec := loadFixture(t)
	bars := publicationBars()

	reference := mustCompile(t, spec)
	withSeries := mustCompile(t, spec)
	for i := range bars {
		want := reference.OnBar(bars, i)
		_ = withSeries.Series(bars[:i+1])
		got := withSeries.OnBar(bars, i)
		if got != want {
			t.Fatalf("bar %d: action with Series() interleaved = %v, want %v", i, got, want)
		}
	}
}

// TestStrategyValuesOnBar proves the per-bar Values snapshot agrees with the
// chart series and omits warm-up indicators.
func TestStrategyValuesOnBar(t *testing.T) {
	spec := loadFixture(t)
	bars := publicationBars()
	strategy := mustCompile(t, spec)
	series := strategy.Series(bars)

	if got := strategy.Values(bars, 10); len(got) != 0 {
		t.Fatalf("Values(warm-up bar 10) = %v, want an empty map", got)
	}
	values := strategy.Values(bars, 250)
	if len(values) != 2 {
		t.Fatalf("Values(bar 250) = %v, want both indicators", values)
	}
	for name, got := range values {
		want := series[name][250]
		if math.Abs(got-want) > 1e-12 {
			t.Fatalf("%s on bar 250 = %v, want %v", name, got, want)
		}
	}
	if got := strategy.Values(bars, len(bars)); len(got) != 0 {
		t.Fatalf("Values(out of range) = %v, want an empty map", got)
	}
}
