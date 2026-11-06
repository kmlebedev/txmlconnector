package rules

import (
	"fmt"
	"math"
	"strings"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// Indicator period bounds. The upper bound keeps a single backtest replay
// bounded; a period above the history length simply produces no values.
const (
	minIndicatorPeriod = 1
	maxIndicatorPeriod = 10_000
)

// supportedKinds is the exhaustive list of indicator kinds, used in errors so a
// model can correct the Spec without guessing.
var supportedKinds = []string{
	KindRSIWilder, KindSMA, KindEMA, KindATR, KindDonchianHigh, KindDonchianLow, KindROC,
}

// supportedSources is the exhaustive list of price sources.
var supportedSources = []string{SourceClose, SourceHigh, SourceLow, SourceVolume}

// indicator is one compiled indicator. It computes its series incrementally
// from the bars it is given, so the value at index i is a pure function of
// bars[0..i] (the no-lookahead invariant). Warm-up values are NaN and are
// reported as "not defined" by at.
type indicator struct {
	kind   string
	period int
	source string
	scale  float64

	values  []float64 // one value per bar; NaN during warm-up
	samples []float64 // source samples (sma, ema, rsi, roc)

	// Rolling state, advanced one bar at a time.
	sum      float64 // sma window sum
	ema      float64
	emaReady bool
	gain     float64 // rsi seed accumulation
	loss     float64
	avgGain  float64
	avgLoss  float64
	rsiReady bool
	atr      float64
	atrReady bool
	trSum    float64 // atr seed accumulation
}

// newIndicator validates one Indicator and returns its compiled form.
func newIndicator(in Indicator) (*indicator, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if !containsString(supportedKinds, kind) {
		return nil, fmt.Errorf("unknown indicator kind %q (want %s)", in.Kind, strings.Join(supportedKinds, "|"))
	}
	if in.Period < minIndicatorPeriod || in.Period > maxIndicatorPeriod {
		return nil, fmt.Errorf("period %d is outside the supported range %d..%d",
			in.Period, minIndicatorPeriod, maxIndicatorPeriod)
	}
	source := strings.ToLower(strings.TrimSpace(in.Source))
	if source == "" {
		source = SourceClose
	}
	if !containsString(supportedSources, source) {
		return nil, fmt.Errorf("unknown source %q (want %s)", in.Source, strings.Join(supportedSources, "|"))
	}
	scale := in.Scale
	switch {
	case math.IsNaN(scale) || math.IsInf(scale, 0):
		return nil, fmt.Errorf("scale %v is not a finite number", in.Scale)
	case scale < 0:
		return nil, fmt.Errorf("scale %v must be positive", in.Scale)
	case scale == 0:
		scale = DefaultScale
	}
	return &indicator{kind: kind, period: in.Period, source: source, scale: scale}, nil
}

// reset drops the computed series and the rolling state (a new bar series is
// being replayed through the same strategy).
func (ind *indicator) reset() {
	ind.values = nil
	ind.samples = nil
	ind.sum = 0
	ind.ema = 0
	ind.emaReady = false
	ind.gain = 0
	ind.loss = 0
	ind.avgGain = 0
	ind.avgLoss = 0
	ind.rsiReady = false
	ind.atr = 0
	ind.atrReady = false
	ind.trSum = 0
}

// usesSource reports whether the kind reads the configured price source.
func (ind *indicator) usesSource() bool {
	switch ind.kind {
	case KindSMA, KindEMA, KindRSIWilder, KindROC:
		return true
	}
	return false
}

// sourceValue returns one price of the configured source series.
func (ind *indicator) sourceValue(bar backtest.Bar) float64 {
	switch ind.source {
	case SourceHigh:
		return bar.High
	case SourceLow:
		return bar.Low
	case SourceVolume:
		return float64(bar.Volume)
	default:
		return bar.Close
	}
}

// advance appends the value for bar i. Bars must be fed in order: index i is
// computed from bars[i] and the state of bars[0..i-1] only.
func (ind *indicator) advance(bars []backtest.Bar, i int) {
	ind.values = append(ind.values, math.NaN())
	switch ind.kind {
	case KindSMA:
		value := ind.sourceValue(bars[i])
		ind.samples = append(ind.samples, value)
		ind.sum += value
		if i >= ind.period {
			ind.sum -= ind.samples[i-ind.period]
		}
		if i >= ind.period-1 {
			ind.values[i] = ind.scale * ind.sum / float64(ind.period)
		}
	case KindEMA:
		value := ind.sourceValue(bars[i])
		ind.samples = append(ind.samples, value)
		switch {
		case i < ind.period-1:
			// Warm-up: no value yet.
		case i == ind.period-1:
			var total float64
			for _, sample := range ind.samples[:ind.period] {
				total += sample
			}
			ind.ema = total / float64(ind.period)
			ind.emaReady = true
			ind.values[i] = ind.scale * ind.ema
		default:
			alpha := 2 / float64(ind.period+1)
			ind.ema += alpha * (value - ind.ema)
			ind.values[i] = ind.scale * ind.ema
		}
	case KindRSIWilder:
		value := ind.sourceValue(bars[i])
		ind.samples = append(ind.samples, value)
		if i == 0 {
			return
		}
		up, down := splitChange(value - ind.samples[i-1])
		if ind.rsiReady {
			ind.avgGain = (ind.avgGain*float64(ind.period-1) + up) / float64(ind.period)
			ind.avgLoss = (ind.avgLoss*float64(ind.period-1) + down) / float64(ind.period)
			ind.values[i] = ind.scale * rsiFromAverages(ind.avgGain, ind.avgLoss)
			return
		}
		ind.gain += up
		ind.loss += down
		if i == ind.period {
			// Wilder seed: simple mean of the first period changes.
			ind.avgGain = ind.gain / float64(ind.period)
			ind.avgLoss = ind.loss / float64(ind.period)
			ind.rsiReady = true
			ind.values[i] = ind.scale * rsiFromAverages(ind.avgGain, ind.avgLoss)
		}
	case KindATR:
		tr := trueRange(bars, i)
		if i == 0 {
			// TR(0) has no previous close: it is not a complete Wilder sample.
			return
		}
		if i <= ind.period {
			ind.trSum += tr
			if i == ind.period {
				ind.atr = ind.trSum / float64(ind.period)
				ind.atrReady = true
				ind.values[i] = ind.scale * ind.atr
			}
			return
		}
		ind.atr = (ind.atr*float64(ind.period-1) + tr) / float64(ind.period)
		ind.values[i] = ind.scale * ind.atr
	case KindDonchianHigh, KindDonchianLow:
		if i < ind.period {
			return
		}
		// The channel of bar i is built from the previous period bars only
		// (bars[i-period..i-1]); the current bar is never part of it.
		if ind.kind == KindDonchianHigh {
			highest := bars[i-ind.period].High
			for j := i - ind.period + 1; j < i; j++ {
				if bars[j].High > highest {
					highest = bars[j].High
				}
			}
			ind.values[i] = ind.scale * highest
			return
		}
		lowest := bars[i-ind.period].Low
		for j := i - ind.period + 1; j < i; j++ {
			if bars[j].Low < lowest {
				lowest = bars[j].Low
			}
		}
		ind.values[i] = ind.scale * lowest
	case KindROC:
		value := ind.sourceValue(bars[i])
		ind.samples = append(ind.samples, value)
		if i < ind.period {
			return
		}
		base := ind.samples[i-ind.period]
		if base == 0 {
			return
		}
		ind.values[i] = ind.scale * (value/base - 1) * 100
	}
}

// at returns the indicator value on bar i and whether it is defined (the
// warm-up window is not).
func (ind *indicator) at(i int) (float64, bool) {
	if i < 0 || i >= len(ind.values) {
		return 0, false
	}
	value := ind.values[i]
	if math.IsNaN(value) {
		return 0, false
	}
	return value, true
}

// splitChange splits one close-to-close change into the up and down parts used
// by Wilder's RSI.
func splitChange(change float64) (up, down float64) {
	if change > 0 {
		return change, 0
	}
	return 0, -change
}

// rsiFromAverages converts Wilder averages into the 0..100 RSI. A flat series
// (no gains and no losses) is 50 by convention; no losses at all is 100.
func rsiFromAverages(avgGain, avgLoss float64) float64 {
	switch {
	case avgLoss == 0 && avgGain == 0:
		return 50
	case avgLoss == 0:
		return 100
	}
	return 100 - 100/(1+avgGain/avgLoss)
}

// trueRange returns the Wilder true range of bar i: the greatest of the bar
// range and the two gaps to the previous close.
func trueRange(bars []backtest.Bar, i int) float64 {
	tr := bars[i].High - bars[i].Low
	if i == 0 {
		return tr
	}
	prevClose := bars[i-1].Close
	if gap := math.Abs(bars[i].High - prevClose); gap > tr {
		tr = gap
	}
	if gap := math.Abs(bars[i].Low - prevClose); gap > tr {
		tr = gap
	}
	return tr
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
