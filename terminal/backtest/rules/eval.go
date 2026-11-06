package rules

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// hashMask keeps the Spec hash inside the 53 bits a float64 represents
// exactly, so Result.Params carries it losslessly.
const hashMask = 1<<53 - 1

// Limits of the optional risk fields.
const (
	maxStopLossPct   = 100
	maxTakeProfitPct = 10_000
)

// operandKind is the resolved kind of one condition operand.
type operandKind int

const (
	operandNumber operandKind = iota
	operandClose
	operandIndicator
)

// operand is one resolved side of a Condition.
type operand struct {
	kind   operandKind
	number float64
	ind    *indicator
}

// value returns the operand value on bar i. ok is false while the underlying
// indicator is still warming up.
func (op operand) value(bars []backtest.Bar, i int) (float64, bool) {
	switch op.kind {
	case operandNumber:
		return op.number, true
	case operandClose:
		if i < 0 || i >= len(bars) {
			return 0, false
		}
		return bars[i].Close, true
	default:
		if op.ind == nil {
			return 0, false
		}
		return op.ind.at(i)
	}
}

// compiledCondition is a validated Condition with its operands resolved.
type compiledCondition struct {
	left  operand
	right operand
	op    string
}

// eval evaluates the condition on bar i. defined is false before the
// indicators have warmed up (or before bar 1 for a cross); a caller treats an
// undefined condition as "not satisfied".
func (c compiledCondition) eval(bars []backtest.Bar, i int) (value, defined bool) {
	left, okLeft := c.left.value(bars, i)
	right, okRight := c.right.value(bars, i)
	if !okLeft || !okRight {
		return false, false
	}
	switch c.op {
	case OpLT:
		return left < right, true
	case OpLE:
		return left <= right, true
	case OpGT:
		return left > right, true
	case OpGE:
		return left >= right, true
	case OpCrossAbove, OpCrossBelow:
		if i < 1 {
			return false, false
		}
		prevLeft, okPrevLeft := c.left.value(bars, i-1)
		prevRight, okPrevRight := c.right.value(bars, i-1)
		if !okPrevLeft || !okPrevRight {
			return false, false
		}
		if c.op == OpCrossAbove {
			return prevLeft <= prevRight && left > right, true
		}
		return prevLeft >= prevRight && left < right, true
	}
	return false, false
}

// Strategy is a compiled rules.Spec. It implements backtest.Strategy: OnBar
// reads only bars[0..i], so the engine fills the decision at the next bar open
// without lookahead.
//
// The strategy tracks its own long position: entries are evaluated only while
// flat and exits only while long, and the optional stop-loss / take-profit are
// checked against the close of every bar while long. The engine may still skip
// an unfunded entry; in that edge case the tracked position can run ahead of
// the engine position until the next exit signal, which is why automated
// callers should size the backtest so that a single unit is affordable.
type Strategy struct {
	spec    Spec
	entry   []compiledCondition
	exit    []compiledCondition
	filters []compiledCondition
	inds    map[string]*indicator
	hash    uint64

	// Incremental indicator state for the bar series being replayed.
	barLen   int
	computed int

	inPosition bool
	entryClose float64
}

// Compile validates a Spec and turns it into a lookahead-free
// backtest.Strategy. Errors are prefixed with "rules: " and list the expected
// values, so a model can fix the Spec without guessing.
func Compile(spec Spec) (backtest.Strategy, error) {
	normalized, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	strategy := &Strategy{
		spec:     normalized,
		inds:     make(map[string]*indicator, len(normalized.Indicators)),
		computed: -1,
		barLen:   -1,
	}
	for name, definition := range normalized.Indicators {
		ind, err := newIndicator(definition)
		if err != nil {
			return nil, fmt.Errorf("rules: indicator %q: %w", name, err)
		}
		strategy.inds[name] = ind
	}
	if strategy.entry, err = compileConditions("entry", normalized.Entry, strategy.inds); err != nil {
		return nil, err
	}
	if strategy.exit, err = compileConditions("exit", normalized.Exit, strategy.inds); err != nil {
		return nil, err
	}
	if strategy.filters, err = compileConditions("filter", normalized.Filters, strategy.inds); err != nil {
		return nil, err
	}
	strategy.hash = specHash(normalized)
	return strategy, nil
}

