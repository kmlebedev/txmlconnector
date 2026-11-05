package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// TestLocalAnalystFullPipeline checks the deterministic demo pipeline:
// compare_strategies → get_market_hints → run_backtest of the chosen strategy.
func TestLocalAnalystFullPipeline(t *testing.T) {
	e, _, _ := newTestEngine(t)
	resp, err := e.Chat(context.Background(), ChatRequest{
		Message: "Протестируй стратегию RSI на Сбербанке за месяц, период 5м, капитал 100000",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.LLM {
		t.Error("local analyst response must have LLM=false")
	}
	names := toolNames(resp.ToolCalls)
	if len(names) < 3 || names[0] != "compare_strategies" || names[1] != "get_market_hints" || names[2] != "run_backtest" {
		t.Fatalf("tool call order = %v", names)
	}
	compare, _ := findToolCall(resp.ToolCalls, "compare_strategies")
	if compare.Error != "" {
		t.Fatalf("compare_strategies error: %s", compare.Error)
	}
	if compare.Args["symbol"] != "TQBR:SBER" || compare.Args["periodSec"] != 300 {
		t.Errorf("compare args = %v", compare.Args)
	}
	backtestCall, _ := findToolCall(resp.ToolCalls, "run_backtest")
	if backtestCall.Error != "" {
		t.Fatalf("run_backtest error: %s", backtestCall.Error)
	}
	if backtestCall.Args["strategy"] != "rsi_reversion" {
		t.Errorf("run_backtest strategy = %v, want rsi_reversion", backtestCall.Args["strategy"])
	}
	if backtestCall.Args["initialCash"] != 100000.0 {
		t.Errorf("initialCash = %v, want 100000", backtestCall.Args["initialCash"])
	}
	result, ok := backtestCall.Result.(backtest.Result)
	if !ok || result.Metrics.TotalReturnPct != 3.2 {
		t.Fatalf("unexpected backtest result: %+v", backtestCall.Result)
	}
	kinds := artifactKinds(resp.Artifacts)
	if len(kinds) < 2 || kinds[0] != "backtest" || kinds[len(kinds)-1] != "backtest" {
		t.Fatalf("artifact kinds = %v", kinds)
	}
	if !strings.Contains(resp.Answer, "rsi_reversion") {
		t.Errorf("answer does not mention the strategy: %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "доходность") || !strings.Contains(resp.Answer, "Риски") {
		t.Errorf("answer is missing metrics/risks: %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "баров") {
		t.Errorf("answer does not mention the bar count: %q", resp.Answer)
	}
}

// TestLocalAnalystBestStrategy verifies the keyword-less request picks the
// winner of compare_strategies.
func TestLocalAnalystBestStrategy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "Проанализируй SBER на 15м за неделю"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	call, ok := findToolCall(resp.ToolCalls, "run_backtest")
	if !ok {
		t.Fatal("run_backtest tool call is missing")
	}
	if call.Args["strategy"] != "sma_cross" {
		t.Errorf("strategy = %v, want sma_cross (best of compare)", call.Args["strategy"])
	}
	if call.Args["periodSec"] != 900 {
		t.Errorf("periodSec = %v, want 900", call.Args["periodSec"])
	}
	// 7 days of 15m bars = 672 → clamped to 600.
	if call.Args["count"] != 600 {
		t.Errorf("count = %v, want 600", call.Args["count"])
	}
}

// TestLocalAnalystStartAutonomy checks "запусти/авто/торгуй" starts the paper
// autonomy after the backtest.
func TestLocalAnalystStartAutonomy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	resp, err := e.Chat(context.Background(), ChatRequest{
		Message: "запусти автоторговлю на SBER по стратегии SMA на 5м",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	call, ok := findToolCall(resp.ToolCalls, "start_autonomy")
	if !ok {
		t.Fatalf("start_autonomy call is missing; tools = %v", toolNames(resp.ToolCalls))
	}
	if call.Error != "" {
		t.Fatalf("start_autonomy error: %s", call.Error)
	}
	if call.Args["strategy"] != "sma_cross" || call.Args["periodSec"] != 300 {
		t.Errorf("start_autonomy args = %v", call.Args)
	}
	if !strings.Contains(resp.Answer, "Автономия запущена") {
		t.Errorf("answer does not report the started autonomy: %q", resp.Answer)
	}
	st := waitAutonomy(t, e)
	foundStart := false
	for _, entry := range st.Log {
		if entry.Kind == "start" {
			foundStart = true
			break
		}
	}
	if !foundStart {
		t.Fatalf("autonomy log has no start entry: %+v", st.Log)
	}
}

// TestLocalAnalystStopAutonomy checks the stop intent.
func TestLocalAnalystStopAutonomy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.newStrategy = strategyScript(map[int]backtest.Action{})
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross",
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "останови автоторговлю"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	call, ok := findToolCall(resp.ToolCalls, "stop_autonomy")
	if !ok {
		t.Fatalf("stop_autonomy call is missing; tools = %v", toolNames(resp.ToolCalls))
	}
	if call.Error != "" {
		t.Fatalf("stop_autonomy error: %s", call.Error)
	}
	if !strings.Contains(resp.Answer, "остановлена") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if e.Status().Autonomy.Running {
		t.Error("autonomy is still running after the stop intent")
	}
}

// TestParseIntent covers the deterministic Russian/English parsing rules.
func TestParseIntent(t *testing.T) {
	e, _, _ := newTestEngine(t)
	cases := []struct {
		message  string
		symbol   string
		period   int
		capital  float64
		strategy string
		start    bool
	}{
		{message: "построй 1м график SBER", symbol: "TQBR:SBER", period: 60},
		{message: "5-минутный график по Сбербанку", symbol: "TQBR:SBER", period: 300},
		{message: "15м GAZP", symbol: "TQBR:GAZP", period: 900},
		{message: "часовой график по газпрому", symbol: "TQBR:GAZP", period: 3600},
		{message: "дневной график YNDX", symbol: "TQBR:YNDX", period: 86400},
		{message: "проанализируй SBER", symbol: "TQBR:SBER", period: 300},
		{message: "SBER 5м капитал 100000", symbol: "TQBR:SBER", period: 300, capital: 100000},
		{message: "GAZP 1ч, бюджет 1 млн", symbol: "TQBR:GAZP", period: 3600, capital: 1_000_000},
		{message: "протестируй на 250000 рублей Сбер", symbol: "TQBR:SBER", period: 300, capital: 250000},
		{message: "sma на SBER", symbol: "TQBR:SBER", period: 300, strategy: "sma_cross"},
		{message: "скользящая средняя SBER", symbol: "TQBR:SBER", period: 300, strategy: "sma_cross"},
		{message: "rsi перепроданность GAZP", symbol: "TQBR:GAZP", period: 300, strategy: "rsi_reversion"},
		{message: "пробой канала SBER", symbol: "TQBR:SBER", period: 300, strategy: "donchian_breakout"},
		{message: "моментум SBER", symbol: "TQBR:SBER", period: 300, strategy: "momentum"},
		{message: "запусти автоторговлю на SBER", symbol: "TQBR:SBER", period: 300, start: true},
		{message: "торгуй SBER", symbol: "TQBR:SBER", period: 300, start: true},
		{message: "start trading SBER", symbol: "TQBR:SBER", period: 300, start: true},
	}
	for _, tc := range cases {
		intent, err := e.parseIntent(tc.message)
		if err != nil {
			t.Errorf("%q: %v", tc.message, err)
			continue
		}
		if intent.Symbol != tc.symbol {
			t.Errorf("%q: symbol = %q, want %q", tc.message, intent.Symbol, tc.symbol)
		}
		if intent.PeriodSec != tc.period {
			t.Errorf("%q: period = %d, want %d", tc.message, intent.PeriodSec, tc.period)
		}
		if tc.capital != 0 && intent.Capital != tc.capital {
			t.Errorf("%q: capital = %v, want %v", tc.message, intent.Capital, tc.capital)
		}
		if intent.Strategy != tc.strategy {
			t.Errorf("%q: strategy = %q, want %q", tc.message, intent.Strategy, tc.strategy)
		}
		if intent.Start != tc.start {
			t.Errorf("%q: start = %v, want %v", tc.message, intent.Start, tc.start)
		}
	}
}

// TestLocalAnalystDataError renders an honest answer when the feed fails.
func TestLocalAnalystDataError(t *testing.T) {
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, &errDataSource{err: context.DeadlineExceeded}, newFakeBroker(1000))
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "проанализируй SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.LLM {
		t.Error("LLM must be false for the local analyst")
	}
	if !strings.Contains(resp.Answer, "Не удалось получить реальные данные") {
		t.Errorf("answer = %q", resp.Answer)
	}
}

// TestLocalAnalystEmptyMessage is a bad request, not a panic.
func TestLocalAnalystEmptyMessage(t *testing.T) {
	e, _, _ := newTestEngine(t)
	if _, err := e.Chat(context.Background(), ChatRequest{Message: "   "}); err == nil {
		t.Fatal("empty message must return an error")
	}
}
