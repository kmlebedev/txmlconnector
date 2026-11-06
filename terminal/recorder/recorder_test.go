package recorder

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
)

// fakeSink collects the batches the recorder emits; no ClickHouse is involved.
type fakeSink struct {
	mu     sync.Mutex
	books  [][]BookEvent
	trades [][]TradeEvent
	news   [][]NewsEvent
}

func (f *fakeSink) Book(events []BookEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.books = append(f.books, append([]BookEvent(nil), events...))
	return nil
}

func (f *fakeSink) Trades(events []TradeEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trades = append(f.trades, append([]TradeEvent(nil), events...))
	return nil
}

func (f *fakeSink) News(events []NewsEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.news = append(f.news, append([]NewsEvent(nil), events...))
	return nil
}

// newsCalls returns the recorded News batches.
func (f *fakeSink) newsCalls() [][]NewsEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]NewsEvent(nil), f.news...)
}

// bookCalls returns the recorded Book batches.
func (f *fakeSink) bookCalls() [][]BookEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]BookEvent(nil), f.books...)
}

// tradeCalls returns the recorded Trades batches.
func (f *fakeSink) tradeCalls() [][]TradeEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]TradeEvent(nil), f.trades...)
}

// fakeSubscriber stands in for *native.Client: it records subscriptions and
// replays the events the test feeds through Run.
type fakeSubscriber struct {
	mu       sync.Mutex
	subs     []native.Subscription
	runs     int
	runFunc  func(ctx context.Context, h func([]model.Event) error) error
	subErr   error
	oldNews  int
	bodyReqs int
}

func (f *fakeSubscriber) Subscribe(ctx context.Context, sub native.Subscription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, sub)
	return f.subErr
}

func (f *fakeSubscriber) Run(ctx context.Context, opt native.RunOptions, h func([]model.Event) error) error {
	f.mu.Lock()
	f.runs++
	fn := f.runFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, h)
	}
	return ctx.Err()
}

func (f *fakeSubscriber) subscriptions() []native.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]native.Subscription(nil), f.subs...)
}

// OldNews and NewsBody make the fake a newsFetcher, so run() starts the news
// poller in every test (the no-op calls are harmless).
func (f *fakeSubscriber) OldNews(ctx context.Context, count int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oldNews++
	return nil
}

func (f *fakeSubscriber) NewsBody(ctx context.Context, id int64) (model.NewsBody, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodyReqs++
	return model.NewsBody{ID: id}, nil
}

func (f *fakeSubscriber) oldNewsCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.oldNews
}

// waitUntil polls cond until it holds or the deadline expires.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

var (
	testSymbol = model.Symbol{Board: "TQBR", SecCode: "SBER"}
	// testNow is the recorder clock: book events carry the receive instant.
	testNow = time.Date(2026, 11, 5, 13, 0, 0, 123_456_000, time.UTC)
)

func fixedClock() time.Time { return testNow }

// newTestRecorder builds the recorder core with a deterministic session id.
func newTestRecorder(sink Sink, symbols ...model.Symbol) *recorder {
	if len(symbols) == 0 {
		symbols = []model.Symbol{testSymbol}
	}
	return newRecorder(sink, symbols, "11111111-2222-4333-8444-555555555555", fixedClock)
}

func quote(priceUnits int64, scale uint8, buy, sell int64) model.Quote {
	return model.Quote{Price: model.Decimal{Units: priceUnits, Scale: scale}, Buy: buy, Sell: sell}
}

// snapshotEvent is a full <quotes> callback of two bid and two ask levels.
func snapshotEvent() *model.Quotes {
	return &model.Quotes{
		Symbol: testSymbol,
		Levels: []model.Quote{
			quote(29146, 2, 10, 0),
			quote(29145, 2, 20, 0),
			quote(29147, 2, 0, 30),
			quote(29148, 2, 0, 40),
		},
	}
}

