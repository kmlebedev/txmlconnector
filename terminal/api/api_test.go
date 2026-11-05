package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// fakeFeed is a FeedAPI fake backed by in-memory maps.
type fakeFeed struct {
	mu          sync.Mutex
	status      feed.Status
	instruments []feed.Instrument
	quotes      map[string]feed.Quote
	books       map[string]feed.OrderBook
	tapes       map[string][]feed.Trade
	bars        []feed.Bar
	subs        []func(feed.Event)
}

func newFakeFeed() *fakeFeed {
	return &fakeFeed{
		status: feed.Status{
			Mode: feed.ModeOffline, Connected: true, Message: "офлайн-снапшот",
			ExchangeTZ:      "Europe/Moscow",
			SeedGeneratedAt: time.Date(2026, 11, 5, 16, 21, 36, 0, time.UTC),
			ServerTime:      time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC),
		},
		instruments: []feed.Instrument{
			{
				Symbol: "TQBR:SBER", Board: "TQBR", SecCode: "SBER", Name: "Сбербанк", ShortName: "Сбербанк",
				Currency: "RUB", Decimals: 2, LotSize: 10, MinStep: 0.01,
				PrevClose: 100, Last: 101, Change: 1, ChangePct: 1,
				Open: 100.5, High: 102, Low: 99, Volume: 12345, Bid: 100.9, Ask: 101.1,
				Spark:     []float64{100, 101},
				UpdatedAt: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC),
			},
			{
				Symbol: "TQBR:GAZP", Board: "TQBR", SecCode: "GAZP", Name: "Газпром", ShortName: "Газпром",
				Currency: "RUB", Decimals: 2, LotSize: 10, MinStep: 0.01,
				PrevClose: 150, Last: 151, Change: 1, ChangePct: 0.67,
				Bid: 150.9, Ask: 151.1, Volume: 42,
				UpdatedAt: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC),
			},
		},
		quotes: map[string]feed.Quote{
			"TQBR:SBER": {Symbol: "TQBR:SBER", Last: 101, Change: 1, ChangePct: 1, Bid: 100.9, Ask: 101.1, Volume: 12345, Time: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC)},
			"TQBR:GAZP": {Symbol: "TQBR:GAZP", Last: 151, Change: 1, ChangePct: 0.67, Bid: 150.9, Ask: 151.1, Volume: 42, Time: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC)},
		},
		books: map[string]feed.OrderBook{
			"TQBR:SBER": {
				Symbol: "TQBR:SBER", Last: 101, Time: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC), Source: "seed",
				Bids: []feed.Level{{Price: 100.9, Qty: 10}, {Price: 100.8, Qty: 5}},
				Asks: []feed.Level{{Price: 101.1, Qty: 20}},
			},
		},
		tapes: map[string][]feed.Trade{
			"TQBR:SBER": {
				{Time: time.Date(2026, 11, 5, 16, 23, 0, 0, time.UTC), Price: 101, Qty: 1000, Side: "buy"},
				{Time: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC), Price: 100.9, Qty: 500, Side: "sell"},
			},
		},
		bars: []feed.Bar{
			{Time: time.Date(2026, 11, 5, 16, 22, 0, 0, time.UTC), Open: 100, High: 101, Low: 99, Close: 100.5, Volume: 10},
			{Time: time.Date(2026, 11, 5, 16, 23, 0, 0, time.UTC), Open: 100.5, High: 102, Low: 100, Close: 101, Volume: 20},
			{Time: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC), Open: 101, High: 101.5, Low: 100.8, Close: 101, Volume: 30},
		},
	}
}

func (f *fakeFeed) Status() feed.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeFeed) Instruments() []feed.Instrument {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]feed.Instrument(nil), f.instruments...)
}

func (f *fakeFeed) Instrument(symbol string) (feed.Instrument, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, inst := range f.instruments {
		if inst.Symbol == symbol {
			return inst, true
		}
	}
	return feed.Instrument{}, false
}

func (f *fakeFeed) Quote(symbol string) (feed.Quote, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.quotes[symbol]
	return q, ok
}

// setQuote replaces a quote (copy-on-write under the fake's lock).
func (f *fakeFeed) setQuote(symbol string, quote feed.Quote) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quotes[symbol] = quote
}

func (f *fakeFeed) Candles(ctx context.Context, symbol string, periodSec, count int) (feed.History, error) {
	if !feed.ValidPeriod(periodSec) {
		return feed.History{}, &feed.Error{Code: feed.CodeBadPeriod, Message: "неподдерживаемый период свечей"}
	}
	f.mu.Lock()
	if _, ok := f.instrumentLocked(symbol); !ok {
		f.mu.Unlock()
		return feed.History{}, &feed.Error{Code: feed.CodeNotFound, Message: "инструмент не найден: " + symbol}
	}
	bars := f.bars
	f.mu.Unlock()
	if len(bars) > count {
		bars = bars[len(bars)-count:]
	}
	return feed.History{Symbol: symbol, PeriodSec: periodSec, Bars: append([]feed.Bar(nil), bars...), Source: "seed", Note: "демо-снапшот 05.11.2026"}, nil
}

func (f *fakeFeed) OrderBook(ctx context.Context, symbol string) (feed.OrderBook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	book, ok := f.books[symbol]
	if !ok {
		return feed.OrderBook{}, &feed.Error{Code: feed.CodeNotFound, Message: "стакан недоступен: " + symbol}
	}
	return book, nil
}

func (f *fakeFeed) Trades(ctx context.Context, symbol string, limit int) ([]feed.Trade, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	trades, ok := f.tapes[symbol]
	if !ok {
		if _, known := f.instrumentLocked(symbol); !known {
			return nil, &feed.Error{Code: feed.CodeNotFound, Message: "инструмент не найден: " + symbol}
		}
		return nil, nil
	}
	if len(trades) > limit {
		trades = trades[len(trades)-limit:]
	}
	return append([]feed.Trade(nil), trades...), nil
}

func (f *fakeFeed) Subscribe(fn func(feed.Event)) (cancel func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fn)
	return func() {}
}

// instrumentLocked finds an instrument; the caller holds f.mu.
func (f *fakeFeed) instrumentLocked(symbol string) (feed.Instrument, bool) {
	for _, inst := range f.instruments {
		if inst.Symbol == symbol {
			return inst, true
		}
	}
	return feed.Instrument{}, false
}

