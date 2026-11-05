package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// ErrAlreadyRunning is returned by StartAutonomy when a paper runner is
// already active. It is exported so the HTTP API can map it to 409 conflict.
var ErrAlreadyRunning = errors.New("agent: autonomy is already running")

// autonomyAccount is the paper sub-account the replay trades. Its budget is
// transferred from the main account by StartAutonomy via Broker.Allocate, so
// the autonomy never keeps a private cash balance of its own (review R7).
const autonomyAccount = "autonomy"

// moscowOnce guards the lazy Europe/Moscow location used by the trading-day
// accounting.
var (
	moscowOnce sync.Once
	moscowLoc  *time.Location
)

// moscowLocation returns the exchange trading-day location. Moscow has no DST
// since 2014, so a fixed +03:00 zone is a safe fallback when the host has no
// zoneinfo database.
func moscowLocation() *time.Location {
	moscowOnce.Do(func() {
		if loc, err := time.LoadLocation("Europe/Moscow"); err == nil {
			moscowLoc = loc
			return
		}
		moscowLoc = time.FixedZone("MSK", 3*60*60)
	})
	return moscowLoc
}

// moscowTradingDay returns the Moscow calendar day of t as a UTC midnight
// instant, so two instants of the same trading day compare equal.
func moscowTradingDay(t time.Time) time.Time {
	year, month, day := t.In(moscowLocation()).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// autonomyRun is the mutable state of one paper replay run. The status is
// guarded by mu so Status()/snapshot() are safe while the replay goroutine
// updates PnL, trades and the log ring.
type autonomyRun struct {
	mu     sync.Mutex
	status AutonomyStatus

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newAutonomyRun(cfg AutonomyConfig, startedAt time.Time) *autonomyRun {
	return &autonomyRun{
		status: AutonomyStatus{
			Running:      true,
			Config:       cfg,
			StartedAt:    startedAt,
			LastActionAt: startedAt,
			Log:          []LogEntry{},
		},
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// snapshot returns a copy of the current status (the log slice is copied).
func (r *autonomyRun) snapshot() AutonomyStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.status
	st.Log = append([]LogEntry(nil), r.status.Log...)
	if st.Log == nil {
		st.Log = []LogEntry{}
	}
	return st
}

func (r *autonomyRun) isRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status.Running
}

func (r *autonomyRun) update(fn func(*AutonomyStatus)) {
	r.mu.Lock()
	fn(&r.status)
	r.mu.Unlock()
}

// appendLog keeps the log ring bounded to autonomyLogLimit entries.
func (r *autonomyRun) appendLog(entry LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.Log = append(r.status.Log, entry)
	if len(r.status.Log) > autonomyLogLimit {
		r.status.Log = append([]LogEntry(nil), r.status.Log[len(r.status.Log)-autonomyLogLimit:]...)
	}
}

// StartAutonomy starts the paper replay runner. It is non-blocking: the bars
// are loaded and the strategy is built synchronously, the run budget is
// reserved on the paper broker, then the replay runs in its own goroutine.
// Live autonomy is rejected.
func (e *Engine) StartAutonomy(ctx context.Context, cfg AutonomyConfig) error {
	cfg, riskBlocks, bars, strategy, err := e.prepareAutonomy(ctx, cfg)
	if err != nil {
		return err
	}
	startedAt := e.now()

	e.mu.Lock()
	prev := e.autonomy
	if prev != nil && prev.isRunning() {
		e.mu.Unlock()
		return ErrAlreadyRunning
	}
	// Reserve the run budget on the "autonomy" paper account: the replay fills
	// are executed by the broker against this balance, never by a private cash
	// simulation (review R7). The transfer is inside the start critical section
	// so a rejected repeat start cannot move money twice.
	if err := e.broker.Allocate(autonomyAccount, cfg.Budget); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("agent: autonomy budget: %w", err)
	}
	run := newAutonomyRun(cfg, startedAt)
	e.autonomy = run
	e.mu.Unlock()

	// Every value reduced by the engine risk ceilings is logged as risk_block
	// before the "start" line, so the UI explains the effective limits of the
	// run instead of only showing the model's request.
	for _, entry := range riskBlocks {
		run.appendLog(entry)
		e.publish(Event{Type: "log", Data: entry})
	}

	// Seed Running/StartedAt/LastActionAt and the "start" log entry
	// synchronously: the API and the UI must see a started runner immediately
	// after this method returns, not only when the replay goroutine wakes up.
	startEntry := LogEntry{
		Time: startedAt,
		Kind: "start",
		Message: fmt.Sprintf("автономия запущена: %s, стратегия %s, период %s, бюджет %.0f ₽, скорость %.0f бар/с",
			cfg.Symbol, cfg.Strategy, periodLabel(cfg.PeriodSec), cfg.Budget, cfg.Speed),
		Data: map[string]any{"strategy": cfg.Strategy, "params": cfg.Params, "budget": cfg.Budget, "speed": cfg.Speed, "account": autonomyAccount},
	}
	run.appendLog(startEntry)
	e.publish(Event{Type: "log", Data: startEntry})
	e.publish(Event{Type: "autonomy", Data: autonomyTick{
		Running:   true,
		Symbol:    cfg.Symbol,
		PeriodSec: cfg.PeriodSec,
		Time:      startedAt,
		Price:     bars[0].Close,
	}})

	go e.runAutonomy(run, bars, strategy)
	return nil
}

// StopAutonomy is the manual kill-switch: it signals the replay goroutine and
// waits until it has finished, so the final PnL and log are visible to the UI.
func (e *Engine) StopAutonomy() error {
	e.mu.Lock()
	run := e.autonomy
	e.mu.Unlock()
	if run == nil {
		return nil
	}
	run.stopOnce.Do(func() { close(run.stop) })
	<-run.done
	return nil
}

// prepareAutonomy validates the config, applies defaults, clamps it to the
// engine risk limits and loads the replay bars and strategy. It runs
// synchronously so validation errors are returned from StartAutonomy instead of
// being lost in the goroutine.
//
// The returned riskBlocks are risk_block log entries for every value that
// clampAutonomy had to reduce. The autonomy run object does not exist yet while
// the config is prepared, so StartAutonomy appends these entries to the run log
// and emits them over SSE before the "start" line (review finding R2).
func (e *Engine) prepareAutonomy(ctx context.Context, cfg AutonomyConfig) (AutonomyConfig, []LogEntry, []backtest.Bar, backtest.Strategy, error) {
	if cfg.Live {
		return AutonomyConfig{}, nil, nil, nil, errLiveAutonomy
	}
	if e.broker == nil {
		return AutonomyConfig{}, nil, nil, nil, errors.New("agent: paper broker is not configured for autonomy")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.Symbol = e.resolveSymbol(cfg.Symbol)
	if cfg.Symbol == "" {
		return AutonomyConfig{}, nil, nil, nil, errors.New("agent: autonomy symbol is required")
	}
	cfg.PeriodSec = e.normalizePeriod(cfg.PeriodSec)
	if cfg.Strategy == "" {
		return AutonomyConfig{}, nil, nil, nil, errors.New("agent: autonomy strategy is required")
	}
	if cfg.Speed <= 0 {
		cfg.Speed = defaultAutonomySpeed
	}
	if cfg.Speed < 1 {
		cfg.Speed = 1
	}
	if cfg.Speed > 60 {
		cfg.Speed = 60
	}
	if cfg.Budget <= 0 {
		cfg.Budget = defaultAutonomyLimit
	}
	if cfg.MaxPositionPct <= 0 {
		cfg.MaxPositionPct = e.cfg.Risk.MaxPositionPct
	}
	if cfg.StopLossPct <= 0 {
		cfg.StopLossPct = e.cfg.Risk.StopLossPct
	}
	if cfg.TakeProfitPct <= 0 {
		cfg.TakeProfitPct = e.cfg.Risk.TakeProfitPct
	}
	if cfg.DailyLossPct <= 0 {
		cfg.DailyLossPct = e.cfg.Risk.DailyLossPct
	}
	if cfg.Params == nil {
		cfg.Params = map[string]float64{}
	}
	// Risk limits are ceilings: the model may ask for a stricter run but never
	// for a looser one. The free cash of the main paper account bounds the
	// budget reserved for the autonomy account.
	cfg, warnings := clampAutonomy(cfg, e.cfg.Risk, e.broker.Cash())
	riskBlocks := make([]LogEntry, 0, len(warnings))
	for _, warning := range warnings {
		riskBlocks = append(riskBlocks, LogEntry{Time: e.now(), Kind: "risk_block", Message: warning})
	}
	strategy, err := e.newBacktestStrategy(cfg.Strategy, cfg.Params)
	if err != nil {
		return AutonomyConfig{}, nil, nil, nil, fmt.Errorf("agent: strategy %q: %w", cfg.Strategy, err)
	}
	bars, err := e.data.History(ctx, cfg.Symbol, cfg.PeriodSec, autonomyBars)
	if err != nil {
		return AutonomyConfig{}, nil, nil, nil, fmt.Errorf("agent: autonomy history for %s: %w", cfg.Symbol, err)
	}
	if len(bars) < 2 {
		return AutonomyConfig{}, nil, nil, nil, fmt.Errorf("agent: not enough history for %s (%d bars)", cfg.Symbol, len(bars))
	}
	return cfg, riskBlocks, bars, strategy, nil
}

// runAutonomy replays the bars: the strategy decides on the close of bar i and
// the paper fill happens at the open of bar i+1. Every fill is executed by the
// paper broker on the "autonomy" account, so positions, cash and fees are the
// broker's state and the fills are visible in the portfolio/orders views. Risk
// limits are enforced before every entry; the DailyLossPct limit is measured
// from the equity at the start of the Moscow trading day and stops the run.
func (e *Engine) runAutonomy(run *autonomyRun, bars []backtest.Bar, strategy backtest.Strategy) {
	defer close(run.done)

	cfg := run.snapshot().Config
	delay := e.stepDelay(cfg.Speed)

	// initial is the account equity when the replay starts (after the budget
	// was reserved); the total PnL of the run is measured against it.
	initial := e.autonomyEquityAt(bars[0].Open)
	if initial <= 0 {
		initial = 1
	}
	var trades int
	tradeTimes := make([]time.Time, 0, 8)
	lastPrice := bars[0].Close
	// lastProcessedTime is the time of the last bar whose open/close the
	// replay actually handled: SSE ticks and barTime must never point at a
	// future bar after an early kill-switch/autostop.
	lastProcessedTime := bars[0].Time
	pending := backtest.Hold

	// dayStartEquity is the equity at the start of the current Moscow trading
	// day; the daily loss limit is measured against it (review R7).
	day := moscowTradingDay(bars[0].Time)
	dayStartEquity := initial

	stopKind := "stop"
	stopMessage := "replay исторических баров завершён"

replay:
	for i := 0; i < len(bars); i++ {
		select {
		case <-run.stop:
			stopKind, stopMessage = "stop", "остановлено вручную (kill-switch)"
			break replay
		default:
		}

		bar := bars[i]

		// Fill the decision made on the close of the previous bar at this
		// bar's open through the paper broker.
		switch pending {
		case backtest.Buy:
			if qty, _ := e.autonomyPosition(cfg.Symbol); qty == 0 && bar.Open > 0 {
				e.autonomyBuy(run, cfg, bar.Open, bar.Time)
			}
		case backtest.Sell:
			if qty, avg := e.autonomyPosition(cfg.Symbol); qty > 0 && bar.Open > 0 {
				if e.autonomySell(run, cfg.Symbol, qty, avg, bar.Open, bar.Time) {
					trades++
					tradeTimes = append(tradeTimes, bar.Time)
				}
			}
		}
		pending = backtest.Hold
		lastPrice = bar.Close
		lastProcessedTime = bar.Time

		// The first bar of a new Moscow trading day resets the daily loss
		// reference to the equity at that trading day's start.
		if barDay := moscowTradingDay(bar.Time); !barDay.Equal(day) {
			day = barDay
			dayStartEquity = e.autonomyEquityAt(bar.Open)
		}

		// Strategy decision on the bar close, overridden by risk exits.
		action := strategy.OnBar(bars, i)
		qty, avg := e.autonomyPosition(cfg.Symbol)
		if qty > 0 && avg > 0 {
			changePct := (bar.Close - avg) / avg * 100
			switch {
			case changePct <= -cfg.StopLossPct:
				e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
					Message: fmt.Sprintf("стоп-лосс: %.2f%% от входа", changePct)})
				action = backtest.Sell
			case changePct >= cfg.TakeProfitPct:
				e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "signal",
					Message: fmt.Sprintf("тейк-профит: +%.2f%% от входа", changePct)})
				action = backtest.Sell
			}
		}
		if action == backtest.Buy && qty == 0 {
			// MaxTradesPerHour lives in Config.Risk: AutonomyConfig has no such field.
			tradeTimes = pruneTradeTimes(tradeTimes, bar.Time.Add(-time.Hour))
			if limit := int(e.cfg.Risk.MaxTradesPerHour); limit > 0 && len(tradeTimes) >= limit {
				e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
					Message: fmt.Sprintf("лимит сделок в час (%d) исчерпан", limit)})
				action = backtest.Hold
			}
		}
		if action == backtest.Sell && qty == 0 {
			action = backtest.Hold
		}
		pending = action

		// Mark-to-market PnL of the autonomy account.
		equity := e.autonomyEquityAt(bar.Close)
		pnl := equity - initial
		pnlPct := pnl / initial * 100
		run.update(func(st *AutonomyStatus) {
			st.PnL = pnl
			st.PnLPct = pnlPct
			st.Trades = trades
		})
		e.publish(Event{Type: "autonomy", Data: autonomyTick{
			Running:   true,
			Symbol:    cfg.Symbol,
			PeriodSec: cfg.PeriodSec,
			Time:      bar.Time,
			Price:     bar.Close,
			Position:  qty,
			Trades:    trades,
			PnL:       pnl,
			PnLPct:    pnlPct,
		}})

		// Automatic kill-switch on the daily loss of the Moscow trading day.
		// The decision is taken on the close of bar i, so the position is
		// closed at open[i+1] like every other replay action; the last bar
		// falls back to its close explicitly.
		dailyPct := 0.0
		if dayStartEquity > 0 {
			dailyPct = (equity - dayStartEquity) / dayStartEquity * 100
		}
		if dailyPct <= -cfg.DailyLossPct {
			e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
				Message: fmt.Sprintf("автостоп: дневной убыток %.2f%% от начала торгового дня (МСК) достиг лимита %.2f%%",
					dailyPct, cfg.DailyLossPct),
				Data: map[string]any{"equity": equity, "dayStartEquity": dayStartEquity}})
			if qty > 0 {
				fillPrice, fillTime := bar.Close, bar.Time
				if i+1 < len(bars) {
					fillPrice, fillTime = bars[i+1].Open, bars[i+1].Time
				} else {
					e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
						Message: fmt.Sprintf("нет следующего бара — закрытие позиции по close последнего бара %.2f ₽", bar.Close)})
				}
				if e.autonomySell(run, cfg.Symbol, qty, avg, fillPrice, fillTime) {
					trades++
					tradeTimes = append(tradeTimes, fillTime)
				}
				lastPrice = fillPrice
				lastProcessedTime = fillTime
			}
			stopKind = "stopped_by_risk"
			stopMessage = fmt.Sprintf("остановлено по дневному убытку %.2f%% (лимит %.2f%% на торговый день МСК)",
				dailyPct, cfg.DailyLossPct)
			break replay
		}

		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-run.stop:
				timer.Stop()
				stopKind, stopMessage = "stop", "остановлено вручную (kill-switch)"
				break replay
			case <-timer.C:
			}
		}
	}

	// An open position at the end of a finished/interrupted replay is valued
	// mark-to-market: §9.4 and the backtest engine keep open positions open, so
	// there is no fictitious closing trade and no extra commission. Trades
	// counts only closed round trips.
	var unrealized float64
	qty, avg := e.autonomyPosition(cfg.Symbol)
	if qty > 0 {
		unrealized = (lastPrice - avg) * float64(qty)
		e.logAutonomy(run, LogEntry{
			Time: e.now(),
			Kind: "mark_to_market",
			Message: fmt.Sprintf("позиция %d шт оценена mark-to-market по %.2f ₽: нереализованный PnL %+.2f ₽",
				qty, lastPrice, unrealized),
			Data: map[string]any{"qty": qty, "price": lastPrice, "unrealizedPnl": unrealized, "barTime": lastProcessedTime},
		})
	}

	view := e.autonomyAccountView()
	finalEquity := e.autonomyEquityAt(lastPrice)
	pnl := finalEquity - initial
	pnlPct := pnl / initial * 100
	run.update(func(st *AutonomyStatus) {
		st.Running = false
		st.PnL = pnl
		st.PnLPct = pnlPct
		st.Trades = trades
		// The stop/finish instant is an action too: the UI shows it in
		// lastActionAt right after StopAutonomy/autostop returns.
		st.LastActionAt = e.now()
	})
	e.logAutonomy(run, LogEntry{Time: e.now(), Kind: stopKind, Message: stopMessage,
		Data: map[string]any{
			"pnl": pnl, "pnlPct": pnlPct, "trades": trades, "cash": view.Cash,
			"position": qty, "unrealizedPnl": unrealized, "equity": finalEquity, "account": autonomyAccount,
		}})
	e.publish(Event{Type: "autonomy", Data: autonomyTick{
		Running:   false,
		Symbol:    cfg.Symbol,
		PeriodSec: cfg.PeriodSec,
		Time:      lastProcessedTime,
		Price:     lastPrice,
		Position:  qty,
		Trades:    trades,
		PnL:       pnl,
		PnLPct:    pnlPct,
		Final:     true,
	}})
}

