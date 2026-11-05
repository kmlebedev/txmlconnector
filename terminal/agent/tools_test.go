package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// TestToolRunBacktest executes the real (fake-seamed) backtest tool.
func TestToolRunBacktest(t *testing.T) {
	e, _, _ := newTestEngine(t)
	call, artifact := e.executeToolCall(context.Background(), "run_backtest", map[string]any{
		"symbol":    "SBER",
		"periodSec": 300,
		"strategy":  "sma_cross",
		"count":     120,
	})
	if call.Error != "" {
		t.Fatalf("run_backtest: %s", call.Error)
	}
	result, ok := call.Result.(backtest.Result)
	if !ok {
		t.Fatalf("run_backtest result type = %T", call.Result)
	}
	if result.Symbol != "TQBR:SBER" || result.Strategy != "sma_cross" || result.PeriodSec != 300 {
		t.Errorf("unexpected result identity: %+v", result)
	}
	if result.Metrics.TotalReturnPct != 7.5 {
		t.Errorf("TotalReturnPct = %v, want 7.5", result.Metrics.TotalReturnPct)
	}
	if artifact == nil || artifact.Kind != "backtest" {
		t.Fatalf("artifact = %+v, want backtest", artifact)
	}
	if got := len(e.Backtests()); got != 1 {
		t.Fatalf("Backtests() = %d, want 1", got)
	}
	if call.DurationMs < 0 {
		t.Errorf("DurationMs = %d", call.DurationMs)
	}
}

// TestToolCompareStrategies ranks all strategies and returns the winner.
func TestToolCompareStrategies(t *testing.T) {
	e, _, _ := newTestEngine(t)
	call, artifact := e.executeToolCall(context.Background(), "compare_strategies", map[string]any{
		"symbol": "TQBR:SBER", "count": 120,
	})
	if call.Error != "" {
		t.Fatalf("compare_strategies: %s", call.Error)
	}
	cmp, ok := call.Result.(compareResult)
	if !ok {
		t.Fatalf("compare result type = %T", call.Result)
	}
	if len(cmp.Ranked) != len(fakeStrategies) {
		t.Fatalf("ranked len = %d, want %d", len(cmp.Ranked), len(fakeStrategies))
	}
	if cmp.Ranked[0].Strategy != "sma_cross" {
		t.Errorf("best ranked = %q, want sma_cross", cmp.Ranked[0].Strategy)
	}
	if cmp.Best.Strategy != "sma_cross" || cmp.Best.Result.Metrics.TotalReturnPct != 7.5 {
		t.Errorf("best = %+v", cmp.Best)
	}
	if artifact == nil || artifact.Kind != "backtest" {
		t.Fatalf("artifact = %+v", artifact)
	}
	if _, ok := artifact.Payload.(backtest.Result); !ok {
		t.Fatalf("artifact payload type = %T", artifact.Payload)
	}
}

