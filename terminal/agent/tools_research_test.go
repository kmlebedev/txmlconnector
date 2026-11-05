package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
	"github.com/kmlebedev/txmlconnector/terminal/research"
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

// batchArgs is a valid batch_backtest argument for the fake data source.
func batchArgs() map[string]any {
	return map[string]any{
		"symbols":     []any{"TQBR:SBER", "TQBR:GAZP"},
		"periodSec":   int(300),
		"count":       int(300),
		"strategy":    "sma_cross",
		"params":      map[string]any{"fast": 2, "slow": 3},
		"feeBps":      5,
		"slippageBps": 1,
	}
}

// TestBatchBacktestTool runs the multi-symbol tool through the core dispatch.
func TestBatchBacktestTool(t *testing.T) {
	e, data, _ := newTestEngine(t)
	for _, symbol := range []string{"TQBR:SBER", "TQBR:GAZP"} {
		data.histories[symbol] = map[int][]backtest.Bar{300: oscillatingBars(300)}
	}
	result, artifact, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameBatchBacktest, batchArgs())
	if err != nil {
		t.Fatalf("batch_backtest: %v", err)
	}
	res, ok := result.(batchBacktestResult)
	if !ok {
		t.Fatalf("result type = %T, want batchBacktestResult", result)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
	if res.Rows[0].Symbol != "TQBR:SBER" || res.Rows[1].Symbol != "TQBR:GAZP" {
		t.Fatalf("rows are not in the requested order: %+v", res.Rows)
	}
	for _, row := range res.Rows {
		if row.BacktestID == "" || row.From.IsZero() || row.To.IsZero() {
			t.Fatalf("row is not filled: %+v", row)
		}
	}
	if res.Best == "" {
		t.Fatalf("best = %q, want a traded ticker", res.Best)
	}

	if artifact == nil || artifact.Kind != "batch_backtest" {
		t.Fatalf("artifact = %+v, want kind batch_backtest", artifact)
	}
	ui, ok := artifact.Payload.(batchBacktestArtifact)
	if !ok {
		t.Fatalf("artifact payload type = %T, want batchBacktestArtifact", artifact.Payload)
	}
	if len(ui.Rows) != 2 || len(ui.Charts) != 2 {
		t.Fatalf("artifact rows/charts = %d/%d, want 2/2", len(ui.Rows), len(ui.Charts))
	}
	for index, chart := range ui.Charts {
		if chart.Symbol != res.Rows[index].Symbol {
			t.Fatalf("chart[%d].Symbol = %q, want %q", index, chart.Symbol, res.Rows[index].Symbol)
		}
		if chart.Bars != 300 {
			t.Fatalf("%s: bars = %d, want 300", chart.Symbol, chart.Bars)
		}
		if len(chart.Trades) != res.Rows[index].Trades {
			t.Fatalf("%s: chart trades = %d, row trades = %d",
				chart.Symbol, len(chart.Trades), res.Rows[index].Trades)
		}
	}
	if got := len(e.Backtests()); got != 2 {
		t.Fatalf("recorded backtests = %d, want 2", got)
	}
}

// TestBatchBacktestRulesCharts proves the rules path returns the indicator
// series for the chart panel and keeps the model payload compact.
func TestBatchBacktestRulesCharts(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: oscillatingBars(300)}
	args := map[string]any{
		"symbols":   []any{"TQBR:SBER"},
		"periodSec": int(300),
		"count":     int(300),
		"rules": map[string]any{
			"indicators": map[string]any{"sma3": map[string]any{"kind": "sma", "period": 3}},
			"entry":      []any{map[string]any{"left": "close", "op": "cross_above", "right": "sma3"}},
			"exit":       []any{map[string]any{"left": "close", "op": "cross_below", "right": "sma3"}},
		},
		"feeBps":      5,
		"slippageBps": 1,
	}
	result, artifact, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameBatchBacktest, args)
	if err != nil {
		t.Fatalf("batch_backtest: %v", err)
	}
	res, ok := result.(batchBacktestResult)
	if !ok {
		t.Fatalf("result type = %T, want batchBacktestResult", result)
	}
	if len(res.Rows) != 1 || res.Rows[0].Trades <= 0 {
		t.Fatalf("rules row = %+v, want trades", res.Rows)
	}
	ui, ok := artifact.Payload.(batchBacktestArtifact)
	if !ok || len(ui.Charts) != 1 {
		t.Fatalf("artifact = %+v", artifact)
	}
	chart := ui.Charts[0]
	series, ok := chart.Series["sma3"]
	if !ok {
		t.Fatalf("chart series = %v, want sma3", chart.Series)
	}
	if len(series) != chart.Bars {
		t.Fatalf("series length = %d, want %d bars", len(series), chart.Bars)
	}
	if len(chart.Trades) != res.Rows[0].Trades {
		t.Fatalf("chart trades = %d, want %d", len(chart.Trades), res.Rows[0].Trades)
	}

	// The model payload must stay compact: no equity, series or trade details.
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal compact result: %v", err)
	}
	for _, forbidden := range []string{"series", "entryTime", "exitTime", "equity"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("model payload carries %q: %s", forbidden, raw)
		}
	}
}

