package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// TestRealBacktestIntegration uses the real frozen terminal/backtest package
// (no test seams) to prove the §8↔§9 integration: the local analyst runs
// compare_strategies and run_backtest through the real engine.
func TestRealBacktestIntegration(t *testing.T) {
	data := newFakeDataSource()
	// Real strategies need price reversals to close round trips.
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: oscillatingBars(300)}
	broker := newFakeBroker(1_000_000)
	e, err := New(Config{
		DefaultSymbol: "TQBR:SBER",
		Now:           func() time.Time { return testNow },
	}, data, broker)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "протестируй лучшую стратегию на SBER 5м за месяц"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.LLM {
		t.Error("local analyst must answer without an LLM")
	}
	compare, ok := findToolCall(resp.ToolCalls, "compare_strategies")
	if !ok || compare.Error != "" {
		t.Fatalf("compare_strategies = %+v", compare)
	}
	btCall, ok := findToolCall(resp.ToolCalls, "run_backtest")
	if !ok || btCall.Error != "" {
		t.Fatalf("run_backtest = %+v (answer=%q)", btCall, resp.Answer)
	}
	result, ok := btCall.Result.(backtest.Result)
	if !ok {
		t.Fatalf("run_backtest result type = %T", btCall.Result)
	}
	if result.Metrics.Bars == 0 || result.Symbol != "TQBR:SBER" {
		t.Errorf("real backtest result = %+v", result)
	}
	cmp, ok := compare.Result.(compareResult)
	if !ok || len(cmp.Ranked) != len(backtest.Strategies()) {
		t.Errorf("real compare result = %+v", compare.Result)
	}
	if got := len(e.Backtests()); got < 2 {
		t.Errorf("Backtests() = %d, want compare winner + run_backtest", got)
	}
	if !strings.Contains(resp.Answer, "%") {
		t.Errorf("answer has no metrics: %q", resp.Answer)
	}
}

// TestRealStrategyAutonomy runs the autonomy replay with the real strategy
// catalog (backtest.NewStrategy) instead of a scripted test strategy.
func TestRealStrategyAutonomy(t *testing.T) {
	data := newFakeDataSource()
	broker := newFakeBroker(1_000_000)
	e, err := New(Config{
		DefaultSymbol: "TQBR:SBER",
		Now:           func() time.Time { return testNow },
	}, data, broker)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.stepDelay = func(float64) time.Duration { return 0 }
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Running {
		t.Fatal("autonomy still running")
	}
	kinds := make(map[string]bool, len(status.Log))
	for _, entry := range status.Log {
		kinds[entry.Kind] = true
	}
	if !kinds["start"] || !kinds["stop"] {
		t.Fatalf("autonomy log = %+v", status.Log)
	}
	if status.Config.Strategy != "sma_cross" {
		t.Errorf("strategy = %q", status.Config.Strategy)
	}
}
