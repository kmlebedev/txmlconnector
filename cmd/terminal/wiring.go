package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// marketSource is the market-data view the adapters need (implemented by
// *feed.Service).
type marketSource interface {
	Instruments() []feed.Instrument
	Quote(symbol string) (feed.Quote, bool)
	OrderBook(ctx context.Context, symbol string) (feed.OrderBook, error)
	Candles(ctx context.Context, symbol string, periodSec, count int) (feed.History, error)
}

// brokerSource is the paper-broker view the adapters need (implemented by
// *paper.Broker).
type brokerSource interface {
	Place(req paper.OrderRequest, q paper.QuoteView) (paper.Order, error)
	Cancel(id string) (paper.Order, error)
	Snapshot() paper.Snapshot
}

// feedAdapter implements agent.DataSource over the market feed.
type feedAdapter struct {
	market marketSource
}

// Compile-time contract checks.
var (
	_ agent.DataSource = (*feedAdapter)(nil)
	_ agent.Broker     = (*paperAdapter)(nil)
)

// newFeedAdapter wraps the market feed as the agent data source.
func newFeedAdapter(market marketSource) *feedAdapter {
	return &feedAdapter{market: market}
}

// Instruments returns the agent view of the instrument list.
func (a *feedAdapter) Instruments() []agent.InstrumentRef {
	instruments := a.market.Instruments()
	out := make([]agent.InstrumentRef, 0, len(instruments))
	for _, inst := range instruments {
		out = append(out, agent.InstrumentRef{
			Symbol:    inst.Symbol,
			Name:      inst.Name,
			Last:      inst.Last,
			ChangePct: inst.ChangePct,
		})
	}
	return out
}

// Quote returns the agent view of one quote.
func (a *feedAdapter) Quote(symbol string) (agent.Quote, bool) {
	quote, ok := a.market.Quote(symbol)
	if !ok {
		return agent.Quote{}, false
	}
	return agent.Quote{
		Symbol:    quote.Symbol,
		Last:      quote.Last,
		Bid:       quote.Bid,
		Ask:       quote.Ask,
		ChangePct: quote.ChangePct,
		Time:      quote.Time,
	}, true
}

// OrderBook returns the agent view of the price ladder. The frozen agent
// DataSource contract has no context, so the feed call uses the background one.
func (a *feedAdapter) OrderBook(symbol string) (agent.OrderBook, error) {
	book, err := a.market.OrderBook(context.Background(), symbol)
	if err != nil {
		return agent.OrderBook{}, err
	}
	out := agent.OrderBook{Symbol: book.Symbol, Last: book.Last, Bids: make([]agent.Level, 0, len(book.Bids))}
	for _, lvl := range book.Bids {
		out.Bids = append(out.Bids, agent.Level{Price: lvl.Price, Qty: lvl.Qty})
	}
	out.Asks = make([]agent.Level, 0, len(book.Asks))
	for _, lvl := range book.Asks {
		out.Asks = append(out.Asks, agent.Level{Price: lvl.Price, Qty: lvl.Qty})
	}
	return out, nil
}

// History returns the agent/backtest candle series.
func (a *feedAdapter) History(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error) {
	history, err := a.market.Candles(ctx, symbol, periodSec, count)
	if err != nil {
		return nil, err
	}
	bars := make([]backtest.Bar, 0, len(history.Bars))
	for _, bar := range history.Bars {
		bars = append(bars, backtest.Bar{
			Time:   bar.Time,
			Open:   bar.Open,
			High:   bar.High,
			Low:    bar.Low,
			Close:  bar.Close,
			Volume: bar.Volume,
		})
	}
	return bars, nil
}

// paperAdapter implements agent.Broker over the paper account and the market
// feed (paper orders need the current quote).
type paperAdapter struct {
	broker brokerSource
	market marketSource
}

// newPaperAdapter wraps the paper broker for the agent.
func newPaperAdapter(broker brokerSource, market marketSource) *paperAdapter {
	return &paperAdapter{broker: broker, market: market}
}

// Place executes one paper order for the agent.
func (a *paperAdapter) Place(order agent.PlaceOrder) (agent.OrderView, error) {
	req := paper.OrderRequest{
		Symbol:        order.Symbol,
		Side:          paper.Side(order.Side),
		Qty:           order.Qty,
		Type:          paper.OrderType(order.Type),
		Price:         order.Price,
		Source:        "agent",
		ClientOrderID: order.ClientOrderID,
	}
	if req.Type == "" {
		req.Type = paper.Market
	}
	view := paper.QuoteView{Symbol: order.Symbol}
	if quote, ok := a.market.Quote(order.Symbol); ok {
		view.Last, view.Bid, view.Ask, view.Time = quote.Last, quote.Bid, quote.Ask, quote.Time
	}
	placed, err := a.broker.Place(req, view)
	if err != nil {
		return agent.OrderView{}, err
	}
	return orderView(placed), nil
}

// Cancel cancels a resting paper order.
func (a *paperAdapter) Cancel(id string) (agent.OrderView, error) {
	order, err := a.broker.Cancel(id)
	if err != nil {
		return agent.OrderView{}, err
	}
	return orderView(order), nil
}

// Orders returns all paper orders, newest last.
func (a *paperAdapter) Orders() []agent.OrderView {
	snapshot := a.broker.Snapshot()
	out := make([]agent.OrderView, 0, len(snapshot.Orders))
	for _, order := range snapshot.Orders {
		out = append(out, orderView(order))
	}
	return out
}