// TestBatchBacktestToolErrors checks the model-facing errors and the explicit
// zero-cost warning.
func TestBatchBacktestToolErrors(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: oscillatingBars(300)}

	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"missing symbols", func(args map[string]any) { delete(args, "symbols") }, "symbols are required"},
		{"fractional count", func(args map[string]any) { args["count"] = 0.5 }, "count"},
		{"missing strategy and rules", func(args map[string]any) { delete(args, "strategy") }, "strategy or rules are required"},
		{"strategy and rules", func(args map[string]any) {
			args["rules"] = map[string]any{
				"entry": []any{map[string]any{"left": "close", "op": ">", "right": "0"}},
				"exit":  []any{map[string]any{"left": "close", "op": "<", "right": "0"}},
			}
		}, "not both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := batchArgs()
			tc.mutate(args)
			if _, _, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameBatchBacktest, args); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}

	tooMany := batchArgs()
	symbols := make([]any, 0, maxBatchSymbols+1)
	for i := 0; i <= maxBatchSymbols; i++ {
		symbols = append(symbols, "TQBR:SBER")
	}
	tooMany["symbols"] = symbols
	if _, _, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameBatchBacktest, tooMany); err == nil ||
		!strings.Contains(err.Error(), "too many symbols") {
		t.Fatalf("too many symbols error = %v", err)
	}

	// Explicit zero costs produce a truthful no_costs warning in the rows.
	zero := batchArgs()
	zero["feeBps"] = 0
	zero["slippageBps"] = 0
	zero["symbols"] = []any{"TQBR:SBER"}
	result, _, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameBatchBacktest, zero)
	if err != nil {
		t.Fatalf("zero-cost batch: %v", err)
	}
	rows := result.(batchBacktestResult).Rows
	if len(rows) != 1 || !hasRuleWarningText(rows[0].Warnings, backtest.WarningNoCosts) {
		t.Fatalf("rows = %+v, want the %s warning", rows, backtest.WarningNoCosts)
	}
}

// hasRuleWarningText reports whether any warning contains substr.
func hasRuleWarningText(warnings []string, substr string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, substr) {
			return true
		}
	}
	return false
}

// walkForwardArgs is a valid walk_forward argument for the fake data source.
func walkForwardArgs() map[string]any {
	return map[string]any{
		"symbol":    "TQBR:SBER",
		"asOf":      testNow.Add(7 * 24 * time.Hour).Format(time.RFC3339),
		"periodSec": int(300),
		"count":     int(300),
		"strategy":  "sma_cross",
		"grid": map[string]any{
			"fast": []any{2, 3},
			"slow": []any{8, 13},
		},
		"trainBars":   int(120),
		"testBars":    int(60),
		"stepBars":    int(60),
		"feeBps":      5,
		"slippageBps": 1,
	}
}

