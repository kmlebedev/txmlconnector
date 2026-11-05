package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// fakeMarket is a marketSource fake.
type fakeMarket struct {
	instruments []feed.Instrument
	quotes      map[string]feed.Quote
	books       map[string]feed.OrderBook
	bars        []feed.Bar
	err         error
}

func (f *fakeMarket) Instruments() []feed.Instrument { return f.instruments }

func (f *fakeMarket) Quote(symbol string) (feed.Quote, bool) {
	q, ok := f.quotes[symbol]
	return q, ok
}

func (f *fakeMarket) OrderBook(ctx context.Context, symbol string) (feed.OrderBook, error) {
	if f.err != nil {
		return feed.OrderBook{}, f.err
	}
	return f.books[symbol], nil
}

func (f *fakeMarket) Candles(ctx context.Context, symbol string, periodSec, count int) (feed.History, error) {
	if f.err != nil {
		return feed.History{}, f.err
	}
	return feed.History{Symbol: symbol, PeriodSec: periodSec, Bars: append([]feed.Bar(nil), f.bars...), Source: "seed"}, nil
}

func newFakeMarket() *fakeMarket {
	return &fakeMarket{
		instruments: []feed.Instrument{
			{Symbol: "TQBR:SBER", Name: "Сбербанк", Last: 101, ChangePct: 1},
			{Symbol: "TQBR:GAZP", Name: "Газпром", Last: 151, ChangePct: 0.67},
		},
		quotes: map[string]feed.Quote{
			"TQBR:SBER": {Symbol: "TQBR:SBER", Last: 101, Bid: 100.9, Ask: 101.1, ChangePct: 1, Time: time.Unix(1793893440, 0).UTC()},
			"TQBR:GAZP": {Symbol: "TQBR:GAZP", Last: 151, Bid: 150.9, Ask: 151.1, ChangePct: 0.67},
		},
		books: map[string]feed.OrderBook{
			"TQBR:SBER": {Symbol: "TQBR:SBER", Last: 101, Bids: []feed.Level{{Price: 100.9, Qty: 10}}, Asks: []feed.Level{{Price: 101.1, Qty: 20}}},
		},
		bars: []feed.Bar{
			{Time: time.Unix(1793893440, 0).UTC(), Open: 100, High: 101, Low: 99, Close: 100.5, Volume: 10},
			{Time: time.Unix(1793893500, 0).UTC(), Open: 100.5, High: 102, Low: 100, Close: 101, Volume: 20},
		},
	}
}

