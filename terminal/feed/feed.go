// Package feed is the market-data source of the AI terminal. It has two modes:
// offline reads an embedded demo-server snapshot (terminal/seeddata) and serves
// it without network, live streams TRANSAQ quotations, order books and trades
// through native.Client. Both modes expose the same typed, concurrency-safe API
// to the HTTP layer and to the agent data adapter.
package feed

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Mode is the data-source mode of the terminal.
type Mode string

// Feed modes.
const (
	ModeLive    Mode = "live"
	ModeOffline Mode = "offline"
)

// Config configures the feed service.
type Config struct {
	Mode       Mode
	Address    string        // host:port (live)
	Login      string        // live login; never logged
	Password   string        // live password; never logged
	SeedDir    string        // empty = embedded seeddata.FS; file or dir otherwise
	Universe   []string      // "TQBR:SBER"; empty = the seed universe
	RqDelay    time.Duration // RQ_MAIN poll period, default 100ms
	HistoryTTL time.Duration // live history cache TTL, default 60s
}

// Status is the current state of the market-data source.
type Status struct {
	Mode            Mode
	Connected       bool
	Message         string    // human-readable state/error
	ServerTime      time.Time // last known exchange/server time, UTC
	ExchangeTZ      string    // e.g. "Europe/Moscow"
	SeedGeneratedAt time.Time
	LastEventAt     time.Time
}

// Instrument is one tradable instrument with its latest quote snapshot.
type Instrument struct {
	Symbol    string
	Board     string
	SecCode   string
	Name      string
	ShortName string
	Currency  string
	Decimals  int
	LotSize   int64
	MinStep   float64

	Last, Change, ChangePct    float64
	PrevClose, Open, High, Low float64
	Volume                     int64
	Bid, Ask                   float64
	Spark                      []float64
	UpdatedAt                  time.Time
}

// Bar is one OHLCV candle.
type Bar struct {
	Time                   time.Time
	Open, High, Low, Close float64
	Volume                 int64
}

// History is one candle series response.
type History struct {
	Symbol    string
	PeriodSec int
	Bars      []Bar
	Source    string // "live" | "seed"
	Note      string // e.g. "демо-снапшот 05.11.2026"
	// Warnings reports a series that cannot be trusted as typed. The live
	// source forwards "unknown candle period N" of native/records: the period
	// was not in the session <candlekinds>, so the bars carry zero times
	// instead of guessed minutes (R8). Empty means the series is clean.
	Warnings []string
}

// Level is one order-book level.
type Level struct{ Price, Qty float64 }

// OrderBook is a price ladder snapshot, best levels first.
type OrderBook struct {
	Symbol     string
	Last       float64
	Time       time.Time
	Bids, Asks []Level
	Source     string
}

// Quote is the latest quote of one instrument.
type Quote struct {
	Symbol    string
	Last      float64
	Change    float64
	ChangePct float64
	Bid, Ask  float64
	Volume    int64
	Time      time.Time
}

// Trade is one tick-tape entry.
type Trade struct {
	Time  time.Time
	Price float64
	Qty   int64
	Side  string // "buy" | "sell" | ""
}

// Event is one change published to subscribers. Type is "quote" | "orderbook" |
// "status"; Data carries the typed value (Quote, OrderBook or Status).
type Event struct {
	Type string
	Data any
}

// Error codes of the feed package.
const (
	// CodeBadPeriod marks an unsupported candle period.
	CodeBadPeriod = "bad_period"
	// CodeNotFound marks an unknown instrument or missing data.
	CodeNotFound = "not_found"
	// CodeUnavailable marks a live-only operation attempted offline.
	CodeUnavailable = "unavailable"
)

// Error is a typed feed error with a stable code and a user-facing Russian
// message.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// Periods are the supported candle periods in seconds (1m/5m/15m/1h/1d).
var Periods = []int{60, 300, 900, 3600, 86400}

// ValidPeriod reports whether periodSec is one of the supported periods.
func ValidPeriod(periodSec int) bool {
	for _, p := range Periods {
		if p == periodSec {
			return true
		}
	}
	return false
}

// periodError builds the bad_period error for an unsupported period.
func periodError(periodSec int) *Error {
	return &Error{
		Code:    CodeBadPeriod,
		Message: fmt.Sprintf("неподдерживаемый период свечей: %d с (допустимы 60, 300, 900, 3600, 86400)", periodSec),
	}
}

// notFound builds a not_found error for a symbol.
func notFound(symbol string) *Error {
	return &Error{Code: CodeNotFound, Message: fmt.Sprintf("инструмент не найден: %s", symbol)}
}

// defaultHistoryCount clamps a candle count request into a sane range.
const (
	defaultHistoryCount = 500
	maxHistoryCount     = 5000
	defaultTradeLimit   = 50
	maxTradeLimit       = 200
	maxBookLevels       = 20
)