// TestWalkForwardTool runs the walk-forward tool through the core dispatch.
func TestWalkForwardTool(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: oscillatingBars(300)}
	result, artifact, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameWalkForward, walkForwardArgs())
	if err != nil {
		t.Fatalf("walk_forward: %v", err)
	}
	view, ok := result.(walkForwardView)
	if !ok {
		t.Fatalf("result type = %T, want walkForwardView", result)
	}
	if view.Symbol != "TQBR:SBER" || view.Strategy != "sma_cross" || view.PeriodSec != 300 {
		t.Fatalf("view header = %+v", view)
	}
	if view.Trials != 4 {
		t.Fatalf("trials = %d, want 4", view.Trials)
	}
	if view.Folds != 3 {
		t.Fatalf("folds = %d, want 3", view.Folds)
	}
	if view.OOS.Trades <= 0 {
		t.Fatalf("OOS trades = %d, want trades", view.OOS.Trades)
	}
	if view.DeflatedSharpe < 0 || view.DeflatedSharpe > 1 {
		t.Fatalf("deflated Sharpe = %v", view.DeflatedSharpe)
	}
	if view.PBO < 0 || view.PBO > 1 {
		t.Fatalf("PBO = %v", view.PBO)
	}
	if artifact == nil || artifact.Kind != "walk_forward" {
		t.Fatalf("artifact = %+v, want walk_forward", artifact)
	}
	full, ok := artifact.Payload.(research.WalkForwardResult)
	if !ok {
		t.Fatalf("artifact payload type = %T, want research.WalkForwardResult", artifact.Payload)
	}
	if len(full.Folds) != 3 {
		t.Fatalf("artifact folds = %d, want 3", len(full.Folds))
	}
	// The model payload must stay compact: no per-fold windows or params.
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	for _, forbidden := range []string{"trainFrom", "testFrom", "testTo", "params"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("model payload carries %q: %s", forbidden, raw)
		}
	}
	if len(raw) > 2048 {
		t.Fatalf("model payload = %d bytes, want <= 2048", len(raw))
	}
}

// TestWalkForwardToolErrors checks the model-facing errors.
func TestWalkForwardToolErrors(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: oscillatingBars(300)}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"missing strategy", func(args map[string]any) { delete(args, "strategy") }, "strategy is required"},
		{"missing trainBars", func(args map[string]any) { delete(args, "trainBars") }, "trainBars is required"},
		{"missing testBars", func(args map[string]any) { delete(args, "testBars") }, "testBars is required"},
		{"too short history", func(args map[string]any) {
			args["trainBars"] = 200
			args["testBars"] = 200
		}, "not enough"},
		{"grid too large", func(args map[string]any) {
			fast := make([]any, 40)
			slow := make([]any, 30)
			for index := range fast {
				fast[index] = index + 2
			}
			for index := range slow {
				slow[index] = index + 50
			}
			args["grid"] = map[string]any{"fast": fast, "slow": slow}
		}, "more than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := walkForwardArgs()
			tc.mutate(args)
			if _, _, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameWalkForward, args); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestCompareStrategiesRanksByOOS is the R6 regression: compare_strategies
// ranks the strategies by their out-of-sample walk-forward metrics, not by the
// in-sample fit of the full history.
func TestCompareStrategiesRanksByOOS(t *testing.T) {
	e, data, _ := newTestEngine(t)
	bars := oscillatingBars(200)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: bars}
	split := bars[len(bars)/2].Time
	e.strategyInfos = func() []backtest.StrategyInfo {
		return []backtest.StrategyInfo{
			{ID: "sma_cross", Name: "SMA", Description: "in-sample winner"},
			{ID: "momentum", Name: "Momentum", Description: "out-of-sample winner"},
		}
	}
	e.backtestRun = func(req backtest.Request) (backtest.Result, error) {
		oos := len(req.Bars) > 0 && req.Bars[len(req.Bars)-1].Time.After(split)
		var totalReturn float64
		switch req.Strategy {
		case "sma_cross":
			if oos {
				totalReturn = -5
			} else {
				totalReturn = 30
			}
		case "momentum":
			if oos {
				totalReturn = 20
			} else {
				totalReturn = -10
			}
		default:
			return backtest.Result{}, fmt.Errorf("unexpected strategy %q", req.Strategy)
		}
		return makeFakeResult(req, 5, totalReturn, 1, -1), nil
	}
	call, artifact := e.executeToolCall(context.Background(), "compare_strategies", map[string]any{
		"symbol": "TQBR:SBER", "periodSec": 300, "count": 200,
	})
	if call.Error != "" {
		t.Fatalf("compare_strategies: %s", call.Error)
	}
	cmp, ok := call.Result.(compareResult)
	if !ok {
		t.Fatalf("compare result type = %T", call.Result)
	}
	if cmp.Ranked[0].Strategy != "momentum" {
		t.Fatalf("ranking[0] = %q, want the OOS winner momentum: %+v", cmp.Ranked[0].Strategy, cmp.Ranked)
	}
	if cmp.Best.Strategy != "momentum" {
		t.Fatalf("best = %q, want momentum", cmp.Best.Strategy)
	}
	if cmp.Best.Result.Metrics.TotalReturnPct != 20 {
		t.Fatalf("best full-period return = %v, want 20", cmp.Best.Result.Metrics.TotalReturnPct)
	}
	if artifact == nil || artifact.Kind != "backtest" {
		t.Fatalf("artifact = %+v, want the winner backtest", artifact)
	}
}
