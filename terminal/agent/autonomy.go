package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// ErrAlreadyRunning is returned by StartAutonomy when a paper runner is
// already active. It is exported so the HTTP API can map it to 409 conflict.
var ErrAlreadyRunning = errors.New("agent: autonomy is already running")

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
// are loaded and the strategy is built synchronously, then the replay runs in
// its own goroutine. Live autonomy is rejected.
func (e *Engine) StartAutonomy(ctx context.Context, cfg AutonomyConfig) error {
	cfg, bars, strategy, err := e.prepareAutonomy(ctx, cfg)
	if err != nil {
		return err
	}
	startedAt := e.now()
	run := newAutonomyRun(cfg, startedAt)

	e.mu.Lock()
	prev := e.autonomy
	if prev != nil && prev.isRunning() {
		e.mu.Unlock()
		return ErrAlreadyRunning
	}
	e.autonomy = run
	e.mu.Unlock()

	// Seed Running/StartedAt/LastActionAt and the "start" log entry
	// synchronously: the API and the UI must see a started runner immediately
	// after this method returns, not only when the replay goroutine wakes up.
	startEntry := LogEntry{
		Time: startedAt,
		Kind: "start",
		Message: fmt.Sprintf("автономия запущена: %s, стратегия %s, период %s, бюджет %.0f ₽, скорость %.0f бар/с",
			cfg.Symbol, cfg.Strategy, periodLabel(cfg.PeriodSec), cfg.Budget, cfg.Speed),
		Data: map[string]any{"strategy": cfg.Strategy, "params": cfg.Params, "budget": cfg.Budget, "speed": cfg.Speed},
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

// prepareAutonomy validates the config, applies defaults and loads the replay
// bars and strategy. It runs synchronously so validation errors are returned
// from StartAutonomy instead of being lost in the goroutine.
func (e *Engine) prepareAutonomy(ctx context.Context, cfg AutonomyConfig) (AutonomyConfig, []backtest.Bar, backtest.Strategy, error) {
	if cfg.Live {
		return AutonomyConfig{}, nil, nil, errLiveAutonomy
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.Symbol = e.resolveSymbol(cfg.Symbol)
	if cfg.Symbol == "" {
		return AutonomyConfig{}, nil, nil, errors.New("agent: autonomy symbol is required")
	}
	cfg.PeriodSec = e.normalizePeriod(cfg.PeriodSec)
	if cfg.Strategy == "" {
		return AutonomyConfig{}, nil, nil, errors.New("agent: autonomy strategy is required")
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
	strategy, err := e.newBacktestStrategy(cfg.Strategy, cfg.Params)
	if err != nil {
		return AutonomyConfig{}, nil, nil, fmt.Errorf("agent: strategy %q: %w", cfg.Strategy, err)
	}
	bars, err := e.data.History(ctx, cfg.Symbol, cfg.PeriodSec, autonomyBars)
	if err != nil {
		return AutonomyConfig{}, nil, nil, fmt.Errorf("agent: autonomy history for %s: %w", cfg.Symbol, err)
	}
	if len(bars) < 2 {
		return AutonomyConfig{}, nil, nil, fmt.Errorf("agent: not enough history for %s (%d bars)", cfg.Symbol, len(bars))
	}
	return cfg, bars, strategy, nil
}

// runAutonomy replays the bars: the strategy decides on the close of bar i and
// the paper fill happens at the open of bar i+1. Risk limits are enforced
// before every entry; DailyLossPct stops the run automatically.
func (e *Engine) runAutonomy(run *autonomyRun, bars []backtest.Bar, strategy backtest.Strategy) {
	defer close(run.done)

	cfg := run.snapshot().Config
	delay := e.stepDelay(cfg.Speed)

	initial := cfg.Budget
	cash := initial
	var position int64
	var entryPrice, entryFee float64
	var trades int
	tradeTimes := make([]time.Time, 0, 8)
	lastPrice := bars[0].Close
	// lastProcessedTime is the time of the last bar whose open/close the
	// replay actually handled: SSE ticks and barTime must never point at a
	// future bar after an early kill-switch/autostop.
	lastProcessedTime := bars[0].Time
	pending := backtest.Hold

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
		// bar's open.
		switch pending {
		case backtest.Buy:
			if position == 0 && bar.Open > 0 {
				cash, position, entryPrice, entryFee = e.autonomyBuy(run, cash, bar.Open, cfg, bar.Time)
			}
		case backtest.Sell:
			if position > 0 && bar.Open > 0 {
				cash, _ = e.autonomySell(run, cash, position, entryPrice, entryFee, bar.Open, cfg, bar.Time)
				position = 0
				trades++
				tradeTimes = append(tradeTimes, bar.Time)
			}
		}
		pending = backtest.Hold
		lastPrice = bar.Close
		lastProcessedTime = bar.Time

		// Strategy decision on the bar close, overridden by risk exits.
		action := strategy.OnBar(bars, i)
		if position > 0 && entryPrice > 0 {
			changePct := (bar.Close - entryPrice) / entryPrice * 100
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
		if action == backtest.Buy && position == 0 {
			// MaxTradesPerHour lives in Config.Risk: AutonomyConfig has no such field.
			tradeTimes = pruneTradeTimes(tradeTimes, bar.Time.Add(-time.Hour))
			if limit := int(e.cfg.Risk.MaxTradesPerHour); limit > 0 && len(tradeTimes) >= limit {
				e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
					Message: fmt.Sprintf("лимит сделок в час (%d) исчерпан", limit)})
				action = backtest.Hold
			}
		}
		if action == backtest.Sell && position == 0 {
			action = backtest.Hold
		}
		pending = action

		// Mark-to-market PnL of the current position.
		equity := cash + float64(position)*bar.Close
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
			Position:  position,
			Trades:    trades,
			PnL:       pnl,
			PnLPct:    pnlPct,
		}})

		// Automatic kill-switch on the daily loss limit (from the initial
		// equity, as frozen by §9.4). The decision is taken on the close of
		// bar i, so the position is closed at open[i+1] like every other
		// replay action; the last bar falls back to its close explicitly.
		if pnlPct <= -cfg.DailyLossPct {
			e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
				Message: fmt.Sprintf("автостоп: убыток %.2f%% достиг лимита %.2f%%", pnlPct, cfg.DailyLossPct),
				Data:    map[string]any{"equity": equity}})
			if position > 0 {
				if i+1 < len(bars) {
					fillPrice := bars[i+1].Open
					fillTime := bars[i+1].Time
					cash, _ = e.autonomySell(run, cash, position, entryPrice, entryFee, fillPrice, cfg, fillTime)
					lastPrice = fillPrice
					lastProcessedTime = fillTime
				} else {
					e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block",
						Message: fmt.Sprintf("нет следующего бара — закрытие позиции по close последнего бара %.2f ₽", bar.Close)})
					cash, _ = e.autonomySell(run, cash, position, entryPrice, entryFee, bar.Close, cfg, bar.Time)
					lastPrice = bar.Close
					lastProcessedTime = bar.Time
				}
				position = 0
				trades++
				tradeTimes = append(tradeTimes, lastProcessedTime)
			}
			stopKind = "stopped_by_risk"
			stopMessage = fmt.Sprintf("остановлено по дневному убытку %.2f%%", pnlPct)
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
	if position > 0 {
		unrealized = (lastPrice-entryPrice)*float64(position) - entryFee
		e.logAutonomy(run, LogEntry{
			Time: e.now(),
			Kind: "mark_to_market",
			Message: fmt.Sprintf("позиция %d шт оценена mark-to-market по %.2f ₽: нереализованный PnL %+.2f ₽",
				position, lastPrice, unrealized),
			Data: map[string]any{"qty": position, "price": lastPrice, "unrealizedPnl": unrealized, "barTime": lastProcessedTime},
		})
	}

	finalEquity := cash + float64(position)*lastPrice
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
			"pnl": pnl, "pnlPct": pnlPct, "trades": trades, "cash": cash,
			"position": position, "unrealizedPnl": unrealized, "equity": finalEquity,
		}})
	e.publish(Event{Type: "autonomy", Data: autonomyTick{
		Running:   false,
		Symbol:    cfg.Symbol,
		PeriodSec: cfg.PeriodSec,
		Time:      lastProcessedTime,
		Price:     lastPrice,
		Position:  position,
		Trades:    trades,
		PnL:       pnl,
		PnLPct:    pnlPct,
		Final:     true,
	}})
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