// TestCompareStrategiesSkipsZeroTradeStrategies is the regression test for the
// "momentum with zero trades wins" defect: a zero-trade strategy has zero
// Sharpe/drawdown, so it must never outrank a strategy that actually traded —
// even a losing one.
func TestCompareStrategiesSkipsZeroTradeStrategies(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.backtestRun = func(req backtest.Request) (backtest.Result, error) {
		switch req.Strategy {
		case "sma_cross":
			return makeFakeResult(req, 14, -1.5, 0.8, -2.0), nil
		case "rsi_reversion":
			return makeFakeResult(req, 5, -2.0, 0.5, -3.0), nil
		case "donchian_breakout":
			return makeFakeResult(req, 6, -0.5, 1.0, -1.5), nil
		case "momentum":
			return makeFakeResult(req, 0, 0, 0, 0), nil
		default:
			return backtest.Result{}, fmt.Errorf("unexpected strategy %q", req.Strategy)
		}
	}
	call, artifact := e.executeToolCall(context.Background(), "compare_strategies", map[string]any{
		"symbol": "TQBR:SBER", "periodSec": 300, "count": 600,
	})
	if call.Error != "" {
		t.Fatalf("compare_strategies: %s", call.Error)
	}
	cmp := call.Result.(compareResult)
	if cmp.Best.Strategy == "momentum" {
		t.Fatal("zero-trade momentum must never be chosen as the best strategy")
	}
	if cmp.Best.Strategy != "donchian_breakout" {
		t.Errorf("best = %q, want donchian_breakout (best return among traded strategies)", cmp.Best.Strategy)
	}
	if cmp.Best.Result.Metrics.Trades <= 0 {
		t.Errorf("best result has %d trades, want > 0", cmp.Best.Result.Metrics.Trades)
	}
	last := cmp.Ranked[len(cmp.Ranked)-1]
	if last.Strategy != "momentum" {
		t.Errorf("zero-trade strategy is not last: %v", cmp.Ranked)
	}
	if last.Warning == "" {
		t.Error("zero-trade strategy must carry a warning")
	}
	if artifact == nil || artifact.Kind != "backtest" {
		t.Fatalf("artifact = %+v, want backtest of the traded winner", artifact)
	}
}

// TestCompareStrategiesNoSignals returns no best strategy when nothing traded.
func TestCompareStrategiesNoSignals(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.backtestRun = func(req backtest.Request) (backtest.Result, error) {
		return makeFakeResult(req, 0, 0, 0, 0), nil
	}
	call, artifact := e.executeToolCall(context.Background(), "compare_strategies", map[string]any{
		"symbol": "TQBR:SBER", "periodSec": 300, "count": 600,
	})
	if call.Error != "" {
		t.Fatalf("compare_strategies: %s", call.Error)
	}
	cmp := call.Result.(compareResult)
	if cmp.Best.Strategy != "" {
		t.Errorf("best = %q, want no best strategy without trades", cmp.Best.Strategy)
	}
	if len(cmp.Warnings) == 0 {
		t.Error("no-signals comparison must carry a warning")
	}
	if artifact != nil {
		t.Errorf("no-signals comparison must not produce a backtest artifact, got %+v", artifact)
	}
	for _, row := range cmp.Ranked {
		if row.Warning == "" {
			t.Errorf("ranked row %q has no warning", row.Strategy)
		}
	}
	if got := len(e.Backtests()); got != 0 {
		t.Errorf("Backtests() = %d, want 0 (zero-trade runs are not recorded)", got)
	}
}

// TestToolGetQuoteAndHistory checks the JSON shapes of two data tools.
func TestToolGetQuoteAndHistory(t *testing.T) {
	e, _, _ := newTestEngine(t)
	quoteCall, _ := e.executeToolCall(context.Background(), "get_quote", map[string]any{"symbol": "SBER"})
	if quoteCall.Error != "" {
		t.Fatalf("get_quote: %s", quoteCall.Error)
	}
	quote, ok := quoteCall.Result.(quoteResult)
	if !ok {
		t.Fatalf("quote result type = %T", quoteCall.Result)
	}
	if quote.Symbol != "TQBR:SBER" || quote.Last != 293.22 || quote.Bid != 293.2 || quote.Ask != 293.26 {
		t.Errorf("unexpected quote: %+v", quote)
	}
	if len(quote.Book.Bids) != 2 || len(quote.Book.Asks) != 2 {
		t.Errorf("unexpected book: %+v", quote.Book)
	}

	historyCall, _ := e.executeToolCall(context.Background(), "get_history", map[string]any{
		"symbol": "SBER", "periodSec": 300, "count": 5000,
	})
	if historyCall.Error != "" {
		t.Fatalf("get_history: %s", historyCall.Error)
	}
	history, ok := historyCall.Result.(historyResult)
	if !ok {
		t.Fatalf("history result type = %T", historyCall.Result)
	}
	if len(history.Bars) > maxHistoryBars {
		t.Errorf("history returned %d bars, want <= %d", len(history.Bars), maxHistoryBars)
	}
	if history.Source == "" || history.Symbol != "TQBR:SBER" {
		t.Errorf("unexpected history: %+v", history)
	}
}

