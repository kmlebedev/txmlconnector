package bot

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// Manager owns every bot of the terminal: creation from backtests, the one-shot
// check calculation, the check/dry-run/paper lifecycle and the journal
// subscription. All methods are safe for concurrent use.
type Manager struct {
	mu     sync.Mutex
	bots   map[string]*state
	order  []string
	seq    int
	dir    string
	closed bool

	feed     BarStream
	broker   Broker
	approver Approver
	now      func() time.Time
	risk     RiskLimits
	// budget is the paper budget transferred to "bot:<id>" on the first paper
	// start (defaultPaperBudget unless SetBudget was called).
	budget float64

	subsMu sync.Mutex
	subs   map[uint64]func(BotEvent)
	subSeq uint64
}

// state is the mutable runtime state of one bot. Its mu guards the bot, the
// replay window and the paper funding flags; the journal store has its own
// lock.
type state struct {
	mu      sync.Mutex
	bot     Bot
	journal *journalStore

	cancel  context.CancelFunc
	running bool

	bars        []backtest.Bar
	lastBar     time.Time
	tradeTimes  []time.Time
	paperFunded bool
}

// NewManager builds the bot runtime. The approver may be nil: promotion to the
// paper stage then fails with ErrApprovalMissing.
func NewManager(feed BarStream, broker Broker, approver Approver, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{
		bots:     map[string]*state{},
		feed:     feed,
		broker:   broker,
		approver: approver,
		now:      now,
		risk:     RiskLimits{MaxOrderPct: 20, MaxPositionPct: 25, MaxTradesPerHour: 30},
		budget:   defaultPaperBudget,
		subs:     map[uint64]func(BotEvent){},
	}
}

// SetRiskLimits overrides the bot guard rails; zero or negative fields keep the
// current value, so a partial override is safe.
func (m *Manager) SetRiskLimits(limits RiskLimits) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limits.MaxOrderPct > 0 {
		m.risk.MaxOrderPct = limits.MaxOrderPct
	}
	if limits.MaxPositionPct > 0 {
		m.risk.MaxPositionPct = limits.MaxPositionPct
	}
	if limits.MaxTradesPerHour > 0 {
		m.risk.MaxTradesPerHour = limits.MaxTradesPerHour
	}
}

// SetBudget overrides the paper budget transferred from the main account to
// "bot:<id>" on the first paper start of a bot (default 100 000 ₽). A
// non-positive amount keeps the current value.
func (m *Manager) SetBudget(amount float64) {
	if amount <= 0 || amount > 100_000_000 {
		return
	}
	m.mu.Lock()
	m.budget = amount
	m.mu.Unlock()
}

// budgetValue returns the configured paper budget under the manager lock.
func (m *Manager) budgetValue() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.budget
}

// SetJournalDir enables the JSONL mirror of every bot journal under
// <dir>/bots/<id>.jsonl. An empty dir disables the mirror.
func (m *Manager) SetJournalDir(dir string) error {
	dir = strings.TrimSpace(dir)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dir = dir
	if dir == "" {
		return nil
	}
	for _, id := range m.order {
		st := m.bots[id]
		if st == nil {
			continue
		}
		if err := st.openJournalFile(dir); err != nil {
			return fmt.Errorf("bot: journal file for %s: %w", id, err)
		}
	}
	return nil
}

