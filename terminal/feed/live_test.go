package feed

import (
	"context"
	"errors"
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