// autonomyAccountView returns the paper sub-account state of the autonomy run.
func (e *Engine) autonomyAccountView() AccountView {
	if e.broker == nil {
		return AccountView{}
	}
	portfolio := e.broker.Portfolio()
	if portfolio.Accounts == nil {
		return AccountView{}
	}
	return portfolio.Accounts[autonomyAccount]
}

// autonomyEquityAt returns the autonomy account equity at the given price:
// cash plus the mark-to-market value of its positions.
func (e *Engine) autonomyEquityAt(price float64) float64 {
	view := e.autonomyAccountView()
	equity := view.Cash
	for _, pos := range view.Positions {
		equity += float64(pos.Qty) * price
	}
	return equity
}

// autonomyPosition returns the quantity and average entry price of one symbol
// on the autonomy account.
func (e *Engine) autonomyPosition(symbol string) (qty int64, avg float64) {
	for _, pos := range e.autonomyAccountView().Positions {
		if strings.EqualFold(pos.Symbol, symbol) {
			return pos.Qty, pos.AvgPrice
		}
	}
	return 0, 0
}

// autonomyOrder routes one replay fill through the paper broker: the quote is
// the historical bar price, so cash, positions and fees stay broker-owned.
func (e *Engine) autonomyOrder(symbol, side string, qty int64, price float64, at time.Time) (OrderView, error) {
	return e.broker.PlaceAt(
		PlaceOrder{Symbol: symbol, Side: side, Qty: qty, Type: "market"},
		Quote{Symbol: symbol, Last: price, Time: at},
		at, autonomyAccount)
}