// TestPlaceOrderPaper checks a normal paper order passes the risk checks and
// reaches the broker.
func TestPlaceOrderPaper(t *testing.T) {
	e, _, broker := newTestEngine(t)
	call, artifact := e.executeToolCall(context.Background(), "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 10, "type": "market",
	})
	if call.Error != "" {
		t.Fatalf("place_order: %s", call.Error)
	}
	if artifact != nil {
		t.Errorf("place_order must not create an artifact, got %+v", artifact)
	}
	order, ok := call.Result.(OrderView)
	if !ok {
		t.Fatalf("order result type = %T", call.Result)
	}
	if order.Symbol != "TQBR:SBER" || order.Side != "buy" || order.Qty != 10 {
		t.Errorf("unexpected order: %+v", order)
	}
	if placed := broker.placedOrders(); len(placed) != 1 {
		t.Fatalf("broker placements = %d, want 1", len(placed))
	}
}

// TestPlaceOrderRiskMaxOrderPct blocks an order above MaxOrderPct of cash.
func TestPlaceOrderRiskMaxOrderPct(t *testing.T) {
	e, _, broker := newTestEngine(t)
	call, _ := e.executeToolCall(context.Background(), "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 2000, "type": "market",
	})
	if call.Error == "" || !strings.Contains(call.Error, "risk:") {
		t.Fatalf("expected a risk error, got %q", call.Error)
	}
	if placed := broker.placedOrders(); len(placed) != 0 {
		t.Fatalf("broker must not receive a blocked order, got %d", len(placed))
	}
}

// TestPlaceOrderRiskMaxPositionPct blocks an order above MaxPositionPct of the
// portfolio value.
func TestPlaceOrderRiskMaxPositionPct(t *testing.T) {
	e, _, broker := newTestEngine(t)
	broker.mu.Lock()
	broker.totalValue = 10_000 // 25% => 2500 ₽ max position
	broker.mu.Unlock()
	call, _ := e.executeToolCall(context.Background(), "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 10, "type": "market", // ~2932 ₽
	})
	if call.Error == "" || !strings.Contains(call.Error, "max position") {
		t.Fatalf("expected a max position error, got %q", call.Error)
	}
	if placed := broker.placedOrders(); len(placed) != 0 {
		t.Fatalf("broker must not receive a blocked order, got %d", len(placed))
	}
}

// TestPlaceOrderRiskSellExceedsPosition blocks a short sale.
func TestPlaceOrderRiskSellExceedsPosition(t *testing.T) {
	e, _, broker := newTestEngine(t)
	call, _ := e.executeToolCall(context.Background(), "place_order", map[string]any{
		"symbol": "SBER", "side": "sell", "qty": 10, "type": "market",
	})
	if call.Error == "" || !strings.Contains(call.Error, "exceeds position") {
		t.Fatalf("expected a position error, got %q", call.Error)
	}
	if placed := broker.placedOrders(); len(placed) != 0 {
		t.Fatalf("broker must not receive a blocked order, got %d", len(placed))
	}
}

// TestPlaceOrderValidation rejects malformed orders.
func TestPlaceOrderValidation(t *testing.T) {
	e, _, _ := newTestEngine(t)
	cases := []map[string]any{
		{"symbol": "SBER", "side": "hold", "qty": 10},
		{"symbol": "SBER", "side": "buy", "qty": 0},
		{"symbol": "SBER", "side": "buy", "qty": 1, "type": "stop"},
		{"symbol": "SBER", "side": "buy", "qty": 1, "type": "limit"},
	}
	for _, args := range cases {
		if call, _ := e.executeToolCall(context.Background(), "place_order", args); call.Error == "" {
			t.Errorf("args %v: expected validation error", args)
		}
	}
}