// historyCount normalizes count: zero or negative means the default, too large
// is clamped to the maximum.
func historyCount(count int) int {
	switch {
	case count <= 0:
		return defaultHistoryCount
	case count > maxHistoryCount:
		return maxHistoryCount
	}
	return count
}

// tradeLimit normalizes a tick-tape limit.
func tradeLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultTradeLimit
	case limit > maxTradeLimit:
		return maxTradeLimit
	}
	return limit
}

// historyEntry is a live history cache record. count is the candle count the
// series was fetched with: a cached series satisfies any request up to that
// count, a larger request re-fetches (a smaller one is trimmed).
type historyEntry struct {
	history  History
	expireAt time.Time
	count    int
}

// Service is the market-data source. It is safe for concurrent use.
type Service struct {
	cfg  Config
	mode Mode
	seed *seedFile

	mu           sync.RWMutex
	status       Status
	universe     []string
	symbols      []symbolRef
	instruments  map[string]*Instrument
	quotes       map[string]Quote
	books        map[string]OrderBook
	trades       map[string][]Trade
	historyCache map[string]*historyEntry
	candleKinds  map[int]int
	serverOffset time.Duration
	exchangeTZ   string

	subsMu sync.Mutex
	subs   map[uint64]func(Event)
	subSeq uint64

	// history flights deduplicate concurrent live history requests of the same
	// symbol+period: native.Client allows only one History request per series
	// in flight, a second concurrent one would fail with "already in flight".
	flightMu sync.Mutex
	flights  map[string]*historyFlight

	// Live state, used by live.go. It is zero in offline mode.
	client        marketClient
	backoffMin    time.Duration
	backoffMax    time.Duration
	stableSession time.Duration
	dial          liveDial
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	closed        bool
}

// New builds the market-data service. Offline mode loads the seed immediately
// (a broken seed is reported as an error); live mode validates the connection
// configuration and dials in Start.
func New(cfg Config) (*Service, error) {
	cfg = withDefaults(cfg)
	if cfg.Mode != ModeOffline && cfg.Mode != ModeLive {
		return nil, fmt.Errorf("feed: неизвестный режим работы %q", cfg.Mode)
	}
	seed, err := loadSeed(cfg.SeedDir)
	if err != nil {
		return nil, err
	}
	s := &Service{
		cfg:          cfg,
		mode:         cfg.Mode,
		seed:         seed,
		instruments:  map[string]*Instrument{},
		quotes:       map[string]Quote{},
		books:        map[string]OrderBook{},
		trades:       map[string][]Trade{},
		historyCache: map[string]*historyEntry{},
		subs:         map[uint64]func(Event){},
		exchangeTZ:   exchangeZoneName(seed.ExchangeTZ),
	}
	s.symbols = parseSymbols(universeSymbols(cfg.Universe, seed.Universe))
	s.universe = universeSymbols(cfg.Universe, seed.Universe)
	switch cfg.Mode {
	case ModeOffline:
		if err := s.loadOffline(seed); err != nil {
			return nil, err
		}
	case ModeLive:
		if cfg.Address == "" || cfg.Login == "" || cfg.Password == "" {
			return nil, fmt.Errorf("feed: для live-режима нужны адрес, логин и пароль (флаги/env)")
		}
		s.loadLiveSkeleton(seed)
	}
	s.dial = dialNative
	s.backoffMin = 2 * time.Second
	s.backoffMax = 30 * time.Second
	s.stableSession = stableSessionDuration
	s.flights = map[string]*historyFlight{}
	return s, nil
}

// withDefaults fills the documented defaults.
func withDefaults(cfg Config) Config {
	if cfg.RqDelay <= 0 {
		cfg.RqDelay = 100 * time.Millisecond
	}
	if cfg.HistoryTTL <= 0 {
		cfg.HistoryTTL = 60 * time.Second
	}
	return cfg
}

// universeSymbols prefers the configured universe and falls back to the seed.
func universeSymbols(configured, seeded []string) []string {
	if len(configured) > 0 {
		out := make([]string, 0, len(configured))
		for _, sym := range configured {
			if sym != "" {
				out = append(out, sym)
			}
		}
		return out
	}
	return append([]string(nil), seeded...)
}

