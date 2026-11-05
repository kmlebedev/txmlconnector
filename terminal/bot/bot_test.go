package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// botNow is the frozen clock of the bot tests.
var botNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// testSpec is an RSI-corridor rule strategy: entry when RSI < 0.2, exit when
// RSI > 0.8 (the scale 0.01 converts the 0..100 RSI into 0..1).
func testSpec() rules.Spec {
	return rules.Spec{
		Indicators: map[string]rules.Indicator{
			"rsi": {Kind: rules.KindRSIWilder, Period: 2, Scale: 0.01},
		},
		Entry: []rules.Condition{{Left: "rsi", Op: rules.OpLT, Right: "0.2"}},
		Exit:  []rules.Condition{{Left: "rsi", Op: rules.OpGT, Right: "0.8"}},
	}
}

// testBacktest is the backtest result a bot is created from.
func testBacktest() backtest.Result {
	return backtest.Result{
		Symbol: "TQBR:SBER", PeriodSec: 60, Strategy: rules.ID,
		Params: map[string]float64{rules.ParamSpecHash: 42},
	}
}

// decliningBars builds n falling 1m bars: RSI falls to 0, so the entry fires.
func decliningBars(n int) []backtest.Bar {
	bars := make([]backtest.Bar, n)
	for i := range bars {
		price := 100 - float64(i)
		bars[i] = backtest.Bar{
			Time: botNow.Add(time.Duration(i) * time.Minute),
			Open: price + 0.4, High: price + 1, Low: price - 1, Close: price, Volume: 100 + int64(i),
		}
	}
	return bars
}

// risingBars builds n rising 1m bars starting at start: RSI rises to 1, so the
// exit fires.
func risingBars(start time.Time, n int) []backtest.Bar {
	bars := make([]backtest.Bar, n)
	for i := range bars {
		price := 80 + float64(i)
		bars[i] = backtest.Bar{
			Time: start.Add(time.Duration(i) * time.Minute),
			Open: price - 0.4, High: price + 1, Low: price - 1, Close: price, Volume: 100 + int64(i),
		}
	}
	return bars
}

// lastBarDropBars builds a rising history whose final bar is a sharp drop, so
// the RSI entry condition fires exactly on the last closed bar.
func lastBarDropBars() []backtest.Bar {
	bars := risingBars(botNow, 19)
	last := bars[len(bars)-1]
	bars = append(bars, backtest.Bar{
		Time: last.Time.Add(time.Minute),
		Open: last.Close, High: last.Close, Low: last.Close - 30, Close: last.Close - 30, Volume: 500,
	})
	return bars
}

// fakeBarStream is the BarStream fake: history is preset and the closed-bar
// stream is fed by the test.
type fakeBarStream struct {
	mu           sync.Mutex
	history      []backtest.Bar
	historyErr   error
	subscribeErr error
	stream       chan backtest.Bar
	subscribed   int
}

func newFakeBarStream(history []backtest.Bar) *fakeBarStream {
	return &fakeBarStream{history: history, stream: make(chan backtest.Bar, 128)}
}

func (f *fakeBarStream) History(_ context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error) {
	if f.historyErr != nil {
		return nil, f.historyErr
	}
	bars := append([]backtest.Bar(nil), f.history...)
	if count > 0 && len(bars) > count {
		bars = bars[len(bars)-count:]
	}
	return bars, nil
}

func (f *fakeBarStream) SubscribeClosedBars(_ context.Context, symbol string, periodSec int) (<-chan backtest.Bar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	f.subscribed++
	return f.stream, nil
}

func (f *fakeBarStream) subscribeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subscribed
}

func (f *fakeBarStream) send(bar backtest.Bar) { f.stream <- bar }

// fakeAccountState is the mutable state of one fake paper account.
type fakeAccountState struct {
	cash      float64
	positions map[string]int64
}

// fakeBroker is the paper Broker fake: it fills market orders at the given
// replay price and records every request.
type fakeBroker struct {
	mu            sync.Mutex
	accounts      map[string]*fakeAccountState
	requests      []paper.OrderRequest
	fills         []paper.QuoteView
	orderAccounts []string
	allocations   []float64
	snapshotCalls int
	fillQtyCap    int64
	placeErr      error
}

