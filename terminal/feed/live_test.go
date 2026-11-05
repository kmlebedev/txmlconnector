package feed

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// fakeClient is a marketClient fake: the live tests never touch the network.
type fakeClient struct {
	mu           sync.Mutex
	runFn        func(ctx context.Context, h func([]model.Event) error) error
	histFn       func(ctx context.Context, req native.HistoryRequest) (model.Candles, error)
	catalog      *records.Catalog
	tz           string
	diff         time.Duration
	subscribes   int
	historyCalls int
	histCounts   []int
	closed       bool
}

func (f *fakeClient) Subscribe(ctx context.Context, sub native.Subscription) error {
	f.mu.Lock()
	f.subscribes++
	f.mu.Unlock()
	return nil
}

func (f *fakeClient) Run(ctx context.Context, opt native.RunOptions, h func([]model.Event) error) error {
	if f.runFn != nil {
		return f.runFn(ctx, h)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeClient) History(ctx context.Context, req native.HistoryRequest) (model.Candles, error) {
	f.mu.Lock()
	f.historyCalls++
	f.histCounts = append(f.histCounts, req.Count)
	f.mu.Unlock()
	if f.histFn != nil {
		return f.histFn(ctx, req)
	}
	return model.Candles{}, nil
}

func (f *fakeClient) ServerTimeDifference(ctx context.Context) (time.Duration, error) {
	return f.diff, nil
}

func (f *fakeClient) ServerTZ() string { return f.tz }

func (f *fakeClient) Catalog() *records.Catalog { return f.catalog }

func (f *fakeClient) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeClient) counts() (subscribes, historyCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subscribes, f.historyCalls
}

// requestedCounts returns the candle counts of the history calls.
func (f *fakeClient) requestedCounts() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.histCounts...)
}

