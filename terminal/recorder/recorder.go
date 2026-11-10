// Package recorder writes the live order book and trade feed of native.Client to
// a column store (ClickHouse). It is read-only on the exchange side: it
// subscribes the <quotes> and <alltrades> feeds and never sends trading
// commands.
package recorder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native"
	"github.com/kmlebedev/txmlconnector-native/native/model"
)

// News ingestion tuning. The TRANSAQ news feed has no subscription on the wire:
// the headers come from get_old_news polling and the bodies from get_news_body
// requests (docs/protocol/news.md in txmlconnector-native).
const (
	// defaultNewsPollInterval is how often the recorder asks for the news
	// headers with get_old_news.
	defaultNewsPollInterval = 30 * time.Second
	// defaultNewsBodyTimeout bounds the wait for one <news_body> event.
	defaultNewsBodyTimeout = 5 * time.Second
	// newsHeaderCount is the get_old_news count (the protocol maximum is 100).
	newsHeaderCount = 100
	// newsNotifyBuffer is the body-request queue size; a full queue drops the
	// notification (the next header poll re-notifies).
	newsNotifyBuffer = 64
)

// BookKind is one md.book_events.kind value: the shape of the row.
type BookKind int8

const (
	// BookKindSnapshot is the full order book written after a (re)subscription.
	BookKindSnapshot BookKind = 1
	// BookKindLevel is one level change; a deleted level keeps its price with
	// qty 0.
	BookKindLevel BookKind = 2
	// BookKindGap marks a recording break (a lost connection): the replay must
	// not join the book before and after it.
	BookKindGap BookKind = 3
)

// String returns the ClickHouse Enum8 name of the kind; "" for an invalid value.
func (k BookKind) String() string {
	switch k {
	case BookKindSnapshot:
		return "snapshot"
	case BookKindLevel:
		return "level"
	case BookKindGap:
		return "gap"
	}
	return ""
}

// Side is the row side. The md schema reuses the values 1/2 for both enums: the
// book names them bid/ask and the trades buy/sell (see schema.go).
type Side int8

const (
	SideNone Side = 0
	SideBid  Side = 1
	SideAsk  Side = 2
)

// String returns the md.book_events.side Enum8 name; "" for an invalid value.
func (s Side) String() string {
	switch s {
	case SideNone:
		return "none"
	case SideBid:
		return "bid"
	case SideAsk:
		return "ask"
	}
	return ""
}

// BookEvent is one md.book_events row. Prices are model.Decimal: no float64
// touches them. TS is the observation instant (the R_QUOTES record carries no
// exchange time), RecvTS the exact receive instant, Seq the session-wide row
// sequence that orders rows observed in the same millisecond.
type BookEvent struct {
	TS        time.Time
	RecvTS    time.Time
	SessionID string
	Seq       uint64
	Board     string
	SecCode   string
	Kind      BookKind
	Side      Side
	Price     model.Decimal
	Qty       int64
}

// TradeEvent is one md.trades row. TS is the exchange trade instant with the
// wire milliseconds (model.Trade.Time already includes them), RecvTS the
// receive instant. The trade number identifies the row (ReplacingMergeTree
// ORDER BY sec_code, trade_no), so no session id is stored.
type TradeEvent struct {
	TS           time.Time
	RecvTS       time.Time
	Board        string
	SecCode      string
	TradeNo      int64
	Price        model.Decimal
	Qty          int64
	Side         Side
	OpenInterest int64
}

// NewsEvent is one md.news row: a TRANSAQ news header with the body when it
// arrives. FirstSeen is the instant the recorder first saw the news — the
// point-in-time used as the ReplacingMergeTree version; a body arriving later
// updates the row content but never FirstSeen. Published is the exchange
// timestamp of the wire header.
type NewsEvent struct {
	FirstSeen   time.Time
	Published   time.Time
	ID          int64
	Source      string
	Title       string
	Body        string
	ContentHash uint64
}