// CreateFromBacktest registers a new bot in the check stage from a backtest
// result and its declarative spec.
func (m *Manager) CreateFromBacktest(res backtest.Result, spec rules.Spec, qty int64) (Bot, error) {
	if qty <= 0 || qty > maxQty {
		return Bot{}, fmt.Errorf("%w: qty must be in 1..%d, got %d", ErrInvalidBot, maxQty, qty)
	}
	compiled, err := rules.Compile(spec)
	if err != nil {
		return Bot{}, fmt.Errorf("%w: %v", ErrInvalidBot, err)
	}
	ruleStrategy, ok := compiled.(*rules.Strategy)
	if !ok {
		return Bot{}, fmt.Errorf("%w: unexpected strategy implementation", ErrInvalidBot)
	}
	spec = ruleStrategy.Spec()
	symbol := strings.TrimSpace(res.Symbol)
	if symbol == "" || res.PeriodSec <= 0 {
		return Bot{}, fmt.Errorf("%w: backtest result has no symbol/period", ErrInvalidBot)
	}
	specHash := uint64(0)
	if value, ok := res.Params[rules.ParamSpecHash]; ok {
		specHash = uint64(value)
	}
	from := fmt.Sprintf("%s:%016x", res.Strategy, specHash)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Bot{}, ErrNotFound
	}
	now := m.now()
	m.seq++
	id := fmt.Sprintf("bot_%04d", m.seq)
	st := &state{
		bot: Bot{
			ID:           id,
			Symbol:       symbol,
			PeriodSec:    res.PeriodSec,
			Spec:         spec,
			Qty:          qty,
			Stage:        StageCheck,
			FromBacktest: from,
			CreatedAt:    now,
		},
		journal: newJournalStore(),
	}
	m.bots[id] = st
	m.order = append(m.order, id)
	dir := m.dir
	m.mu.Unlock()

	if dir != "" {
		if err := st.openJournalFile(dir); err != nil {
			return Bot{}, fmt.Errorf("bot: journal file: %w", err)
		}
	}
	entry := st.appendJournal(JournalEntry{
		Time: now, Kind: KindStage, Signal: SignalNone,
		Message: fmt.Sprintf("бот создан из бэктеста %s: %s %s, %d шт", from, symbol, periodLabel(res.PeriodSec), qty),
		Data:    map[string]any{"fromBacktest": from, "symbol": symbol, "periodSec": res.PeriodSec, "qty": qty},
	})
	m.emit(BotEvent{BotID: id, Entry: entry})
	return st.snapshotBot(), nil
}

// Check runs one deterministic calculation over the last closed history bars:
// it does not subscribe to the bar stream and never reads the paper account.
func (m *Manager) Check(ctx context.Context, id string) (JournalEntry, error) {
	st, err := m.state(id)
	if err != nil {
		return JournalEntry{}, err
	}
	if m.feed == nil {
		return JournalEntry{}, ErrNotConfigured
	}
	if ctx == nil {
		ctx = context.Background()
	}
	symbol, periodSec := st.botIDentity()
	bars, err := m.feed.History(ctx, symbol, periodSec, historyCount)
	if err != nil {
		return JournalEntry{}, fmt.Errorf("bot: history for %s: %w", symbol, err)
	}
	bars = closedBars(bars)
	if len(bars) < 2 {
		return JournalEntry{}, fmt.Errorf("bot: not enough closed history for %s (%d bars)", symbol, len(bars))
	}
	strategy, err := rules.Compile(st.botSpec())
	if err != nil {
		return JournalEntry{}, fmt.Errorf("bot: compile spec: %w", err)
	}
	action := backtest.Hold
	for i := range bars {
		action = strategy.OnBar(bars, i)
	}
	last := bars[len(bars)-1]
	indicators := indicatorValues(strategy, bars)
	signal := signalOf(action)
	entry := st.appendJournal(JournalEntry{
		Time:       m.now(),
		BarTime:    last.Time,
		Kind:       KindClosedBar,
		Close:      last.Close,
		Indicators: indicators,
		Signal:     signal,
		Message:    describeBar(last.Close, indicators, signal),
		Data:       map[string]any{"check": true, "bars": len(bars)},
	})
	m.emit(BotEvent{BotID: id, Entry: entry})
	return entry, nil
}