// subscribers copies the registered feed sinks.
func (f *fakeFeed) subscribers() []func(feed.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]func(feed.Event), len(f.subs))
	copy(out, f.subs)
	return out
}

// fakeAgent is an AgentAPI fake.
type fakeAgent struct {
	status     agent.Status
	chat       agent.ChatResponse
	chatErr    error
	hints      agent.MarketHints
	hintsErr   error
	tools      []agent.ToolInfo
	backtests  []backtest.Result
	startErr   error
	stopErr    error
	started    bool
	stopped    bool
	startCfg   agent.AutonomyConfig
	subs       []func(agent.Event)
	autonomyID string
}

func (f *fakeAgent) Chat(ctx context.Context, req agent.ChatRequest) (agent.ChatResponse, error) {
	if f.chatErr != nil {
		return agent.ChatResponse{}, f.chatErr
	}
	out := f.chat
	if out.Answer == "" {
		out.Answer = "ответ на: " + req.Message
	}
	return out, nil
}

func (f *fakeAgent) Status() agent.Status { return f.status }

func (f *fakeAgent) StartAutonomy(ctx context.Context, cfg agent.AutonomyConfig) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = true
	f.startCfg = cfg
	f.status.Autonomy.Running = true
	f.status.Autonomy.Config = cfg
	return nil
}

func (f *fakeAgent) StopAutonomy() error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = true
	f.status.Autonomy.Running = false
	return nil
}

func (f *fakeAgent) Subscribe(fn func(agent.Event)) (cancel func()) {
	f.subs = append(f.subs, fn)
	return func() {}
}

func (f *fakeAgent) Backtests() []backtest.Result { return f.backtests }

func (f *fakeAgent) Hints(ctx context.Context, symbol string, periodSec int) (agent.MarketHints, error) {
	if f.hintsErr != nil {
		return agent.MarketHints{}, f.hintsErr
	}
	out := f.hints
	out.Symbol = symbol
	out.PeriodSec = periodSec
	return out, nil
}

func (f *fakeAgent) Tools() []agent.ToolInfo { return f.tools }

// newTestServer builds a Server with fake deps, short SSE intervals and an
// httptest server around Handler.
func newTestServer(t *testing.T, deps Deps, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	srv, err := New(cfg, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.quoteInterval = 20 * time.Millisecond
	srv.statusInterval = 20 * time.Millisecond
	srv.heartbeatInterval = 30 * time.Millisecond
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	return srv, ts
}

// defaultDeps returns a feed fake plus a real paper broker and a fake agent.
func defaultDeps() (Deps, *fakeFeed, *paper.Broker, *fakeAgent) {
	f := newFakeFeed()
	broker := paper.New(paper.Config{InitialCash: 1_000_000, FeeBps: 5})
	a := &fakeAgent{
		status: agent.Status{
			LLM: agent.LLMStatus{Enabled: false, Model: "deepseek-chat", Note: "локальный аналитик"},
		},
		tools:     []agent.ToolInfo{{Name: "list_instruments", Title: "Инструменты", Description: "список", Input: json.RawMessage(`{"type":"object"}`)}},
		backtests: []backtest.Result{{Symbol: "TQBR:SBER", Strategy: "sma_cross"}},
		hints:     agent.MarketHints{SMA20: 100.5, Trend: "up"},
	}
	return Deps{Feed: f, Paper: broker, Agent: a}, f, broker, a
}

// do performs one HTTP request against ts and returns status plus body.
func do(t *testing.T, ts *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, data
}

// decode unmarshals the response body into dst.
func decode(t *testing.T, data []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("decode %s: %v", string(data), err)
	}
}

// errorCode extracts the error code of an error envelope.
func errorCode(t *testing.T, data []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decode(t, data, &env)
	if env.Error.Message == "" {
		t.Fatalf("error message is empty: %s", data)
	}
	return env.Error.Code
}

// TestHealth checks GET /api/health.
func TestHealth(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{Version: "test-1"})
	status, data := do(t, ts, http.MethodGet, "/api/health", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, data)
	}
	var health struct {
		OK         bool   `json:"ok"`
		Version    string `json:"version"`
		Mode       string `json:"mode"`
		Connected  bool   `json:"connected"`
		ExchangeTZ string `json:"exchangeTZ"`
		LLM        struct {
			Enabled bool   `json:"enabled"`
			Model   string `json:"model"`
		} `json:"llm"`
		Trading struct {
			Paper bool `json:"paper"`
			Live  bool `json:"live"`
		} `json:"trading"`
		ServerTime string `json:"serverTime"`
	}
	decode(t, data, &health)
	if !health.OK || health.Version != "test-1" || health.Mode != "offline" || !health.Connected {
		t.Fatalf("health = %+v", health)
	}
	if health.ExchangeTZ != "Europe/Moscow" || health.LLM.Model != "deepseek-chat" {
		t.Fatalf("health metadata = %+v", health)
	}
	if !health.Trading.Paper || health.Trading.Live {
		t.Fatalf("trading = %+v", health.Trading)
	}
	if !strings.HasSuffix(health.ServerTime, "Z") {
		t.Fatalf("serverTime is not UTC: %q", health.ServerTime)
	}
}

// TestInstruments checks GET /api/instruments.
func TestInstruments(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	status, data := do(t, ts, http.MethodGet, "/api/instruments", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, data)
	}
	var resp struct {
		ServerTime  string `json:"serverTime"`
		Instruments []struct {
			Symbol    string    `json:"symbol"`
			Name      string    `json:"name"`
			Decimals  int       `json:"decimals"`
			Last      float64   `json:"last"`
			ChangePct float64   `json:"changePct"`
			Spark     []float64 `json:"spark"`
			Bid       float64   `json:"bid"`
		} `json:"instruments"`
	}
	decode(t, data, &resp)
	if resp.ServerTime == "" || len(resp.Instruments) != 2 {
		t.Fatalf("instruments response = %+v", resp)
	}
	inst := resp.Instruments[0]
	if inst.Symbol != "TQBR:SBER" || inst.Name != "Сбербанк" || inst.Decimals != 2 || inst.Last != 101 || len(inst.Spark) != 2 {
		t.Fatalf("instrument = %+v", inst)
	}
}