// TestRecorderWritesSnapshotThenLevels is the plan test: the first <quotes>
// after a subscription is the full snapshot (kind snapshot), later callbacks are
// level changes (kind level, -1 quantity = deletion -> qty 0).
func TestRecorderWritesSnapshotThenLevels(t *testing.T) {
	sink := &fakeSink{}
	rec := newTestRecorder(sink)
	if err := rec.handle(context.Background(), []model.Event{snapshotEvent()}); err != nil {
		t.Fatalf("handle snapshot: %v", err)
	}
	books := sink.bookCalls()
	if len(books) != 1 || len(books[0]) != 4 {
		t.Fatalf("book batches = %d, first = %d rows, want 1/4", len(books), len(books[0]))
	}
	wantSides := []Side{SideBid, SideBid, SideAsk, SideAsk}
	wantQty := []int64{10, 20, 30, 40}
	for i, row := range books[0] {
		if row.Kind != BookKindSnapshot {
			t.Fatalf("row %d kind = %d, want snapshot", i, row.Kind)
		}
		if row.Side != wantSides[i] || row.Qty != wantQty[i] {
			t.Fatalf("row %d side/qty = %d/%d, want %d/%d", i, row.Side, row.Qty, wantSides[i], wantQty[i])
		}
		if row.Seq != uint64(i+1) {
			t.Fatalf("row %d seq = %d, want %d", i, row.Seq, i+1)
		}
		if row.SessionID != "11111111-2222-4333-8444-555555555555" {
			t.Fatalf("row %d session = %q", i, row.SessionID)
		}
		if row.Board != "TQBR" || row.SecCode != "SBER" {
			t.Fatalf("row %d symbol = %s:%s", i, row.Board, row.SecCode)
		}
		if !row.TS.Equal(testNow) || !row.RecvTS.Equal(testNow) {
			t.Fatalf("row %d ts/recv_ts = %v/%v, want %v", i, row.TS, row.RecvTS, testNow)
		}
	}
	if got := books[0][0].Price; got != (model.Decimal{Units: 29146, Scale: 2}) {
		t.Fatalf("bid price = %+v, want the exact wire decimal", got)
	}

	changes := &model.Quotes{Symbol: testSymbol, Levels: []model.Quote{
		quote(29146, 2, 15, 0), // quantity change
		quote(29145, 2, -1, 0), // bid deletion
		quote(29147, 2, 0, -1), // ask deletion
	}}
	if err := rec.handle(context.Background(), []model.Event{changes}); err != nil {
		t.Fatalf("handle changes: %v", err)
	}
	books = sink.bookCalls()
	if len(books) != 2 || len(books[1]) != 3 {
		t.Fatalf("book batches = %d, second = %d rows, want 2/3", len(books), len(books[1]))
	}
	for i, row := range books[1] {
		if row.Kind != BookKindLevel {
			t.Fatalf("change row %d kind = %d, want level", i, row.Kind)
		}
		if want := uint64(5 + i); row.Seq != want {
			t.Fatalf("change row %d seq = %d, want %d (monotonic across events)", i, row.Seq, want)
		}
	}
	if got := books[1][0]; got.Side != SideBid || got.Qty != 15 {
		t.Fatalf("bid update = %+v", got)
	}
	if got := books[1][1]; got.Side != SideBid || got.Qty != 0 {
		t.Fatalf("bid deletion = %+v, want qty 0 (schema has no -1)", got)
	}
	if got := books[1][2]; got.Side != SideAsk || got.Qty != 0 {
		t.Fatalf("ask deletion = %+v, want qty 0", got)
	}
	if len(sink.tradeCalls()) != 0 {
		t.Fatal("book-only feed must not emit trades")
	}
}

