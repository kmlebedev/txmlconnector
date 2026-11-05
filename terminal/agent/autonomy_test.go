package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// TestAutonomyReplayPnL checks the paper replay: fill at the next bar open,
// mark-to-market PnL, trades counter and log.
func TestAutonomyReplayPnL(t *testing.T) {
	e, data, _ := newTestEngine(t)
	bars := syntheticBars(10, 100, 1) // bar i open = 100+i, close = 100+i+0.5
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: bars}
	e.newStrategy = strategyScript(map[int]backtest.Action{
		2: backtest.Buy,  // decided on close of bar 2 → filled at open of bar 3 (103)
		6: backtest.Sell, // decided on close of bar 6 → filled at open of bar 7 (107)
	})

	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Running {
		t.Fatal("autonomy still running")
	}
	if status.Trades != 1 {
		t.Errorf("Trades = %d, want 1", status.Trades)
	}
	if status.PnL <= 0 {
		t.Errorf("PnL = %v, want positive on rising bars", status.PnL)
	}
	wantPnLPct := status.PnL / status.Config.Budget * 100
	if diff := status.PnLPct - wantPnLPct; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("PnLPct = %v, want %v", status.PnLPct, wantPnLPct)
	}
	if !status.StartedAt.Equal(testNow) {
		t.Errorf("StartedAt = %v, want %v", status.StartedAt, testNow)
	}
	kinds := make(map[string]int)
	for _, entry := range status.Log {
		kinds[entry.Kind]++
	}
	if kinds["start"] != 1 || kinds["order"] != 1 || kinds["fill"] != 1 || kinds["stop"] != 1 {
		t.Errorf("log kinds = %v (log: %+v)", kinds, status.Log)
	}
	var entryPrice float64
	for _, entry := range status.Log {
		if entry.Kind == "order" && strings.Contains(entry.Message, "103.00") {
			entryPrice = 103
		}
	}
	if entryPrice != 103 {
		t.Errorf("entry was not filled at the next bar open (103 ₽); log: %+v", status.Log)
	}
}

// TestAutonomySynchronousStartStopStatus is the regression test for the
// "lastActionAt = 0001-01-01" defect: StartAutonomy must expose Running,
// StartedAt, LastActionAt and the start log entry synchronously before it
// returns; StopAutonomy must set LastActionAt to the stop instant and add the
// stop log entry before returning.
func TestAutonomySynchronousStartStopStatus(t *testing.T) {
	now := testNow
	e := newTestEngineWith(t, Config{
		DefaultSymbol: "TQBR:SBER",
		Now:           func() time.Time { return now },
	}, newFakeDataSource(), newFakeBroker(1_000_000))
	e.stepDelay = func(float64) time.Duration { return 20 * time.Millisecond }

	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross",
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	// No polling: the status is filled synchronously by StartAutonomy.
	started := e.Status().Autonomy
	if !started.Running {
		t.Error("Running = false right after StartAutonomy")
	}
	if !started.StartedAt.Equal(testNow) {
		t.Errorf("StartedAt = %v, want %v", started.StartedAt, testNow)
	}
	if !started.LastActionAt.Equal(testNow) {
		t.Errorf("LastActionAt = %v, want the start time %v", started.LastActionAt, testNow)
	}
	if len(started.Log) != 1 || started.Log[0].Kind != "start" {
		t.Errorf("log right after StartAutonomy = %+v, want one start entry", started.Log)
	}

	now = now.Add(time.Minute)
	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy: %v", err)
	}
	stopped := e.Status().Autonomy
	if stopped.Running {
		t.Error("Running = true after StopAutonomy")
	}
	if !stopped.StartedAt.Equal(testNow) {
		t.Errorf("StartedAt changed to %v on stop", stopped.StartedAt)
	}
	if !stopped.LastActionAt.Equal(now) {
		t.Errorf("LastActionAt = %v, want the stop time %v", stopped.LastActionAt, now)
	}
	if last := stopped.Log[len(stopped.Log)-1]; last.Kind != "stop" {
		t.Errorf("last log entry = %+v, want stop", last)
	}
}

