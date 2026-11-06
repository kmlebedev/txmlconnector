package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// errBadArgument marks a tool argument that cannot be used as-is: a fractional
// or overflowing integer, a non-numeric value or a number outside the allowed
// range. The tool loop reports it to the model instead of silently truncating
// the value (review finding R9).
var errBadArgument = errors.New("agent: bad tool argument")

// Bounds of the integer tool arguments. Range violations are argument errors,
// not silent clamps: the model has to correct the call.
const (
	minToolPeriodSec = 1
	maxToolPeriodSec = 86_400
	minToolCount     = 1
	minToolQty       = 1
	maxToolQty       = 1_000_000_000
	// maxExactFloatInt is the largest integer a float64 represents exactly;
	// beyond it a float-to-int conversion cannot be trusted.
	maxExactFloatInt = 1 << 53
)

// minTakeProfitPct is the floor of a take-profit target: a near-zero target
// would exit a position immediately and only pay the commission.
const minTakeProfitPct = 0.1

// errNotWholeNumber is the cause of errBadArgument for a value that is not an
// integer (fractional, NaN/Inf, text or out of the exact float range).
var errNotWholeNumber = errors.New("expected a whole number")

// clampAutonomy applies the engine risk limits to an autonomy config supplied
// by the model. The engine limits are ceilings: the config may be stricter than
// them but never looser, so a prompt can not disable the stop-loss, daily-loss
// or position guard rails (review finding R2). Take-profit is a profit target
// rather than a risk limit, so it is not capped — only floored at
// minTakeProfitPct. The budget may not exceed the free cash of the paper
// account; cash <= 0 means "unknown" (no broker configured) and disables the
// budget clamp.
//
// prepareAutonomy fills the config defaults before calling this function, so an
// unset (<= 0) value is left untouched here. Every reduction is returned as a
// human-readable warning for the autonomy log.
func clampAutonomy(cfg AutonomyConfig, risk RiskLimits, cash float64) (AutonomyConfig, []string) {
	var warnings []string
	ceiling := func(label string, value, max float64) float64 {
		if value <= 0 || max <= 0 || value <= max {
			return value
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s %.2f%% превышает лимит движка %.2f%% — ограничен лимитом движка", label, value, max))
		return max
	}
	cfg.MaxPositionPct = ceiling("доля позиции", cfg.MaxPositionPct, risk.MaxPositionPct)
	cfg.StopLossPct = ceiling("стоп-лосс", cfg.StopLossPct, risk.StopLossPct)
	cfg.DailyLossPct = ceiling("дневной убыток", cfg.DailyLossPct, risk.DailyLossPct)
	if cfg.TakeProfitPct < minTakeProfitPct {
		cfg.TakeProfitPct = minTakeProfitPct
	}
	if cash > 0 && cfg.Budget > cash {
		warnings = append(warnings, fmt.Sprintf(
			"бюджет %.0f ₽ превышает свободный кэш %.0f ₽ — уменьшен до %.0f ₽", cfg.Budget, cash, cash))
		cfg.Budget = cash
	}
	return cfg, warnings
}

// argIntStrict reads an integer tool argument without lossy conversions. It
// accepts JSON integers (json.Number), Go integers, whole floats and integer
// strings; fractional numbers, NaN/Inf, overflow and non-numeric text are
// reported as errBadArgument so the model can correct the call. A missing or
// null argument returns def unchanged; every provided value must be inside
// [min, max].
func argIntStrict(args map[string]any, key string, def, min, max int64) (int64, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return def, nil
	}
	n, err := parseIntArg(v)
	if err != nil {
		return 0, fmt.Errorf("%w: %s=%v: %v (allowed range %d..%d)", errBadArgument, key, v, err, min, max)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("%w: %s=%d is outside the allowed range %d..%d", errBadArgument, key, n, min, max)
	}
	return n, nil
}

// parseIntArg converts one raw tool argument to int64 without truncation.
func parseIntArg(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case float64:
		return floatToInt(x)
	case float32:
		return floatToInt(float64(x))
	case json.Number:
		// JSON integers keep their exact decimal text; exponent forms (1e3) and
		// a trailing ".0" fall through to the exact-float path.
		if n, err := strconv.ParseInt(x.String(), 10, 64); err == nil {
			return n, nil
		}
		f, err := x.Float64()
		if err != nil {
			return 0, errNotWholeNumber
		}
		return floatToInt(f)
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return 0, errNotWholeNumber
		}
		return n, nil
	default:
		return 0, errNotWholeNumber
	}
}

// floatToInt converts an exactly integral float64 to int64. Values above
// maxExactFloatInt are rejected: past 2^53 the float representation no longer
// maps one-to-one onto integers.
func floatToInt(f float64) (int64, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, errNotWholeNumber
	}
	if f < -maxExactFloatInt || f > maxExactFloatInt {
		return 0, fmt.Errorf("integer %v is too large", f)
	}
	return int64(f), nil
}