// Sink receives the recorder batches: Book for md.book_events rows, Trades for
// md.trades rows and News for md.news rows. A nil error means the batch is
// durably accepted; the recorder stops the stream on an error (a lost insert is
// a gap in the data).
type Sink interface {
	Book([]BookEvent) error
	Trades([]TradeEvent) error
	News([]NewsEvent) error
}

// subscriber is the native.Client subset the recorder needs; the tests replace
// it with a fake, and the only production implementation is *native.Client.
type subscriber interface {
	Subscribe(ctx context.Context, sub native.Subscription) error
	Run(ctx context.Context, opt native.RunOptions, h func([]model.Event) error) error
}

// Run records the order book and trade feed of cl into sink until ctx is
// cancelled or the session fails. symbols are "BOARD:SECCODE" entries; each is
// subscribed to the read-only <quotes> and <alltrades> feeds. The first
// <quotes> callback after a subscription is the snapshot; later callbacks are
// level changes.
//
// A lost connection (model.ServerStatus{Recover:true}) writes one kind='gap'
// row per symbol; after a successful recovery the recorder re-subscribes, so
// the next <quotes> is a new snapshot instead of a delta against a stale book.
func Run(ctx context.Context, cl *native.Client, sink Sink, symbols []string) error {
	if cl == nil {
		return errors.New("recorder: nil native client")
	}
	return run(ctx, cl, sink, symbols)
}

// run is Run over the subscriber interface (unit tests pass a fake).
func run(ctx context.Context, cl subscriber, sink Sink, symbols []string) error {
	if cl == nil {
		return errors.New("recorder: nil native client")
	}
	if sink == nil {
		return errors.New("recorder: nil sink")
	}
	syms, err := parseSymbols(symbols)
	if err != nil {
		return err
	}
	sessionID, err := newSessionID()
	if err != nil {
		return err
	}
	rec := newRecorder(sink, syms, sessionID, time.Now)
	rec.client = cl
	if err := rec.subscribe(ctx); err != nil {
		return fmt.Errorf("recorder: subscribe: %w", err)
	}
	// The news feed is request-driven, so the recorder polls it in a goroutine
	// bound to the session lifetime; the data itself arrives via Run.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if fetcher, ok := cl.(newsFetcher); ok {
		go rec.pollNews(sessionCtx, fetcher)
	}
	return cl.Run(sessionCtx, native.RunOptions{}, func(events []model.Event) error {
		return rec.handle(ctx, events)
	})
}

// newsFetcher is the native.Client news API: the news headers are requested
// with get_old_news and the bodies with get_news_body (news.md).
type newsFetcher interface {
	OldNews(ctx context.Context, count int) error
	NewsBody(ctx context.Context, id int64) (model.NewsBody, error)
}

// newsRecord is the recorder state of one news story.
type newsRecord struct {
	header    model.NewsHeader
	firstSeen time.Time
	body      string
	requested bool // a get_news_body request is in flight
}

// recorder is the stateful event-to-row converter.
type recorder struct {
	sink      Sink
	client    subscriber // nil in the pure event tests
	symbols   []model.Symbol
	sessionID string
	now       func() time.Time
	// pending marks the symbols whose next <quotes> callback is a full
	// snapshot (after the initial subscription or a reconnect).
	pending map[model.Symbol]bool
	seq     uint64

	// News state: the map is shared with the news poller goroutine.
	newsMu          sync.Mutex
	news            map[int64]*newsRecord
	bodyWaiters     map[int64]chan struct{}
	newsNotify      chan int64
	newsPoll        time.Duration // test seam; defaultNewsPollInterval when 0
	newsBodyTimeout time.Duration // test seam; defaultNewsBodyTimeout when 0
}

// newRecorder builds the converter; now is the clock used for receive instants.
func newRecorder(sink Sink, symbols []model.Symbol, sessionID string, now func() time.Time) *recorder {
	if now == nil {
		now = time.Now
	}
	pending := make(map[model.Symbol]bool, len(symbols))
	for _, symbol := range symbols {
		pending[symbol] = true
	}
	return &recorder{
		sink:        sink,
		symbols:     symbols,
		sessionID:   sessionID,
		now:         now,
		pending:     pending,
		news:        map[int64]*newsRecord{},
		bodyWaiters: map[int64]chan struct{}{},
		newsNotify:  make(chan int64, newsNotifyBuffer),
	}
}