// TestAutonomyAlreadyRunningExportedError checks the exported 409 sentinel.
func TestAutonomyAlreadyRunningExportedError(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.stepDelay = func(float64) time.Duration { return 10 * time.Millisecond }
	cfg := AutonomyConfig{Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross"}
	if err := e.StartAutonomy(context.Background(), cfg); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	err := e.StartAutonomy(context.Background(), cfg)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second StartAutonomy error = %v, want ErrAlreadyRunning", err)
	}
	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy: %v", err)
	}
}

// TestAutonomyDailyLossFillsAtNextOpen is the P2 regression test: the daily
// loss decision is taken on the close of bar i, so the position must be closed
// at open[i+1] (90.50), not at the triggering close (93.30).
func TestAutonomyDailyLossFillsAtNextOpen(t *testing.T) {
	e, data, _ := newTestEngine(t)
	minute := time.Minute
	bars := []backtest.Bar{
		{Time: testNow, Open: 100, High: 100.5, Low: 99.5, Close: 100, Volume: 1},
		{Time: testNow.Add(minute), Open: 100, High: 100.5, Low: 99.5, Close: 100, Volume: 1},
		{Time: testNow.Add(2 * minute), Open: 100, High: 100.6, Low: 93.0, Close: 93.3, Volume: 1},
		{Time: testNow.Add(3 * minute), Open: 90.5, High: 91.0, Low: 89.5, Close: 90.0, Volume: 1},
		{Time: testNow.Add(4 * minute), Open: 90.0, High: 90.5, Low: 89.0, Close: 90.2, Volume: 1},
	}
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: bars}
	e.newStrategy = strategyScript(map[int]backtest.Action{0: backtest.Buy})
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
		StopLossPct: 90, TakeProfitPct: 90, DailyLossPct: 1,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Trades != 1 {
		t.Errorf("Trades = %d, want 1 autostop exit", status.Trades)
	}
	var sellMessage string
	foundRisk := false
	for _, entry := range status.Log {
		if strings.HasPrefix(entry.Message, "продажа") {
			sellMessage = entry.Message
		}
		if entry.Kind == "stopped_by_risk" {
			foundRisk = true
		}
	}
	if !strings.Contains(sellMessage, "90.50") {
		t.Errorf("autostop exit = %q, want fill at open of the next bar (90.50)", sellMessage)
	}
	if strings.Contains(sellMessage, "93.30") {
		t.Errorf("autostop exit used the triggering close, not open[i+1]: %q", sellMessage)
	}
	if !foundRisk {
		t.Errorf("no stopped_by_risk entry: %+v", status.Log)
	}
}

// TestAutonomyNaturalFinishOpenPositionMTM is the P3 regression test: a
// position left open at the natural end of the replay is valued
// mark-to-market (no fictitious closing trade, no extra commission, Trades
// unchanged).
func TestAutonomyNaturalFinishOpenPositionMTM(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: syntheticBars(10, 100, 1)}
	e.newStrategy = strategyScript(map[int]backtest.Action{0: backtest.Buy})
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
		StopLossPct: 90, TakeProfitPct: 90, // keep the position open until the replay ends
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Trades != 0 {
		t.Errorf("Trades = %d, want 0 (open position is not a closed round trip)", status.Trades)
	}
	if status.PnL <= 0 {
		t.Errorf("PnL = %v, want a positive mark-to-market value on rising bars", status.PnL)
	}
	foundMTM := false
	for _, entry := range status.Log {
		if entry.Kind == "fill" {
			t.Errorf("unexpected fictitious closing trade: %+v", entry)
		}
		if entry.Kind == "mark_to_market" {
			foundMTM = true
			data, ok := entry.Data.(map[string]any)
			if !ok {
				t.Fatalf("mark_to_market data = %T", entry.Data)
			}
			if unrealized, ok := data["unrealizedPnl"].(float64); !ok || unrealized <= 0 {
				t.Errorf("unrealizedPnl = %v, want > 0", data["unrealizedPnl"])
			}
		}
	}
	if !foundMTM {
		t.Fatalf("no mark_to_market log entry: %+v", status.Log)
	}
}