// Start subscribes one bot to the live closed bars of the requested stage. The
// paper stage funds the bot paper account on its first start and executes its
// signals on the paper broker. Promotion to paper is expected to be human
// approved through Promote/bot_start before this method runs.
func (m *Manager) Start(ctx context.Context, id string, stage Stage) error {
	stage = normalizeStage(stage)
	if !runnableStage(stage) {
		return fmt.Errorf("%w: %q", ErrInvalidStage, stage)
	}
	if m.feed == nil || m.broker == nil {
		return ErrNotConfigured
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	st, err := m.state(id)
	if err != nil {
		return err
	}
	st.mu.Lock()
	if st.running {
		if st.bot.Stage == stage {
			st.mu.Unlock()
			return nil
		}
		st.mu.Unlock()
		return fmt.Errorf("%w: bot %s is already running as %s", ErrInvalidStage, id, st.bot.Stage)
	}
	if !transitionAllowed(st.bot.Stage, stage) {
		from := st.bot.Stage
		st.mu.Unlock()
		return stageError(from, stage)
	}
	if stage == StagePaper && !st.paperFunded {
		if err := m.broker.Allocate(accountFor(id), m.budgetValue()); err != nil {
			st.mu.Unlock()
			return fmt.Errorf("bot: fund paper account: %w", err)
		}
		st.paperFunded = true
	}
	runCtx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	st.running = true
	st.bot.Stage = stage
	st.bars = nil
	st.lastBar = time.Time{}
	entry := st.appendJournal(JournalEntry{
		Time: m.now(), Kind: KindStage, Signal: SignalNone,
		Message: fmt.Sprintf("бот переведён в стадию %s", stage),
		Data:    map[string]any{"stage": string(stage)},
	})
	st.mu.Unlock()
	m.emit(BotEvent{BotID: id, Entry: entry})

	stream, err := m.feed.SubscribeClosedBars(runCtx, st.botSymbol(), st.botPeriod())
	if err != nil {
		cancel()
		st.mu.Lock()
		st.running = false
		st.cancel = nil
		st.bot.Stage = StageStopped
		failed := st.appendJournal(JournalEntry{
			Time: m.now(), Kind: KindError, Signal: SignalNone,
			Message: fmt.Sprintf("не удалось подписаться на закрытые бары: %v", err),
		})
		st.mu.Unlock()
		m.emit(BotEvent{BotID: id, Entry: failed})
		return fmt.Errorf("bot: subscribe closed bars: %w", err)
	}
	go m.consume(runCtx, st, stream)
	return nil
}

// Promote requests the stage change of a bot. The paper stage is possible only
// through a human proposal (task 7): the returned proposal id is executed later
// by the approved bot_start tool. The live stage does not exist in iteration 1.
func (m *Manager) Promote(id string, to Stage) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(string(to)))
	if raw == "live" {
		return "", ErrLiveDisabled
	}
	to = Stage(raw)
	if !validStage(to) {
		return "", fmt.Errorf("%w: %q", ErrInvalidStage, to)
	}
	st, err := m.state(id)
	if err != nil {
		return "", err
	}
	switch to {
	case StageDryRun:
		st.mu.Lock()
		from := st.bot.Stage
		if !transitionAllowed(from, to) {
			st.mu.Unlock()
			return "", stageError(from, to)
		}
		st.bot.Stage = to
		entry := st.appendJournal(JournalEntry{
			Time: m.now(), Kind: KindStage, Signal: SignalNone,
			Message: "бот переведён в dry-run",
			Data:    map[string]any{"from": string(from), "to": string(to)},
		})
		st.mu.Unlock()
		m.emit(BotEvent{BotID: id, Entry: entry})
		return "", nil
	case StagePaper:
		if m.approver == nil {
			return "", ErrApprovalMissing
		}
		bot := st.snapshotBot()
		proposalID, err := m.approver.Propose("bot_start",
			map[string]any{"id": id, "stage": string(StagePaper)},
			fmt.Sprintf("Перевести бота %s (%s) на бумажный счёт", id, bot.Symbol))
		if err != nil {
			return "", err
		}
		entry := st.appendJournal(JournalEntry{
			Time: m.now(), Kind: KindStage, Signal: SignalNone,
			Message: fmt.Sprintf("запрошен перевод на бумажный счёт: предложение %s", proposalID),
			Data:    map[string]any{"proposal": proposalID, "to": string(StagePaper)},
		})
		m.emit(BotEvent{BotID: id, Entry: entry})
		return proposalID, nil
	default:
		return "", fmt.Errorf("%w: promote to %q", ErrInvalidStage, to)
	}
}

// Stop cancels one running bot (idempotent). Its journal is kept for the UI.
func (m *Manager) Stop(id string) error {
	st, err := m.state(id)
	if err != nil {
		return err
	}
	st.mu.Lock()
	cancel := st.cancel
	st.cancel = nil
	from := st.bot.Stage
	st.running = false
	st.bars = nil
	st.lastBar = time.Time{}
	if from != StageStopped {
		st.bot.Stage = StageStopped
	}
	entry := st.appendJournal(JournalEntry{
		Time: m.now(), Kind: KindStage, Signal: SignalNone,
		Message: "бот остановлен (kill-switch)",
		Data:    map[string]any{"from": string(from)},
	})
	st.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.emit(BotEvent{BotID: id, Entry: entry})
	return nil
}