// normalizeSpec validates the structural rules of a Spec and returns the
// canonical form: default side, default sources and scales filled in, names and
// operators trimmed. The canonical form is what gets hashed, so an omitted
// field and its explicit default hash identically.
func normalizeSpec(spec Spec) (Spec, error) {
	out := spec.clone()
	out.Side = strings.ToLower(strings.TrimSpace(spec.Side))
	if out.Side == "" {
		out.Side = SideLong
	}
	if out.Side != SideLong {
		return Spec{}, fmt.Errorf("rules: unsupported side %q: only %q is supported", spec.Side, SideLong)
	}
	if len(out.Entry) == 0 {
		return Spec{}, fmt.Errorf("rules: entry conditions are required: set at least one condition in \"entry\"")
	}
	if len(out.Exit) == 0 && out.StopLossPct == 0 && out.TakeProfitPct == 0 {
		return Spec{}, fmt.Errorf("rules: exit conditions are required: set \"exit\" or stopLossPct/takeProfitPct")
	}
	if err := validatePct("stopLossPct", out.StopLossPct, maxStopLossPct); err != nil {
		return Spec{}, err
	}
	if err := validatePct("takeProfitPct", out.TakeProfitPct, maxTakeProfitPct); err != nil {
		return Spec{}, err
	}
	if out.Indicators != nil {
		normalized := make(map[string]Indicator, len(out.Indicators))
		for name, definition := range out.Indicators {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				return Spec{}, fmt.Errorf("rules: indicator name must not be empty")
			}
			if strings.EqualFold(trimmed, "close") {
				return Spec{}, fmt.Errorf("rules: indicator name %q is reserved for the close price", trimmed)
			}
			if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
				return Spec{}, fmt.Errorf("rules: indicator name %q is reserved for numbers", trimmed)
			}
			definition.Kind = strings.ToLower(strings.TrimSpace(definition.Kind))
			definition.Source = strings.ToLower(strings.TrimSpace(definition.Source))
			if definition.Source == "" {
				definition.Source = SourceClose
			}
			if definition.Scale == 0 {
				definition.Scale = DefaultScale
			}
			normalized[trimmed] = definition
		}
		out.Indicators = normalized
	}
	out.Entry = normalizeConditions(out.Entry)
	out.Exit = normalizeConditions(out.Exit)
	out.Filters = normalizeConditions(out.Filters)
	return out, nil
}

// normalizeConditions trims the string fields of a condition slice.
func normalizeConditions(conditions []Condition) []Condition {
	if conditions == nil {
		return nil
	}
	out := make([]Condition, len(conditions))
	for i, condition := range conditions {
		out[i] = Condition{
			Left:  strings.TrimSpace(condition.Left),
			Op:    strings.ToLower(strings.TrimSpace(condition.Op)),
			Right: strings.TrimSpace(condition.Right),
		}
	}
	return out
}

// validatePct checks one optional percentage field: 0 disables it, everything
// else must be finite and inside (0, max).
func validatePct(name string, value, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("rules: %s must be a finite number, got %v", name, value)
	}
	if value < 0 || (max > 0 && value >= max) {
		return fmt.Errorf("rules: %s %v is outside the supported range [0, %v)", name, value, max)
	}
	return nil
}

// compileConditions validates and resolves one condition set.
func compileConditions(role string, conditions []Condition, inds map[string]*indicator) ([]compiledCondition, error) {
	if len(conditions) == 0 {
		return nil, nil
	}
	out := make([]compiledCondition, 0, len(conditions))
	for i, condition := range conditions {
		op := strings.ToLower(strings.TrimSpace(condition.Op))
		switch op {
		case OpLT, OpLE, OpGT, OpGE, OpCrossAbove, OpCrossBelow:
		default:
			return nil, fmt.Errorf("rules: %s condition %d: unknown operator %q (want %s)", role, i+1, condition.Op,
				strings.Join([]string{OpLT, OpLE, OpGT, OpGE, OpCrossAbove, OpCrossBelow}, "|"))
		}
		left, err := compileOperand(condition.Left, inds)
		if err != nil {
			return nil, fmt.Errorf("rules: %s condition %d: left: %w", role, i+1, err)
		}
		right, err := compileOperand(condition.Right, inds)
		if err != nil {
			return nil, fmt.Errorf("rules: %s condition %d: right: %w", role, i+1, err)
		}
		out = append(out, compiledCondition{left: left, right: right, op: op})
	}
	return out, nil
}