// TestAutonomyEarlyStopFinalTickTime is the P3 regression test: after a kill
// switch in the middle of the replay the final SSE tick must carry the time of
// the last processed bar, not bars[len-1].Time.
func TestAutonomyEarlyStopFinalTickTime(t *testing.T) {
	e, data, _ := newTestEngine(t)
	const totalBars = 60
	bars := syntheticBars(totalBars, 100, 1)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: bars}

	var mu sync.Mutex
	var ticks []autonomyTick
	cancel := e.Subscribe(func(ev Event) {
		if ev.Type != "autonomy" {
			return
		}
		tick, ok := ev.Data.(autonomyTick)
		if !ok {
			return
		}
		mu.Lock()
		ticks = append(ticks, tick)
		mu.Unlock()
	})
	defer cancel()

	e.stepDelay = func(float64) time.Duration { return 30 * time.Millisecond }
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy: %v", err)
	}

	mu.Lock()
	captured := append([]autonomyTick(nil), ticks...)
	mu.Unlock()
	var final *autonomyTick
	for i := range captured {
		if captured[i].Final {
			final = &captured[i]
		}
	}
	if final == nil {
		t.Fatalf("no final autonomy tick in %d events", len(captured))
	}
	lastBarTime := bars[len(bars)-1].Time
	if !final.Time.Before(lastBarTime) {
		t.Errorf("final tick time = %v, must be before the unprocessed last bar %v", final.Time, lastBarTime)
	}
	if final.Time.Before(bars[0].Time) {
		t.Errorf("final tick time = %v, before the first bar %v", final.Time, bars[0].Time)
	}
}

// TestAutonomyDefaults checks the frozen defaults of Speed/Budget/risk.
func TestAutonomyDefaults(t *testing.T) {
	e, _, _ := newTestEngine(t)
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", Strategy: "sma_cross",
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Config.Speed != defaultAutonomySpeed {
		t.Errorf("Speed = %v, want %v", status.Config.Speed, defaultAutonomySpeed)
	}
	if status.Config.Budget != defaultAutonomyLimit {
		t.Errorf("Budget = %v, want %v", status.Config.Budget, defaultAutonomyLimit)
	}
	if status.Config.PeriodSec != 300 {
		t.Errorf("PeriodSec = %d, want 300", status.Config.PeriodSec)
	}
	if status.Config.MaxPositionPct != 25 || status.Config.StopLossPct != 2 ||
		status.Config.TakeProfitPct != 5 || status.Config.DailyLossPct != 3 {
		t.Errorf("risk defaults = %+v", status.Config)
	}
}

// TestAutonomyDailyLossAutoStop verifies the automatic kill-switch.
func TestAutonomyDailyLossAutoStop(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: crashBars(30)}
	e.newStrategy = strategyScript(map[int]backtest.Action{0: backtest.Buy})

	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60,
		Budget: 100_000, StopLossPct: 90, TakeProfitPct: 90, DailyLossPct: 1,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Running {
		t.Fatal("autonomy must stop itself on the daily loss limit")
	}
	if status.PnLPct > -1 {
		t.Errorf("PnLPct = %v, want <= -1", status.PnLPct)
	}
	foundAutoStop := false
	for _, entry := range status.Log {
		if entry.Kind == "stopped_by_risk" {
			foundAutoStop = true
		}
		if entry.Kind == "risk_block" && strings.Contains(entry.Message, "автостоп") {
			foundAutoStop = true
		}
	}
	if !foundAutoStop {
		t.Fatalf("no stopped_by_risk log entry: %+v", status.Log)
	}
}

