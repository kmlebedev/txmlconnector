package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// TestClampAutonomyNeverLoosensRisk is the R2 regression test: the engine risk
// limits are ceilings, so an autonomy config supplied by the model can be
// stricter but never looser. Take-profit is a target rather than a risk limit:
// the plan only floors it at 0.1%.
func TestClampAutonomyNeverLoosensRisk(t *testing.T) {
	risk := RiskLimits{
		MaxPositionPct:   25,
		MaxOrderPct:      20,
		StopLossPct:      2,
		TakeProfitPct:    5,
		DailyLossPct:     3,
		MaxTradesPerHour: 30,
	}
	loose := AutonomyConfig{
		Symbol:         "TQBR:SBER",
		Budget:         100_000,
		MaxPositionPct: 100,
		StopLossPct:    99,
		DailyLossPct:   100,
		TakeProfitPct:  5,
	}
	got, warnings := clampAutonomy(loose, risk, 1_000_000)
	if got.StopLossPct != risk.StopLossPct {
		t.Errorf("StopLossPct = %v, want the engine ceiling %v", got.StopLossPct, risk.StopLossPct)
	}
	if got.DailyLossPct != risk.DailyLossPct {
		t.Errorf("DailyLossPct = %v, want the engine ceiling %v", got.DailyLossPct, risk.DailyLossPct)
	}
	if got.MaxPositionPct != risk.MaxPositionPct {
		t.Errorf("MaxPositionPct = %v, want the engine ceiling %v", got.MaxPositionPct, risk.MaxPositionPct)
	}
	if got.TakeProfitPct != 5 {
		t.Errorf("TakeProfitPct = %v, want the user target 5 kept as-is", got.TakeProfitPct)
	}
	if len(warnings) != 3 {
		t.Errorf("warnings = %v, want 3 clamped risk fields", warnings)
	}

	// A config stricter than the engine limits must survive untouched.
	stricter := AutonomyConfig{MaxPositionPct: 10, StopLossPct: 1, DailyLossPct: 1.5, TakeProfitPct: 8}
	got, warnings = clampAutonomy(stricter, risk, 1_000_000)
	if got.MaxPositionPct != 10 || got.StopLossPct != 1 || got.DailyLossPct != 1.5 || got.TakeProfitPct != 8 {
		t.Errorf("stricter config was modified: %+v", got)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings for a stricter config = %v, want none", warnings)
	}

	// The take-profit floor keeps a degenerate target usable.
	floored, _ := clampAutonomy(AutonomyConfig{TakeProfitPct: 0.01}, risk, 0)
	if floored.TakeProfitPct != 0.1 {
		t.Errorf("TakeProfitPct floor = %v, want 0.1", floored.TakeProfitPct)
	}
}

// TestClampAutonomyBudgetByCash checks that the model cannot allocate more
// money than the paper account actually holds (R2).
func TestClampAutonomyBudgetByCash(t *testing.T) {
	risk := RiskLimits{MaxPositionPct: 25, StopLossPct: 2, TakeProfitPct: 5, DailyLossPct: 3}
	got, warnings := clampAutonomy(AutonomyConfig{Budget: 1e12}, risk, 1e6)
	if got.Budget != 1e6 {
		t.Errorf("Budget = %v, want the free cash 1e6", got.Budget)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "кэш") {
		t.Errorf("warnings = %v, want one cash warning", warnings)
	}

	// A budget inside the free cash and an unknown cash balance are kept.
	small, warnings := clampAutonomy(AutonomyConfig{Budget: 1_000}, risk, 1e6)
	if small.Budget != 1_000 || len(warnings) != 0 {
		t.Errorf("small budget = %+v warnings = %v, want unchanged", small, warnings)
	}
	unknown, warnings := clampAutonomy(AutonomyConfig{Budget: 1e12}, risk, 0)
	if unknown.Budget != 1e12 || len(warnings) != 0 {
		t.Errorf("budget with unknown cash = %+v warnings = %v, want unchanged", unknown, warnings)
	}
}