func newFakeBroker() *fakeBroker {
	return &fakeBroker{accounts: map[string]*fakeAccountState{
		"main": {cash: 1_000_000, positions: map[string]int64{}},
	}}
}

func (b *fakeBroker) accountLocked(name string) *fakeAccountState {
	acc := b.accounts[name]
	if acc == nil {
		acc = &fakeAccountState{positions: map[string]int64{}}
		b.accounts[name] = acc
	}
	return acc
}

func (b *fakeBroker) Allocate(account string, amount float64) error {
	if account == "" || account == "main" || amount <= 0 {
		return fmt.Errorf("fake: invalid allocation")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	main := b.accountLocked("main")
	if main.cash < amount {
		return fmt.Errorf("fake: not enough main cash")
	}
	main.cash -= amount
	b.accountLocked(account).cash += amount
	b.allocations = append(b.allocations, amount)
	return nil
}

func (b *fakeBroker) PlaceAt(req paper.OrderRequest, fill paper.QuoteView, at time.Time, account string) (paper.Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.placeErr != nil {
		return paper.Order{}, b.placeErr
	}
	qty := req.Qty
	if b.fillQtyCap > 0 && qty > b.fillQtyCap {
		qty = b.fillQtyCap
	}
	acc := b.accountLocked(account)
	price := fill.Last
	if req.Side == paper.Buy {
		cost := price * float64(qty)
		if acc.cash < cost {
			return paper.Order{}, fmt.Errorf("fake: not enough cash")
		}
		acc.cash -= cost
		acc.positions[req.Symbol] += qty
	} else {
		position := acc.positions[req.Symbol]
		if position < qty {
			return paper.Order{}, fmt.Errorf("fake: sell %d exceeds position %d", qty, position)
		}
		acc.positions[req.Symbol] = position - qty
		acc.cash += price * float64(qty)
	}
	b.requests = append(b.requests, req)
	b.fills = append(b.fills, fill)
	b.orderAccounts = append(b.orderAccounts, account)
	return paper.Order{
		ID:           fmt.Sprintf("ord_%06d", len(b.requests)),
		Symbol:       req.Symbol,
		Side:         req.Side,
		Type:         req.Type,
		Qty:          req.Qty,
		Filled:       qty,
		AvgFillPrice: price,
		Status:       paper.StatusFilled,
		CreatedAt:    at,
		Account:      account,
	}, nil
}

func (b *fakeBroker) Snapshot() paper.Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snapshotCalls++
	snapshot := paper.Snapshot{Accounts: make(map[string]paper.AccountView, len(b.accounts))}
	for name, acc := range b.accounts {
		view := paper.AccountView{Cash: acc.cash}
		for symbol, qty := range acc.positions {
			view.Positions = append(view.Positions, paper.Position{Symbol: symbol, Qty: qty})
		}
		snapshot.Accounts[name] = view
		if name == "main" {
			snapshot.Cash = acc.cash
		}
	}
	return snapshot
}

func (b *fakeBroker) placeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.requests)
}

func (b *fakeBroker) requestsCopy() []paper.OrderRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]paper.OrderRequest(nil), b.requests...)
}

func (b *fakeBroker) accountsCopy() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.orderAccounts...)
}

func (b *fakeBroker) snapshotCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotCalls
}

func (b *fakeBroker) allocationCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.allocations)
}

// fakeApprover is the Approver fake of the proposal gate.
type fakeApprover struct {
	mu     sync.Mutex
	calls  []approverCall
	err    error
	nextID string
}

type approverCall struct {
	tool    string
	args    map[string]any
	summary string
}

func (a *fakeApprover) Propose(tool string, args map[string]any, summary string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return "", a.err
	}
	a.calls = append(a.calls, approverCall{tool: tool, args: args, summary: summary})
	if a.nextID == "" {
		return "prop_000001", nil
	}
	return a.nextID, nil
}