// TestAutonomyStopLoss verifies the per-position stop-loss.
func TestAutonomyStopLoss(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: crashBars(15)}
	e.newStrategy = strategyScript(map[int]backtest.Action{0: backtest.Buy})

	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60,
		Budget: 100_000, StopLossPct: 1, TakeProfitPct: 90, DailyLossPct: 90,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Trades != 1 {
		t.Errorf("Trades = %d, want 1 (stop-loss exit)", status.Trades)
	}
	foundStopLoss := false
	for _, entry := range status.Log {
		if entry.Kind == "risk_block" && strings.Contains(entry.Message, "стоп-лосс") {
			foundStopLoss = true
		}
	}
	if !foundStopLoss {
		t.Fatalf("no stop-loss log entry: %+v", status.Log)
	}
}

// TestAutonomyMaxTradesPerHour verifies the trades-per-hour limit blocks the
// second entry.
func TestAutonomyMaxTradesPerHour(t *testing.T) {
	data := newFakeDataSource()
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: syntheticBars(10, 100, 1)}
	e := newTestEngineWith(t, Config{
		DefaultSymbol: "TQBR:SBER",
		Risk:          RiskLimits{MaxTradesPerHour: 1},
	}, data, newFakeBroker(1_000_000))
	e.newStrategy = strategyScript(map[int]backtest.Action{
		0: backtest.Buy, 2: backtest.Sell,
		4: backtest.Buy, 6: backtest.Sell,
	})
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Trades != 1 {
		t.Errorf("Trades = %d, want 1 (second entry blocked)", status.Trades)
	}
	foundLimit := false
	for _, entry := range status.Log {
		if entry.Kind == "risk_block" && strings.Contains(entry.Message, "лимит сделок в час") {
			foundLimit = true
		}
	}
	if !foundLimit {
		t.Fatalf("no max-trades-per-hour log entry: %+v", status.Log)
	}
}

// TestAutonomyKillSwitchAndRestart checks the manual stop waits for the
// goroutine and that a second run can be started afterwards.
func TestAutonomyKillSwitchAndRestart(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.stepDelay = func(float64) time.Duration { return 2 * time.Millisecond }
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 5, Budget: 100_000,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("StopAutonomy took %v, want a prompt goroutine join", elapsed)
	}
	status := e.Status().Autonomy
	if status.Running {
		t.Fatal("autonomy still running after StopAutonomy")
	}
	last := status.Log[len(status.Log)-1]
	if last.Kind != "stop" || !strings.Contains(last.Message, "kill-switch") {
		t.Errorf("last log = %+v, want a manual stop", last)
	}
	// A finished run may be replaced by a new one.
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross",
	}); err != nil {
		t.Fatalf("restart autonomy: %v", err)
	}
	waitAutonomy(t, e)
}

// TestAutonomyRepeatedStartRejected rejects a start while one is running.
func TestAutonomyRepeatedStartRejected(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.stepDelay = func(float64) time.Duration { return 5 * time.Millisecond }
	cfg := AutonomyConfig{Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 5}
	if err := e.StartAutonomy(context.Background(), cfg); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	err := e.StartAutonomy(context.Background(), cfg)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second StartAutonomy error = %v, want ErrAlreadyRunning", err)
	}
	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy: %v", err)
	}
}