// TestRecorderMarksGapOnReconnect is the Review Focus 3 test: the loss event
// writes one kind='gap' marker per symbol before the books resume, and the
// recorder re-subscribes after the recovery so the next <quotes> is a new
// snapshot instead of a delta against a stale book.
func TestRecorderMarksGapOnReconnect(t *testing.T) {
	sink := &fakeSink{}
	sub := &fakeSubscriber{}
	rec := newTestRecorder(sink)
	rec.client = sub
	// Reproduce the run start: the initial subscription is sent by run().
	if len(sink.bookCalls()) != 0 {
		t.Fatal("no book rows before the first event")
	}
	if err := rec.handle(context.Background(), []model.Event{snapshotEvent()}); err != nil {
		t.Fatalf("handle snapshot: %v", err)
	}
	// DLL order on a link loss: server_status recover="true".
	if err := rec.handle(context.Background(), []model.Event{model.ServerStatus{Connected: true, Recover: true}}); err != nil {
		t.Fatalf("handle reconnect: %v", err)
	}
	books := sink.bookCalls()
	if len(books) != 2 || len(books[1]) != 1 {
		t.Fatalf("book batches after reconnect = %d, want 2 with the single gap row", len(books))
	}
	gap := books[1][0]
	if gap.Kind != BookKindGap || gap.Side != SideNone || gap.Qty != 0 || gap.Price != (model.Decimal{}) {
		t.Fatalf("gap row = %+v", gap)
	}
	if gap.Board != "TQBR" || gap.SecCode != "SBER" {
		t.Fatalf("gap row symbol = %s:%s", gap.Board, gap.SecCode)
	}
	if len(sub.subscriptions()) != 0 {
		t.Fatal("the recorder must not resubscribe while the link is down")
	}
	// Recovery succeeded: resubscribe (read-only Quotes+AllTrades), no second gap.
	if err := rec.handle(context.Background(), []model.Event{model.ServerStatus{Connected: true}}); err != nil {
		t.Fatalf("handle recovered: %v", err)
	}
	if got := len(sink.bookCalls()); got != 2 {
		t.Fatalf("book batches after recovery = %d, want 2 (callbacks are not books)", got)
	}
	subs := sub.subscriptions()
	if len(subs) != 1 {
		t.Fatalf("subscriptions after recovery = %d, want 1", len(subs))
	}
	if len(subs[0].Quotes) != 1 || subs[0].Quotes[0] != testSymbol || len(subs[0].AllTrades) != 1 || subs[0].AllTrades[0] != testSymbol {
		t.Fatalf("resubscription = %+v, want quotes+alltrades of the symbol", subs[0])
	}
	if len(subs[0].Quotations) != 0 {
		t.Fatal("the recorder is book+trades only: no quotations feed")
	}
	// The first <quotes> after the re-subscription is a fresh snapshot.
	if err := rec.handle(context.Background(), []model.Event{snapshotEvent()}); err != nil {
		t.Fatalf("handle snapshot after reconnect: %v", err)
	}
	books = sink.bookCalls()
	if len(books) != 3 || books[2][0].Kind != BookKindSnapshot {
		t.Fatalf("post-reconnect batch = %+v, want a new snapshot", books[len(books)-1])
	}
}

// TestRecorderKeepsMillis is the plan test: the trade instant keeps the wire
// milliseconds (Time already carries them) and the price stays a model.Decimal.
func TestRecorderKeepsMillis(t *testing.T) {
	sink := &fakeSink{}
	rec := newTestRecorder(sink)
	tradeTime := time.Date(2026, 11, 5, 13, 0, 0, 123_000_000, time.UTC)
	trade := &model.Trade{
		Board: "TQBR", SecCode: "SBER", TradeNo: 42,
		Time: tradeTime, WallTime: tradeTime.Unix(), Millis: 123,
		Price: model.Decimal{Units: 2914600, Scale: 4}, Quantity: 10, BuySell: 'B',
		OpenInterest: 777, HasOpenInterest: true,
	}
	if err := rec.handle(context.Background(), []model.Event{trade}); err != nil {
		t.Fatalf("handle trade: %v", err)
	}
	calls := sink.tradeCalls()
	if len(calls) != 1 || len(calls[0]) != 1 {
		t.Fatalf("trade batches = %d, want 1/1", len(calls))
	}
	row := calls[0][0]
	if !row.TS.Equal(tradeTime) {
		t.Fatalf("ts = %v, want %v", row.TS, tradeTime)
	}
	if got := row.TS.Nanosecond() / int(time.Millisecond); got != 123 {
		t.Fatalf("ts milliseconds = %d, want 123", got)
	}
	if row.Price != (model.Decimal{Units: 2914600, Scale: 4}) {
		t.Fatalf("price = %+v, want the exact model.Decimal", row.Price)
	}
	if row.Side != SideBid || row.Qty != 10 || row.TradeNo != 42 || row.OpenInterest != 777 {
		t.Fatalf("trade row = %+v", row)
	}
	if row.Board != "TQBR" || row.SecCode != "SBER" {
		t.Fatalf("trade row symbol = %s:%s", row.Board, row.SecCode)
	}
	if !row.RecvTS.Equal(testNow) {
		t.Fatalf("recv_ts = %v, want the receive instant %v", row.RecvTS, testNow)
	}
}