// autonomyTick is the SSE payload of one replay step.
type autonomyTick struct {
	Running   bool      `json:"running"`
	Symbol    string    `json:"symbol"`
	PeriodSec int       `json:"periodSec"`
	Time      time.Time `json:"time"`
	Price     float64   `json:"price"`
	Position  int64     `json:"position"`
	Trades    int       `json:"trades"`
	PnL       float64   `json:"pnl"`
	PnLPct    float64   `json:"pnlPct"`
	Final     bool      `json:"final,omitempty"`
}

// autonomyBuy sizes one paper entry with the frozen risk limits, routes the
// order through the paper broker and logs it. It returns the filled quantity
// (zero when the entry was blocked or rejected).
func (e *Engine) autonomyBuy(run *autonomyRun, cfg AutonomyConfig, price float64, barTime time.Time) int64 {
	cash := e.autonomyAccountView().Cash
	if cash <= 0 {
		e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
			Message: "недостаточно средств: кэш счёта автономии пуст"})
		return 0
	}
	// MaxOrderPct lives in Config.Risk: AutonomyConfig has no such field.
	notionalLimit := cash * e.cfg.Risk.MaxOrderPct / 100
	if cfg.MaxPositionPct > 0 {
		if posLimit := cash * cfg.MaxPositionPct / 100; posLimit < notionalLimit {
			notionalLimit = posLimit
		}
	}
	if notionalLimit > cash {
		notionalLimit = cash
	}
	qty := int64(notionalLimit / price)
	if qty < 1 {
		e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
			Message: fmt.Sprintf("недостаточно средств: лимит заявки %.2f ₽ меньше цены %.2f ₽", notionalLimit, price)})
		return 0
	}
	order, err := e.autonomyOrder(cfg.Symbol, "buy", qty, price, barTime)
	if err != nil {
		e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
			Message: fmt.Sprintf("заявка на покупку отклонена: %v", err)})
		return 0
	}
	if order.Filled <= 0 {
		e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block", Message: "заявка на покупку не исполнена"})
		return 0
	}
	notional := order.AvgFillPrice * float64(order.Filled)
	fee := notional * e.feeBps() / 10000
	e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "order",
		Message: fmt.Sprintf("покупка %d шт по %.2f ₽ (комиссия %.2f ₽)", order.Filled, order.AvgFillPrice, fee),
		Data:    map[string]any{"side": "buy", "qty": order.Filled, "price": order.AvgFillPrice, "fee": fee, "barTime": barTime}})
	run.update(func(st *AutonomyStatus) { st.LastActionAt = e.now() })
	return order.Filled
}

