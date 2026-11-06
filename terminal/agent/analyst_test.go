package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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
	// 7 days of 15m bars = 672.
	if call.Args["count"] != 672 {
		t.Errorf("count = %v, want 672", call.Args["count"])
	}
}

// TestLocalAnalystStartAutonomy checks that "запусти/авто/торгуй" produces a
// pending proposal (R5) instead of starting the paper autonomy immediately.
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
	if call.Args["strategy"] != "sma_cross" || call.Args["periodSec"] != 300 {
		t.Errorf("start_autonomy args = %v", call.Args)
	}
	proposal := proposalOf(t, call)
	if proposal.Tool != "start_autonomy" || proposal.Status != ProposalPending {
		t.Fatalf("proposal = %+v, want a pending start_autonomy", proposal)
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy must not start before the human approval")
	}
	if !strings.Contains(resp.Answer, "Подготовил предложение") {
		t.Errorf("answer does not report the proposal: %q", resp.Answer)
	}
	props := e.Proposals()
	if len(props) != 1 || props[0].ID != proposal.ID {
		t.Fatalf("proposals = %+v, want the one pending proposal", props)
	}
}

// TestLocalAnalystStopAutonomy checks the stop intent.
func TestLocalAnalystStopAutonomy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.newStrategy = strategyScript(map[int]backtest.Action{})
	e.stepDelay = func(float64) time.Duration { return 20 * time.Millisecond }
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
		{message: "запускай автоторговлю на SBER", symbol: "TQBR:SBER", period: 300, start: true},
		{message: "стартуй автоторговлю на SBER", symbol: "TQBR:SBER", period: 300, start: true},
		{message: "включи автоторговлю на SBER", symbol: "TQBR:SBER", period: 300, start: true},
		{message: "начни торговать SBER", symbol: "TQBR:SBER", period: 300, start: true},
		{message: "торгуй SBER", symbol: "TQBR:SBER", period: 300, start: true},
		{message: "start trading SBER", symbol: "TQBR:SBER", period: 300, start: true},
		// Questions, doubts and negations must never start a paper runner.
		{message: "Протестируй лучшую стратегию на SBER за последний месяц и скажи, стоит ли запускать", symbol: "TQBR:SBER", period: 300},
		{message: "стоит ли запускать автоторговлю?", symbol: "TQBR:SBER", period: 300},
		{message: "запускать?", symbol: "TQBR:SBER", period: 300},
		{message: "Запусти автоторговлю?", symbol: "TQBR:SBER", period: 300},
		{message: "не запускай автоторговлю", symbol: "TQBR:SBER", period: 300},
		{message: "не надо запускать автоторговлю", symbol: "TQBR:SBER", period: 300},
		// Modal / permission / hypothetical constructions without "?".
		{message: "нельзя запустить автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "можно запустить автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "можно ли запустить автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "что будет если запустить автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "если запустить автоторговлю на SBER, что будет", symbol: "TQBR:SBER", period: 300},
		{message: "хотел бы запустить автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "должен ли я запустить автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "не стоит запускать автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "запрещено ли запускать автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "разрешено ли запустить автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "как запустить автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
		{message: "почему запускать автоторговлю на SBER", symbol: "TQBR:SBER", period: 300},
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

// TestLocalAnalystQuestionDoesNotStartAutonomy is the regression test for the
// false autonomy launch: "скажи, стоит ли запускать" is a question, not an
// imperative, so no runner may start.
func TestLocalAnalystQuestionDoesNotStartAutonomy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	resp, err := e.Chat(context.Background(), ChatRequest{
		Message: "Протестируй лучшую стратегию на SBER за последний месяц и скажи, стоит ли запускать",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := findToolCall(resp.ToolCalls, "start_autonomy"); ok {
		t.Fatalf("question must not start autonomy; tools = %v", toolNames(resp.ToolCalls))
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy was started by a question")
	}
	if !strings.Contains(resp.Answer, "Лучшая стратегия: sma_cross") {
		t.Errorf("answer must name a traded strategy: %q", resp.Answer)
	}
	btCall, ok := findToolCall(resp.ToolCalls, "run_backtest")
	if !ok || btCall.Args["strategy"] != "sma_cross" {
		t.Errorf("run_backtest = %+v", btCall)
	}
	if !strings.Contains(resp.Answer, "Чтобы запустить автоторговлю") {
		t.Errorf("answer does not offer the explicit command: %q", resp.Answer)
	}
}

// TestLocalAnalystNoSignalsNoAutonomy: when no strategy traded, the analyst
// must say there are no signals instead of picking a zero-trade "best".
func TestLocalAnalystNoSignalsNoAutonomy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.backtestRun = func(req backtest.Request) (backtest.Result, error) {
		return makeFakeResult(req, 0, 0, 0, 0), nil
	}
	resp, err := e.Chat(context.Background(), ChatRequest{
		Message: "протестируй лучшую стратегию на SBER за месяц",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(resp.Answer, "ни одна стратегия не дала сделок") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "не рекомендую") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if _, ok := findToolCall(resp.ToolCalls, "start_autonomy"); ok {
		t.Fatal("no-signal analysis must not start autonomy")
	}
	if _, ok := findToolCall(resp.ToolCalls, "run_backtest"); ok {
		t.Fatal("no-signal analysis must not run a zero-trade backtest artifact")
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy was started without signals")
	}
	if len(resp.Artifacts) != 0 {
		t.Errorf("artifacts = %+v, want none for a no-signal comparison", resp.Artifacts)
	}
	if !strings.Contains(resp.Answer, "Рекомендация: не запускать — сигналов нет") {
		t.Errorf("answer lacks the explicit no-signals recommendation: %q", resp.Answer)
	}
}

// TestLocalAnalystNoTradeStrategyDoesNotStartAutonomy: an explicitly requested
// strategy that produced no trades is never started as paper autonomy.
func TestLocalAnalystNoTradeStrategyDoesNotStartAutonomy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.backtestRun = func(req backtest.Request) (backtest.Result, error) {
		if req.Strategy == "rsi_reversion" {
			return makeFakeResult(req, 0, 0, 0, 0), nil
		}
		return makeFakeResult(req, 5, 2.0, 1.0, -1.0), nil
	}
	resp, err := e.Chat(context.Background(), ChatRequest{
		Message: "запусти автоторговлю по RSI на SBER",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := findToolCall(resp.ToolCalls, "start_autonomy"); ok {
		t.Fatalf("zero-trade strategy must not be started; tools = %v", toolNames(resp.ToolCalls))
	}
	if !strings.Contains(resp.Answer, "не дала сделок") || !strings.Contains(resp.Answer, "не рекомендую") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy was started on a zero-trade strategy")
	}
}

// TestLocalAnalystAlreadyRunningNoSecondRunner: repeating "запусти" while a
// runner is active must not create a second one.
func TestLocalAnalystAlreadyRunningNoSecondRunner(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.stepDelay = func(float64) time.Duration { return 20 * time.Millisecond }
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross",
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	before := e.Status().Autonomy

	resp, err := e.Chat(context.Background(), ChatRequest{
		Message: "запусти автоторговлю по SMA на SBER",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	call, ok := findToolCall(resp.ToolCalls, "start_autonomy")
	if !ok {
		t.Fatalf("start_autonomy call is missing; tools = %v", toolNames(resp.ToolCalls))
	}
	if !strings.Contains(call.Error, "already running") {
		t.Errorf("start_autonomy error = %q, want already running", call.Error)
	}
	if !strings.Contains(resp.Answer, "уже запущена") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "Запущено по вашей команде на бумажном счёте") {
		t.Errorf("answer lacks the action-consistent closing line: %q", resp.Answer)
	}
	after := e.Status().Autonomy
	if !after.StartedAt.Equal(before.StartedAt) {
		t.Errorf("StartedAt changed from %v to %v: a second runner was created", before.StartedAt, after.StartedAt)
	}
	starts := 0
	for _, entry := range after.Log {
		if entry.Kind == "start" {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("start log entries = %d, want 1 (single runner)", starts)
	}
	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy: %v", err)
	}
}

// fakeReturnsOverrides builds a backtest seam with per-strategy returns and a
// fixed number of trades.
func fakeReturnsOverrides(returns map[string]float64, trades int) func(backtest.Request) (backtest.Result, error) {
	return func(req backtest.Request) (backtest.Result, error) {
		totalReturn, ok := returns[req.Strategy]
		if !ok {
			return backtest.Result{}, fmt.Errorf("unexpected strategy %q", req.Strategy)
		}
		return makeFakeResult(req, trades, totalReturn, 0.8, -2.4), nil
	}
}

// TestLocalAnalystExplicitCommandLosingStrategy: an explicit start command for
// a losing (but trading) strategy produces a proposal, still warning that the
// history is losing (R5: nothing runs before the human approval).
func TestLocalAnalystExplicitCommandLosingStrategy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.stepDelay = func(float64) time.Duration { return 20 * time.Millisecond }
	e.backtestRun = func(req backtest.Request) (backtest.Result, error) {
		if req.Strategy == "sma_cross" {
			return makeFakeResult(req, 11, -2.69, -20.36, -3.27), nil
		}
		return makeFakeResult(req, 5, 0.4, 0.8, -2.4), nil
	}
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "запусти автоторговлю по SMA на SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	call, ok := findToolCall(resp.ToolCalls, "start_autonomy")
	if !ok || call.Error != "" {
		t.Fatalf("start_autonomy = %+v", call)
	}
	if proposal := proposalOf(t, call); proposal.Status != ProposalPending {
		t.Fatalf("proposal = %+v, want pending", proposal)
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy must not run before the human approval")
	}
	if !strings.Contains(resp.Answer, "Подготовил предложение") {
		t.Errorf("answer does not report the proposal: %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "убыточна") {
		t.Errorf("answer does not warn about the losing history: %q", resp.Answer)
	}
}

// TestLocalAnalystExplicitCommandWinningStrategy: the same command for a
// strategy that beats buy&hold gets the proposal wording while staying pending.
func TestLocalAnalystExplicitCommandWinningStrategy(t *testing.T) {
	e, _, _ := newTestEngine(t) // default fake: sma_cross +7.5% vs benchmark +1.5%
	e.stepDelay = func(float64) time.Duration { return 20 * time.Millisecond }
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "запусти автоторговлю по SMA на SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	call, ok := findToolCall(resp.ToolCalls, "start_autonomy")
	if !ok || call.Error != "" {
		t.Fatalf("start_autonomy = %+v", call)
	}
	if proposal := proposalOf(t, call); proposal.Status != ProposalPending {
		t.Fatalf("proposal = %+v, want pending", proposal)
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy must not run before the human approval")
	}
	if !strings.Contains(resp.Answer, "Подготовил предложение") {
		t.Errorf("answer does not report the proposal: %q", resp.Answer)
	}
	if strings.Contains(resp.Answer, "Автономия запущена") {
		t.Errorf("answer must not claim the runner started: %q", resp.Answer)
	}
}

// TestLocalAnalystRecommendationUnprofitable covers the negative-return branch:
// the answer must end with an explicit "не запускать — убыточна".
func TestLocalAnalystRecommendationUnprofitable(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.backtestRun = fakeReturnsOverrides(map[string]float64{
		"sma_cross": -1.5, "rsi_reversion": -0.5, "donchian_breakout": -2.0, "momentum": -3.0,
	}, 5)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "проанализируй лучшую стратегию на SBER 5м"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(resp.Answer, "Рекомендация: не запускать") || !strings.Contains(resp.Answer, "убыточна") {
		t.Errorf("answer lacks the unprofitable recommendation: %q", resp.Answer)
	}
	if e.Status().Autonomy.Running {
		t.Fatal("unprofitable strategy must not start autonomy")
	}
}

// TestLocalAnalystRecommendationBelowBenchmark covers the positive-but-below-
// buy&hold branch.
func TestLocalAnalystRecommendationBelowBenchmark(t *testing.T) {
	e, _, _ := newTestEngine(t)
	// makeFakeResult uses BenchmarkReturnPct = 1.5, so 0.5% is below buy&hold.
	e.backtestRun = fakeReturnsOverrides(map[string]float64{
		"sma_cross": 0.5, "rsi_reversion": 0.3, "donchian_breakout": 0.2, "momentum": 0.4,
	}, 5)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "проанализируй лучшую стратегию на SBER 5м"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(resp.Answer, "Рекомендация: не запускать") ||
		!strings.Contains(resp.Answer, "не обгоняет buy&hold") {
		t.Errorf("answer lacks the below-benchmark recommendation: %q", resp.Answer)
	}
	if e.Status().Autonomy.Running {
		t.Fatal("below-benchmark strategy must not start autonomy")
	}
}

// TestLocalAnalystRecommendationOutperforms covers the branch that beats
// buy&hold: "можно рассмотреть на бумажном счёте".
func TestLocalAnalystRecommendationOutperforms(t *testing.T) {
	e, _, _ := newTestEngine(t) // default fake: sma_cross 7.5% vs benchmark 1.5%
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "проанализируй лучшую стратегию на SBER 5м"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(resp.Answer, "Рекомендация: можно рассмотреть на бумажном счёте") {
		t.Errorf("answer lacks the outperform recommendation: %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "не инвестиционная рекомендация") {
		t.Errorf("answer lacks the paper/not-advice reminder: %q", resp.Answer)
	}
}

// TestLocalAnalystExplicitStrategyRecommendation checks that a user-named
// losing strategy (SMA) gets the same explicit conclusion and is not started.
func TestLocalAnalystExplicitStrategyRecommendation(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.backtestRun = func(req backtest.Request) (backtest.Result, error) {
		if req.Strategy == "sma_cross" {
			return makeFakeResult(req, 11, -2.69, -20.36, -3.27), nil
		}
		return makeFakeResult(req, 5, 0.4, 0.8, -2.4), nil
	}
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "протестируй стратегию SMA на SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(resp.Answer, "Стратегия sma_cross (по запросу)") {
		t.Errorf("answer does not mention the requested strategy: %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "Рекомендация: не запускать") ||
		!strings.Contains(resp.Answer, "убыточна") {
		t.Errorf("answer lacks the explicit conclusion for a losing explicit strategy: %q", resp.Answer)
	}
	if _, ok := findToolCall(resp.ToolCalls, "start_autonomy"); ok {
		t.Fatalf("explicit losing strategy must not be auto-started; tools = %v", toolNames(resp.ToolCalls))
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy was started for a losing strategy")
	}
}

// TestLocalAnalystModalDoesNotStartAutonomy is the P2 regression test: modal
// and hypothetical constructions without a "?" must not start the runner.
func TestLocalAnalystModalDoesNotStartAutonomy(t *testing.T) {
	messages := []string{
		"нельзя запустить автоторговлю на SBER",
		"можно запустить автоторговлю на SBER",
		"что будет если запустить автоторговлю на SBER",
	}
	for _, message := range messages {
		e, _, _ := newTestEngine(t)
		resp, err := e.Chat(context.Background(), ChatRequest{Message: message})
		if err != nil {
			t.Fatalf("%q: Chat: %v", message, err)
		}
		if _, ok := findToolCall(resp.ToolCalls, "start_autonomy"); ok {
			t.Errorf("%q: start_autonomy called; tools = %v", message, toolNames(resp.ToolCalls))
		}
		if e.Status().Autonomy.Running {
			t.Errorf("%q: autonomy started by a modal message", message)
		}
		if resp.Answer == "" {
			t.Errorf("%q: empty answer", message)
		}
	}
}

// TestLocalAnalystStopGuards is the P3 regression test: only an explicit stop
// imperative stops the runner, and stopping an inactive runner is reported
// honestly instead of faking a stopped state.
func TestLocalAnalystStopGuards(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.stepDelay = func(float64) time.Duration { return 20 * time.Millisecond }

	// No runner: a question does not stop anything, and the answer is honest.
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "можно остановить автоторговлю?"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := findToolCall(resp.ToolCalls, "stop_autonomy"); ok {
		t.Fatalf("question must not call stop_autonomy; tools = %v", toolNames(resp.ToolCalls))
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy unexpectedly running")
	}

	// No runner: an imperative gets an honest "not running" answer.
	resp, err = e.Chat(context.Background(), ChatRequest{Message: "останови автоторговлю"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(resp.Answer, "не запущена") {
		t.Errorf("answer must say the runner is not active: %q", resp.Answer)
	}
	if _, ok := findToolCall(resp.ToolCalls, "stop_autonomy"); ok {
		t.Errorf("stop_autonomy called for an inactive runner; tools = %v", toolNames(resp.ToolCalls))
	}

	// Active runner: a modal question leaves it running...
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross",
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	resp, err = e.Chat(context.Background(), ChatRequest{Message: "можно остановить автоторговлю?"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := findToolCall(resp.ToolCalls, "stop_autonomy"); ok {
		t.Errorf("modal question stopped a running runner; tools = %v", toolNames(resp.ToolCalls))
	}
	if !e.Status().Autonomy.Running {
		t.Fatal("modal question stopped the running runner")
	}

	// ...while an explicit imperative really stops it.
	resp, err = e.Chat(context.Background(), ChatRequest{Message: "останови автоторговлю"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := findToolCall(resp.ToolCalls, "stop_autonomy"); !ok {
		t.Fatalf("explicit stop imperative must call stop_autonomy; tools = %v", toolNames(resp.ToolCalls))
	}
	if !strings.Contains(resp.Answer, "остановлена") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if e.Status().Autonomy.Running {
		t.Fatal("autonomy still running after the stop imperative")
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