// handle converts one Run callback batch into sink batches. Book rows of the
// whole batch are written first (their order inside the batch is preserved),
// then the trades.
func (r *recorder) handle(ctx context.Context, events []model.Event) error {
	var books []BookEvent
	var trades []TradeEvent
	var news []NewsEvent
	for _, event := range events {
		switch ev := event.(type) {
		case *model.Quotes:
			books = append(books, r.quotesEvents(ev)...)
		case *model.Trade:
			if row, ok := r.tradeEvent(ev); ok {
				trades = append(trades, row)
			}
		case *model.NewsHeader:
			news = append(news, r.handleNewsHeader(ev)...)
		case *model.NewsBody:
			news = append(news, r.handleNewsBody(ev)...)
		case model.ServerStatus:
			switch {
			case ev.Recover:
				// The link is lost: mark the break before any possible recovery
				// failure, and make the next <quotes> a fresh snapshot.
				books = append(books, r.gapEvents()...)
			case ev.Connected:
				// Recovery succeeded: re-subscribe to get a new book snapshot.
				if err := r.subscribe(ctx); err != nil {
					return fmt.Errorf("recorder: resubscribe after reconnect: %w", err)
				}
			}
		}
	}
	if len(books) > 0 {
		if err := r.sink.Book(books); err != nil {
			return fmt.Errorf("recorder: write book events: %w", err)
		}
	}
	if len(trades) > 0 {
		if err := r.sink.Trades(trades); err != nil {
			return fmt.Errorf("recorder: write trades: %w", err)
		}
	}
	if len(news) > 0 {
		if err := r.sink.News(news); err != nil {
			return fmt.Errorf("recorder: write news: %w", err)
		}
	}
	return nil
}

// subscribe sends the read-only feeds of the recorder symbols.
func (r *recorder) subscribe(ctx context.Context) error {
	if r.client == nil {
		return nil
	}
	return r.client.Subscribe(ctx, native.Subscription{Quotes: r.symbols, AllTrades: r.symbols})
}

// quotesEvents converts one <quotes> callback: the full snapshot after a
// (re)subscription, a level delta afterwards. A deletion (-1) becomes a level
// row of the same price with qty 0, as the schema has no negative quantity.
func (r *recorder) quotesEvents(ev *model.Quotes) []BookEvent {
	if ev.Symbol.Board == "" || ev.Symbol.SecCode == "" {
		return nil
	}
	snapshot := r.pending[ev.Symbol]
	now := r.now().UTC()
	out := make([]BookEvent, 0, len(ev.Levels))
	for _, level := range ev.Levels {
		side, qty, ok := levelSideQty(level)
		if !ok {
			continue
		}
		kind := BookKindLevel
		if snapshot {
			kind = BookKindSnapshot
		}
		r.seq++
		out = append(out, BookEvent{
			TS: now, RecvTS: now, SessionID: r.sessionID, Seq: r.seq,
			Board: ev.Symbol.Board, SecCode: ev.Symbol.SecCode,
			Kind: kind, Side: side, Price: level.Price, Qty: qty,
		})
	}
	// An empty book snapshot keeps the symbol pending: nothing was written, so
	// the next callback is still the snapshot baseline.
	if snapshot && len(out) > 0 {
		r.pending[ev.Symbol] = false
	}
	return out
}

// levelSideQty maps one <quote> level: Buy/Sell hold the quantity of their
// side, -1 is the DLL deletion marker (PDF 4.9, no separate flag).
func levelSideQty(level model.Quote) (Side, int64, bool) {
	switch {
	case level.Buy != 0:
		if level.Buy < 0 {
			return SideBid, 0, true
		}
		return SideBid, level.Buy, true
	case level.Sell != 0:
		if level.Sell < 0 {
			return SideAsk, 0, true
		}
		return SideAsk, level.Sell, true
	}
	return SideNone, 0, false
}

