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
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
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

// Sink receives the recorder batches: Book for md.book_events rows and Trades
// for md.trades rows. A nil error means the batch is durably accepted; the
// recorder stops the stream on an error (a lost insert is a gap in the data).
type Sink interface {
	Book([]BookEvent) error
	Trades([]TradeEvent) error
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
	return cl.Run(ctx, native.RunOptions{}, func(events []model.Event) error {
		return rec.handle(ctx, events)
	})
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
	return &recorder{sink: sink, symbols: symbols, sessionID: sessionID, now: now, pending: pending}
}

// handle converts one Run callback batch into sink batches. Book rows of the
// whole batch are written first (their order inside the batch is preserved),
// then the trades.
func (r *recorder) handle(ctx context.Context, events []model.Event) error {
	var books []BookEvent
	var trades []TradeEvent
	for _, event := range events {
		switch ev := event.(type) {
		case *model.Quotes:
			books = append(books, r.quotesEvents(ev)...)
		case *model.Trade:
			if row, ok := r.tradeEvent(ev); ok {
				trades = append(trades, row)
			}
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