// TestCandles checks the candles endpoint, both raw and percent-encoded symbols,
// plus the error mapping.
func TestCandles(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	for _, path := range []string{
		"/api/instruments/TQBR:SBER/candles?period=60&count=2",
		"/api/instruments/TQBR%3ASBER/candles?period=60&count=2",
	} {
		status, data := do(t, ts, http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("%s: status = %d, body %s", path, status, data)
		}
		var resp struct {
			Symbol    string `json:"symbol"`
			PeriodSec int    `json:"periodSec"`
			Source    string `json:"source"`
			Note      string `json:"note"`
			Candles   []struct {
				T int64   `json:"t"`
				O float64 `json:"o"`
				C float64 `json:"c"`
				V int64   `json:"v"`
			} `json:"candles"`
		}
		decode(t, data, &resp)
		if resp.Symbol != "TQBR:SBER" || resp.PeriodSec != 60 || resp.Source != "seed" || len(resp.Candles) != 2 {
			t.Fatalf("%s: resp = %+v", path, resp)
		}
		if resp.Candles[0].T == 0 || resp.Candles[1].C != 101 || resp.Candles[1].V != 30 {
			t.Fatalf("%s: candles = %+v", path, resp.Candles)
		}
	}

	status, data := do(t, ts, http.MethodGet, "/api/instruments/TQBR:SBER/candles?period=77", nil)
	if status != http.StatusBadRequest || errorCode(t, data) != codeBadRequest {
		t.Fatalf("bad period: %d %s", status, data)
	}
	status, data = do(t, ts, http.MethodGet, "/api/instruments/TQBR:UNKNOWN/candles?period=60", nil)
	if status != http.StatusNotFound || errorCode(t, data) != codeNotFound {
		t.Fatalf("unknown symbol: %d %s", status, data)
	}
	status, data = do(t, ts, http.MethodGet, "/api/instruments/TQBR:SBER/candles?period=x", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("malformed period: %d %s", status, data)
	}
}

// TestOrderBookAndTrades checks the two market-data detail endpoints.
func TestOrderBookAndTrades(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})

	status, data := do(t, ts, http.MethodGet, "/api/instruments/TQBR:SBER/orderbook", nil)
	if status != http.StatusOK {
		t.Fatalf("orderbook status = %d, body %s", status, data)
	}
	var book struct {
		Symbol string `json:"symbol"`
		Last   float64
		Bids   []struct{ Price, Qty float64 }
		Asks   []struct{ Price, Qty float64 }
		Source string
	}
	decode(t, data, &book)
	if book.Symbol != "TQBR:SBER" || book.Last != 101 || len(book.Bids) != 2 || len(book.Asks) != 1 || book.Source != "seed" {
		t.Fatalf("book = %+v", book)
	}

	status, data = do(t, ts, http.MethodGet, "/api/instruments/TQBR:SBER/trades?limit=1", nil)
	if status != http.StatusOK {
		t.Fatalf("trades status = %d, body %s", status, data)
	}
	var tape struct {
		Symbol string `json:"symbol"`
		Trades []struct {
			Price float64 `json:"price"`
			Qty   int64   `json:"qty"`
			Side  string  `json:"side"`
			Time  string  `json:"time"`
		} `json:"trades"`
	}
	decode(t, data, &tape)
	if tape.Symbol != "TQBR:SBER" || len(tape.Trades) != 1 || tape.Trades[0].Side != "sell" || tape.Trades[0].Time == "" {
		t.Fatalf("tape = %+v", tape)
	}
}

// TestPaperOrders checks placing, listing, executing and cancelling paper
// orders through the API with the real paper broker.
func TestPaperOrders(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})

	// A market buy fills at the ask.
	status, data := do(t, ts, http.MethodPost, "/api/orders", map[string]any{
		"symbol": "TQBR:SBER", "side": "buy", "qty": 10, "type": "market",
	})
	if status != http.StatusCreated {
		t.Fatalf("place status = %d, body %s", status, data)
	}
	var placed struct {
		Order struct {
			ID           string  `json:"id"`
			Symbol       string  `json:"symbol"`
			Status       string  `json:"status"`
			Qty          int64   `json:"qty"`
			Filled       int64   `json:"filled"`
			AvgFillPrice float64 `json:"avgFillPrice"`
			Source       string  `json:"source"`
			CreatedAt    string  `json:"createdAt"`
		} `json:"order"`
	}
	decode(t, data, &placed)
	if placed.Order.ID == "" || placed.Order.Status != "filled" || placed.Order.Filled != 10 || placed.Order.AvgFillPrice != 101.1 {
		t.Fatalf("placed = %+v", placed.Order)
	}
	if placed.Order.Source != "ui" || !strings.HasSuffix(placed.Order.CreatedAt, "Z") {
		t.Fatalf("placed metadata = %+v", placed.Order)
	}

	// GET /api/orders sees it.
	status, data = do(t, ts, http.MethodGet, "/api/orders", nil)
	if status != http.StatusOK {
		t.Fatalf("orders status = %d", status)
	}
	var orders struct {
		Orders []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"orders"`
	}
	decode(t, data, &orders)
	if len(orders.Orders) != 1 || orders.Orders[0].ID != placed.Order.ID {
		t.Fatalf("orders = %+v", orders.Orders)
	}

	// GET /api/trades sees the fill.
	status, data = do(t, ts, http.MethodGet, "/api/trades", nil)
	if status != http.StatusOK {
		t.Fatalf("trade history status = %d", status)
	}
	var trades struct {
		Trades []struct {
			Symbol string  `json:"symbol"`
			Fee    float64 `json:"fee"`
		} `json:"trades"`
	}
	decode(t, data, &trades)
	if len(trades.Trades) != 1 || trades.Trades[0].Symbol != "TQBR:SBER" || trades.Trades[0].Fee <= 0 {
		t.Fatalf("trades = %+v", trades.Trades)
	}

	// A resting limit order can be cancelled; a repeated cancel is a conflict.
	status, data = do(t, ts, http.MethodPost, "/api/orders", map[string]any{
		"symbol": "TQBR:SBER", "side": "buy", "qty": 1, "type": "limit", "price": 50,
	})
	if status != http.StatusCreated {
		t.Fatalf("limit place status = %d, body %s", status, data)
	}
	var limitOrder struct {
		Order struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"order"`
	}
	decode(t, data, &limitOrder)
	if limitOrder.Order.Status != "new" || limitOrder.Order.ID == "" {
		t.Fatalf("limit order = %+v", limitOrder.Order)
	}
	status, data = do(t, ts, http.MethodDelete, "/api/orders/"+limitOrder.Order.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("cancel status = %d, body %s", status, data)
	}
	var cancelled struct {
		OK bool `json:"ok"`
	}
	decode(t, data, &cancelled)
	if !cancelled.OK {
		t.Fatalf("cancel resp = %s", data)
	}
	status, data = do(t, ts, http.MethodDelete, "/api/orders/"+limitOrder.Order.ID, nil)
	if status != http.StatusConflict {
		t.Fatalf("second cancel status = %d, body %s", status, data)
	}
	status, data = do(t, ts, http.MethodDelete, "/api/orders/ord_999999", nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing cancel status = %d, body %s", status, data)
	}
}