// tradeEvent converts one <alltrades> trade.
func (r *recorder) tradeEvent(ev *model.Trade) (TradeEvent, bool) {
	if ev.Board == "" || ev.SecCode == "" {
		return TradeEvent{}, false
	}
	ts := ev.Time.UTC()
	if ts.IsZero() {
		// A record without a usable trade time is dated by its receive instant:
		// the column has no year-1 value to store.
		ts = r.now().UTC()
	}
	row := TradeEvent{
		TS: ts, RecvTS: r.now().UTC(),
		Board: ev.Board, SecCode: ev.SecCode,
		TradeNo: ev.TradeNo, Price: ev.Price, Qty: ev.Quantity,
		Side: tradeSide(ev.BuySell),
	}
	if ev.HasOpenInterest {
		row.OpenInterest = ev.OpenInterest
	}
	return row, true
}

// tradeSide maps the wire <buysell> byte to the enum value.
func tradeSide(buySell byte) Side {
	switch buySell {
	case 'B':
		return SideBid
	case 'S':
		return SideAsk
	}
	return SideNone
}

// gapEvents writes one kind='gap' row per recorded symbol.
func (r *recorder) gapEvents() []BookEvent {
	now := r.now().UTC()
	out := make([]BookEvent, 0, len(r.symbols))
	for _, symbol := range r.symbols {
		r.seq++
		out = append(out, BookEvent{
			TS: now, RecvTS: now, SessionID: r.sessionID, Seq: r.seq,
			Board: symbol.Board, SecCode: symbol.SecCode,
			Kind: BookKindGap, Side: SideNone,
		})
		r.pending[symbol] = true
	}
	return out
}

// parseSymbols parses "BOARD:SECCODE" entries, dropping duplicates.
func parseSymbols(raw []string) ([]model.Symbol, error) {
	out := make([]model.Symbol, 0, len(raw))
	seen := make(map[model.Symbol]bool, len(raw))
	for _, item := range raw {
		board, secCode, ok := strings.Cut(strings.TrimSpace(item), ":")
		board, secCode = strings.TrimSpace(board), strings.TrimSpace(secCode)
		if !ok || board == "" || secCode == "" {
			return nil, fmt.Errorf("recorder: bad symbol %q (want BOARD:SECCODE)", item)
		}
		symbol := model.Symbol{Board: board, SecCode: secCode}
		if seen[symbol] {
			continue
		}
		seen[symbol] = true
		out = append(out, symbol)
	}
	if len(out) == 0 {
		return nil, errors.New("recorder: no symbols to record")
	}
	return out, nil
}

// newSessionID returns a random UUIDv4 for md.book_events.session_id.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("recorder: session id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:]), nil
}

// handleNewsHeader records one <news_header> event. The first sighting sets
// FirstSeen; get_old_news is polled repeatedly, so an unchanged header is not
// written again. A changed header (a correction) is written as a new row of the
// same news keeping the original FirstSeen.
func (r *recorder) handleNewsHeader(ev *model.NewsHeader) []NewsEvent {
	if ev == nil || ev.ID == 0 {
		return nil
	}
	r.newsMu.Lock()
	rec, seen := r.news[ev.ID]
	emit := false
	if !seen {
		rec = &newsRecord{header: *ev, firstSeen: r.now().UTC()}
		r.news[ev.ID] = rec
		emit = true
	} else if rec.header != *ev {
		rec.header = *ev
		emit = true
	}
	needBody := rec.body == "" && !rec.requested
	row := newsEvent(rec)
	r.newsMu.Unlock()
	if needBody {
		r.notifyNewsBody(ev.ID)
	}
	if !emit {
		return nil
	}
	return []NewsEvent{row}
}