// TestRunSubscribesReadOnlyFeeds checks the wiring of Run: book+trades feeds
// only, then the native Run callback feeds the sink.
func TestRunSubscribesReadOnlyFeeds(t *testing.T) {
	sub := &fakeSubscriber{}
	sub.runFunc = func(ctx context.Context, h func([]model.Event) error) error {
		return h([]model.Event{&model.Trade{Board: "TQBR", SecCode: "SBER", TradeNo: 1, Time: testNow}})
	}
	sink := &fakeSink{}
	if err := run(context.Background(), sub, sink, []string{"TQBR:SBER"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	subs := sub.subscriptions()
	if len(subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(subs))
	}
	if len(subs[0].Quotes) != 1 || subs[0].Quotes[0] != testSymbol ||
		len(subs[0].AllTrades) != 1 || subs[0].AllTrades[0] != testSymbol ||
		len(subs[0].Quotations) != 0 {
		t.Fatalf("subscription = %+v, want read-only quotes+alltrades", subs[0])
	}
	if len(sink.tradeCalls()) != 1 {
		t.Fatalf("trade batches = %d, want 1", len(sink.tradeCalls()))
	}
}

// TestRunRejectsBadInput checks the argument validation: a malformed symbol and
// a nil sink are errors, not a silent empty recording.
func TestRunRejectsBadInput(t *testing.T) {
	sub := &fakeSubscriber{}
	if err := run(context.Background(), sub, &fakeSink{}, []string{"SBER"}); err == nil || !strings.Contains(err.Error(), "BOARD:SECCODE") {
		t.Fatalf("bad symbol error = %v", err)
	}
	if err := run(context.Background(), sub, nil, []string{"TQBR:SBER"}); err == nil {
		t.Fatal("nil sink must fail")
	}
	if len(sub.subscriptions()) != 0 {
		t.Fatal("validation must happen before any subscription")
	}
}

// TestRecorderPropagatesSinkError stops the stream when the sink cannot write:
// losing a ClickHouse insert is a gap in the recording, not something to ignore.
func TestRecorderPropagatesSinkError(t *testing.T) {
	sink := &failingSink{err: errors.New("clickhouse is down")}
	rec := newTestRecorder(sink)
	err := rec.handle(context.Background(), []model.Event{snapshotEvent()})
	if err == nil || !strings.Contains(err.Error(), "clickhouse is down") {
		t.Fatalf("handle error = %v, want the sink error", err)
	}
}

type failingSink struct{ err error }

func (f *failingSink) Book([]BookEvent) error    { return f.err }
func (f *failingSink) Trades([]TradeEvent) error { return f.err }
func (f *failingSink) News([]NewsEvent) error    { return f.err }

// newsHeader is one <news_header> event.
func newsHeader(id int64, published time.Time, source, title string) *model.NewsHeader {
	return &model.NewsHeader{ID: id, Time: published, Source: source, Title: title}
}

// TestRecorderStoresNewsWithFirstSeen is the plan Task 12 test: the header is
// recorded with first_seen = the receive instant, a repeated get_old_news answer
// does not write it again, and the body arriving later updates title/body/hash
// but never the first_seen (the point-in-time of the news).
func TestRecorderStoresNewsWithFirstSeen(t *testing.T) {
	sink := &fakeSink{}
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	rec := newRecorder(sink, []model.Symbol{testSymbol}, "sid", func() time.Time { return now })
	ctx := context.Background()
	published := time.Date(2026, 11, 5, 9, 59, 30, 0, time.UTC)
	header := newsHeader(7, published, "SRC", "Заголовок")

	if err := rec.handle(ctx, []model.Event{header}); err != nil {
		t.Fatalf("handle header: %v", err)
	}
	calls := sink.newsCalls()
	if len(calls) != 1 || len(calls[0]) != 1 {
		t.Fatalf("news batches = %v, want one header row", calls)
	}
	first := calls[0][0]
	if !first.FirstSeen.Equal(now) {
		t.Fatalf("first_seen = %v, want the receive instant %v", first.FirstSeen, now)
	}
	if !first.Published.Equal(published) || first.ID != 7 || first.Source != "SRC" || first.Title != "Заголовок" {
		t.Fatalf("header row = %+v", first)
	}
	if first.Body != "" || first.ContentHash == 0 {
		t.Fatalf("header row body/hash = %q/%d, want empty body and a content hash", first.Body, first.ContentHash)
	}

	// get_old_news is polled repeatedly and returns the same header again: the
	// unchanged copy must not produce a second row.
	if err := rec.handle(ctx, []model.Event{newsHeader(7, published, "SRC", "Заголовок")}); err != nil {
		t.Fatalf("handle repeated header: %v", err)
	}
	if got := len(sink.newsCalls()); got != 1 {
		t.Fatalf("news batches after the repeated header = %d, want 1", got)
	}

	// The body comes later: first_seen is frozen, body and hash are updated.
	now = now.Add(2 * time.Minute)
	if err := rec.handle(ctx, []model.Event{&model.NewsBody{ID: 7, Text: "Текст новости"}}); err != nil {
		t.Fatalf("handle body: %v", err)
	}
	calls = sink.newsCalls()
	if len(calls) != 2 || len(calls[1]) != 1 {
		t.Fatalf("news batches = %v, want the body row", calls)
	}
	updated := calls[1][0]
	if !updated.FirstSeen.Equal(first.FirstSeen) {
		t.Fatalf("first_seen after the body = %v, want the header instant %v (body must not move it)", updated.FirstSeen, first.FirstSeen)
	}
	if updated.Body != "Текст новости" || updated.Title != "Заголовок" || updated.ID != 7 {
		t.Fatalf("body row = %+v", updated)
	}
	if updated.ContentHash == first.ContentHash || updated.ContentHash == 0 {
		t.Fatalf("content hash = %d, want a new non-zero hash of the body", updated.ContentHash)
	}

	// A body of an unknown news is ignored (native emits it only for requested
	// headers, this is a defensive check).
	if err := rec.handle(ctx, []model.Event{&model.NewsBody{ID: 999, Text: "чужая"}}); err != nil {
		t.Fatalf("handle unknown body: %v", err)
	}
	if got := len(sink.newsCalls()); got != 2 {
		t.Fatalf("news batches after the unknown body = %d, want 2", got)
	}
}

// TestRecorderNewsHeaderChangeKeepsFirstSeen checks the ReplacingMergeTree
// version rule: a corrected title is written as a new row of the same news, and
// the first_seen is still the original receive instant.
func TestRecorderNewsHeaderChangeKeepsFirstSeen(t *testing.T) {
	sink := &fakeSink{}
	now := time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC)
	rec := newRecorder(sink, []model.Symbol{testSymbol}, "sid", func() time.Time { return now })
	published := now.Add(-time.Minute)
	ctx := context.Background()
	if err := rec.handle(ctx, []model.Event{newsHeader(8, published, "SRC", "Черновик")}); err != nil {
		t.Fatalf("handle header: %v", err)
	}
	now = now.Add(5 * time.Minute)
	if err := rec.handle(ctx, []model.Event{newsHeader(8, published, "SRC", "Исправленный заголовок")}); err != nil {
		t.Fatalf("handle changed header: %v", err)
	}
	calls := sink.newsCalls()
	if len(calls) != 2 {
		t.Fatalf("news batches = %d, want a row for the correction", len(calls))
	}
	if got := calls[1][0]; got.Title != "Исправленный заголовок" || !got.FirstSeen.Equal(calls[0][0].FirstSeen) {
		t.Fatalf("corrected row = %+v, want the same first_seen", got)
	}
}

// TestRunPollsOldNews checks the recorder news ingestion: Run requests the news
// headers from the client (there is no news subscription on the wire), and the
// poller stops with the recording session.
func TestRunPollsOldNews(t *testing.T) {
	sub := &fakeSubscriber{}
	sub.runFunc = func(ctx context.Context, h func([]model.Event) error) error {
		waitUntil(t, "the initial get_old_news request", func() bool { return sub.oldNewsCalls() > 0 })
		return nil
	}
	sink := &fakeSink{}
	if err := run(context.Background(), sub, sink, []string{"TQBR:SBER"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := sub.oldNewsCalls(); got == 0 {
		t.Fatal("Run did not request the news headers")
	}
}