// StopAll is the kill-switch of every bot of the terminal.
func (m *Manager) StopAll() error {
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	var firstErr error
	for _, id := range ids {
		if err := m.Stop(id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Journal returns the entries with Seq > afterSeq (newest last); limit <= 0
// returns everything the bounded ring has, limit is capped to journalLimit.
func (m *Manager) Journal(id string, afterSeq uint64, limit int) []JournalEntry {
	st, err := m.state(id)
	if err != nil {
		return nil
	}
	if limit <= 0 || limit > journalLimit {
		limit = journalLimit
	}
	return st.journal.after(afterSeq, limit)
}

// JournalCounts returns how many entries of every kind one bot has.
func (m *Manager) JournalCounts(id string) map[string]int {
	st, err := m.state(id)
	if err != nil {
		return map[string]int{}
	}
	return st.journal.counts()
}

// Bots returns every bot in creation order.
func (m *Manager) Bots() []Bot {
	m.mu.Lock()
	states := make([]*state, 0, len(m.order))
	for _, id := range m.order {
		if st := m.bots[id]; st != nil {
			states = append(states, st)
		}
	}
	m.mu.Unlock()
	out := make([]Bot, 0, len(states))
	for _, st := range states {
		out = append(out, st.snapshotBot())
	}
	return out
}

// Get returns one bot definition.
func (m *Manager) Get(id string) (Bot, error) {
	st, err := m.state(id)
	if err != nil {
		return Bot{}, err
	}
	return st.snapshotBot(), nil
}

// Subscribe registers the bot event sink and returns its cancel function.
func (m *Manager) Subscribe(fn func(BotEvent)) (cancel func()) {
	if fn == nil {
		return func() {}
	}
	m.subsMu.Lock()
	id := m.subSeq
	m.subSeq++
	m.subs[id] = fn
	m.subsMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.subsMu.Lock()
			delete(m.subs, id)
			m.subsMu.Unlock()
		})
	}
}

// Close stops every bot and releases the journal files.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	if err := m.StopAll(); err != nil {
		return err
	}
	m.mu.Lock()
	states := make([]*state, 0, len(m.order))
	for _, id := range m.order {
		if st := m.bots[id]; st != nil {
			states = append(states, st)
		}
	}
	m.mu.Unlock()
	for _, st := range states {
		_ = st.journal.close()
	}
	return nil
}

// ---- internals ------------------------------------------------------------

// state returns the runtime state of one bot.
func (m *Manager) state(id string) (*state, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.bots[id]
	if st == nil {
		return nil, ErrNotFound
	}
	return st, nil
}

// emit fans one bot event out to every subscriber (a panicking subscriber is
// isolated, sinks must not block).
func (m *Manager) emit(ev BotEvent) {
	m.subsMu.Lock()
	sinks := make([]func(BotEvent), 0, len(m.subs))
	for _, fn := range m.subs {
		sinks = append(sinks, fn)
	}
	m.subsMu.Unlock()
	for _, fn := range sinks {
		func() {
			defer func() { _ = recover() }()
			fn(ev)
		}()
	}
}

// consume processes the closed-bar stream of one running bot until it closes or
// the run context is canceled.
func (m *Manager) consume(ctx context.Context, st *state, stream <-chan backtest.Bar) {
	for {
		select {
		case <-ctx.Done():
			return
		case bar, ok := <-stream:
			if !ok {
				return
			}
			m.handleBar(st, bar)
		}
	}
}