// TestArgIntStrictRejectsFractionAndOverflow is the R9 regression test: an
// integer tool argument must never be silently truncated or wrapped.
func TestArgIntStrictRejectsFractionAndOverflow(t *testing.T) {
	bad := []struct {
		name  string
		value any
	}{
		{"fraction", 2.7},
		{"json fraction", json.Number("2.7")},
		{"overflow", 1e30},
		{"json overflow", json.Number("1e30")},
		{"nan", math.NaN()},
		{"inf", math.Inf(1)},
		{"text", "abc"},
		{"empty text", ""},
		{"bool", true},
	}
	for _, tc := range bad {
		n, err := argIntStrict(map[string]any{"qty": tc.value}, "qty", 7, 1, 1_000)
		if !errors.Is(err, errBadArgument) {
			t.Errorf("%s: error = %v, want errBadArgument", tc.name, err)
		}
		if n != 0 {
			t.Errorf("%s: value = %d, want 0 alongside the error", tc.name, n)
		}
	}

	good := []struct {
		name  string
		value any
		want  int64
	}{
		{"go int", 300, 300},
		{"go int64", int64(300), 300},
		{"float", 300.0, 300},
		{"json integer", json.Number("300"), 300},
		{"json fraction with zero scale", json.Number("300.0"), 300},
		{"numeric string", " 300 ", 300},
	}
	for _, tc := range good {
		n, err := argIntStrict(map[string]any{"periodSec": tc.value}, "periodSec", 0, 1, 86400)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
			continue
		}
		if n != tc.want {
			t.Errorf("%s: value = %d, want %d", tc.name, n, tc.want)
		}
	}

	// Missing and null arguments fall back to the default.
	for _, args := range []map[string]any{{}, {"periodSec": nil}} {
		n, err := argIntStrict(args, "periodSec", 300, 1, 86400)
		if err != nil || n != 300 {
			t.Errorf("args %v: value = %d error = %v, want the default 300", args, n, err)
		}
	}

	// Out-of-range integers are argument errors, not silent clamps.
	for _, value := range []any{0, -1, 86_401, int64(math.MaxInt64)} {
		n, err := argIntStrict(map[string]any{"periodSec": value}, "periodSec", 0, 1, 86400)
		if !errors.Is(err, errBadArgument) || n != 0 {
			t.Errorf("value %v: value = %d error = %v, want errBadArgument", value, n, err)
		}
	}
}

// TestPlaceOrderToolRejectsFractionalQty is the R9 regression test at the tool
// level: a fractional qty must fail the tool call before the broker sees it.
func TestPlaceOrderToolRejectsFractionalQty(t *testing.T) {
	e, _, broker := newTestEngine(t)
	call, _ := e.executeToolCall(context.Background(), "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 2.7, "type": "market",
	})
	if call.Error == "" || !strings.Contains(call.Error, "qty") {
		t.Fatalf("fractional qty error = %q, want a tool argument error mentioning qty", call.Error)
	}
	if !strings.Contains(call.Error, errBadArgument.Error()) {
		t.Errorf("fractional qty error = %q, want it to wrap errBadArgument", call.Error)
	}
	if placed := broker.placedOrders(); len(placed) != 0 {
		t.Fatalf("broker received %d orders, want 0 for a fractional qty", len(placed))
	}
}

// TestToolsRejectNonIntegerArguments covers every integer tool argument named
// by the plan: qty, count and periodSec.
func TestToolsRejectNonIntegerArguments(t *testing.T) {
	e, _, _ := newTestEngine(t)
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"get_history", map[string]any{"symbol": "SBER", "periodSec": 300, "count": 2.5}},
		{"get_market_hints", map[string]any{"symbol": "SBER", "periodSec": 299.5}},
		{"run_backtest", map[string]any{"symbol": "SBER", "strategy": "sma_cross", "count": 1e30}},
		{"compare_strategies", map[string]any{"symbol": "SBER", "periodSec": "abc"}},
		{"start_autonomy", map[string]any{"symbol": "SBER", "strategy": "sma_cross", "periodSec": "abc"}},
	}
	for _, tc := range cases {
		call, _ := e.executeToolCall(context.Background(), tc.tool, tc.args)
		if call.Error == "" {
			t.Errorf("%s%v: no tool error for a non-integer argument", tc.tool, tc.args)
			continue
		}
		if !strings.Contains(call.Error, errBadArgument.Error()) {
			t.Errorf("%s: error = %q, want it to wrap errBadArgument", tc.tool, call.Error)
		}
	}
}

// TestAutonomyClampedRiskLogged checks the prepareAutonomy integration: the
// clamped values reach the run status and every reduction is logged as a
// risk_block entry.
func TestAutonomyClampedRiskLogged(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: syntheticBars(10, 100, 1)}
	e.newStrategy = strategyScript(nil)
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60,
		Budget: 1e12, MaxPositionPct: 100, StopLossPct: 99, DailyLossPct: 100,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Config.Budget != 1_000_000 {
		t.Errorf("Budget = %v, want the paper cash 1000000", status.Config.Budget)
	}
	if status.Config.StopLossPct != 2 || status.Config.DailyLossPct != 3 || status.Config.MaxPositionPct != 25 {
		t.Errorf("risk limits = %+v, want the engine ceilings 25/2/3", status.Config)
	}
	riskBlocks := 0
	for _, entry := range status.Log {
		if entry.Kind == "risk_block" {
			riskBlocks++
		}
	}
	if riskBlocks != 4 {
		t.Errorf("risk_block log entries = %d, want 4 (three ceilings and the budget)", riskBlocks)
	}
}
