// Package rules turns a declarative Spec — indicators plus entry, exit and
// filter conditions — into a lookahead-free backtest.Strategy. A Spec is what
// the LLM sends instead of code: it is JSON-serializable, validated by Compile
// and reproducible (the normalized form is hashed into Result.Params).
//
// Signals are decided on the close of closed bars only: a bar with Partial set
// (the still forming candle of a live feed) is never used.
package rules

import (
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// Indicator kinds supported by the rules compiler.
const (
	KindRSIWilder    = "rsi_wilder"
	KindSMA          = "sma"
	KindEMA          = "ema"
	KindATR          = "atr"
	KindDonchianHigh = "donchian_high"
	KindDonchianLow  = "donchian_low"
	KindROC          = "roc"
)

// Indicator price sources.
const (
	SourceClose  = "close"
	SourceHigh   = "high"
	SourceLow    = "low"
	SourceVolume = "volume"
)

// Condition operators. cross_above / cross_below compare the condition on bar
// i with the same condition on bar i-1 and need at least two bars.
const (
	OpLT         = "<"
	OpLE         = "<="
	OpGT         = ">"
	OpGE         = ">="
	OpCrossAbove = "cross_above"
	OpCrossBelow = "cross_below"
)

// Strategy identity and Spec contract constants.
const (
	// ID is the strategy catalog id of a compiled rule strategy. It matches
	// backtest.StrategyRules so the catalog, the agent tool and the backtest
	// results all agree on one identifier.
	ID = backtest.StrategyRules
	// SideLong is the only supported side in iteration 1.
	SideLong = "long"
	// ParamSpecHash is the Result.Params key that carries the normalized Spec
	// hash (a float64 that fits exactly; see Strategy.Hash).
	ParamSpecHash = "specHash"
	// DefaultScale is the indicator multiplier used when Scale is zero.
	DefaultScale = 1
)

// Spec is one declarative strategy: named indicators, the entry and exit
// condition sets (all conditions of a set must hold) and optional entry
// filters. StopLossPct and TakeProfitPct are checked against the close of every
// bar while a position is open and are percentages of the signal-bar close.
//
// JSON keys are lower camel case; side defaults to "long" when omitted.
type Spec struct {
	Indicators    map[string]Indicator `json:"indicators,omitempty"`
	Entry         []Condition          `json:"entry"`
	Exit          []Condition          `json:"exit,omitempty"`
	Filters       []Condition          `json:"filters,omitempty"`
	Side          string               `json:"side,omitempty"`
	StopLossPct   float64              `json:"stopLossPct,omitempty"`
	TakeProfitPct float64              `json:"takeProfitPct,omitempty"`
}

// Indicator is one named indicator of a Spec.
//
// Kind is one of the Kind* constants. Period is the look-back window in bars.
// Source is the price series (close by default); it is used by sma, ema, rsi
// and roc and ignored by atr and the Donchian channel (they always use
// high/low/high-low). Scale multiplies the indicator value: Scale=0.01 turns
// the 0..100 RSI into the 0..1 scale used by the Finam publication example; a
// zero Scale means DefaultScale.
type Indicator struct {
	Kind   string  `json:"kind"`
	Period int     `json:"period"`
	Source string  `json:"source,omitempty"`
	Scale  float64 `json:"scale,omitempty"`
}

// Condition is one comparison: Left <Op> Right. Both operands are either a
// declared indicator name, the literal string "close" or a decimal number.
type Condition struct {
	Left  string `json:"left"`
	Op    string `json:"op"`
	Right string `json:"right"`
}

// clone returns a deep copy of the Spec so callers cannot mutate a compiled
// strategy through the maps or slices they passed to Compile.
func (s Spec) clone() Spec {
	out := s
	if s.Indicators != nil {
		out.Indicators = make(map[string]Indicator, len(s.Indicators))
		for name, ind := range s.Indicators {
			out.Indicators[name] = ind
		}
	}
	out.Entry = append([]Condition(nil), s.Entry...)
	out.Exit = append([]Condition(nil), s.Exit...)
	out.Filters = append([]Condition(nil), s.Filters...)
	return out
}
