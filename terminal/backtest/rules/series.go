package rules

import (
	"math"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// Series computes every indicator of the Spec over the given bars and returns
// the series aligned with bars: index i is the value on bar i and NaN marks the
// warm-up window. It uses fresh indicator instances, so it neither reads nor
// changes the incremental state of OnBar — a strategy can be replayed and then
// asked for its series (or the other way round).
func (s *Strategy) Series(bars []backtest.Bar) map[string][]float64 {
	out := make(map[string][]float64, len(s.spec.Indicators))
	for name, definition := range s.spec.Indicators {
		ind, err := newIndicator(definition)
		if err != nil {
			// Compile validated the Spec already; this cannot happen.
			continue
		}
		values := make([]float64, len(bars))
		for i := range bars {
			ind.advance(bars, i)
			value, ok := ind.at(i)
			if !ok {
				value = math.NaN()
			}
			values[i] = value
		}
		out[name] = values
	}
	return out
}

// Values returns the defined indicator values on bar i, keyed by indicator
// name (warm-up indicators are omitted). It is the per-bar form used by the bot
// journal; for a whole chart use Series. Cost is O(bars * indicators) per call,
// which is fine for one bar; callers that need every bar should build Series
// once.
func (s *Strategy) Values(bars []backtest.Bar, i int) map[string]float64 {
	out := make(map[string]float64, len(s.spec.Indicators))
	if i < 0 || i >= len(bars) {
		return out
	}
	for name, definition := range s.spec.Indicators {
		ind, err := newIndicator(definition)
		if err != nil {
			continue
		}
		for j := 0; j <= i; j++ {
			ind.advance(bars, j)
		}
		if value, ok := ind.at(i); ok {
			out[name] = value
		}
	}
	return out
}