func (a *fakeApprover) callsCopy() []approverCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]approverCall(nil), a.calls...)
}

// newTestManager builds a Manager with the frozen test clock and cleanup.
func newTestManager(t *testing.T, stream BarStream, broker Broker, approver Approver) *Manager {
	t.Helper()
	m := NewManager(stream, broker, approver, func() time.Time { return botNow })
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// mustCreateBot creates one bot and fails the test on an error.
func mustCreateBot(t *testing.T, m *Manager, spec rules.Spec, qty int64) Bot {
	t.Helper()
	bot, err := m.CreateFromBacktest(testBacktest(), spec, qty)
	if err != nil {
		t.Fatalf("CreateFromBacktest: %v", err)
	}
	if bot.ID == "" {
		t.Fatal("CreateFromBacktest returned an empty id")
	}
	return bot
}

// waitFor polls one condition for up to three seconds.
func waitFor(t *testing.T, what string, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// journalOf returns the full journal of one bot.
func journalOf(m *Manager, id string) []JournalEntry { return m.Journal(id, 0, 0) }

// entriesOfKind returns the journal entries of one kind.
func entriesOfKind(entries []JournalEntry, kind string) []JournalEntry {
	out := make([]JournalEntry, 0, 4)
	for _, entry := range entries {
		if entry.Kind == kind {
			out = append(out, entry)
		}
	}
	return out
}

// TestBotCheckDoesNotSubscribeOrReadAccount is the R7/plan guard: the one-shot
// check reads history only — no subscription and no paper-account reads.
func TestBotCheckDoesNotSubscribeOrReadAccount(t *testing.T) {
	stream := newFakeBarStream(lastBarDropBars())
	broker := newFakeBroker()
	m := newTestManager(t, stream, broker, nil)
	bot := mustCreateBot(t, m, testSpec(), 10)

	entry, err := m.Check(context.Background(), bot.ID)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if entry.Kind != KindClosedBar {
		t.Errorf("kind = %q, want closed_bar", entry.Kind)
	}
	if entry.Signal != SignalEntry {
		t.Errorf("signal = %q, want entry on the sharp last bar", entry.Signal)
	}
	if !strings.Contains(entry.Message, "close=") || !strings.Contains(entry.Message, "signal=") {
		t.Errorf("message = %q, want close=… rsi=… signal=…", entry.Message)
	}
	if value, ok := entry.Indicators["rsi"]; !ok || value > 0.2 {
		t.Errorf("indicators = %+v, want rsi < 0.2", entry.Indicators)
	}
	if got := stream.subscribeCalls(); got != 0 {
		t.Errorf("subscriptions during Check = %d, want 0", got)
	}
	if got := broker.snapshotCount(); got != 0 {
		t.Errorf("broker snapshot reads during Check = %d, want 0", got)
	}
	if got := broker.placeCount(); got != 0 {
		t.Errorf("broker orders during Check = %d, want 0", got)
	}
}

// TestBotDryRunLogsSignalsWithoutOrders checks the dry-run stage: closed bars
// and signals are journaled, no broker order is placed.
func TestBotDryRunLogsSignalsWithoutOrders(t *testing.T) {
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	m := newTestManager(t, stream, broker, nil)
	bot := mustCreateBot(t, m, testSpec(), 10)
	if err := m.Start(context.Background(), bot.ID, StageDryRun); err != nil {
		t.Fatalf("Start dry_run: %v", err)
	}
	if got := stream.subscribeCalls(); got != 1 {
		t.Fatalf("subscriptions = %d, want 1", got)
	}
	for _, bar := range decliningBars(20) {
		stream.send(bar)
	}
	waitFor(t, "a dry-run entry signal", func() bool {
		for _, entry := range entriesOfKind(journalOf(m, bot.ID), KindSignal) {
			if entry.Signal == SignalEntry {
				return true
			}
		}
		return false
	})
	if got := broker.placeCount(); got != 0 {
		t.Fatalf("dry-run placed %d orders, want 0", got)
	}
	if err := m.Stop(bot.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestBotPaperEntersOnlyFromFlat checks that a flat bot account is required for
// an entry and a duplicate closed bar never produces a second order.
func TestBotPaperEntersOnlyFromFlat(t *testing.T) {
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	m := newTestManager(t, stream, broker, nil)
	bot := mustCreateBot(t, m, testSpec(), 10)
	if err := m.Start(context.Background(), bot.ID, StagePaper); err != nil {
		t.Fatalf("Start paper: %v", err)
	}
	bars := decliningBars(20)
	for _, bar := range bars {
		stream.send(bar)
	}
	waitFor(t, "the first paper order", func() bool { return broker.placeCount() == 1 })
	// A duplicated last bar (backfill) must not produce a second order.
	stream.send(bars[len(bars)-1])
	time.Sleep(20 * time.Millisecond)
	if got := broker.placeCount(); got != 1 {
		t.Fatalf("orders = %d, want exactly 1 (flat account + duplicate dedupe)", got)
	}
	request := broker.requestsCopy()[0]
	if request.Side != paper.Buy || request.ClientOrderID == "" || !strings.HasPrefix(request.ClientOrderID, bot.ID+":") {
		t.Errorf("paper request = %+v, want a bot buy with ClientOrderID botID:barTime", request)
	}
	if got := broker.accountsCopy(); len(got) != 1 || got[0] != accountFor(bot.ID) {
		t.Errorf("order accounts = %v, want [%s]", got, accountFor(bot.ID))
	}
	if err := m.Stop(bot.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestBotPaperSellsNoMoreThanPosition checks that a short bot position is never
// sold short: the exit quantity is capped by the account position.
func TestBotPaperSellsNoMoreThanPosition(t *testing.T) {
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	broker.fillQtyCap = 3 // the paper account ends up with 3 shares only
	m := newTestManager(t, stream, broker, nil)
	bot := mustCreateBot(t, m, testSpec(), 10)
	if err := m.Start(context.Background(), bot.ID, StagePaper); err != nil {
		t.Fatalf("Start paper: %v", err)
	}
	bars := decliningBars(10)
	bars = append(bars, risingBars(bars[len(bars)-1].Time.Add(time.Minute), 12)...)
	for _, bar := range bars {
		stream.send(bar)
	}
	waitFor(t, "an exit paper order", func() bool {
		for _, request := range broker.requestsCopy() {
			if request.Side == paper.Sell {
				return true
			}
		}
		return false
	})
	var sells []paper.OrderRequest
	for _, request := range broker.requestsCopy() {
		if request.Side == paper.Sell {
			sells = append(sells, request)
		}
	}
	if len(sells) == 0 {
		t.Fatal("no sell order")
	}
	if sells[0].Qty != 3 {
		t.Errorf("sell qty = %d, want the 3-share position, not the 10-share bot qty", sells[0].Qty)
	}
	if err := m.Stop(bot.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestBotPositionsIsolatedFromManual checks that the bot reads its own paper
// account ("bot:<id>"), not the manual main account.
func TestBotPositionsIsolatedFromManual(t *testing.T) {
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	broker.mu.Lock()
	broker.accountLocked("main").positions["TQBR:SBER"] = 100
	broker.mu.Unlock()
	m := newTestManager(t, stream, broker, nil)
	bot := mustCreateBot(t, m, testSpec(), 10)
	if err := m.Start(context.Background(), bot.ID, StagePaper); err != nil {
		t.Fatalf("Start paper: %v", err)
	}
	for _, bar := range decliningBars(20) {
		stream.send(bar)
	}
	waitFor(t, "the bot paper order", func() bool { return broker.placeCount() == 1 })
	if got := broker.accountsCopy()[0]; got != accountFor(bot.ID) {
		t.Errorf("order account = %q, want the bot account %q", got, accountFor(bot.ID))
	}
	if got := broker.placeCount(); got != 1 {
		t.Errorf("orders = %d, want 1: the manual main position must not block the bot", got)
	}
	if err := m.Stop(bot.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestBotSignalsMatchBacktest checks the "backtest = bot" link: the live closed
// bar signals of a bot are exactly the backtest decisions on the same bars.
func TestBotSignalsMatchBacktest(t *testing.T) {
	bars := decliningBars(10)
	bars = append(bars, risingBars(bars[len(bars)-1].Time.Add(time.Minute), 10)...)
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	m := newTestManager(t, stream, broker, nil)
	bot := mustCreateBot(t, m, testSpec(), 10)
	if err := m.Start(context.Background(), bot.ID, StageDryRun); err != nil {
		t.Fatalf("Start dry_run: %v", err)
	}
	for _, bar := range bars {
		stream.send(bar)
	}
	waitFor(t, "all closed bars in the journal", func() bool {
		return len(entriesOfKind(journalOf(m, bot.ID), KindClosedBar)) == len(bars)
	})
	got := make([]string, 0, len(bars))
	for _, entry := range entriesOfKind(journalOf(m, bot.ID), KindClosedBar) {
		got = append(got, entry.Signal)
	}
	strategy, err := rules.Compile(testSpec())
	if err != nil {
		t.Fatalf("rules.Compile: %v", err)
	}
	want := make([]string, 0, len(bars))
	for i := range bars {
		want = append(want, signalOf(strategy.OnBar(bars, i)))
	}
	if len(got) != len(want) {
		t.Fatalf("bot signals = %d, backtest signals = %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("signal %d = %q, want the backtest decision %q", i, got[i], want[i])
		}
	}
	if err := m.Stop(bot.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestBotReconnectBackfillsWithoutDuplicates is the Review Focus 8 regression:
// after a stream gap the bot logs one reconnect, backfills the missed bars and
// never replays a duplicate.
func TestBotReconnectBackfillsWithoutDuplicates(t *testing.T) {
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	m := newTestManager(t, stream, broker, nil)
	bot := mustCreateBot(t, m, testSpec(), 10)
	if err := m.Start(context.Background(), bot.ID, StageDryRun); err != nil {
		t.Fatalf("Start dry_run: %v", err)
	}
	bars := decliningBars(6)
	for _, bar := range bars {
		stream.send(bar)
	}
	// Reconnect: the stream resumes 5 bars later and re-sends an old bar.
	resume := botNow.Add(11 * time.Minute)
	stream.send(backtest.Bar{Time: resume, Open: 99, High: 100, Low: 98, Close: 99, Volume: 100})
	stream.send(bars[3]) // duplicate of an already replayed bar
	stream.send(backtest.Bar{Time: resume.Add(time.Minute), Open: 99, High: 100, Low: 98, Close: 99, Volume: 100})
	waitFor(t, "the reconnect marker", func() bool {
		return len(entriesOfKind(journalOf(m, bot.ID), KindReconnect)) == 1
	})
	waitFor(t, "the replayed bars", func() bool {
		return len(entriesOfKind(journalOf(m, bot.ID), KindClosedBar)) >= len(bars)+2
	})
	entries := entriesOfKind(journalOf(m, bot.ID), KindClosedBar)
	seen := map[time.Time]bool{}
	for _, entry := range entries {
		if seen[entry.BarTime] {
			t.Fatalf("duplicate closed_bar for %v", entry.BarTime)
		}
		seen[entry.BarTime] = true
	}
	if len(seen) != len(bars)+2 {
		t.Errorf("unique closed bars = %d, want %d", len(seen), len(bars)+2)
	}
	if reconnects := entriesOfKind(journalOf(m, bot.ID), KindReconnect); len(reconnects) != 1 {
		t.Errorf("reconnect entries = %d, want 1", len(reconnects))
	}
	if err := m.Stop(bot.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestBotPromoteToPaperRequiresApproval checks that the dry_run -> paper
// promotion always goes through a human proposal (task 7).
func TestBotPromoteToPaperRequiresApproval(t *testing.T) {
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	approver := &fakeApprover{}
	m := newTestManager(t, stream, broker, approver)
	bot := mustCreateBot(t, m, testSpec(), 10)
	if err := m.Start(context.Background(), bot.ID, StageDryRun); err != nil {
		t.Fatalf("Start dry_run: %v", err)
	}
	proposalID, err := m.Promote(bot.ID, StagePaper)
	if err != nil {
		t.Fatalf("Promote paper: %v", err)
	}
	if proposalID != "prop_000001" {
		t.Errorf("proposal id = %q", proposalID)
	}
	calls := approver.callsCopy()
	if len(calls) != 1 || calls[0].tool != "bot_start" {
		t.Fatalf("approver calls = %+v, want one bot_start proposal", calls)
	}
	if calls[0].args["id"] != bot.ID || calls[0].args["stage"] != "paper" {
		t.Errorf("proposal args = %v", calls[0].args)
	}
	// The bot stays in dry-run until the approved bot_start runs.
	if current, err := m.Get(bot.ID); err != nil || current.Stage != StageDryRun {
		t.Errorf("bot stage after Promote = %v (err %v), want dry_run", current.Stage, err)
	}
	// Without an approver the promotion is impossible.
	m2 := newTestManager(t, newFakeBarStream(nil), newFakeBroker(), nil)
	bot2 := mustCreateBot(t, m2, testSpec(), 10)
	if _, err := m2.Promote(bot2.ID, StagePaper); !errors.Is(err, ErrApprovalMissing) {
		t.Fatalf("Promote without approver = %v, want ErrApprovalMissing", err)
	}
}

// TestBotPromoteToLiveRejected checks that the live stage really does not exist.
func TestBotPromoteToLiveRejected(t *testing.T) {
	m := newTestManager(t, newFakeBarStream(nil), newFakeBroker(), &fakeApprover{})
	bot := mustCreateBot(t, m, testSpec(), 10)
	if _, err := m.Promote(bot.ID, Stage("live")); !errors.Is(err, ErrLiveDisabled) {
		t.Fatalf("Promote live = %v, want ErrLiveDisabled", err)
	}
	if current, err := m.Get(bot.ID); err != nil || current.Stage != StageCheck {
		t.Errorf("bot stage after a rejected live promotion = %v (err %v), want check", current.Stage, err)
	}
}

// TestBotPaperBudgetConfigurable checks that SetBudget funds the bot paper
// account with the configured amount (default 100 000 ₽).
func TestBotPaperBudgetConfigurable(t *testing.T) {
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	m := newTestManager(t, stream, broker, nil)
	m.SetBudget(250_000)
	bot := mustCreateBot(t, m, testSpec(), 1)
	if err := m.Start(context.Background(), bot.ID, StagePaper); err != nil {
		t.Fatalf("Start paper: %v", err)
	}
	broker.mu.Lock()
	allocations := append([]float64(nil), broker.allocations...)
	broker.mu.Unlock()
	if len(allocations) != 1 || allocations[0] != 250_000 {
		t.Fatalf("allocations = %v, want one 250000 ₽ transfer", allocations)
	}
	if err := m.Stop(bot.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestStopAllStopsEveryBot checks the global kill-switch.
func TestStopAllStopsEveryBot(t *testing.T) {
	stream := newFakeBarStream(nil)
	broker := newFakeBroker()
	m := newTestManager(t, stream, broker, nil)
	first := mustCreateBot(t, m, testSpec(), 10)
	second := mustCreateBot(t, m, testSpec(), 5)
	for _, id := range []string{first.ID, second.ID} {
		if err := m.Start(context.Background(), id, StageDryRun); err != nil {
			t.Fatalf("Start %s: %v", id, err)
		}
	}
	if err := m.StopAll(); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	for _, id := range []string{first.ID, second.ID} {
		current, err := m.Get(id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if current.Stage != StageStopped {
			t.Errorf("bot %s stage = %q, want stopped", id, current.Stage)
		}
		entries := journalOf(m, id)
		if len(entries) == 0 {
			t.Fatalf("bot %s journal is empty", id)
		}
		last := entries[len(entries)-1]
		if last.Kind != KindStage || !strings.Contains(last.Message, "остановлен") {
			t.Errorf("bot %s last journal entry = %+v, want a stop stage entry", id, last)
		}
	}
}