func (f *fakeClient) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// newLive builds a live service on the synthetic seed and replaces the dialer
// with the given fake. It never starts the service.
func newLive(t *testing.T, client *fakeClient) *Service {
	t.Helper()
	dir := writeSeedGzip(t, testSeed())
	svc, err := New(Config{
		Mode:     ModeLive,
		Address:  "test:1",
		Login:    "test-login",
		Password: "test-password",
		SeedDir:  dir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.dial = func(ctx context.Context, cfg native.ClientConfig) (marketClient, error) {
		return client, nil
	}
	svc.backoffMin = 10 * time.Millisecond
	svc.backoffMax = 20 * time.Millisecond
	return svc
}

// waitFor polls cond until it holds or the deadline expires.
func waitFor(t *testing.T, what string, cond func() bool) {
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

func dec(v float64) *model.Decimal {
	d := model.Decimal{Units: int64(v*100 + 0.5), Scale: 2}
	if v < 0 {
		d.Units = int64(v*100 - 0.5)
	}
	return &d
}

func i64ptr(v int64) *int64 { return &v }

// TestLiveStartDialFailure checks that a failed initial dial is reported as an
// error and no run loop is started.
func TestLiveStartDialFailure(t *testing.T) {
	svc := newLive(t, &fakeClient{})
	svc.dial = func(ctx context.Context, cfg native.ClientConfig) (marketClient, error) {
		return nil, errors.New("dial failed")
	}
	err := svc.Start(context.Background())
	if err == nil {
		t.Fatal("want dial error")
	}
	if !contains(err.Error(), "подключиться") {
		t.Fatalf("error = %q", err)
	}
	if svc.Status().Connected {
		t.Fatal("status must stay disconnected")
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestLiveQuotationFlow checks that quotation events update the instrument and
// quote caches and publish an event.
func TestLiveQuotationFlow(t *testing.T) {
	client := &fakeClient{}
	client.runFn = func(ctx context.Context, h func([]model.Event) error) error {
		h([]model.Event{&model.Quotation{
			Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"},
			Fields: model.QuotationFields{
				Last: dec(102.5), Change: dec(2.5), Bid: dec(102.4), Offer: dec(102.6), VolToday: i64ptr(555),
			},
		}})
		<-ctx.Done()
		return ctx.Err()
	}
	svc := newLive(t, client)
	var mu sync.Mutex
	var events []Event
	svc.Subscribe(func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()

	waitFor(t, "updated quote", func() bool {
		q, ok := svc.Quote("TQBR:SBER")
		return ok && q.Last == 102.5
	})
	q, _ := svc.Quote("TQBR:SBER")
	if q.Bid != 102.4 || q.Ask != 102.6 || q.Volume != 555 || q.ChangePct != 2.5 {
		t.Fatalf("quote = %+v", q)
	}
	inst, _ := svc.Instrument("TQBR:SBER")
	if inst.Last != 102.5 || inst.Bid != 102.4 || inst.Ask != 102.6 {
		t.Fatalf("instrument = %+v", inst)
	}
	if !svc.Status().Connected {
		t.Fatal("status must be connected")
	}
	subs, _ := client.counts()
	if subs != 1 {
		t.Fatalf("subscribe calls = %d, want 1", subs)
	}
	mu.Lock()
	defer mu.Unlock()
	var quotes int
	for _, ev := range events {
		if ev.Type == "quote" {
			if qq, ok := ev.Data.(Quote); !ok || qq.Last != 102.5 {
				t.Fatalf("quote event data = %+v", ev.Data)
			}
			quotes++
		}
	}
	if quotes != 1 {
		t.Fatalf("quote events = %d, want 1", quotes)
	}
}

// TestLiveOrderBookAndTrades checks the order-book and tick-tape caches.
func TestLiveOrderBookAndTrades(t *testing.T) {
	client := &fakeClient{}
	quoteEvent := &model.Quotation{
		Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"},
		Fields: model.QuotationFields{Last: dec(101)},
	}
	client.runFn = func(ctx context.Context, h func([]model.Event) error) error {
		h([]model.Event{
			quoteEvent,
			&model.Quotes{
				Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"},
				Levels: []model.Quote{
					{Price: *dec(100.5), Buy: 10},
					{Price: *dec(101.5), Sell: 20},
					{Price: *dec(99), Buy: -1}, // deletion marker, skipped
				},
			},
			&model.Trade{
				Board: "TQBR", SecCode: "SBER", Time: time.Unix(testBase, 0).UTC(),
				Price: model.Decimal{Units: 10150, Scale: 2}, Quantity: 3, BuySell: 1,
			},
		})
		<-ctx.Done()
		return ctx.Err()
	}
	svc := newLive(t, client)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	waitFor(t, "order book", func() bool {
		book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
		return err == nil && len(book.Bids) > 0 && len(book.Asks) > 0
	})
	book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
	if err != nil {
		t.Fatalf("OrderBook: %v", err)
	}
	if book.Source != "live" || len(book.Bids) != 1 || book.Bids[0].Price != 100.5 || book.Bids[0].Qty != 10 {
		t.Fatalf("book bids = %+v", book)
	}
	if len(book.Asks) != 1 || book.Asks[0].Price != 101.5 || book.Asks[0].Qty != 20 {
		t.Fatalf("book asks = %+v", book.Asks)
	}
	trades, err := svc.Trades(context.Background(), "TQBR:SBER", 50)
	if err != nil {
		t.Fatalf("Trades: %v", err)
	}
	if len(trades) != 1 || trades[0].Price != 101.5 || trades[0].Qty != 3 || trades[0].Side != "buy" {
		t.Fatalf("trades = %+v", trades)
	}
}

// TestLiveCandlesCache checks the live history path: candle-kind resolution,
// decimal conversion and the TTL cache.
func TestLiveCandlesCache(t *testing.T) {
	client := &fakeClient{}
	client.histFn = func(ctx context.Context, req native.HistoryRequest) (model.Candles, error) {
		if req.Period != 7 || req.Count != 10 || req.Symbol.Board != "TQBR" || req.Symbol.SecCode != "SBER" {
			t.Errorf("history request = %+v", req)
		}
		return model.Candles{
			Symbol: req.Symbol, Period: req.Period, Status: 0,
			Items: []model.Candle{
				{Time: time.Unix(testBase-60, 0).UTC(), Open: model.Decimal{Units: 10000, Scale: 2}, High: model.Decimal{Units: 10100, Scale: 2}, Low: model.Decimal{Units: 9900, Scale: 2}, Close: model.Decimal{Units: 10050, Scale: 2}, Volume: 10},
				{Time: time.Unix(testBase, 0).UTC(), Open: model.Decimal{Units: 10050, Scale: 2}, High: model.Decimal{Units: 10200, Scale: 2}, Low: model.Decimal{Units: 10050, Scale: 2}, Close: model.Decimal{Units: 10150, Scale: 2}, Volume: 20},
			},
		}, nil
	}
	svc := newLive(t, client)
	svc.candleKinds = map[int]int{60: 7}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	ctx := context.Background()
	h, err := svc.Candles(ctx, "TQBR:SBER", 60, 10)
	if err != nil {
		t.Fatalf("Candles: %v", err)
	}
	if h.Source != "live" || len(h.Bars) != 2 || h.Bars[1].Close != 101.5 || h.Bars[0].Open != 100 {
		t.Fatalf("history = %+v", h)
	}
	// The second call is served from the TTL cache.
	if _, err := svc.Candles(ctx, "TQBR:SBER", 60, 10); err != nil {
		t.Fatalf("cached Candles: %v", err)
	}
	if _, calls := client.counts(); calls != 1 {
		t.Fatalf("history calls = %d, want 1 (cache)", calls)
	}
	// A period the server has no candle kind for is a bad_period error.
	_, err = svc.Candles(ctx, "TQBR:SBER", 300, 10)
	var ferr *Error
	if !errors.As(err, &ferr) || ferr.Code != CodeBadPeriod {
		t.Fatalf("unknown kind err = %v", err)
	}
	if _, err := svc.Candles(ctx, "TQBR:UNKNOWN", 60, 10); err == nil {
		t.Fatal("unknown symbol must fail")
	}
}

// TestLiveCandlesDictionaryNotLoaded is the R8 wire of the 503: before
// <candlekinds> arrives nothing is known about the period, so the request must
// be a temporary unavailable, not a bad_period (which claims the period does
// not exist).
func TestLiveCandlesDictionaryNotLoaded(t *testing.T) {
	client := &fakeClient{}
	svc := newLive(t, client) // no candleKinds: the dictionary has not arrived
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	_, err := svc.Candles(context.Background(), "TQBR:SBER", 60, 10)
	var ferr *Error
	if !errors.As(err, &ferr) || ferr.Code != CodeUnavailable {
		t.Fatalf("Candles without the period dictionary = %v, want unavailable", err)
	}
	if !strings.Contains(ferr.Message, "справочник периодов ещё не загружен") {
		t.Fatalf("message = %q, want the dictionary hint", ferr.Message)
	}
	if _, calls := client.counts(); calls != 0 {
		t.Fatalf("history calls = %d, want 0 (no request without a period id)", calls)
	}
}

// TestLiveCandlesForwardsUnknownPeriodWarning checks that the native warning
// of an undatable series reaches feed.History instead of being dropped.
func TestLiveCandlesForwardsUnknownPeriodWarning(t *testing.T) {
	client := &fakeClient{}
	client.histFn = func(ctx context.Context, req native.HistoryRequest) (model.Candles, error) {
		return model.Candles{
			Symbol:   req.Symbol,
			Period:   req.Period,
			Warnings: []string{"unknown candle period 2"},
			Items:    []model.Candle{{Open: model.Decimal{Units: 10000, Scale: 2}}},
		}, nil
	}
	svc := newLive(t, client)
	svc.candleKinds = map[int]int{60: 7}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	h, err := svc.Candles(context.Background(), "TQBR:SBER", 60, 10)
	if err != nil {
		t.Fatalf("Candles: %v", err)
	}
	if len(h.Warnings) != 1 || h.Warnings[0] != "unknown candle period 2" {
		t.Fatalf("warnings = %v, want [unknown candle period 2]", h.Warnings)
	}
	if len(h.Bars) != 1 || !h.Bars[0].Time.IsZero() {
		t.Fatalf("bars = %+v, want one undated bar", h.Bars)
	}
}

// TestLiveNoConnection checks live-only operations before Start.
func TestLiveNoConnection(t *testing.T) {
	svc := newLive(t, &fakeClient{})
	defer svc.Close()
	ctx := context.Background()
	_, err := svc.Candles(ctx, "TQBR:SBER", 60, 10)
	var ferr *Error
	if !errors.As(err, &ferr) || ferr.Code != CodeUnavailable {
		t.Fatalf("Candles without connection = %v", err)
	}
	if _, err := svc.OrderBook(ctx, "TQBR:SBER"); !errors.As(err, &ferr) || ferr.Code != CodeNotFound {
		t.Fatalf("OrderBook without book = %v", err)
	}
	trades, err := svc.Trades(ctx, "TQBR:SBER", 10)
	if err != nil || len(trades) != 0 {
		t.Fatalf("Trades without tape = %v, %v", trades, err)
	}
	// Deadlines are also respected for live history.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.Candles(cctx, "TQBR:SBER", 60, 10); !errors.As(err, &ferr) || ferr.Code != CodeUnavailable {
		t.Fatalf("cancelled Candles = %v", err)
	}
}

// TestApplyCatalog checks the catalog refresh: names, currency, decimals, candle
// kinds and the exchange time zone.
func TestApplyCatalog(t *testing.T) {
	startup := &model.Startup{
		Boards:      []model.Board{{Num: 1, Code: "TQBR", Name: "Т+1"}},
		CandleKinds: []model.CandleKind{{ID: 5, Name: "1m", Period: 60}, {ID: 9, Name: "5m", Period: 300}},
	}
	catalog := records.NewCatalog(startup)
	catalog.AddSecurity(records.SecNew{
		SecID: 7, SecCode: "SBER", ShortName: "Сбер (live)", Currency: "RUR",
		Pits: []records.Pit{{Board: 1, Decimals: 3}},
	})
	client := &fakeClient{catalog: catalog, tz: "Europe/Moscow"}
	svc := newLive(t, client)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	inst, ok := svc.Instrument("TQBR:SBER")
	if !ok {
		t.Fatal("instrument missing")
	}
	if inst.ShortName != "Сбер (live)" || inst.Currency != "RUB" || inst.Decimals != 3 {
		t.Fatalf("instrument after catalog = %+v", inst)
	}
	svc.mu.RLock()
	kinds := svc.candleKinds
	tz := svc.status.ExchangeTZ
	svc.mu.RUnlock()
	if kinds[60] != 5 || kinds[300] != 9 {
		t.Fatalf("candle kinds = %v", kinds)
	}
	if tz != "Europe/Moscow" {
		t.Fatalf("exchange tz = %q", tz)
	}
}

// TestLiveReconnect checks the reconnect loop: after a session failure the feed
// dials again and keeps serving data.
func TestLiveReconnect(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	client := &fakeClient{}
	client.runFn = func(ctx context.Context, h func([]model.Event) error) error {
		mu.Lock()
		runs++
		n := runs
		mu.Unlock()
		h([]model.Event{&model.Quotation{
			Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"},
			Fields: model.QuotationFields{Last: dec(100 + float64(n))},
		}})
		if n == 1 {
			return errors.New("session lost")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	svc := newLive(t, client)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	waitFor(t, "reconnect", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runs >= 2
	})
	waitFor(t, "quote after reconnect", func() bool {
		q, ok := svc.Quote("TQBR:SBER")
		return ok && q.Last == 102
	})
}

// TestApplyServerStatus checks the session-state messages.
func TestApplyServerStatus(t *testing.T) {
	svc := &Service{
		mode:        ModeLive,
		instruments: map[string]*Instrument{},
		quotes:      map[string]Quote{},
		books:       map[string]OrderBook{},
		trades:      map[string][]Trade{},
		subs:        map[uint64]func(Event){},
	}
	var last Status
	svc.Subscribe(func(ev Event) {
		if ev.Type == "status" {
			last, _ = ev.Data.(Status)
		}
	})
	svc.applyServerStatus(model.ServerStatus{Connected: true})
	if !last.Connected || last.Message == "" {
		t.Fatalf("connected status = %+v", last)
	}
	svc.applyServerStatus(model.ServerStatus{Connected: true, Recover: true})
	if !contains(last.Message, "восстановление") {
		t.Fatalf("recover message = %q", last.Message)
	}
	svc.applyServerStatus(model.ServerStatus{Err: "timeout"})
	if last.Connected || !contains(last.Message, "ошибка сессии") {
		t.Fatalf("error status = %+v", last)
	}
	svc.applyServerStatus(model.ServerStatus{})
	if last.Connected || !contains(last.Message, "потеряно") {
		t.Fatalf("lost status = %+v", last)
	}
}

// TestHelpersLive checks pure conversion helpers.
func TestHelpersLive(t *testing.T) {
	if board, sec, ok := cutSymbol("TQBR:SBER"); !ok || board != "TQBR" || sec != "SBER" {
		t.Fatalf("cutSymbol = %q %q %v", board, sec, ok)
	}
	if _, _, ok := cutSymbol("SBER"); ok {
		t.Fatal("cutSymbol without colon")
	}
	inst := Instrument{Last: 10, Change: 1}
	q := quoteOf(inst, time.Unix(testBase, 0).UTC())
	if q.ChangePct != 0 { // changePct is computed on the instrument only
		t.Fatalf("quoteOf pct = %v", q.ChangePct)
	}
	book := orderBookFromQuotes(&model.Quotes{Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}}, 1, time.Now())
	if book.Symbol != "TQBR:SBER" || len(book.Bids) != 0 {
		t.Fatalf("empty book = %+v", book)
	}
	hist := trimHistory(History{Bars: []Bar{{Close: 1}, {Close: 2}, {Close: 3}}}, 2)
	if len(hist.Bars) != 2 || hist.Bars[0].Close != 2 {
		t.Fatalf("trimHistory = %+v", hist)
	}
}

// TestLiveCandlesCacheCount is the P1 regression: a live candle cache filled by
// a small request must re-fetch when a larger count is asked for (the HTTP
// backtest asked for 700 bars and silently got the 50 cached ones), while a
// request below the fetched count is served from the cache.
func TestLiveCandlesCacheCount(t *testing.T) {
	client := &fakeClient{}
	client.histFn = func(ctx context.Context, req native.HistoryRequest) (model.Candles, error) {
		items := make([]model.Candle, req.Count)
		for i := range items {
			value := dec(float64(i))
			items[i] = model.Candle{
				Time: time.Unix(testBase+int64(i)*60, 0).UTC(),
				Open: *value, High: *value, Low: *value, Close: *value,
				Volume: int64(i),
			}
		}
		return model.Candles{Symbol: req.Symbol, Period: req.Period, Items: items}, nil
	}
	svc := newLive(t, client)
	svc.candleKinds = map[int]int{300: 9}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	ctx := context.Background()

	h50, err := svc.Candles(ctx, "TQBR:SBER", 300, 50)
	if err != nil {
		t.Fatalf("Candles(50): %v", err)
	}
	if len(h50.Bars) != 50 {
		t.Fatalf("Candles(50) bars = %d", len(h50.Bars))
	}
	h700, err := svc.Candles(ctx, "TQBR:SBER", 300, 700)
	if err != nil {
		t.Fatalf("Candles(700): %v", err)
	}
	if len(h700.Bars) != 700 {
		t.Fatalf("Candles(700) bars = %d, want 700 (cache must re-fetch on a larger count)", len(h700.Bars))
	}
	// A smaller request is served from the cached 700-bar series: its last 100
	// bars (the series ends at close 699).
	h100, err := svc.Candles(ctx, "TQBR:SBER", 300, 100)
	if err != nil {
		t.Fatalf("Candles(100): %v", err)
	}
	if len(h100.Bars) != 100 {
		t.Fatalf("Candles(100) bars = %d", len(h100.Bars))
	}
	if h100.Bars[0].Close != 600 || h100.Bars[99].Close != 699 {
		t.Fatalf("Candles(100) window = %v..%v, want 600..699", h100.Bars[0].Close, h100.Bars[99].Close)
	}
	if got, want := client.requestedCounts(), []int{50, 700}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("client history counts = %v, want %v (the 100 request must be cached)", got, want)
	}
}

// TestIANATimeZoneMapping checks the Windows -> IANA exchange zone mapping.
func TestIANATimeZoneMapping(t *testing.T) {
	cases := map[string]string{
		"Russian Standard Time":       "Europe/Moscow",
		"Moscow Standard Time":        "Europe/Moscow",
		"UTC":                         "UTC",
		"Coordinated Universal Time":  "UTC",
		"Europe/Moscow":               "Europe/Moscow",
		"europe/moscow":               "Europe/Moscow", // canonicalised: zoneinfo lookups are case-sensitive, users are not
		"":                            "",
		"Unknown Time Zone":           "",
		"Vladivostok Standard Time":   "Asia/Vladivostok",
		"Yekaterinburg Standard Time": "Asia/Yekaterinburg",
	}
	for name, want := range cases {
		if got := ianaTimeZone(name); got != want {
			t.Fatalf("ianaTimeZone(%q) = %q, want %q", name, got, want)
		}
	}
	// exchangeZoneName keeps an unmapped name instead of dropping it.
	if got := exchangeZoneName("Weird Zone"); got != "Weird Zone" {
		t.Fatalf("exchangeZoneName(unknown) = %q", got)
	}
}

// TestServerTimeFromWall checks the server-wall -> UTC-instant conversion.
func TestServerTimeFromWall(t *testing.T) {
	// The demo reports MSK wall clock 19:38:55; the real UTC instant is 16:38:55.
	wall := time.Date(2026, 11, 5, 19, 38, 55, 0, time.UTC)
	instant, ok := serverTimeFromWall(wall, "Europe/Moscow")
	if !ok {
		t.Fatal("serverTimeFromWall(Europe/Moscow) failed")
	}
	want := time.Date(2026, 11, 5, 16, 38, 55, 0, time.UTC)
	if !instant.Equal(want) {
		t.Fatalf("instant = %v, want %v", instant, want)
	}
	if _, ok := serverTimeFromWall(wall, ""); ok {
		t.Fatal("empty zone must not convert")
	}
	if _, ok := serverTimeFromWall(wall, "Not/AZone"); ok {
		t.Fatal("unknown zone must not convert")
	}
	// UTC stays the same wall clock.
	if instant, ok := serverTimeFromWall(wall, "UTC"); !ok || !instant.Equal(wall) {
		t.Fatalf("UTC instant = %v (ok=%v)", instant, ok)
	}
}

// TestLiveServerTimeAndExchangeTZ is the P2 regression: the demo reports the
// Windows zone "Russian Standard Time"; the feed must expose IANA
// "Europe/Moscow" and a real UTC instant, not the MSK wall clock labelled UTC.
func TestLiveServerTimeAndExchangeTZ(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation(Europe/Moscow): %v", err)
	}
	now := time.Now()
	localWall := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.UTC)
	mskNow := now.In(msk)
	mskWall := time.Date(mskNow.Year(), mskNow.Month(), mskNow.Day(), mskNow.Hour(), mskNow.Minute(), mskNow.Second(), 0, time.UTC)
	client := &fakeClient{tz: "Russian Standard Time", diff: mskWall.Sub(localWall)}
	svc := newLive(t, client)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	waitFor(t, "live status", func() bool {
		st := svc.Status()
		return st.Connected && !st.ServerTime.IsZero()
	})
	st := svc.Status()
	if st.ExchangeTZ != "Europe/Moscow" {
		t.Fatalf("ExchangeTZ = %q, want Europe/Moscow", st.ExchangeTZ)
	}
	if delta := st.ServerTime.Sub(time.Now().UTC()); delta > 5*time.Second || delta < -5*time.Second {
		t.Fatalf("ServerTime = %v is shifted from UTC now (delta %v)", st.ServerTime, delta)
	}
}

// TestLiveHistorySingleFlight is the P2 regression: concurrent requests for the
// same symbol+period share one client.History call (native rejects a second
// concurrent request with "already in flight"), and both callers get the series.
func TestLiveHistorySingleFlight(t *testing.T) {
	client := &fakeClient{}
	release := make(chan struct{})
	client.histFn = func(ctx context.Context, req native.HistoryRequest) (model.Candles, error) {
		<-release
		items := make([]model.Candle, req.Count)
		for i := range items {
			value := dec(float64(i))
			items[i] = model.Candle{Time: time.Unix(testBase+int64(i)*60, 0).UTC(), Open: *value, High: *value, Low: *value, Close: *value}
		}
		return model.Candles{Symbol: req.Symbol, Period: req.Period, Items: items}, nil
	}
	svc := newLive(t, client)
	svc.candleKinds = map[int]int{300: 9}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()

	// The leader blocks inside client.History; a second caller must join the
	// same flight instead of issuing a second exchange request.
	leaderDone := make(chan History, 1)
	leaderErr := make(chan error, 1)
	go func() {
		h, err := svc.Candles(context.Background(), "TQBR:SBER", 300, 100)
		leaderDone <- h
		leaderErr <- err
	}()
	waitFor(t, "leader history call", func() bool {
		_, calls := client.counts()
		return calls >= 1
	})
	time.AfterFunc(20*time.Millisecond, func() { close(release) })
	follower, err := svc.Candles(context.Background(), "TQBR:SBER", 300, 100)
	if err != nil {
		t.Fatalf("follower Candles: %v", err)
	}
	leader := <-leaderDone
	if err := <-leaderErr; err != nil {
		t.Fatalf("leader Candles: %v", err)
	}
	if len(leader.Bars) != 100 || len(follower.Bars) != 100 {
		t.Fatalf("leader/follower bars = %d/%d", len(leader.Bars), len(follower.Bars))
	}
	if _, calls := client.counts(); calls != 1 {
		t.Fatalf("client History calls = %d, want 1 (single-flight)", calls)
	}
}

// TestBackoffSchedule checks the reconnect escalation: the wait doubles up to
// the cap, a flapping session keeps it, and only a stable session resets it.
func TestBackoffSchedule(t *testing.T) {
	schedule := newBackoffSchedule(2*time.Second, 30*time.Second)
	for i, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		if got := schedule.next(); got != want {
			t.Fatalf("next #%d = %v, want %v", i, got, want)
		}
	}
	// A stable session resets the escalation back to the minimum.
	schedule.observeSession(31*time.Second, 30*time.Second)
	if got := schedule.next(); got != 2*time.Second {
		t.Fatalf("after stable session next = %v, want 2s", got)
	}
	// A flapping session keeps the escalation (2s -> 4s).
	schedule.observeSession(2*time.Second, 30*time.Second)
	if got := schedule.next(); got != 4*time.Second {
		t.Fatalf("after flapping session next = %v, want 4s", got)
	}
	// Defensive defaults for a zeroed schedule.
	zero := newBackoffSchedule(0, 0)
	if got := zero.next(); got != 2*time.Second {
		t.Fatalf("zero schedule next = %v, want 2s", got)
	}
}