// handleBar consumes one closed bar: dedupe/backfill, strategy signal, journal
// and (on the paper stage) the paper order.
func (m *Manager) handleBar(st *state, bar backtest.Bar) {
	if bar.Partial || bar.Close <= 0 {
		return
	}
	st.mu.Lock()
	if !st.lastBar.IsZero() && !bar.Time.After(st.lastBar) {
		// A duplicate or late backfilled bar: it was already replayed.
		st.mu.Unlock()
		return
	}
	var events []JournalEntry
	if !st.lastBar.IsZero() {
		gap := bar.Time.Sub(st.lastBar)
		if gap > time.Duration(st.bot.PeriodSec)*time.Second {
			events = append(events, st.appendJournal(JournalEntry{
				Time: m.now(), BarTime: bar.Time, Kind: KindReconnect, Signal: SignalNone,
				Message: fmt.Sprintf("поток восстановлен: пропуск %s, бары догружены без дублей", gap),
				Data:    map[string]any{"gap": gap.String(), "lastBarTime": st.lastBar},
			}))
		}
	}
	st.bars = append(st.bars, bar)
	if len(st.bars) > barHistoryLimit {
		st.bars = append([]backtest.Bar(nil), st.bars[len(st.bars)-barHistoryLimit:]...)
	}
	st.lastBar = bar.Time
	// The rules strategy keeps its own position state, so the window is replayed
	// with a freshly compiled strategy on every bar: the decision on bar i is
	// then identical to the backtest decision on that bar.
	strategy, compileErr := rules.Compile(st.bot.Spec)
	action := backtest.Hold
	var indicators map[string]float64
	if compileErr == nil {
		for i := range st.bars {
			action = strategy.OnBar(st.bars, i)
		}
		indicators = indicatorValues(strategy, st.bars)
	}
	signal := signalOf(action)
	stage := st.bot.Stage
	id := st.bot.ID
	events = append(events, st.appendJournal(JournalEntry{
		Time: m.now(), BarTime: bar.Time, Kind: KindClosedBar,
		Close: bar.Close, Indicators: indicators, Signal: signal,
		Message: describeBar(bar.Close, indicators, signal),
	}))
	if signal != SignalNone {
		events = append(events, st.appendJournal(JournalEntry{
			Time: m.now(), BarTime: bar.Time, Kind: KindSignal, Close: bar.Close, Signal: signal,
			Message: fmt.Sprintf("сигнал %s на закрытии бара (стадия %s)", signal, stage),
		}))
	}
	st.mu.Unlock()
	for _, entry := range events {
		m.emit(BotEvent{BotID: id, Entry: entry})
	}
	if signal == SignalNone || stage != StagePaper {
		return
	}
	m.executePaper(st, bar, signal)
}

// executePaper routes one bot signal through the paper broker on the bot
// account "bot:<id>": entry only from a flat account, exit never larger than the
// position, and the task-2 risk limits are checked before every entry.
//
// Fill convention (plan Task 22, no lookahead): the signal is decided on the
// close of the closed bar and the paper fill happens at that same close. The
// next bar's open is not known at the decision instant, so the close of the
// signal bar is the freshest executable price; the bot never peeks forward.
func (m *Manager) executePaper(st *state, bar backtest.Bar, signal string) {
	if m.broker == nil {
		return
	}
	id := st.botID()
	account := accountFor(id)
	symbol := st.botSymbol()
	qty := st.botQty()
	price := bar.Close
	now := m.now()

	snapshot := m.broker.Snapshot()
	accountView := snapshot.Accounts[account]
	position := paperPosition(accountView.Positions, symbol)

	if signal == SignalEntry {
		if position != 0 {
			m.paperRiskBlock(st, bar, fmt.Sprintf("вход отклонён: позиция бота уже %d шт", position))
			return
		}
		if reason := m.riskBlockReason(st, accountView.Cash, qty, price, now); reason != "" {
			m.paperRiskBlock(st, bar, reason)
			return
		}
	} else {
		if position <= 0 {
			return
		}
		if qty > position {
			qty = position
		}
	}

	side := paper.Buy
	verb := "покупка"
	if signal == SignalExit {
		side = paper.Sell
		verb = "продажа"
	}
	clientOrderID := id + ":" + bar.Time.UTC().Format(time.RFC3339)
	order, err := m.broker.PlaceAt(paper.OrderRequest{
		Symbol: symbol, Side: side, Qty: qty, Type: paper.Market,
		Source: "bot", ClientOrderID: clientOrderID,
	}, paper.QuoteView{Symbol: symbol, Last: price, Time: bar.Time}, bar.Time, account)
	if err != nil {
		entry := st.appendJournal(JournalEntry{
			Time: now, BarTime: bar.Time, Kind: KindRiskCheck, Close: price, Signal: signal,
			Message: fmt.Sprintf("%s отклонена брокером: %v", verb, err),
			Data:    map[string]any{"qty": qty, "price": price, "clientOrderId": clientOrderID},
		})
		m.emit(BotEvent{BotID: id, Entry: entry})
		return
	}
	orderEntry := st.appendJournal(JournalEntry{
		Time: now, BarTime: bar.Time, Kind: KindOrder, Close: price, Signal: signal,
		Message: fmt.Sprintf("%s %d шт по %.2f ₽ (заявка %s)", verb, qty, order.AvgFillPrice, order.ID),
		Data: map[string]any{
			"side": side, "qty": qty, "price": order.AvgFillPrice, "orderId": order.ID,
			"clientOrderId": clientOrderID, "account": account,
		},
	})
	m.emit(BotEvent{BotID: id, Entry: orderEntry})
	if order.Filled > 0 {
		fill := st.appendJournal(JournalEntry{
			Time: now, BarTime: bar.Time, Kind: KindFill, Close: order.AvgFillPrice, Signal: signal,
			Message: fmt.Sprintf("исполнено %d шт по %.2f ₽", order.Filled, order.AvgFillPrice),
			Data:    map[string]any{"side": side, "qty": order.Filled, "price": order.AvgFillPrice, "orderId": order.ID},
		})
		m.emit(BotEvent{BotID: id, Entry: fill})
		if signal == SignalEntry {
			st.recordTrade(now)
		}
	}
}