// autonomySell closes the paper position through the broker and logs the
// realized trade. It returns true when the position was actually closed.
func (e *Engine) autonomySell(run *autonomyRun, symbol string, qty int64, avgPrice, price float64, barTime time.Time) bool {
	if qty <= 0 {
		return false
	}
	order, err := e.autonomyOrder(symbol, "sell", qty, price, barTime)
	if err != nil {
		e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
			Message: fmt.Sprintf("заявка на продажу отклонена: %v", err)})
		return false
	}
	if order.Filled <= 0 {
		e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block", Message: "заявка на продажу не исполнена"})
		return false
	}
	realized := (order.AvgFillPrice - avgPrice) * float64(order.Filled)
	fee := order.AvgFillPrice * float64(order.Filled) * e.feeBps() / 10000
	e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "fill",
		Message: fmt.Sprintf("продажа %d шт по %.2f ₽: PnL %+.2f ₽ (комиссия %.2f ₽)", order.Filled, order.AvgFillPrice, realized, fee),
		Data:    map[string]any{"side": "sell", "qty": order.Filled, "price": order.AvgFillPrice, "fee": fee, "pnl": realized, "barTime": barTime}})
	run.update(func(st *AutonomyStatus) { st.LastActionAt = e.now() })
	return true
}

// logAutonomy appends an entry to the bounded log ring and emits it as an SSE
// "log" event.
func (e *Engine) logAutonomy(run *autonomyRun, entry LogEntry) {
	if entry.Time.IsZero() {
		entry.Time = e.now()
	}
	run.appendLog(entry)
	e.publish(Event{Type: "log", Data: entry})
}

// pruneTradeTimes drops trade stamps older than the given instant.
func pruneTradeTimes(times []time.Time, cutoff time.Time) []time.Time {
	out := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}