// Portfolio returns the paper portfolio marked to the current market quotes.
func (a *paperAdapter) Portfolio() agent.PortfolioView {
	snapshot := a.broker.Snapshot()
	view := agent.PortfolioView{
		Cash:      snapshot.Cash,
		Positions: make([]agent.PositionView, 0, len(snapshot.Positions)),
	}
	total := snapshot.Cash
	var dayChange float64
	for _, pos := range snapshot.Positions {
		price := pos.AvgPrice
		changePct := 0.0
		if quote, ok := a.market.Quote(pos.Symbol); ok && quote.Last > 0 {
			price = quote.Last
			changePct = quote.ChangePct
		}
		value := price * float64(pos.Qty)
		item := agent.PositionView{
			Symbol:   pos.Symbol,
			Qty:      pos.Qty,
			AvgPrice: pos.AvgPrice,
			Price:    price,
			Value:    value,
			Profit:   (price - pos.AvgPrice) * float64(pos.Qty),
		}
		if pos.AvgPrice > 0 {
			item.ProfitPct = (price/pos.AvgPrice - 1) * 100
		}
		total += value
		dayChange += value * changePct / 100
		view.Positions = append(view.Positions, item)
	}
	view.TotalValue = total
	view.DayChange = dayChange
	if base := total - dayChange; base != 0 {
		view.DayChangePct = dayChange / base * 100
	}
	return view
}

// Cash returns the free paper cash.
func (a *paperAdapter) Cash() float64 {
	return a.broker.Snapshot().Cash
}

// orderView maps a paper order to the agent view.
func orderView(order paper.Order) agent.OrderView {
	return agent.OrderView{
		ID:           order.ID,
		Symbol:       order.Symbol,
		Side:         string(order.Side),
		Type:         string(order.Type),
		Status:       string(order.Status),
		Qty:          order.Qty,
		Filled:       order.Filled,
		Price:        order.Price,
		AvgFillPrice: order.AvgFillPrice,
		CreatedAt:    order.CreatedAt,
	}
}

// exchangeConfig is the live-session configuration read from the environment.
// The values are secrets: they are never logged.
type exchangeConfig struct {
	Address  string
	Login    string
	Password string
}

// Complete reports whether a live session can be attempted.
func (c exchangeConfig) Complete() bool {
	return c.Address != "" && c.Login != "" && c.Password != ""
}

// exchangeConfigFromEnv reads TRANSAQ_* with the TC_* fallback of §2. Empty
// variables are fine: auto mode then stays offline.
func exchangeConfigFromEnv() exchangeConfig {
	return exchangeConfig{
		Address:  firstNonEmpty(os.Getenv("TRANSAQ_ADDR"), hostPortFromEnv()),
		Login:    firstNonEmpty(os.Getenv("TRANSAQ_LOGIN"), os.Getenv("TC_LOGIN")),
		Password: firstNonEmpty(os.Getenv("TRANSAQ_PASSWORD"), os.Getenv("TC_PASSWORD")),
	}
}

// hostPortFromEnv combines TC_HOST/TC_PORT; the TRANSAQ demo port is 3939.
func hostPortFromEnv() string {
	host := strings.TrimSpace(os.Getenv("TC_HOST"))
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	port := strings.TrimSpace(os.Getenv("TC_PORT"))
	if port == "" {
		port = "3939"
	}
	return net.JoinHostPort(host, port)
}

// resolveMode applies the -mode/auto rule of §2: auto is live when the exchange
// address, login and password are all set, offline otherwise.
func resolveMode(flagMode string, cfg exchangeConfig) (feed.Mode, error) {
	switch strings.ToLower(strings.TrimSpace(flagMode)) {
	case "", "auto":
		if cfg.Complete() {
			return feed.ModeLive, nil
		}
		return feed.ModeOffline, nil
	case "offline":
		return feed.ModeOffline, nil
	case "live":
		if !cfg.Complete() {
			return "", errors.New("для live-режима нужны TRANSAQ_ADDR (или TC_HOST/TC_PORT), TRANSAQ_LOGIN и TRANSAQ_PASSWORD")
		}
		return feed.ModeLive, nil
	default:
		return "", fmt.Errorf("неизвестный режим %q: ожидается auto, offline или live", flagMode)
	}
}

// llmConfigFromEnv builds the OpenAI-compatible LLM config from TERMINAL_LLM_*.
// Enabled is computed by the agent package from BaseURL and APIKey.
func llmConfigFromEnv() (agent.LLMConfig, error) {
	cfg := agent.LLMConfig{
		BaseURL: os.Getenv("TERMINAL_LLM_BASE_URL"),
		APIKey:  os.Getenv("TERMINAL_LLM_API_KEY"),
		Model:   os.Getenv("TERMINAL_LLM_MODEL"),
		Timeout: 90 * time.Second,
	}
	if raw := strings.TrimSpace(os.Getenv("TERMINAL_LLM_TIMEOUT")); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil {
			return agent.LLMConfig{}, fmt.Errorf("TERMINAL_LLM_TIMEOUT: ожидается длительность, например 90s, получено %q", raw)
		}
		if timeout <= 0 {
			return agent.LLMConfig{}, fmt.Errorf("TERMINAL_LLM_TIMEOUT: должно быть больше нуля, получено %q", raw)
		}
		cfg.Timeout = timeout
	}
	return cfg, nil
}

// firstNonEmpty returns the first non-empty trimmed value.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
