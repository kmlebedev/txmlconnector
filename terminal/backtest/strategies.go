package backtest

import (
	"fmt"
	"math"
)

// paramSpec is the internal description of one tunable strategy parameter. It
// mirrors the frozen Param contract and adds normalization (defaulting and
// clamping) used by NewStrategy.
type paramSpec struct {
	Key     string
	Title   string
	Default float64
	Min     float64
	Max     float64
	Step    float64
}

// info converts the spec into the public Param DTO.
func (p paramSpec) info() Param {
	return Param{
		Key:     p.Key,
		Title:   p.Title,
		Default: p.Default,
		Min:     p.Min,
		Max:     p.Max,
		Step:    p.Step,
	}
}

// value resolves a raw caller value: a missing or non-finite parameter falls
// back to the default, everything else is clamped into [Min, Max].
func (p paramSpec) value(raw map[string]float64) float64 {
	v, ok := raw[p.Key]
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return p.Default
	}
	return clampFloat(v, p.Min, p.Max)
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// intParam converts a normalized float parameter to an int (periods are whole
// bars).
func intParam(v float64) int {
	return int(math.Round(v))
}

// paramInfos converts a list of specs into the public catalog description.
func paramInfos(specs ...paramSpec) []Param {
	out := make([]Param, len(specs))
	for i, spec := range specs {
		out[i] = spec.info()
	}
	return out
}

// strategyDef registers one strategy in the catalog.
type strategyDef struct {
	info  StrategyInfo
	build func(params map[string]float64) Strategy
}

// clone returns a deep-enough copy so callers cannot mutate the registry.
func (d strategyDef) clone() StrategyInfo {
	out := d.info
	out.Params = append([]Param(nil), d.info.Params...)
	return out
}

// strategyRegistry is the ordered strategy catalog. It is read-only after
// package initialization.
var strategyRegistry = []strategyDef{
	{info: smaCrossInfo, build: newSMACross},
	{info: rsiReversionInfo, build: newRSIReversion},
	{info: donchianBreakoutInfo, build: newDonchianBreakout},
	{info: momentumInfo, build: newMomentum},
}

// Strategies returns the strategy catalog for the UI and the agent. The
// returned slice is a copy: callers cannot corrupt the registry.
func Strategies() []StrategyInfo {
	out := make([]StrategyInfo, len(strategyRegistry))
	for i, def := range strategyRegistry {
		out[i] = def.clone()
	}
	return out
}

// NewStrategy validates params and builds an incremental, lookahead-free
// strategy. Missing or non-finite params fall back to the documented defaults,
// out-of-range values are clamped, and dependent params are kept consistent
// (fast < slow, oversold < overbought). Params() of the returned strategy
// exposes the normalized values.
func NewStrategy(id string, params map[string]float64) (Strategy, error) {
	for _, def := range strategyRegistry {
		if def.info.ID == id {
			return def.build(params), nil
		}
	}
	return nil, fmt.Errorf("backtest: unknown strategy %q", id)
}