// Start connects the live session and starts the reconnect loop. Offline mode
// needs no work: the seed is already loaded.
func (s *Service) Start(ctx context.Context) error {
	if s.mode != ModeLive {
		return nil
	}
	s.mu.Lock()
	if s.client != nil || s.ctx != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	runCtx, cancel := context.WithCancel(ctx)
	client, err := s.dial(runCtx, s.clientConfig())
	if err != nil {
		cancel()
		return fmt.Errorf("feed: не удалось подключиться к серверу биржи: %w", err)
	}
	s.mu.Lock()
	s.client = client
	s.ctx = runCtx
	s.cancel = cancel
	s.mu.Unlock()
	// The catalog is only safe to read while Run is not polling, so the
	// instrument metadata is refreshed here, before the run loop starts.
	s.applyCatalog(client)
	s.refreshServerTime(runCtx, client)
	s.mu.Lock()
	s.status.Connected = true
	s.status.Message = "подключено к серверу биржи"
	s.mu.Unlock()
	s.publish(Event{Type: "status", Data: s.Status()})
	s.wg.Add(1)
	go s.runLoop(client)
	return nil
}

// Close stops the live session (idempotent). Offline mode has nothing to stop.
func (s *Service) Close() error {
	if s.mode != ModeLive {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.mu.Lock()
	client := s.client
	s.client = nil
	s.mu.Unlock()
	if client != nil {
		return client.Close()
	}
	return nil
}

// Status returns the current state of the data source.
func (s *Service) Status() Status {
	s.mu.RLock()
	st := s.status
	offset := s.serverOffset
	s.mu.RUnlock()
	// In live mode the exchange clock keeps running: render the current
	// instant from the measured offset (a zero offset is meaningful too).
	if st.Mode == ModeLive && st.Connected {
		st.ServerTime = time.Now().UTC().Add(offset)
	}
	return st
}

// Universe returns the instrument list order (a copy).
func (s *Service) Universe() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.universe...)
}

// Instruments returns all instruments in universe order (deep copies).
func (s *Service) Instruments() []Instrument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Instrument, 0, len(s.universe))
	for _, symbol := range s.universe {
		if inst := s.instruments[symbol]; inst != nil {
			out = append(out, copyInstrument(inst))
		}
	}
	return out
}

// Instrument returns one instrument by symbol.
func (s *Service) Instrument(symbol string) (Instrument, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst := s.instruments[symbol]
	if inst == nil {
		return Instrument{}, false
	}
	return copyInstrument(inst), true
}

// Quote returns the latest quote of one instrument.
func (s *Service) Quote(symbol string) (Quote, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q, ok := s.quotes[symbol]
	return q, ok
}

// Candles returns a candle series. Supported periods are 60, 300, 900, 3600 and
// 86400 seconds; unsupported periods return a *feed.Error with code
// bad_period.
func (s *Service) Candles(ctx context.Context, symbol string, periodSec, count int) (History, error) {
	if !ValidPeriod(periodSec) {
		return History{}, periodError(periodSec)
	}
	if s.mode == ModeOffline {
		return s.offlineCandles(symbol, periodSec, historyCount(count))
	}
	return s.liveCandles(ctx, symbol, periodSec, historyCount(count))
}

// OrderBook returns the current price ladder.
func (s *Service) OrderBook(ctx context.Context, symbol string) (OrderBook, error) {
	if s.mode == ModeOffline {
		return s.offlineOrderBook(symbol)
	}
	return s.liveOrderBook(symbol)
}

// Trades returns the most recent tick-tape entries.
func (s *Service) Trades(ctx context.Context, symbol string, limit int) ([]Trade, error) {
	if s.mode == ModeOffline {
		return s.offlineTrades(symbol, tradeLimit(limit))
	}
	return s.liveTrades(symbol, tradeLimit(limit))
}

// Subscribe registers a market-data event sink and returns its cancel function.
// Sinks are called synchronously from the feed goroutine, so they must not
// block.
func (s *Service) Subscribe(fn func(Event)) (cancel func()) {
	if fn == nil {
		return func() {}
	}
	s.subsMu.Lock()
	s.subSeq++
	id := s.subSeq
	s.subs[id] = fn
	s.subsMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.subsMu.Lock()
			delete(s.subs, id)
			s.subsMu.Unlock()
		})
	}
}

// publish delivers an event to every current subscriber.
func (s *Service) publish(ev Event) {
	s.subsMu.Lock()
	sinks := make([]func(Event), 0, len(s.subs))
	for _, fn := range s.subs {
		sinks = append(sinks, fn)
	}
	s.subsMu.Unlock()
	for _, fn := range sinks {
		fn(ev)
	}
}

// copyInstrument returns a deep copy (the Spark slice is copied too).
func copyInstrument(in *Instrument) Instrument {
	out := *in
	if in.Spark != nil {
		out.Spark = append([]float64(nil), in.Spark...)
	}
	return out
}

// changePct derives the day change percentage from the last price and the price
// change. A zero previous close yields zero.
func changePct(last, change float64) float64 {
	prev := last - change
	if prev == 0 {
		return 0
	}
	return change / prev * 100
}

// limitLevels clamps a price ladder to the best maxBookLevels levels.
func limitLevels(levels []Level) []Level {
	if len(levels) > maxBookLevels {
		return levels[:maxBookLevels]
	}
	return levels
}
