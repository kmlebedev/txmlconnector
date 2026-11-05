package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
)

// rulesSpecArgs is a minimal valid rules.Spec tool argument: buy on the first
// close above zero and hold (the exit condition is unreachable).
func rulesSpecArgs() map[string]any {
	return map[string]any{
		"symbol":    "TQBR:SBER",
		"periodSec": int(300),
		"count":     int(100),
		"rules": map[string]any{
			"entry": []any{map[string]any{"left": "close", "op": ">", "right": "0"}},
			"exit":  []any{map[string]any{"left": "close", "op": "<", "right": "0"}},
		},
	}
}

// TestBacktestRulesTool runs the research tool through its dispatch hook (the
// core invokeTool calls it before the frozen switch).
func TestBacktestRulesTool(t *testing.T) {
	e, _, _ := newTestEngine(t)
	result, artifact, handled, err := e.invokeResearchTool(context.Background(), toolNameBacktestRules, rulesSpecArgs())
	if !handled {
		t.Fatal("backtest_rules was not handled by the research dispatch")
	}
	if err != nil {
		t.Fatalf("backtest_rules: %v", err)
	}
	if artifact == nil || artifact.Kind != "backtest" {
		t.Fatalf("artifact = %+v, want a backtest artifact", artifact)
	}
	res, ok := result.(backtest.Result)
	if !ok {
		t.Fatalf("result type = %T, want backtest.Result", result)
	}
	if res.Symbol != "TQBR:SBER" || res.Strategy != rules.ID {
		t.Fatalf("result header = %+v", res)
	}
	if len(res.Params) != 1 {
		t.Fatalf("params = %v, want the spec hash", res.Params)
	}
	if _, ok := res.Params[rules.ParamSpecHash]; !ok {
		t.Fatalf("params = %v, want the %q key", res.Params, rules.ParamSpecHash)
	}
	if res.Metrics.Trades != 0 || !hasRuleWarning(res.Warnings) {
		t.Fatalf("metrics = %+v, warnings = %v, want an open-position run", res.Metrics, res.Warnings)
	}
	if got := len(e.Backtests()); got != 1 {
		t.Fatalf("recorded backtests = %d, want 1", got)
	}
}

func hasRuleWarning(warnings []string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, "позиц") {
			return true
		}
	}
	return false
}