// autonomyBuy sizes one paper entry with the frozen risk limits, spends the
// cash, charges the commission and logs the order. It returns the new cash and
// position (position stays zero when the order was blocked).
func (e *Engine) autonomyBuy(run *autonomyRun, cash, price float64, cfg AutonomyConfig, barTime time.Time) (newCash float64, position int64, entryPrice, entryFee float64) {
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
		return cash, 0, 0, 0
	}
	notional := price * float64(qty)
	fee := notional * e.feeBps() / 10000
	if notional+fee > cash {
		e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "risk_block", Message: "недостаточно кэша с учётом комиссии"})
		return cash, 0, 0, 0
	}
	e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "order",
		Message: fmt.Sprintf("покупка %d шт по %.2f ₽ (комиссия %.2f ₽)", qty, price, fee),
		Data:    map[string]any{"side": "buy", "qty": qty, "price": price, "fee": fee, "barTime": barTime}})
	run.update(func(st *AutonomyStatus) { st.LastActionAt = e.now() })
	return cash - notional - fee, qty, price, fee
}

// autonomySell closes the paper position, charges the commission and logs the
// realized trade. It returns the new cash and the realized PnL of the round
// trip (entry commission included).
func (e *Engine) autonomySell(run *autonomyRun, cash float64, position int64, entryPrice, entryFee, price float64, cfg AutonomyConfig, barTime time.Time) (newCash, realized float64) {
	notional := price * float64(position)
	fee := notional * e.feeBps() / 10000
	realized = (price-entryPrice)*float64(position) - entryFee - fee
	e.logAutonomy(run, LogEntry{Time: e.now(), Kind: "fill",
		Message: fmt.Sprintf("продажа %d шт по %.2f ₽: PnL %+.2f ₽ (комиссия %.2f ₽)", position, price, realized, fee),
		Data:    map[string]any{"side": "sell", "qty": position, "price": price, "fee": fee, "pnl": realized, "barTime": barTime}})
	run.update(func(st *AutonomyStatus) { st.LastActionAt = e.now() })
	return cash + notional - fee, realized
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
