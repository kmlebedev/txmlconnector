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

// StrategyRules is the catalog id of the declarative rule strategy. It is built
// from a rules.Spec by rules.Compile, not from numeric Params; the catalog entry
// exists so the UI and compare_strategies see one uniform strategy list.
const StrategyRules = "rules"

// strategyDef registers one strategy in the catalog.
type strategyDef struct {
	info  StrategyInfo
	build func(params map[string]float64) (Strategy, error)
}

// fixed adapts a strategy constructor that cannot fail to the registry shape.
func fixed(build func(params map[string]float64) Strategy) func(params map[string]float64) (Strategy, error) {
	return func(params map[string]float64) (Strategy, error) { return build(params), nil }
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
	{info: smaCrossInfo, build: fixed(newSMACross)},
	{info: rsiReversionInfo, build: fixed(newRSIReversion)},
	{info: donchianBreakoutInfo, build: fixed(newDonchianBreakout)},
	{info: momentumInfo, build: fixed(newMomentum)},
	{info: rulesInfo, build: newRulesStrategy},
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
			return def.build(params)
		}
	}
	return nil, fmt.Errorf("backtest: unknown strategy %q", id)
}

// newRulesStrategy is the catalog builder of the declarative rule strategy. A
// rule strategy needs a JSON rules.Spec (see rules.Compile) and cannot be
// described by the numeric Params map, so the catalog builder returns a valid
// placeholder that never signals: compare_strategies and the UI keep one
// uniform catalog, and a direct run with the "rules" id is rejected by the
// agent's run_backtest tool with a pointer to backtest_rules.
func newRulesStrategy(params map[string]float64) (Strategy, error) {
	return rulesPlaceholder{}, nil
}

// rulesInfo is the catalog entry of the declarative rule strategy.
var rulesInfo = StrategyInfo{
	ID:   StrategyRules,
	Name: "Правила из промпта",
	Description: "Декларативные правила из JSON вместо кода: индикаторы (RSI Уайлдера, SMA, EMA, ATR, канал Дончиана, ROC) " +
		"и условия входа, выхода и фильтры. Собирается из rules.Spec через rules.Compile; " +
		"в бэктесте запускается инструментом backtest_rules, а не run_backtest.",
}

// rulesPlaceholder is the catalog stand-in of a rule strategy: it is valid and
// never signals because a Spec was not supplied.
type rulesPlaceholder struct{}

func (rulesPlaceholder) ID() string                 { return StrategyRules }
func (rulesPlaceholder) Params() map[string]float64 { return map[string]float64{} }
func (rulesPlaceholder) OnBar([]Bar, int) Action    { return Hold }