// TestCancelOrder places and cancels a paper order through tools.
func TestCancelOrder(t *testing.T) {
	e, _, _ := newTestEngine(t)
	placed, _ := e.executeToolCall(context.Background(), "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 1, "type": "market",
	})
	order, ok := placed.Result.(OrderView)
	if !ok {
		t.Fatalf("place result type = %T", placed.Result)
	}
	call, _ := e.executeToolCall(context.Background(), "cancel_order", map[string]any{"orderId": order.ID})
	if call.Error != "" {
		t.Fatalf("cancel_order: %s", call.Error)
	}
	payload, ok := call.Result.(map[string]any)
	if !ok {
		t.Fatalf("cancel result type = %T", call.Result)
	}
	cancelled, ok := payload["order"].(OrderView)
	if !ok || cancelled.Status != "cancelled" {
		t.Fatalf("cancel payload = %+v", payload)
	}
	if call, _ := e.executeToolCall(context.Background(), "cancel_order", map[string]any{"orderId": "nope"}); call.Error == "" {
		t.Fatal("expected an error for an unknown order")
	}
}

// TestStartAutonomyLiveRejected is the frozen §9.4 guard rail.
func TestStartAutonomyLiveRejected(t *testing.T) {
	e, _, _ := newTestEngine(t)
	call, _ := e.executeToolCall(context.Background(), "start_autonomy", map[string]any{
		"symbol": "SBER", "strategy": "sma_cross", "live": true,
	})
	if call.Error == "" || !strings.Contains(call.Error, "live autonomy is not implemented in iteration 1") {
		t.Fatalf("expected the live guard error, got %q", call.Error)
	}
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{Symbol: "TQBR:SBER", Strategy: "sma_cross", Live: true}); err == nil ||
		!strings.Contains(err.Error(), "live autonomy is not implemented") {
		t.Fatalf("StartAutonomy live error = %v", err)
	}
}

// TestStartAutonomyTool runs the tool path (non-blocking start + snapshot).
func TestStartAutonomyTool(t *testing.T) {
	e, _, _ := newTestEngine(t)
	call, artifact := e.executeToolCall(context.Background(), "start_autonomy", map[string]any{
		"symbol": "SBER", "periodSec": 300, "strategy": "sma_cross",
	})
	if call.Error != "" {
		t.Fatalf("start_autonomy: %s", call.Error)
	}
	status, ok := call.Result.(AutonomyStatus)
	if !ok {
		t.Fatalf("autonomy result type = %T", call.Result)
	}
	if status.Config.Symbol != "TQBR:SBER" || status.Config.Strategy != "sma_cross" {
		t.Errorf("unexpected autonomy config: %+v", status.Config)
	}
	if status.Config.Budget != defaultAutonomyLimit {
		t.Errorf("budget = %v, want default %v", status.Config.Budget, defaultAutonomyLimit)
	}
	if artifact == nil || artifact.Kind != "autonomy" {
		t.Fatalf("artifact = %+v", artifact)
	}
	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy: %v", err)
	}
}

// TestUnknownTool reports a tool error instead of panicking.
func TestUnknownTool(t *testing.T) {
	e, _, _ := newTestEngine(t)
	call, _ := e.executeToolCall(context.Background(), "no_such_tool", nil)
	if call.Error == "" || !strings.Contains(call.Error, "unknown tool") {
		t.Fatalf("unknown tool error = %q", call.Error)
	}
}

// TestToolDataSourceErrorIsWrapped reports a failed history call as a tool
// error (never a panic).
func TestToolDataSourceErrorIsWrapped(t *testing.T) {
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, &errDataSource{err: context.DeadlineExceeded}, newFakeBroker(1000))
	call, _ := e.executeToolCall(context.Background(), "get_market_hints", map[string]any{"symbol": "SBER"})
	if call.Error == "" {
		t.Fatal("expected a data source error")
	}
}