// TestBacktestRulesToolErrors checks the model-facing errors of the tool.
func TestBacktestRulesToolErrors(t *testing.T) {
	e, _, _ := newTestEngine(t)
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"missing rules", func(args map[string]any) { delete(args, "rules") }, "rules are required"},
		{"unknown indicator", func(args map[string]any) {
			args["rules"] = map[string]any{
				"indicators": map[string]any{"rsi": map[string]any{"kind": "supertrend", "period": 14}},
				"entry":      []any{map[string]any{"left": "rsi", "op": "<", "right": "20"}},
				"exit":       []any{map[string]any{"left": "rsi", "op": ">", "right": "80"}},
			}
		}, "supertrend"},
		{"bad count", func(args map[string]any) { args["count"] = 0.5 }, "count"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := rulesSpecArgs()
			tc.mutate(args)
			_, _, handled, err := e.invokeResearchTool(context.Background(), toolNameBacktestRules, args)
			if !handled {
				t.Fatal("backtest_rules was not handled")
			}
			if err == nil {
				t.Fatalf("backtest_rules accepted invalid arguments (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestInvokeResearchToolUnknownName ensures the research hook never swallows a
// core tool call: unknown names fall through to the frozen switch.
func TestInvokeResearchToolUnknownName(t *testing.T) {
	e, _, _ := newTestEngine(t)
	if _, _, handled, err := e.invokeResearchTool(context.Background(), "run_backtest", nil); handled || err != nil {
		t.Fatalf("run_backtest: handled=%v err=%v, want the core dispatch", handled, err)
	}
}

// TestParseRulesSpec covers the accepted wire forms and the strict decoder.
func TestParseRulesSpec(t *testing.T) {
	object := `{"indicators":{"rsi":{"kind":"rsi_wilder","period":14}},"entry":[{"left":"rsi","op":"<","right":"20"}],"exit":[{"left":"rsi","op":">","right":"80"}]}`
	spec, err := parseRulesSpec(object)
	if err != nil {
		t.Fatalf("parse JSON string: %v", err)
	}
	if spec.Indicators["rsi"].Period != 14 {
		t.Fatalf("spec = %+v", spec)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(object), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, err := parseRulesSpec(decoded); err != nil {
		t.Fatalf("parse JSON object: %v", err)
	}

	if _, err := parseRulesSpec(map[string]any{"entry": []any{}, "mystery": 1}); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v, want a strict-decoder error", err)
	}
	if _, err := parseRulesSpec(nil); err == nil {
		t.Fatal("nil rules accepted")
	}
}

// TestResearchToolsAreWired proves the hooks in terminal/agent/tools.go are in
// place: the catalog lists the tool, the core invokeTool dispatch routes to it,
// and run_backtest points a direct "rules" call at backtest_rules.
func TestResearchToolsAreWired(t *testing.T) {
	e, _, _ := newTestEngine(t)
	catalog := make(map[string]bool, len(e.Tools()))
	for _, tool := range e.Tools() {
		catalog[tool.Name] = true
	}
	named := make(map[string]bool, len(toolCatalogNames))
	for _, name := range toolCatalogNames {
		named[name] = true
	}
	for _, tool := range researchToolCatalog() {
		if !catalog[tool.Name] {
			t.Errorf("research tool %q is missing from Engine.Tools()", tool.Name)
		}
		if !named[tool.Name] {
			t.Errorf("research tool %q is missing from toolCatalogNames", tool.Name)
		}
	}

	result, artifact, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameBacktestRules, rulesSpecArgs())
	if err != nil {
		t.Fatalf("invokeTool(%s): %v", toolNameBacktestRules, err)
	}
	if _, ok := result.(backtest.Result); !ok {
		t.Fatalf("invokeTool result type = %T, want backtest.Result", result)
	}
	if artifact == nil || artifact.Kind != "backtest" {
		t.Fatalf("invokeTool artifact = %+v, want a backtest artifact", artifact)
	}

	args := rulesSpecArgs()
	args["strategy"] = backtest.StrategyRules
	if _, _, err := e.invokeTool(context.Background(), toolInvocation{}, "run_backtest", args); err == nil ||
		!strings.Contains(err.Error(), toolNameBacktestRules) {
		t.Fatalf("run_backtest with strategy %q error = %v, want a pointer to %s",
			backtest.StrategyRules, err, toolNameBacktestRules)
	}
}

// TestBacktestRulesResultCompactForLLM guards the LLM context budget: the
// equity curve must be compacted away before the result is sent to the model.
func TestBacktestRulesResultCompactForLLM(t *testing.T) {
	e, _, _ := newTestEngine(t)
	result, _, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameBacktestRules, rulesSpecArgs())
	if err != nil {
		t.Fatalf("invokeTool(%s): %v", toolNameBacktestRules, err)
	}
	full, ok := result.(backtest.Result)
	if !ok {
		t.Fatalf("result type = %T, want backtest.Result", result)
	}
	if len(full.Equity) == 0 {
		t.Fatal("fixture produced no equity points, the compaction test would be vacuous")
	}
	content := toolMessageContent(ToolCall{Name: toolNameBacktestRules, Result: full})
	if len(content) > 2048 {
		t.Fatalf("model payload = %d bytes, want <= 2048", len(content))
	}
	if strings.Contains(content, "equity") {
		t.Fatalf("model payload still carries the equity curve: %s", content)
	}
	if !strings.Contains(content, "metrics") {
		t.Fatalf("model payload lost the metrics: %s", content)
	}
}