// riskBlockReason checks the task-2 guard rails of one paper entry.
func (m *Manager) riskBlockReason(st *state, cash float64, qty int64, price float64, now time.Time) string {
	m.mu.Lock()
	risk := m.risk
	m.mu.Unlock()
	notional := price * float64(qty)
	if cash <= 0 {
		return "недостаточно кэша счёта бота"
	}
	if maxOrder := cash * risk.MaxOrderPct / 100; maxOrder > 0 && notional > maxOrder {
		return fmt.Sprintf("заявка %.2f ₽ превышает лимит %.2f ₽ (maxOrderPct %.0f%%)",
			notional, maxOrder, risk.MaxOrderPct)
	}
	if maxPos := cash * risk.MaxPositionPct / 100; maxPos > 0 && notional > maxPos {
		return fmt.Sprintf("позиция %.2f ₽ превышает лимит %.2f ₽ (maxPositionPct %.0f%%)",
			notional, maxPos, risk.MaxPositionPct)
	}
	if limit := risk.MaxTradesPerHour; limit > 0 {
		if recent := st.recentTrades(now.Add(-time.Hour)); len(recent) >= limit {
			return fmt.Sprintf("лимит сделок в час (%d) исчерпан", limit)
		}
	}
	return ""
}

// paperRiskBlock writes one risk_check journal record.
func (m *Manager) paperRiskBlock(st *state, bar backtest.Bar, reason string) {
	id := st.botID()
	entry := st.appendJournal(JournalEntry{
		Time: m.now(), BarTime: bar.Time, Kind: KindRiskCheck, Close: bar.Close, Signal: SignalEntry,
		Message: reason,
	})
	m.emit(BotEvent{BotID: id, Entry: entry})
}

// ---- state helpers --------------------------------------------------------

// openJournalFile enables the JSONL mirror of one bot journal.
func (st *state) openJournalFile(dir string) error {
	st.mu.Lock()
	id := st.bot.ID
	st.mu.Unlock()
	return st.journal.openFile(filepath.Join(dir, "bots", id+".jsonl"))
}

// snapshotBot returns a copy of the bot definition.
func (st *state) snapshotBot() Bot {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := st.bot
	if out.Spec.Indicators != nil {
		indicators := make(map[string]rules.Indicator, len(out.Spec.Indicators))
		for name, ind := range out.Spec.Indicators {
			indicators[name] = ind
		}
		out.Spec.Indicators = indicators
	}
	out.Spec.Entry = append([]rules.Condition(nil), out.Spec.Entry...)
	out.Spec.Exit = append([]rules.Condition(nil), out.Spec.Exit...)
	out.Spec.Filters = append([]rules.Condition(nil), out.Spec.Filters...)
	return out
}

func (st *state) botID() string     { st.mu.Lock(); defer st.mu.Unlock(); return st.bot.ID }
func (st *state) botSymbol() string { st.mu.Lock(); defer st.mu.Unlock(); return st.bot.Symbol }
func (st *state) botPeriod() int    { st.mu.Lock(); defer st.mu.Unlock(); return st.bot.PeriodSec }
func (st *state) botQty() int64     { st.mu.Lock(); defer st.mu.Unlock(); return st.bot.Qty }

// botSpec returns a copy of the declarative spec of the bot.
func (st *state) botSpec() rules.Spec { return st.snapshotBot().Spec }

// botIDentity returns the symbol and the period of the bot.
func (st *state) botIDentity() (string, int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.bot.Symbol, st.bot.PeriodSec
}

// periodLabel renders one candle period in Russian (mirrors the API wording).
func periodLabel(periodSec int) string {
	switch periodSec {
	case 60:
		return "1м"
	case 300:
		return "5м"
	case 900:
		return "15м"
	case 3600:
		return "1ч"
	case 86400:
		return "1д"
	default:
		return fmt.Sprintf("%d с", periodSec)
	}
}