// compileOperand resolves one operand token: an indicator name, "close" or a
// decimal number. The order matters: "close" and numbers are reserved names,
// which normalizeSpec enforces on the indicator map.
func compileOperand(raw string, inds map[string]*indicator) (operand, error) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return operand{}, fmt.Errorf("operand is empty (want an indicator name, \"close\" or a number)")
	}
	if strings.EqualFold(token, "close") {
		return operand{kind: operandClose}, nil
	}
	if value, err := strconv.ParseFloat(token, 64); err == nil {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return operand{}, fmt.Errorf("operand %q is not a finite number", raw)
		}
		return operand{kind: operandNumber, number: value}, nil
	}
	if ind, ok := inds[token]; ok {
		return operand{kind: operandIndicator, ind: ind}, nil
	}
	return operand{}, fmt.Errorf("unknown operand %q (declare it in \"indicators\" or use \"close\" or a number)", raw)
}

// ID returns the strategy catalog id.
func (s *Strategy) ID() string { return ID }

// Params returns the frozen Result.Params map: the normalized Spec hash.
func (s *Strategy) Params() map[string]float64 {
	return map[string]float64{ParamSpecHash: float64(s.hash)}
}

// Hash returns the normalized Spec hash (an FNV-1a of the canonical JSON).
func (s *Strategy) Hash() uint64 { return s.hash }

// Spec returns the normalized Spec the strategy was compiled from.
func (s *Strategy) Spec() Spec { return s.spec.clone() }

// reset drops the computed indicator state when a new bar series is replayed.
func (s *Strategy) reset() {
	for _, ind := range s.inds {
		ind.reset()
	}
	s.computed = -1
	s.inPosition = false
	s.entryClose = 0
}

// prepare advances every indicator through bar i. OnBar expects the same bar
// slice for the whole replay (the engine contract); a different series length
// restarts the computation.
func (s *Strategy) prepare(bars []backtest.Bar, i int) {
	if len(bars) != s.barLen {
		s.reset()
		s.barLen = len(bars)
	}
	if i <= s.computed {
		return
	}
	if i >= len(bars) {
		i = len(bars) - 1
	}
	for start := s.computed + 1; start <= i; start++ {
		for _, ind := range s.inds {
			ind.advance(bars, start)
		}
	}
	s.computed = i
}

// OnBar returns the decision taken on the close of bar i. A partial (still
// forming) bar never produces a decision.
func (s *Strategy) OnBar(bars []backtest.Bar, i int) backtest.Action {
	if i < 0 || i >= len(bars) || bars[i].Partial {
		return backtest.Hold
	}
	s.prepare(bars, i)
	if s.inPosition {
		if s.stopHit(bars[i].Close) {
			s.inPosition = false
			s.entryClose = 0
			return backtest.Sell
		}
		if evalAll(s.exit, bars, i) {
			s.inPosition = false
			s.entryClose = 0
			return backtest.Sell
		}
		return backtest.Hold
	}
	if !evalAll(s.filters, bars, i) {
		return backtest.Hold
	}
	if !evalAll(s.entry, bars, i) {
		return backtest.Hold
	}
	s.inPosition = true
	s.entryClose = bars[i].Close
	return backtest.Buy
}

// stopHit reports whether the close of the current bar crossed the configured
// stop-loss or take-profit of the signal-bar close.
func (s *Strategy) stopHit(close float64) bool {
	if s.entryClose <= 0 {
		return false
	}
	if pct := s.spec.StopLossPct; pct > 0 && close <= s.entryClose*(1-pct/100) {
		return true
	}
	if pct := s.spec.TakeProfitPct; pct > 0 && close >= s.entryClose*(1+pct/100) {
		return true
	}
	return false
}

// evalAll reports whether every condition of the set is defined and true. An
// empty set is satisfied.
func evalAll(conditions []compiledCondition, bars []backtest.Bar, i int) bool {
	for _, condition := range conditions {
		value, defined := condition.eval(bars, i)
		if !defined || !value {
			return false
		}
	}
	return true
}

// specHash hashes the canonical JSON of a normalized Spec. encoding/json sorts
// map keys, so the hash is stable across runs and map iteration order.
func specHash(spec Spec) uint64 {
	raw, err := json.Marshal(spec)
	if err != nil {
		// The Spec contains only plain maps, slices and numbers.
		panic("rules: cannot marshal a normalized spec: " + err.Error())
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write(raw)
	return hasher.Sum64() & hashMask
}