// TestAutonomyRejectsInvalidConfig covers the synchronous validation.
func TestAutonomyRejectsInvalidConfig(t *testing.T) {
	e, _, _ := newTestEngine(t)
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{Symbol: "TQBR:SBER"}); err == nil {
		t.Fatal("expected an error for a missing strategy")
	}
	e.newStrategy = func(string, map[string]float64) (backtest.Strategy, error) {
		return nil, context.DeadlineExceeded
	}
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", Strategy: "unknown",
	}); err == nil {
		t.Fatal("expected an error for an unknown strategy")
	}

	// An empty symbol falls back to the configured default symbol.
	e2, _, _ := newTestEngine(t)
	if err := e2.StartAutonomy(context.Background(), AutonomyConfig{Strategy: "sma_cross"}); err != nil {
		t.Fatalf("StartAutonomy with default symbol: %v", err)
	}
	waitAutonomy(t, e2)
}

// TestAutonomyLogRing checks the 500-entry ring buffer bound.
func TestAutonomyLogRing(t *testing.T) {
	run := newAutonomyRun(AutonomyConfig{}, testNow)
	for i := 0; i < autonomyLogLimit+150; i++ {
		run.appendLog(LogEntry{Kind: "log", Message: "line"})
	}
	if got := len(run.snapshot().Log); got != autonomyLogLimit {
		t.Fatalf("log ring = %d entries, want %d", got, autonomyLogLimit)
	}
}

// TestAutonomyStopIsIdempotent makes sure repeated stops do not panic.
func TestAutonomyStopIsIdempotent(t *testing.T) {
	e, _, _ := newTestEngine(t)
	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy on a never-started engine: %v", err)
	}
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross",
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := e.StopAutonomy(); err != nil {
			t.Fatalf("StopAutonomy #%d: %v", i, err)
		}
	}
}

// TestAutonomyFillsVisibleInPortfolio is the R7 regression test: the replay
// executes its fills through the paper broker on the "autonomy" account, so the
// order history and the portfolio account contain the autonomy trades, and the
// main account is only debited by the reserved budget.
func TestAutonomyFillsVisibleInPortfolio(t *testing.T) {
	e, data, broker := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: syntheticBars(12, 100, 1)}
	e.newStrategy = strategyScript(map[int]backtest.Action{1: backtest.Buy, 5: backtest.Sell})
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
		StopLossPct: 90, TakeProfitPct: 90,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}
	status := waitAutonomy(t, e)
	if status.Trades != 1 {
		t.Errorf("Trades = %d, want 1 closed round trip", status.Trades)
	}

	// The /api/orders view is the global broker order history.
	orders := broker.Orders()
	if len(orders) != 2 {
		t.Fatalf("broker orders = %d, want the two autonomy fills visible", len(orders))
	}
	if orders[0].Side != "buy" || orders[1].Side != "sell" || orders[1].Filled <= 0 {
		t.Errorf("autonomy orders = %+v", orders)
	}

	portfolio := broker.Portfolio()
	acc, ok := portfolio.Accounts["autonomy"]
	if !ok {
		t.Fatalf("no autonomy account in portfolio %+v", portfolio.Accounts)
	}
	if acc.Orders != 2 || acc.Trades != 1 {
		t.Errorf("autonomy account orders/trades = %d/%d, want 2/1", acc.Orders, acc.Trades)
	}
	if acc.RealizedPnL <= 0 {
		t.Errorf("autonomy realized PnL = %v, want > 0 on rising bars", acc.RealizedPnL)
	}
	if len(acc.Positions) != 0 {
		t.Errorf("autonomy positions = %+v, want flat after the sell", acc.Positions)
	}
	if status.PnL <= 0 {
		t.Errorf("status PnL = %v, want > 0", status.PnL)
	}

	// The main account only lost the reserved budget, not the fills.
	main, ok := portfolio.Accounts["main"]
	if !ok {
		t.Fatalf("no main account in portfolio %+v", portfolio.Accounts)
	}
	if main.Cash != 900_000 {
		t.Errorf("main cash = %v, want 900000 after the 100000 budget reservation", main.Cash)
	}
	if len(main.Positions) != 0 {
		t.Errorf("main positions = %+v, want none: the replay must not trade it", main.Positions)
	}
}