// handleNewsBody records the <news_body> of a known news: the content changes,
// the FirstSeen does not.
func (r *recorder) handleNewsBody(ev *model.NewsBody) []NewsEvent {
	if ev == nil {
		return nil
	}
	r.newsMu.Lock()
	rec := r.news[ev.ID]
	if rec == nil {
		r.newsMu.Unlock()
		return nil
	}
	rec.body = ev.Text
	row := newsEvent(rec)
	if waiter := r.bodyWaiters[ev.ID]; waiter != nil {
		close(waiter)
		delete(r.bodyWaiters, ev.ID)
	}
	r.newsMu.Unlock()
	return []NewsEvent{row}
}

// newsEvent builds the md.news row of one news. FirstSeen is frozen at the first
// sighting; the content hash covers source, title and body of the moment.
func newsEvent(rec *newsRecord) NewsEvent {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(rec.header.Source))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(rec.header.Title))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(rec.body))
	return NewsEvent{
		FirstSeen:   rec.firstSeen,
		Published:   rec.header.Time.UTC(),
		ID:          rec.header.ID,
		Source:      rec.header.Source,
		Title:       rec.header.Title,
		Body:        rec.body,
		ContentHash: hash.Sum64(),
	}
}

// pollNews owns the news ingestion of the recording session: get_old_news every
// news poll and one get_news_body per newly seen header. The events themselves
// arrive through the Run callback, so the poller only sends the requests.
func (r *recorder) pollNews(ctx context.Context, fetcher newsFetcher) {
	interval := r.newsPoll
	if interval <= 0 {
		interval = defaultNewsPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	r.fetchNewsHeaders(ctx, fetcher)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.fetchNewsHeaders(ctx, fetcher)
		case id := <-r.newsNotify:
			r.fetchNewsBody(ctx, fetcher, id)
		}
	}
}

// fetchNewsHeaders requests the news headers. A failure is best effort: a news
// hiccup must not stop the market recording.
func (r *recorder) fetchNewsHeaders(ctx context.Context, fetcher newsFetcher) {
	if ctx.Err() != nil {
		return
	}
	_ = fetcher.OldNews(ctx, newsHeaderCount)
}

// fetchNewsBody requests one news body and waits for its <news_body> event: the
// native client keeps a single pending body request, so this goroutine
// serializes the requests (it is the only caller of NewsBody).
func (r *recorder) fetchNewsBody(ctx context.Context, fetcher newsFetcher, id int64) {
	waiter, ok := r.registerBodyWaiter(id)
	if !ok {
		return
	}
	defer r.dropBodyWaiter(id)
	if _, err := fetcher.NewsBody(ctx, id); err != nil {
		r.resetBodyRequest(id)
		return
	}
	timeout := r.newsBodyTimeout
	if timeout <= 0 {
		timeout = defaultNewsBodyTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-waiter:
	case <-ctx.Done():
		r.resetBodyRequest(id)
	case <-timer.C:
		r.resetBodyRequest(id)
	}
}

// registerBodyWaiter starts waiting for the body of one news.
func (r *recorder) registerBodyWaiter(id int64) (<-chan struct{}, bool) {
	r.newsMu.Lock()
	defer r.newsMu.Unlock()
	rec := r.news[id]
	if rec == nil || rec.body != "" || r.bodyWaiters[id] != nil {
		return nil, false
	}
	waiter := make(chan struct{})
	r.bodyWaiters[id] = waiter
	rec.requested = true
	return waiter, true
}

// dropBodyWaiter forgets the waiter of one news.
func (r *recorder) dropBodyWaiter(id int64) {
	r.newsMu.Lock()
	delete(r.bodyWaiters, id)
	r.newsMu.Unlock()
}

// resetBodyRequest allows another get_news_body attempt after a failure.
func (r *recorder) resetBodyRequest(id int64) {
	r.newsMu.Lock()
	if rec := r.news[id]; rec != nil {
		rec.requested = false
	}
	r.newsMu.Unlock()
}

// notifyNewsBody queues a body request without blocking the Run callback; a full
// queue drops it (the next header poll re-notifies).
func (r *recorder) notifyNewsBody(id int64) {
	select {
	case r.newsNotify <- id:
	default:
	}
}