// TestFeedAdapter checks the feed -> agent.DataSource adapter.
func TestFeedAdapter(t *testing.T) {
	adapter := newFeedAdapter(newFakeMarket())
	var _ agent.DataSource = adapter // compile-time contract

	instruments := adapter.Instruments()
	if len(instruments) != 2 || instruments[0].Symbol != "TQBR:SBER" || instruments[0].Name != "Сбербанк" {
		t.Fatalf("instruments = %+v", instruments)
	}
	quote, ok := adapter.Quote("TQBR:SBER")
	if !ok || quote.Last != 101 || quote.Bid != 100.9 || quote.Ask != 101.1 || quote.ChangePct != 1 {
		t.Fatalf("quote = %+v (ok=%v)", quote, ok)
	}
	if _, ok := adapter.Quote("TQBR:UNKNOWN"); ok {
		t.Fatal("unknown quote found")
	}
	book, err := adapter.OrderBook("TQBR:SBER")
	if err != nil {
		t.Fatalf("OrderBook: %v", err)
	}
	if book.Symbol != "TQBR:SBER" || len(book.Bids) != 1 || book.Bids[0].Price != 100.9 || len(book.Asks) != 1 {
		t.Fatalf("book = %+v", book)
	}
	bars, err := adapter.History(context.Background(), "TQBR:SBER", 60, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(bars) != 2 || bars[1].Close != 101 || bars[0].Volume != 10 {
		t.Fatalf("bars = %+v", bars)
	}
}

// TestFeedAdapterErrors checks that feed errors are passed through.
func TestFeedAdapterErrors(t *testing.T) {
	market := newFakeMarket()
	market.err = errors.New("feed unavailable")
	adapter := newFeedAdapter(market)
	if _, err := adapter.OrderBook("TQBR:SBER"); err == nil {
		t.Fatal("OrderBook must fail")
	}
	if _, err := adapter.History(context.Background(), "TQBR:SBER", 60, 10); err == nil {
		t.Fatal("History must fail")
	}
}

// TestPaperAdapter checks the paper -> agent.Broker adapter: place, cancel,
// orders, portfolio and cash.
func TestPaperAdapter(t *testing.T) {
	market := newFakeMarket()
	broker := paper.New(paper.Config{InitialCash: 100_000, FeeBps: 5})
	adapter := newPaperAdapter(broker, market)
	var _ agent.Broker = adapter // compile-time contract

	cash := adapter.Cash()
	if cash != 100_000 {
		t.Fatalf("initial cash = %v", cash)
	}
	order, err := adapter.Place(agent.PlaceOrder{Symbol: "TQBR:SBER", Side: "buy", Qty: 10, Type: "market"})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if order.Status != "filled" || order.AvgFillPrice != 101.1 {
		t.Fatalf("order = %+v", order)
	}
	if orders := adapter.Orders(); len(orders) != 1 || orders[0].ID != order.ID {
		t.Fatalf("orders = %+v", orders)
	}
	portfolio := adapter.Portfolio()
	if portfolio.TotalValue <= 0 || portfolio.Cash >= 100_000 || len(portfolio.Positions) != 1 {
		t.Fatalf("portfolio = %+v", portfolio)
	}
	pos := portfolio.Positions[0]
	if pos.Symbol != "TQBR:SBER" || pos.Qty != 10 || pos.Price != 101 || pos.Value != 1010 {
		t.Fatalf("position = %+v", pos)
	}
	// A resting limit order can be cancelled through the adapter.
	limit, err := adapter.Place(agent.PlaceOrder{Symbol: "TQBR:SBER", Side: "buy", Qty: 1, Type: "limit", Price: 50})
	if err != nil {
		t.Fatalf("limit place: %v", err)
	}
	if limit.Status != "new" {
		t.Fatalf("limit = %+v", limit)
	}
	cancelled, err := adapter.Cancel(limit.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancelled = %+v", cancelled)
	}
	// A rejected order surfaces the broker error.
	if _, err := adapter.Place(agent.PlaceOrder{Symbol: "TQBR:SBER", Side: "sell", Qty: 100, Type: "market"}); err == nil {
		t.Fatal("oversell must fail")
	}
}

// TestResolveMode checks the auto/offline/live rule.
func TestResolveMode(t *testing.T) {
	full := exchangeConfig{Address: "host:1", Login: "login", Password: "secret"}
	cases := []struct {
		flag string
		cfg  exchangeConfig
		want feed.Mode
		err  bool
	}{
		{"auto", exchangeConfig{}, feed.ModeOffline, false},
		{"auto", full, feed.ModeLive, false},
		{"", full, feed.ModeLive, false},
		{"offline", full, feed.ModeOffline, false},
		{"live", full, feed.ModeLive, false},
		{"live", exchangeConfig{}, "", true},
		{"live", exchangeConfig{Address: "host:1"}, "", true},
		{"weird", full, "", true},
	}
	for _, tc := range cases {
		mode, err := resolveMode(tc.flag, tc.cfg)
		if tc.err {
			if err == nil {
				t.Fatalf("resolveMode(%q, %+v): want error", tc.flag, tc.cfg)
			}
			continue
		}
		if err != nil {
			t.Fatalf("resolveMode(%q, %+v): %v", tc.flag, tc.cfg, err)
		}
		if mode != tc.want {
			t.Fatalf("resolveMode(%q, %+v) = %q, want %q", tc.flag, tc.cfg, mode, tc.want)
		}
	}
}

// TestExchangeConfigFromEnv checks the TRANSAQ_*/TC_* fallback rules.
func TestExchangeConfigFromEnv(t *testing.T) {
	for _, name := range []string{"TRANSAQ_ADDR", "TRANSAQ_LOGIN", "TRANSAQ_PASSWORD", "TC_HOST", "TC_PORT", "TC_LOGIN", "TC_PASSWORD"} {
		t.Setenv(name, "")
	}
	cfg := exchangeConfigFromEnv()
	if cfg.Complete() {
		t.Fatalf("empty env must be incomplete: %+v", cfg)
	}
	for _, name := range []string{"TRANSAQ_ADDR", "TRANSAQ_LOGIN", "TRANSAQ_PASSWORD"} {
		t.Setenv(name, "value-"+name)
	}
	cfg = exchangeConfigFromEnv()
	if cfg.Address != "value-TRANSAQ_ADDR" || cfg.Login != "value-TRANSAQ_LOGIN" || cfg.Password != "value-TRANSAQ_PASSWORD" {
		t.Fatalf("cfg = %+v", cfg)
	}
	// TC_* fallback and host:port combining.
	t.Setenv("TRANSAQ_ADDR", "")
	t.Setenv("TRANSAQ_LOGIN", "")
	t.Setenv("TRANSAQ_PASSWORD", "")
	t.Setenv("TC_HOST", "127.0.0.1")
	t.Setenv("TC_PORT", "3939")
	t.Setenv("TC_LOGIN", "tc-login")
	t.Setenv("TC_PASSWORD", "tc-password")
	cfg = exchangeConfigFromEnv()
	if cfg.Address != "127.0.0.1:3939" || cfg.Login != "tc-login" || cfg.Password != "tc-password" || !cfg.Complete() {
		t.Fatalf("TC fallback cfg = %+v", cfg)
	}
	// A host that already carries a port is kept as is.
	t.Setenv("TC_HOST", "host.example:1234")
	if got := hostPortFromEnv(); got != "host.example:1234" {
		t.Fatalf("hostPortFromEnv = %q", got)
	}
	// Without TC_PORT the TRANSAQ default port is used.
	t.Setenv("TC_HOST", "host.example")
	t.Setenv("TC_PORT", "")
	if got := hostPortFromEnv(); got != "host.example:3939" {
		t.Fatalf("hostPortFromEnv default = %q", got)
	}
}

// TestLLMConfigFromEnv checks the LLM env parsing (no network).
func TestLLMConfigFromEnv(t *testing.T) {
	t.Setenv("TERMINAL_LLM_BASE_URL", "https://api.example.com/v1")
	t.Setenv("TERMINAL_LLM_API_KEY", "test-key")
	t.Setenv("TERMINAL_LLM_MODEL", "")
	t.Setenv("TERMINAL_LLM_TIMEOUT", "")
	cfg, err := llmConfigFromEnv()
	if err != nil {
		t.Fatalf("llmConfigFromEnv: %v", err)
	}
	if cfg.BaseURL != "https://api.example.com/v1" || cfg.APIKey != "test-key" || cfg.Timeout != 90*time.Second {
		t.Fatalf("cfg = %+v", cfg)
	}
	t.Setenv("TERMINAL_LLM_TIMEOUT", "5s")
	cfg, err = llmConfigFromEnv()
	if err != nil || cfg.Timeout != 5*time.Second {
		t.Fatalf("timeout cfg = %+v, err %v", cfg, err)
	}
	t.Setenv("TERMINAL_LLM_TIMEOUT", "soon")
	if _, err := llmConfigFromEnv(); err == nil {
		t.Fatal("invalid timeout must fail")
	}
	t.Setenv("TERMINAL_LLM_TIMEOUT", "-5s")
	if _, err := llmConfigFromEnv(); err == nil {
		t.Fatal("negative timeout must fail")
	}
}

// TestNewsStoreFromEnv checks the optional md.news store wiring: the get_news
// tool is enabled only when a ClickHouse endpoint is configured, and the MD_*
// recorder variables are accepted next to the generic CLICKHOUSE_* ones.
func TestNewsStoreFromEnv(t *testing.T) {
	t.Setenv("MD_CLICKHOUSE_URL", "")
	t.Setenv("CLICKHOUSE_URL", "")
	if store := newsStoreFromEnv(); store != nil {
		t.Fatalf("newsStoreFromEnv without a URL = %T, want nil (recording off)", store)
	}
	t.Setenv("CLICKHOUSE_URL", "http://127.0.0.1:8123")
	if store := newsStoreFromEnv(); store == nil {
		t.Fatal("newsStoreFromEnv with CLICKHOUSE_URL = nil, want the md.news store")
	}
	t.Setenv("MD_CLICKHOUSE_URL", "http://127.0.0.2:8123")
	t.Setenv("MD_CLICKHOUSE_USER", "md-reader")
	t.Setenv("MD_CLICKHOUSE_PASSWORD", "test-secret")
	if store := newsStoreFromEnv(); store == nil {
		t.Fatal("newsStoreFromEnv with MD_CLICKHOUSE_URL = nil, want the md.news store")
	}
}

// TestIsLoopbackAddr checks the bind-address warning helper.
func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8090": true,
		"localhost:8090": true,
		"[::1]:8090":     true,
		":8090":          false, // empty host: all interfaces, not loopback
		"0.0.0.0:8090":   false,
		"[::]:8090":      false,
		"192.168.1.2:80": false,
		"example.com:80": false,
		"bad":            false,
	}
	for addr, want := range cases {
		if got := isLoopbackAddr(addr); got != want {
			t.Fatalf("isLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

// TestPaperAdapterWithoutQuotes checks that portfolio marking falls back to the
// average price when the feed has no quote.
func TestPaperAdapterWithoutQuotes(t *testing.T) {
	market := &fakeMarket{quotes: map[string]feed.Quote{}}
	broker := paper.New(paper.Config{InitialCash: 1000, FeeBps: 5})
	adapter := newPaperAdapter(broker, market)
	order, err := adapter.Place(agent.PlaceOrder{Symbol: "TQBR:SBER", Side: "buy", Qty: 1, Type: "limit", Price: 100})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if order.Status != "new" {
		t.Fatalf("order = %+v", order)
	}
	portfolio := adapter.Portfolio()
	if portfolio.TotalValue != 1000 || len(portfolio.Positions) != 0 {
		t.Fatalf("portfolio = %+v", portfolio)
	}
}

// TestBacktestRunner checks the HTTP backtest seam.
func TestBacktestRunner(t *testing.T) {
	bars := []backtest.Bar{
		{Time: time.Unix(1793893440, 0).UTC(), Open: 100, High: 101, Low: 99, Close: 100.5},
		{Time: time.Unix(1793893500, 0).UTC(), Open: 100.5, High: 102, Low: 100, Close: 101},
	}
	result, err := runBacktest(backtest.Request{Symbol: "TQBR:SBER", PeriodSec: 60, Strategy: "sma_cross", Bars: bars})
	if err != nil {
		t.Fatalf("runBacktest: %v", err)
	}
	if result.Symbol != "TQBR:SBER" || result.Strategy != "sma_cross" || result.Metrics.Bars != 2 {
		t.Fatalf("result = %+v", result)
	}
}