// moscowTestDayBars builds four bars of one Moscow trading day starting at
// 10:00 MSK; the second bar dips to dipPrice and the third recovers to 100.
func moscowTestDayBars(dayOffset int, dipPrice float64) []backtest.Bar {
	msk := time.FixedZone("MSK", 3*60*60)
	start := time.Date(2026, 11, 5+dayOffset, 10, 0, 0, 0, msk)
	return []backtest.Bar{
		{Time: start, Open: 100, High: 100, Low: 100, Close: 100},
		{Time: start.Add(30 * time.Minute), Open: 100, High: 100, Low: dipPrice, Close: dipPrice},
		{Time: start.Add(time.Hour), Open: dipPrice, High: 100, Low: dipPrice, Close: 100},
		{Time: start.Add(90 * time.Minute), Open: 100, High: 100, Low: 100, Close: 100},
	}
}

// TestDailyLossResetsPerMoscowDay is the R7 regression test: the daily loss is
// measured from the equity at the start of the Moscow trading day, so three
// consecutive -2.9% days do not stop the runner, while a -3.1% move inside one
// trading day triggers stopped_by_risk.
func TestDailyLossResetsPerMoscowDay(t *testing.T) {
	const budget = 100_000.0
	newEngine := func(t *testing.T, bars []backtest.Bar) (*Engine, *fakeBroker) {
		t.Helper()
		data := newFakeDataSource()
		data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: bars}
		broker := newFakeBroker(1_000_000)
		e := newTestEngineWith(t, Config{
			DefaultSymbol: "TQBR:SBER",
			Risk: RiskLimits{
				MaxPositionPct: 100, MaxOrderPct: 99,
				StopLossPct: 90, TakeProfitPct: 90,
				DailyLossPct: 3, MaxTradesPerHour: 30,
			},
		}, data, broker)
		e.newStrategy = strategyScript(map[int]backtest.Action{0: backtest.Buy})
		if err := e.StartAutonomy(context.Background(), AutonomyConfig{
			Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: budget,
			StopLossPct: 90, TakeProfitPct: 90, DailyLossPct: 3, MaxPositionPct: 100,
		}); err != nil {
			t.Fatalf("StartAutonomy: %v", err)
		}
		return e, broker
	}

	// Three trading days, each dipping -2.9% from that day's starting equity:
	// the runner must survive all of them.
	var threeDays []backtest.Bar
	for day := 0; day < 3; day++ {
		threeDays = append(threeDays, moscowTestDayBars(day, 97.12)...)
	}
	e, broker := newEngine(t, threeDays)
	status := waitAutonomy(t, e)
	for _, entry := range status.Log {
		if entry.Kind == "stopped_by_risk" {
			t.Fatalf("runner stopped on a -2.9%% day: %+v", entry)
		}
		if strings.Contains(entry.Message, "дневной убыток") {
			t.Fatalf("daily loss limit fired on a -2.9%% day: %+v", entry)
		}
	}
	if status.Trades != 0 {
		t.Errorf("Trades = %d, want 0 (the position stays open)", status.Trades)
	}
	if _, ok := broker.Portfolio().Accounts[autonomyAccount]; !ok {
		t.Errorf("autonomy account missing after the replay")
	}

	// A -3.1% move inside a single Moscow trading day stops the runner.
	e, _ = newEngine(t, moscowTestDayBars(0, 96.8))
	status = waitAutonomy(t, e)
	foundRisk := false
	for _, entry := range status.Log {
		if entry.Kind == "stopped_by_risk" {
			foundRisk = true
		}
		if entry.Kind == "risk_block" && strings.Contains(entry.Message, "дневной убыток") {
			foundRisk = true
		}
	}
	if !foundRisk {
		t.Fatalf("no stopped_by_risk entry for a -3.1%% day: %+v", status.Log)
	}
	if status.Trades != 1 {
		t.Errorf("Trades = %d, want 1 (the autostop closed the position)", status.Trades)
	}
}