// TestOrderRejected checks the paper rejection mapping.
func TestOrderRejected(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	status, data := do(t, ts, http.MethodPost, "/api/orders", map[string]any{
		"symbol": "TQBR:SBER", "side": "sell", "qty": 5, "type": "market",
	})
	if status != http.StatusBadRequest || errorCode(t, data) != codeBadRequest {
		t.Fatalf("reject = %d %s", status, data)
	}
	// The rejected order is kept in the history.
	status, data = do(t, ts, http.MethodGet, "/api/orders", nil)
	if status != http.StatusOK {
		t.Fatalf("orders status = %d", status)
	}
	var orders struct {
		Orders []struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"orders"`
	}
	decode(t, data, &orders)
	if len(orders.Orders) != 1 || orders.Orders[0].Status != "rejected" || orders.Orders[0].Message == "" {
		t.Fatalf("rejected order = %+v", orders.Orders)
	}
	// Malformed bodies are bad_request.
	status, _ = do(t, ts, http.MethodPost, "/api/orders", "not json")
	if status != http.StatusBadRequest {
		t.Fatalf("malformed body status = %d", status)
	}
}

// TestLiveTradingGate checks the 403/501 live-order contract.
func TestLiveTradingGate(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	body := map[string]any{"symbol": "TQBR:SBER", "side": "buy", "qty": 1, "type": "market", "confirm": "LIVE"}
	status, data := do(t, ts, http.MethodPost, "/api/orders", body)
	if status != http.StatusForbidden || errorCode(t, data) != codeLiveTradingDisabled {
		t.Fatalf("live disabled = %d %s", status, data)
	}

	deps2, _, _, _ := defaultDeps()
	_, ts2 := newTestServer(t, deps2, Config{LiveTradingAllowed: true})
	status, data = do(t, ts2, http.MethodPost, "/api/orders", body)
	if status != http.StatusNotImplemented || errorCode(t, data) != codeNotImplemented {
		t.Fatalf("live allowed = %d %s", status, data)
	}
	// Without the LIVE confirmation the same server still takes paper orders.
	status, _ = do(t, ts2, http.MethodPost, "/api/orders", map[string]any{
		"symbol": "TQBR:SBER", "side": "buy", "qty": 1, "type": "market",
	})
	if status != http.StatusCreated {
		t.Fatalf("paper order in live-allowed mode = %d", status)
	}
}

// TestPortfolio checks the marked-to-market portfolio response.
func TestPortfolio(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	do(t, ts, http.MethodPost, "/api/orders", map[string]any{
		"symbol": "TQBR:SBER", "side": "buy", "qty": 10, "type": "market",
	})
	status, data := do(t, ts, http.MethodGet, "/api/portfolio", nil)
	if status != http.StatusOK {
		t.Fatalf("portfolio status = %d, body %s", status, data)
	}
	var portfolio struct {
		TotalValue float64 `json:"totalValue"`
		Cash       float64 `json:"cash"`
		Fees       float64 `json:"fees"`
		MarginPct  float64 `json:"marginPct"`
		Positions  []struct {
			Symbol    string  `json:"symbol"`
			Name      string  `json:"name"`
			Qty       int64   `json:"qty"`
			AvgPrice  float64 `json:"avgPrice"`
			Price     float64 `json:"price"`
			Value     float64 `json:"value"`
			Profit    float64 `json:"profit"`
			ProfitPct float64 `json:"profitPct"`
		} `json:"positions"`
	}
	decode(t, data, &portfolio)
	if len(portfolio.Positions) != 1 {
		t.Fatalf("positions = %+v", portfolio.Positions)
	}
	pos := portfolio.Positions[0]
	if pos.Symbol != "TQBR:SBER" || pos.Name != "Сбербанк" || pos.Qty != 10 || pos.Price != 101 || pos.AvgPrice != 101.1 {
		t.Fatalf("position = %+v", pos)
	}
	if portfolio.MarginPct != 100 || portfolio.Fees <= 0 {
		t.Fatalf("portfolio = %+v", portfolio)
	}
	wantCash := 1_000_000 - 101.1*10 - 101.1*10*5/10000
	if diff := portfolio.Cash - wantCash; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("cash = %v, want %v", portfolio.Cash, wantCash)
	}
	if diff := portfolio.TotalValue - (portfolio.Cash + 10*101); diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("total value = %v", portfolio.TotalValue)
	}
	if diff := pos.Profit - (-1); diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("position profit = %v, want -1", pos.Profit)
	}
}

// TestPortfolioUnavailable checks the 503 when the paper broker is missing.
func TestPortfolioUnavailable(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	deps.Paper = nil
	_, ts := newTestServer(t, deps, Config{})
	for _, path := range []string{"/api/portfolio", "/api/orders", "/api/trades"} {
		status, data := do(t, ts, http.MethodGet, path, nil)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, body %s", path, status, data)
		}
	}
}

// TestBacktest checks the backtest endpoints.
func TestBacktest(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	var gotBars int
	deps.Backtest = func(req backtest.Request) (backtest.Result, error) {
		gotBars = len(req.Bars)
		return backtest.Result{Symbol: req.Symbol, Strategy: req.Strategy, PeriodSec: req.PeriodSec, Metrics: backtest.Metrics{Bars: len(req.Bars)}}, nil
	}
	_, ts := newTestServer(t, deps, Config{})

	status, data := do(t, ts, http.MethodGet, "/api/backtest/strategies", nil)
	if status != http.StatusOK {
		t.Fatalf("strategies status = %d, body %s", status, data)
	}
	var strategies struct {
		Strategies []struct {
			ID     string `json:"id"`
			Params []struct {
				Key string `json:"key"`
			} `json:"params"`
		} `json:"strategies"`
	}
	decode(t, data, &strategies)
	if len(strategies.Strategies) == 0 || strategies.Strategies[0].ID == "" {
		t.Fatalf("strategies = %+v", strategies.Strategies)
	}

	status, data = do(t, ts, http.MethodPost, "/api/backtest", map[string]any{
		"symbol": "TQBR:SBER", "periodSec": 60, "strategy": "sma_cross", "count": 50,
	})
	if status != http.StatusOK {
		t.Fatalf("backtest status = %d, body %s", status, data)
	}
	var result struct {
		Result struct {
			Symbol   string `json:"symbol"`
			Strategy string `json:"strategy"`
			Metrics  struct {
				Bars int `json:"bars"`
			} `json:"metrics"`
		} `json:"result"`
	}
	decode(t, data, &result)
	if result.Result.Symbol != "TQBR:SBER" || result.Result.Strategy != "sma_cross" || result.Result.Metrics.Bars != 3 {
		t.Fatalf("backtest result = %+v", result.Result)
	}
	if gotBars != 3 {
		t.Fatalf("backtest bars = %d", gotBars)
	}

	status, data = do(t, ts, http.MethodPost, "/api/backtest", map[string]any{"symbol": "TQBR:SBER"})
	if status != http.StatusBadRequest {
		t.Fatalf("missing strategy status = %d, body %s", status, data)
	}
}

// TestBacktestNotImplemented checks the 503 mapping of an unimplemented engine.
func TestBacktestNotImplemented(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	deps.Backtest = func(req backtest.Request) (backtest.Result, error) {
		return backtest.Result{}, errors.New("backtest: not implemented")
	}
	_, ts := newTestServer(t, deps, Config{})
	status, data := do(t, ts, http.MethodPost, "/api/backtest", map[string]any{
		"symbol": "TQBR:SBER", "strategy": "sma_cross", "periodSec": 60,
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("engine error status = %d, body %s", status, data)
	}
}

// TestAgentEndpoints checks the agent REST surface.
func TestAgentEndpoints(t *testing.T) {
	deps, _, _, a := defaultDeps()
	a.status.Autonomy.Log = []agent.LogEntry{
		{Kind: "start", Message: "старт"},
		{Kind: "fill", Message: "сделка"},
		{Kind: "stop", Message: "стоп"},
	}
	_, ts := newTestServer(t, deps, Config{})

	status, data := do(t, ts, http.MethodGet, "/api/agent/tools", nil)
	if status != http.StatusOK {
		t.Fatalf("tools status = %d, body %s", status, data)
	}
	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	decode(t, data, &tools)
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "list_instruments" {
		t.Fatalf("tools = %+v", tools.Tools)
	}

	status, data = do(t, ts, http.MethodPost, "/api/agent/chat", map[string]any{"message": "привет"})
	if status != http.StatusOK {
		t.Fatalf("chat status = %d, body %s", status, data)
	}
	var chat struct {
		Answer string `json:"answer"`
		LLM    bool   `json:"llm"`
	}
	decode(t, data, &chat)
	if chat.Answer != "ответ на: привет" {
		t.Fatalf("chat = %+v", chat)
	}
	status, _ = do(t, ts, http.MethodPost, "/api/agent/chat", map[string]any{"message": "  "})
	if status != http.StatusBadRequest {
		t.Fatalf("empty chat status = %d", status)
	}

	status, data = do(t, ts, http.MethodGet, "/api/agent/status", nil)
	if status != http.StatusOK {
		t.Fatalf("agent status = %d", status)
	}

	// Autonomy start/stop.
	status, data = do(t, ts, http.MethodPost, "/api/agent/autonomy", map[string]any{
		"action": "start",
		"config": map[string]any{"symbol": "TQBR:SBER", "periodSec": 60, "strategy": "sma_cross", "speed": 5, "budget": 100000},
	})
	if status != http.StatusOK {
		t.Fatalf("autonomy start = %d, body %s", status, data)
	}
	var autonomy struct {
		Autonomy struct {
			Running bool `json:"running"`
		} `json:"autonomy"`
	}
	decode(t, data, &autonomy)
	if !autonomy.Autonomy.Running || !a.started || a.startCfg.Symbol != "TQBR:SBER" {
		t.Fatalf("autonomy start resp = %+v started=%v cfg=%+v", autonomy, a.started, a.startCfg)
	}
	status, _ = do(t, ts, http.MethodPost, "/api/agent/autonomy", map[string]any{"action": "stop"})
	if status != http.StatusOK || !a.stopped {
		t.Fatalf("autonomy stop status = %d stopped=%v", status, a.stopped)
	}
	status, data = do(t, ts, http.MethodPost, "/api/agent/autonomy", map[string]any{"action": "start", "config": map[string]any{"live": true}})
	if status != http.StatusBadRequest {
		t.Fatalf("live autonomy status = %d, body %s", status, data)
	}
	status, data = do(t, ts, http.MethodPost, "/api/agent/autonomy", map[string]any{"action": "weird"})
	if status != http.StatusBadRequest {
		t.Fatalf("unknown action status = %d, body %s", status, data)
	}

	status, data = do(t, ts, http.MethodGet, "/api/agent/autonomy/log?limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("autonomy log status = %d", status)
	}
	var logResp struct {
		Log []struct {
			Kind string `json:"kind"`
		} `json:"log"`
	}
	decode(t, data, &logResp)
	if len(logResp.Log) != 2 || logResp.Log[0].Kind != "fill" || logResp.Log[1].Kind != "stop" {
		t.Fatalf("log = %+v", logResp.Log)
	}

	status, data = do(t, ts, http.MethodGet, "/api/agent/backtests", nil)
	if status != http.StatusOK {
		t.Fatalf("agent backtests status = %d", status)
	}
	var backtests struct {
		Results []struct {
			Strategy string `json:"strategy"`
		} `json:"results"`
	}
	decode(t, data, &backtests)
	if len(backtests.Results) != 1 || backtests.Results[0].Strategy != "sma_cross" {
		t.Fatalf("agent backtests = %+v", backtests.Results)
	}
}

// TestHints checks the hints endpoint.
func TestHints(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	status, data := do(t, ts, http.MethodGet, "/api/instruments/TQBR:SBER/hints?period=60", nil)
	if status != http.StatusOK {
		t.Fatalf("hints status = %d, body %s", status, data)
	}
	var hints struct {
		Symbol    string  `json:"symbol"`
		PeriodSec int     `json:"periodSec"`
		SMA20     float64 `json:"sma20"`
		Trend     string  `json:"trend"`
	}
	decode(t, data, &hints)
	if hints.Symbol != "TQBR:SBER" || hints.PeriodSec != 60 || hints.SMA20 != 100.5 || hints.Trend != "up" {
		t.Fatalf("hints = %+v", hints)
	}
}

// TestAgentUnavailable checks the 503 responses when the agent is absent.
func TestAgentUnavailable(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	deps.Agent = nil
	_, ts := newTestServer(t, deps, Config{})
	checks := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/agent/tools", nil},
		{http.MethodPost, "/api/agent/chat", map[string]any{"message": "x"}},
		{http.MethodGet, "/api/agent/status", nil},
		{http.MethodPost, "/api/agent/autonomy", map[string]any{"action": "start"}},
		{http.MethodGet, "/api/agent/autonomy/log", nil},
		{http.MethodGet, "/api/agent/backtests", nil},
		{http.MethodGet, "/api/instruments/TQBR:SBER/hints", nil},
	}
	for _, tc := range checks {
		status, data := do(t, ts, tc.method, tc.path, tc.body)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("%s %s status = %d, body %s", tc.method, tc.path, status, data)
		}
	}
}

// TestUnknownAPIRoute checks the JSON 404 for unknown /api paths.
func TestUnknownAPIRoute(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	status, data := do(t, ts, http.MethodGet, "/api/nope", nil)
	if status != http.StatusNotFound || errorCode(t, data) != codeNotFound {
		t.Fatalf("unknown route = %d %s", status, data)
	}
	if !strings.Contains(string(data), "error") {
		t.Fatalf("404 body = %s", data)
	}
}

// TestStaticSPA checks the embedded SPA serving and the index fallback.
func TestStaticSPA(t *testing.T) {
	web := fstest.MapFS{
		"index.html": {Data: []byte("<html><body>AI Terminal</body></html>")},
		"app.js":     {Data: []byte("export const x = 1;")},
	}
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{Web: web})

	status, data := do(t, ts, http.MethodGet, "/", nil)
	if status != http.StatusOK || !strings.Contains(string(data), "AI Terminal") {
		t.Fatalf("index status = %d, body %s", status, data)
	}
	// A client-side route falls back to index.html.
	status, data = do(t, ts, http.MethodGet, "/portfolio/overview", nil)
	if status != http.StatusOK || !strings.Contains(string(data), "AI Terminal") {
		t.Fatalf("spa fallback status = %d, body %s", status, data)
	}
	// A real asset is served as a file.
	status, data = do(t, ts, http.MethodGet, "/app.js", nil)
	if status != http.StatusOK || !strings.Contains(string(data), "export const") {
		t.Fatalf("asset status = %d, body %s", status, data)
	}
	// A missing asset is a JSON 404 (not the index page).
	status, data = do(t, ts, http.MethodGet, "/missing.js", nil)
	if status != http.StatusNotFound || errorCode(t, data) != codeNotFound {
		t.Fatalf("missing asset = %d %s", status, data)
	}
}

// TestNoWebFS checks the no-SPA build.
func TestNoWebFS(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	status, _ := do(t, ts, http.MethodGet, "/", nil)
	if status != http.StatusNotFound {
		t.Fatalf("no-web status = %d", status)
	}
}

// TestSSE checks the event stream: headers, quote events, portfolio and
// heartbeat.
func TestSSE(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache control = %q", cc)
	}
	seen := map[string]bool{}
	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !(seen["quote"] && seen["portfolio"] && seen["heartbeat"]) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var message struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &message); err != nil {
			t.Fatalf("decode SSE %q: %v", line, err)
		}
		seen[message.Type] = true
		switch message.Type {
		case "quote":
			var quote struct {
				Symbol string  `json:"symbol"`
				Last   float64 `json:"last"`
				Time   string  `json:"time"`
			}
			if err := json.Unmarshal(message.Data, &quote); err != nil {
				t.Fatalf("decode quote: %v", err)
			}
			if quote.Symbol == "" || quote.Time == "" {
				t.Fatalf("quote event = %s", message.Data)
			}
		}
	}
	if !seen["quote"] || !seen["portfolio"] || !seen["heartbeat"] {
		t.Fatalf("seen events = %v", seen)
	}
}

// TestSSESlowClientDropped checks the hub policy: a client that does not read
// is dropped instead of blocking the publisher.
func TestSSESlowClientDropped(t *testing.T) {
	h := newHub()
	ch, cancel := h.subscribe()
	defer cancel()
	for i := 0; i < hubBuffer+10; i++ {
		h.publish(event{Type: "quote", Data: i})
	}
	if got := h.size(); got != 0 {
		t.Fatalf("hub size = %d, want 0 after a slow client", got)
	}
	// The channel is closed: draining it ends with a closed channel.
	drained := 0
	for range ch {
		drained++
	}
	if drained != hubBuffer {
		t.Fatalf("drained %d events, want %d", drained, hubBuffer)
	}
	// A client that reads keeps receiving.
	ch2, cancel2 := h.subscribe()
	defer cancel2()
	h.publish(event{Type: "status"})
	select {
	case ev := <-ch2:
		if ev.Type != "status" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

// TestFeedEventForwarding checks that feed quotation events reach SSE clients.
func TestFeedEventForwarding(t *testing.T) {
	deps, f, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()
	subs := f.subscribers()
	if len(subs) == 0 {
		t.Fatal("feed was not subscribed by the event loop")
	}
	reader := bufio.NewReader(resp.Body)
	// Emit a live quotation update through the feed subscription.
	subs[0](feed.Event{Type: "quote", Data: feed.Quote{Symbol: "TQBR:SBER", Last: 123.45, Time: time.Now()}})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		if strings.Contains(line, "123.45") {
			return
		}
	}
	t.Fatal("forwarded quote not seen on SSE")
}

// TestPaperOnQuoteFillsRestingOrder checks that the 1s tick drives paper limit
// execution: a resting limit order fills when the quote touches it.
func TestPaperOnQuoteFillsRestingOrder(t *testing.T) {
	deps, f, _, _ := defaultDeps()
	srv, ts := newTestServer(t, deps, Config{})
	// Place a resting limit buy below the market.
	status, data := do(t, ts, http.MethodPost, "/api/orders", map[string]any{
		"symbol": "TQBR:SBER", "side": "buy", "qty": 1, "type": "limit", "price": 90,
	})
	if status != http.StatusCreated {
		t.Fatalf("place limit status = %d, body %s", status, data)
	}
	// Move the market down to the limit price; the event loop tick must fill it.
	f.setQuote("TQBR:SBER", feed.Quote{Symbol: "TQBR:SBER", Last: 90, Bid: 89.9, Ask: 90, Volume: 1, Time: time.Now()})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, data := do(t, ts, http.MethodGet, "/api/orders", nil)
		if status != http.StatusOK {
			t.Fatalf("orders status = %d", status)
		}
		var orders struct {
			Orders []struct {
				Status string `json:"status"`
			} `json:"orders"`
		}
		decode(t, data, &orders)
		if len(orders.Orders) == 1 && orders.Orders[0].Status == "filled" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("resting limit order was not filled by the event loop")
	_ = srv
}

// TestWrongMethod checks that a wrong method on a known API path is a JSON 404
// (the ServeMux catch-all), never a panic or an HTML page.
func TestWrongMethod(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	status, data := do(t, ts, http.MethodPost, "/api/health", nil)
	if status != http.StatusNotFound || errorCode(t, data) != codeNotFound {
		t.Fatalf("wrong method = %d %s", status, data)
	}
}

// TestIntParam checks the query parameter parsing helper.
func TestIntParam(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?a=5&b=x&c=99999999", nil)
	if v, ok := intParam(r, "a", 1); !ok || v != 5 {
		t.Fatalf("a = %d %v", v, ok)
	}
	if v, ok := intParam(r, "missing", 7); !ok || v != 7 {
		t.Fatalf("missing = %d %v", v, ok)
	}
	if _, ok := intParam(r, "b", 1); ok {
		t.Fatal("b must be malformed")
	}
	if _, ok := intParam(r, "c", 1); ok {
		t.Fatal("c must be out of range")
	}
}

// autonomyStatusDTO is the autonomy part of the POST /api/agent/autonomy
// response (only the fields the tests assert on).
type autonomyStatusDTO struct {
	Running      bool       `json:"running"`
	StartedAt    *time.Time `json:"startedAt"`
	LastActionAt *time.Time `json:"lastActionAt"`
	Config       struct {
		Symbol   string `json:"symbol"`
		Strategy string `json:"strategy"`
	} `json:"config"`
}

// postAutonomy performs one POST /api/agent/autonomy call and decodes the
// autonomy object.
func postAutonomy(t *testing.T, ts *httptest.Server, body any) (int, []byte, *autonomyStatusDTO) {
	t.Helper()
	status, data := do(t, ts, http.MethodPost, "/api/agent/autonomy", body)
	if status != http.StatusOK {
		return status, data, nil
	}
	var resp struct {
		Autonomy autonomyStatusDTO `json:"autonomy"`
	}
	decode(t, data, &resp)
	return status, data, &resp.Autonomy
}

// TestAutonomyDuplicateStartIsConflict checks the P1 defect: a second start
// while the runner is active is a 409 conflict with a Russian message, both
// when detected from the engine status and when the engine returns the error.
func TestAutonomyDuplicateStartIsConflict(t *testing.T) {
	cfg := map[string]any{"symbol": "TQBR:SBER", "periodSec": 60, "strategy": "sma_cross", "speed": 5}

	// Path 1: the API sees a running autonomy before calling the engine.
	deps, _, _, a := defaultDeps()
	a.status.Autonomy.Running = true
	_, ts := newTestServer(t, deps, Config{})
	status, data, _ := postAutonomy(t, ts, map[string]any{"action": "start", "config": cfg})
	if status != http.StatusConflict {
		t.Fatalf("running status start = %d, body %s", status, data)
	}
	if code := errorCode(t, data); code != codeConflict {
		t.Fatalf("running status code = %q", code)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decode(t, data, &env)
	if env.Error.Message != autonomyAlreadyRunning {
		t.Fatalf("conflict message = %q, want %q", env.Error.Message, autonomyAlreadyRunning)
	}
	if a.started {
		t.Fatal("engine must not be started when the API already sees a run")
	}

	// Path 2: the start/check race — the engine reports the conflict.
	deps2, _, _, a2 := defaultDeps()
	a2.startErr = errors.New("agent: autonomy is already running")
	_, ts2 := newTestServer(t, deps2, Config{})
	status, data, _ = postAutonomy(t, ts2, map[string]any{"action": "start", "config": cfg})
	if status != http.StatusConflict || errorCode(t, data) != codeConflict {
		t.Fatalf("engine conflict = %d, body %s", status, data)
	}
}

// TestAutonomyErrorMapping checks the mapping of start/stop failures to the
// API error codes of §10.
func TestAutonomyErrorMapping(t *testing.T) {
	startCfg := map[string]any{"symbol": "TQBR:SBER", "periodSec": 60, "strategy": "sma_cross", "speed": 5}
	cases := []struct {
		name     string
		action   string
		startErr error
		stopErr  error
		status   int
		code     string
	}{
		{"invalid strategy", "start", errors.New(`agent: strategy "nope": backtest: unknown strategy "nope"`), nil, http.StatusBadRequest, codeBadRequest},
		{"missing strategy", "start", errors.New("agent: autonomy strategy is required"), nil, http.StatusBadRequest, codeBadRequest},
		{"not enough history", "start", errors.New("agent: not enough history for TQBR:SBER (5 bars)"), nil, http.StatusBadRequest, codeBadRequest},
		{"not implemented", "start", errors.New("agent: not implemented"), nil, http.StatusServiceUnavailable, codeUnavailable},
		{"deadline", "start", context.DeadlineExceeded, nil, http.StatusServiceUnavailable, codeUnavailable},
		{"unknown start error", "start", errors.New("boom"), nil, http.StatusInternalServerError, codeInternal},
		{"unknown stop error", "stop", nil, errors.New("boom"), http.StatusInternalServerError, codeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _, a := defaultDeps()
			a.startErr = tc.startErr
			a.stopErr = tc.stopErr
			_, ts := newTestServer(t, deps, Config{})
			body := map[string]any{"action": tc.action}
			if tc.action == "start" {
				body["config"] = startCfg
			}
			status, data, _ := postAutonomy(t, ts, body)
			if status != tc.status {
				t.Fatalf("status = %d, want %d, body %s", status, tc.status, data)
			}
			if code := errorCode(t, data); code != tc.code {
				t.Fatalf("code = %q, want %q, body %s", code, tc.code, data)
			}
		})
	}
}

// TestAutonomyResponseTimestamps checks the P2 defect: a successful start/stop
// always returns non-zero startedAt and lastActionAt even if the engine status
// still carries zero times.
func TestAutonomyResponseTimestamps(t *testing.T) {
	deps, _, _, a := defaultDeps()
	a.status.Autonomy.StartedAt = time.Time{}
	a.status.Autonomy.LastActionAt = time.Time{}
	_, ts := newTestServer(t, deps, Config{})

	status, data, autonomy := postAutonomy(t, ts, map[string]any{
		"action": "start",
		"config": map[string]any{"symbol": "TQBR:SBER", "periodSec": 60, "strategy": "sma_cross", "speed": 5},
	})
	if status != http.StatusOK {
		t.Fatalf("start status = %d, body %s", status, data)
	}
	if !autonomy.Running {
		t.Fatalf("autonomy must be running: %+v", autonomy)
	}
	if autonomy.StartedAt == nil || autonomy.StartedAt.IsZero() {
		t.Fatalf("startedAt is zero in the start response: %s", data)
	}
	if autonomy.LastActionAt == nil || autonomy.LastActionAt.IsZero() {
		t.Fatalf("lastActionAt is zero in the start response: %s", data)
	}

	// A fresh stop of a runner whose engine left lastActionAt unset must also
	// carry a timestamp.
	a.status.Autonomy.LastActionAt = time.Time{}
	status, data, autonomy = postAutonomy(t, ts, map[string]any{"action": "stop"})
	if status != http.StatusOK {
		t.Fatalf("stop status = %d, body %s", status, data)
	}
	if autonomy.Running {
		t.Fatalf("autonomy must be stopped: %+v", autonomy)
	}
	if autonomy.LastActionAt == nil || autonomy.LastActionAt.IsZero() {
		t.Fatalf("lastActionAt is zero in the stop response: %s", data)
	}
}

// TestAgentStatusLastActionFallback checks that a running autonomy with an
// empty lastActionAt reports the start time instead of a zero time.
func TestAgentStatusLastActionFallback(t *testing.T) {
	started := time.Date(2026, 11, 5, 16, 30, 0, 0, time.UTC)
	deps, _, _, a := defaultDeps()
	a.status.Autonomy = agent.AutonomyStatus{Running: true, StartedAt: started}
	_, ts := newTestServer(t, deps, Config{})
	status, data := do(t, ts, http.MethodGet, "/api/agent/status", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, data)
	}
	var resp struct {
		Autonomy struct {
			StartedAt    time.Time `json:"startedAt"`
			LastActionAt time.Time `json:"lastActionAt"`
		} `json:"autonomy"`
	}
	decode(t, data, &resp)
	if !resp.Autonomy.StartedAt.Equal(started) {
		t.Fatalf("startedAt = %v", resp.Autonomy.StartedAt)
	}
	if !resp.Autonomy.LastActionAt.Equal(started) {
		t.Fatalf("lastActionAt = %v, want start time %v", resp.Autonomy.LastActionAt, started)
	}
	// A never-started status keeps its zero times.
	emptyDeps, _, _, emptyAgent := defaultDeps()
	emptyAgent.status.Autonomy = agent.AutonomyStatus{}
	_, emptyTS := newTestServer(t, emptyDeps, Config{})
	status, data = do(t, emptyTS, http.MethodGet, "/api/agent/status", nil)
	if status != http.StatusOK {
		t.Fatalf("empty status = %d, body %s", status, data)
	}
	var empty struct {
		Autonomy struct {
			LastActionAt time.Time `json:"lastActionAt"`
		} `json:"autonomy"`
	}
	decode(t, data, &empty)
	if !empty.Autonomy.LastActionAt.IsZero() {
		t.Fatalf("never-started lastActionAt = %v, want zero", empty.Autonomy.LastActionAt)
	}
}

// TestStableAutonomyNormalizesUTC checks the autonomy timestamp normalization:
// a missing lastActionAt of a running runner falls back to startedAt and all
// times are rendered as UTC (§10.2), even if the engine uses the local zone.
func TestStableAutonomyNormalizesUTC(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	started := time.Date(2026, 11, 5, 19, 30, 0, 0, msk)
	st := stableAutonomy(agent.AutonomyStatus{
		Running:   true,
		StartedAt: started,
		Log:       []agent.LogEntry{{Time: started, Kind: "start", Message: "старт"}},
	})
	if _, offset := st.StartedAt.Zone(); offset != 0 {
		t.Fatalf("startedAt zone offset = %d, want UTC", offset)
	}
	if !st.LastActionAt.Equal(started) {
		t.Fatalf("lastActionAt = %v, want %v", st.LastActionAt, started)
	}
	if _, offset := st.LastActionAt.Zone(); offset != 0 {
		t.Fatalf("lastActionAt zone offset = %d, want UTC", offset)
	}
	if _, offset := st.Log[0].Time.Zone(); offset != 0 {
		t.Fatalf("log time zone offset = %d, want UTC", offset)
	}
	// A never-started status stays zero.
	empty := stableAutonomy(agent.AutonomyStatus{})
	if !empty.StartedAt.IsZero() || !empty.LastActionAt.IsZero() {
		t.Fatalf("empty autonomy = %+v", empty)
	}
}
