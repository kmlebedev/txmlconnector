package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// --- response DTOs (docs/ai-terminal/ARCHITECTURE.md §10.1–§10.2) ----------

// healthResponse is GET /api/health.
type healthResponse struct {
	OK              bool          `json:"ok"`
	Version         string        `json:"version"`
	Mode            string        `json:"mode"`
	Connected       bool          `json:"connected"`
	Message         string        `json:"message"`
	ServerTime      time.Time     `json:"serverTime"`
	ExchangeTZ      string        `json:"exchangeTZ"`
	SeedGeneratedAt time.Time     `json:"seedGeneratedAt"`
	LLM             llmStatusDTO  `json:"llm"`
	Trading         tradingStatus `json:"trading"`
}

type llmStatusDTO struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
	Note    string `json:"note"`
}

type tradingStatus struct {
	Paper bool `json:"paper"`
	Live  bool `json:"live"`
}

// instrumentDTO is one instrument of GET /api/instruments.
type instrumentDTO struct {
	Symbol    string    `json:"symbol"`
	Board     string    `json:"board"`
	SecCode   string    `json:"seccode"`
	Name      string    `json:"name"`
	ShortName string    `json:"shortName"`
	Currency  string    `json:"currency"`
	Decimals  int       `json:"decimals"`
	LotSize   int64     `json:"lotSize"`
	MinStep   float64   `json:"minStep"`
	PrevClose float64   `json:"prevClose"`
	Last      float64   `json:"last"`
	Change    float64   `json:"change"`
	ChangePct float64   `json:"changePct"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Volume    int64     `json:"volume"`
	Bid       float64   `json:"bid"`
	Ask       float64   `json:"ask"`
	Spark     []float64 `json:"spark"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type instrumentsResponse struct {
	ServerTime  time.Time       `json:"serverTime"`
	Instruments []instrumentDTO `json:"instruments"`
}

// candleDTO is one bar: t is the bar instant in Unix seconds (ARCHITECTURE §5).
type candleDTO struct {
	T int64   `json:"t"`
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V int64   `json:"v"`
}

type candlesResponse struct {
	Symbol    string      `json:"symbol"`
	PeriodSec int         `json:"periodSec"`
	Source    string      `json:"source"`
	Note      string      `json:"note"`
	Candles   []candleDTO `json:"candles"`
}

// levelDTO is one order-book level.
type levelDTO struct {
	Price float64 `json:"price"`
	Qty   float64 `json:"qty"`
}

type orderBookResponse struct {
	Symbol string     `json:"symbol"`
	Last   float64    `json:"last"`
	Time   time.Time  `json:"time"`
	Bids   []levelDTO `json:"bids"`
	Asks   []levelDTO `json:"asks"`
	Source string     `json:"source"`
}

// marketTradeDTO is one tick-tape entry of GET /api/instruments/{symbol}/trades.
type marketTradeDTO struct {
	Time  time.Time `json:"time"`
	Price float64   `json:"price"`
	Qty   int64     `json:"qty"`
	Side  string    `json:"side"`
}

type marketTradesResponse struct {
	Symbol string           `json:"symbol"`
	Trades []marketTradeDTO `json:"trades"`
}

// orderDTO is one paper order.
type orderDTO struct {
	ID           string    `json:"id"`
	Symbol       string    `json:"symbol"`
	Side         string    `json:"side"`
	Type         string    `json:"type"`
	Qty          int64     `json:"qty"`
	Filled       int64     `json:"filled"`
	Price        float64   `json:"price"`
	AvgFillPrice float64   `json:"avgFillPrice"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Source       string    `json:"source"`
	Message      string    `json:"message"`
}

type ordersResponse struct {
	Orders []orderDTO `json:"orders"`
}

// positionDTO is one paper position of the portfolio.
type positionDTO struct {
	Symbol       string  `json:"symbol"`
	Name         string  `json:"name"`
	Qty          int64   `json:"qty"`
	AvgPrice     float64 `json:"avgPrice"`
	Price        float64 `json:"price"`
	Value        float64 `json:"value"`
	DayChangePct float64 `json:"dayChangePct"`
	Profit       float64 `json:"profit"`
	ProfitPct    float64 `json:"profitPct"`
	RealizedPnL  float64 `json:"realizedPnl"`
}

// portfolioResponse is GET /api/portfolio and the SSE portfolio payload.
type portfolioResponse struct {
	TotalValue   float64       `json:"totalValue"`
	Cash         float64       `json:"cash"`
	DayChange    float64       `json:"dayChange"`
	DayChangePct float64       `json:"dayChangePct"`
	RealizedPnL  float64       `json:"realizedPnl"`
	Fees         float64       `json:"fees"`
	MarginPct    float64       `json:"marginPct"`
	Positions    []positionDTO `json:"positions"`
	UpdatedAt    time.Time     `json:"updatedAt"`
}

// tradeDTO is one executed paper trade (GET /api/trades).
type tradeDTO struct {
	ID      string    `json:"id"`
	OrderID string    `json:"orderId"`
	Symbol  string    `json:"symbol"`
	Side    string    `json:"side"`
	Qty     int64     `json:"qty"`
	Price   float64   `json:"price"`
	Fee     float64   `json:"fee"`
	Time    time.Time `json:"time"`
	Source  string    `json:"source"`
}

type tradesResponse struct {
	Trades []tradeDTO `json:"trades"`
}

// orderRequest is the POST /api/orders body.
type orderRequest struct {
	Symbol  string  `json:"symbol"`
	Side    string  `json:"side"`
	Qty     int64   `json:"qty"`
	Type    string  `json:"type"`
	Price   float64 `json:"price"`
	Confirm string  `json:"confirm"`
}

// --- handlers --------------------------------------------------------------

// handleHealth serves GET /api/health.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	st := s.feedStatus()
	ast := s.agentStatus()
	resp := healthResponse{
		OK:              true,
		Version:         s.version(),
		Mode:            string(st.Mode),
		Connected:       st.Connected,
		Message:         st.Message,
		ServerTime:      utc(st.ServerTime),
		ExchangeTZ:      st.ExchangeTZ,
		SeedGeneratedAt: utc(st.SeedGeneratedAt),
		LLM:             llmStatusDTO{Enabled: ast.LLM.Enabled, Model: ast.LLM.Model, Note: ast.LLM.Note},
		Trading:         tradingStatus{Paper: true, Live: s.cfg.LiveTradingAllowed},
	}
	if resp.SeedGeneratedAt.IsZero() {
		resp.SeedGeneratedAt = utc(s.cfg.SeedGeneratedAt)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleInstruments serves GET /api/instruments.
func (s *Server) handleInstruments(w http.ResponseWriter, r *http.Request) {
	instruments := s.deps.Feed.Instruments()
	resp := instrumentsResponse{
		ServerTime:  utc(s.feedStatus().ServerTime),
		Instruments: make([]instrumentDTO, 0, len(instruments)),
	}
	for _, inst := range instruments {
		resp.Instruments = append(resp.Instruments, instrumentToDTO(inst))
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCandles serves GET /api/instruments/{symbol}/candles.
func (s *Server) handleCandles(w http.ResponseWriter, r *http.Request) {
	symbol := symbolParam(r)
	if symbol == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент")
		return
	}
	periodSec, ok := intParam(r, "period", 300)
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректный период свечей")
		return
	}
	count, ok := intParam(r, "count", 500)
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректное количество свечей")
		return
	}
	ctx, cancel := requestContext(r, defaultRequestTimeout)
	defer cancel()
	history, err := s.deps.Feed.Candles(ctx, symbol, periodSec, count)
	if err != nil {
		s.mapError(w, err)
		return
	}
	resp := candlesResponse{
		Symbol:    history.Symbol,
		PeriodSec: history.PeriodSec,
		Source:    history.Source,
		Note:      history.Note,
		Candles:   make([]candleDTO, 0, len(history.Bars)),
	}
	for _, bar := range history.Bars {
		resp.Candles = append(resp.Candles, candleDTO{
			T: bar.Time.Unix(), O: bar.Open, H: bar.High, L: bar.Low, C: bar.Close, V: bar.Volume,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleOrderBook serves GET /api/instruments/{symbol}/orderbook.
func (s *Server) handleOrderBook(w http.ResponseWriter, r *http.Request) {
	symbol := symbolParam(r)
	if symbol == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент")
		return
	}
	ctx, cancel := requestContext(r, defaultRequestTimeout)
	defer cancel()
	book, err := s.deps.Feed.OrderBook(ctx, symbol)
	if err != nil {
		s.mapError(w, err)
		return
	}
	resp := orderBookResponse{
		Symbol: book.Symbol,
		Last:   book.Last,
		Time:   utc(book.Time),
		Bids:   make([]levelDTO, 0, len(book.Bids)),
		Asks:   make([]levelDTO, 0, len(book.Asks)),
		Source: book.Source,
	}
	for _, lvl := range book.Bids {
		resp.Bids = append(resp.Bids, levelDTO{Price: lvl.Price, Qty: lvl.Qty})
	}
	for _, lvl := range book.Asks {
		resp.Asks = append(resp.Asks, levelDTO{Price: lvl.Price, Qty: lvl.Qty})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleTrades serves GET /api/instruments/{symbol}/trades.
func (s *Server) handleTrades(w http.ResponseWriter, r *http.Request) {
	symbol := symbolParam(r)
	if symbol == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент")
		return
	}
	limit, ok := intParam(r, "limit", 50)
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректный лимит сделок")
		return
	}
	ctx, cancel := requestContext(r, defaultRequestTimeout)
	defer cancel()
	trades, err := s.deps.Feed.Trades(ctx, symbol, limit)
	if err != nil {
		s.mapError(w, err)
		return
	}
	resp := marketTradesResponse{Symbol: symbol, Trades: make([]marketTradeDTO, 0, len(trades))}
	for _, trade := range trades {
		resp.Trades = append(resp.Trades, marketTradeDTO{
			Time: utc(trade.Time), Price: trade.Price, Qty: trade.Qty, Side: trade.Side,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleHints serves GET /api/instruments/{symbol}/hints.
func (s *Server) handleHints(w http.ResponseWriter, r *http.Request) {
	if s.deps.Agent == nil {
		writeUnavailable(w, "AI-агент недоступен")
		return
	}
	symbol := symbolParam(r)
	if symbol == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент")
		return
	}
	periodSec, ok := intParam(r, "period", 300)
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректный период свечей")
		return
	}
	ctx, cancel := requestContext(r, agentRequestTimeout)
	defer cancel()
	hints, err := s.deps.Agent.Hints(ctx, symbol, periodSec)
	if err != nil {
		s.mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hints)
}

// handlePortfolio serves GET /api/portfolio.
func (s *Server) handlePortfolio(w http.ResponseWriter, r *http.Request) {
	portfolio, ok := s.portfolio()
	if !ok {
		writeUnavailable(w, "бумажный счёт недоступен")
		return
	}
	writeJSON(w, http.StatusOK, portfolio)
}

// handleOrders serves GET /api/orders.
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.paperSnapshot()
	if !ok {
		writeUnavailable(w, "бумажный счёт недоступен")
		return
	}
	resp := ordersResponse{Orders: make([]orderDTO, 0, len(snapshot.Orders))}
	for _, order := range snapshot.Orders {
		resp.Orders = append(resp.Orders, orderToDTO(order))
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleTradeHistory serves GET /api/trades.
func (s *Server) handleTradeHistory(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.paperSnapshot()
	if !ok {
		writeUnavailable(w, "бумажный счёт недоступен")
		return
	}
	resp := tradesResponse{Trades: make([]tradeDTO, 0, len(snapshot.Trades))}
	for _, trade := range snapshot.Trades {
		resp.Trades = append(resp.Trades, tradeToDTO(trade))
	}
	writeJSON(w, http.StatusOK, resp)
}

// handlePlaceOrder serves POST /api/orders: paper orders, plus the live-trading
// gate of §10.4.
func (s *Server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	var req orderRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.checkLiveOrderRequest(w, req.Confirm) {
		return
	}
	if s.deps.Paper == nil {
		writeUnavailable(w, "бумажный счёт недоступен")
		return
	}
	orderReq := paper.OrderRequest{
		Symbol: strings.TrimSpace(req.Symbol),
		Side:   paper.Side(strings.TrimSpace(req.Side)),
		Qty:    req.Qty,
		Type:   paper.OrderType(strings.TrimSpace(req.Type)),
		Price:  req.Price,
		Source: "ui",
	}
	if orderReq.Type == "" {
		orderReq.Type = paper.Market
	}
	order, err := s.deps.Paper.Place(orderReq, s.quoteView(orderReq.Symbol))
	if err != nil {
		// The rejected order is still part of the order history; refresh the
		// SSE state so the UI shows it and the rejection message.
		s.publishPaperState()
		s.mapError(w, err)
		return
	}
	s.publishPaperState()
	writeJSON(w, http.StatusCreated, map[string]any{"order": orderToDTO(order)})
}

// handleCancelOrder serves DELETE /api/orders/{id}.
func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	if s.deps.Paper == nil {
		writeUnavailable(w, "бумажный счёт недоступен")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указана заявка")
		return
	}
	if _, err := s.deps.Paper.Cancel(id); err != nil {
		s.mapError(w, err)
		return
	}
	s.publishPaperState()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleStrategies serves GET /api/backtest/strategies.
func (s *Server) handleStrategies(w http.ResponseWriter, r *http.Request) {
	strategies := backtest.Strategies()
	if strategies == nil {
		strategies = []backtest.StrategyInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"strategies": strategies})
}

// backtestRequest is the POST /api/backtest body.
type backtestRequest struct {
	Symbol      string             `json:"symbol"`
	PeriodSec   int                `json:"periodSec"`
	Strategy    string             `json:"strategy"`
	Params      map[string]float64 `json:"params"`
	InitialCash float64            `json:"initialCash"`
	FeeBps      float64            `json:"feeBps"`
	SlippageBps float64            `json:"slippageBps"`
	Count       int                `json:"count"`
}

// handleBacktest serves POST /api/backtest.
func (s *Server) handleBacktest(w http.ResponseWriter, r *http.Request) {
	var req backtestRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Symbol = strings.TrimSpace(req.Symbol)
	req.Strategy = strings.TrimSpace(req.Strategy)
	if req.Symbol == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент")
		return
	}
	if req.Strategy == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указана стратегия")
		return
	}
	periodSec := req.PeriodSec
	if periodSec == 0 {
		periodSec = 300
	}
	if !feed.ValidPeriod(periodSec) {
		writeError(w, http.StatusBadRequest, codeBadRequest, "неподдерживаемый период свечей")
		return
	}
	count := req.Count
	if count <= 0 {
		count = 500
	}
	if count > 5000 {
		count = 5000
	}
	ctx, cancel := requestContext(r, backtestRequestTimeout)
	defer cancel()
	history, err := s.deps.Feed.Candles(ctx, req.Symbol, periodSec, count)
	if err != nil {
		s.mapError(w, err)
		return
	}
	bars := make([]backtest.Bar, 0, len(history.Bars))
	for _, bar := range history.Bars {
		bars = append(bars, backtest.Bar{
			Time: bar.Time, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume,
		})
	}
	run := s.deps.Backtest
	if run == nil {
		run = backtest.Run
	}
	result, err := run(backtest.Request{
		Symbol:      req.Symbol,
		PeriodSec:   periodSec,
		Bars:        bars,
		Strategy:    req.Strategy,
		Params:      req.Params,
		InitialCash: req.InitialCash,
		FeeBps:      req.FeeBps,
		SlippageBps: req.SlippageBps,
	})
	if err != nil {
		s.mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

// handleAgentTools serves GET /api/agent/tools.
func (s *Server) handleAgentTools(w http.ResponseWriter, r *http.Request) {
	if s.deps.Agent == nil {
		writeUnavailable(w, "AI-агент недоступен")
		return
	}
	tools := s.deps.Agent.Tools()
	if tools == nil {
		tools = []agent.ToolInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

// handleAgentChat serves POST /api/agent/chat.
func (s *Server) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	if s.deps.Agent == nil {
		writeUnavailable(w, "AI-агент недоступен")
		return
	}
	var req agent.ChatRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "пустой запрос к AI-агенту")
		return
	}
	ctx, cancel := requestContext(r, agentRequestTimeout)
	defer cancel()
	resp, err := s.deps.Agent.Chat(ctx, req)
	if err != nil {
		s.mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAgentStatus serves GET /api/agent/status.
func (s *Server) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.Agent == nil {
		writeUnavailable(w, "AI-агент недоступен")
		return
	}
	status := s.deps.Agent.Status()
	status.Autonomy = stableAutonomy(status.Autonomy)
	writeJSON(w, http.StatusOK, status)
}

// autonomyRequest is the POST /api/agent/autonomy body.
type autonomyRequest struct {
	Action string                `json:"action"`
	Config *agent.AutonomyConfig `json:"config"`
}

// autonomyAlreadyRunning is the user-facing conflict text of a duplicate start.
const autonomyAlreadyRunning = "автономия уже запущена"

// handleAutonomy serves POST /api/agent/autonomy.
func (s *Server) handleAutonomy(w http.ResponseWriter, r *http.Request) {
	if s.deps.Agent == nil {
		writeUnavailable(w, "AI-агент недоступен")
		return
	}
	var req autonomyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	switch strings.TrimSpace(req.Action) {
	case "start":
		cfg := agent.AutonomyConfig{}
		if req.Config != nil {
			cfg = *req.Config
		}
		if cfg.Live {
			writeError(w, http.StatusBadRequest, codeBadRequest,
				"live-автономия в итерации 1 не реализована; доступен только бумажный счёт")
			return
		}
		// A duplicate start is a state conflict, not an internal error: check
		// the engine state first and keep the error mapping below as a guard
		// for the start/check race.
		if s.deps.Agent.Status().Autonomy.Running {
			writeError(w, http.StatusConflict, codeConflict, autonomyAlreadyRunning)
			return
		}
		ctx, cancel := requestContext(r, agentRequestTimeout)
		defer cancel()
		if err := s.deps.Agent.StartAutonomy(ctx, cfg); err != nil {
			s.mapAutonomyError(w, err)
			return
		}
	case "stop":
		if err := s.deps.Agent.StopAutonomy(); err != nil {
			s.mapAutonomyError(w, err)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, codeBadRequest, "неизвестное действие автономии: "+req.Action)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"autonomy": s.autonomyResponse()})
}

// autonomyResponse returns the autonomy status after a state transition with
// non-zero startedAt/lastActionAt: the frozen contract renders both as
// timestamps, and a zero time shows up as year 0001 in the UI. The engine sets
// them synchronously; this is a defensive normalization for older engines.
func (s *Server) autonomyResponse() agent.AutonomyStatus {
	status := stableAutonomy(s.deps.Agent.Status().Autonomy)
	now := time.Now().UTC()
	if status.StartedAt.IsZero() {
		status.StartedAt = now
	}
	if status.LastActionAt.IsZero() {
		status.LastActionAt = now
	}
	return status
}

// stableAutonomy renders the autonomy timestamps for the API: a missing
// lastActionAt of a running runner falls back to startedAt (the UI never shows
// a zero "last action" for an active run), and every timestamp is normalized to
// RFC3339 UTC as §10.2 requires. A never-started status keeps its zero times.
func stableAutonomy(status agent.AutonomyStatus) agent.AutonomyStatus {
	if status.Running && status.LastActionAt.IsZero() && !status.StartedAt.IsZero() {
		status.LastActionAt = status.StartedAt
	}
	if !status.StartedAt.IsZero() {
		status.StartedAt = status.StartedAt.UTC()
	}
	if !status.LastActionAt.IsZero() {
		status.LastActionAt = status.LastActionAt.UTC()
	}
	if len(status.Log) > 0 {
		log := make([]agent.LogEntry, len(status.Log))
		copy(log, status.Log)
		for i := range log {
			if !log[i].Time.IsZero() {
				log[i].Time = log[i].Time.UTC()
			}
		}
		status.Log = log
	}
	return status
}

// mapAutonomyError maps agent StartAutonomy/StopAutonomy failures to the API
// codes of §10: a duplicate start is a conflict, a bad configuration is a bad
// request, an unimplemented feature is unavailable, everything else is
// internal.
func (s *Server) mapAutonomyError(w http.ResponseWriter, err error) {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "already running"), strings.Contains(message, "уже запущена"):
		writeError(w, http.StatusConflict, codeConflict, autonomyAlreadyRunning)
	case isNotImplementedError(err):
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "функция пока не реализована")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "превышено время ожидания ответа")
	case isAutonomyConfigError(err):
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректные параметры автономии: "+err.Error())
	default:
		writeInternal(w)
	}
}

// isAutonomyConfigError reports whether err is a validation problem of the
// autonomy configuration (as opposed to a runtime failure).
func isAutonomyConfigError(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"is required", "not enough history", "unknown strategy",
		"agent: strategy", "agent: autonomy symbol", "live autonomy",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// isNotImplementedError reports whether err marks an unimplemented feature.
// The package sentinels of the contract scaffold (agent/backtest
// ErrNotImplemented) were replaced by the implementations, so the check is
// textual and defensive.
func isNotImplementedError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not implemented")
}

// handleAutonomyLog serves GET /api/agent/autonomy/log.
func (s *Server) handleAutonomyLog(w http.ResponseWriter, r *http.Request) {
	if s.deps.Agent == nil {
		writeUnavailable(w, "AI-агент недоступен")
		return
	}
	limit, ok := intParam(r, "limit", 200)
	if !ok || limit < 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректный лимит лога")
		return
	}
	entries := s.deps.Agent.Status().Autonomy.Log
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	if entries == nil {
		entries = []agent.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"log": entries})
}

// handleAgentBacktests serves GET /api/agent/backtests.
func (s *Server) handleAgentBacktests(w http.ResponseWriter, r *http.Request) {
	if s.deps.Agent == nil {
		writeUnavailable(w, "AI-агент недоступен")
		return
	}
	results := s.deps.Agent.Backtests()
	if results == nil {
		results = []backtest.Result{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// --- paper helpers ---------------------------------------------------------

// paperSnapshot returns the paper account snapshot.
func (s *Server) paperSnapshot() (paper.Snapshot, bool) {
	if s.deps.Paper == nil {
		return paper.Snapshot{}, false
	}
	return s.deps.Paper.Snapshot(), true
}

// quoteView builds the paper broker quote from the feed, with the instrument
// last price as a fallback.
func (s *Server) quoteView(symbol string) paper.QuoteView {
	view := paper.QuoteView{Symbol: symbol}
	if quote, ok := s.deps.Feed.Quote(symbol); ok {
		view.Last, view.Bid, view.Ask, view.Time = quote.Last, quote.Bid, quote.Ask, quote.Time
	}
	if view.Last == 0 {
		if inst, ok := s.deps.Feed.Instrument(symbol); ok {
			view.Last, view.Bid, view.Ask = inst.Last, inst.Bid, inst.Ask
			view.Time = inst.UpdatedAt
		}
	}
	return view
}

// quoteEventData is the SSE quote payload (§10.3).
type quoteEventData struct {
	Symbol    string    `json:"symbol"`
	Last      float64   `json:"last"`
	Change    float64   `json:"change"`
	ChangePct float64   `json:"changePct"`
	Bid       float64   `json:"bid"`
	Ask       float64   `json:"ask"`
	Volume    int64     `json:"volume"`
	Time      time.Time `json:"time"`
}

// statusEventData is the SSE status payload (§10.3).
type statusEventData struct {
	OK         bool      `json:"ok"`
	Mode       string    `json:"mode"`
	Connected  bool      `json:"connected"`
	Message    string    `json:"message"`
	ServerTime time.Time `json:"serverTime"`
}

// portfolio builds the portfolio response: paper account marked to the latest
// market quotes.
func (s *Server) portfolio() (portfolioResponse, bool) {
	snapshot, ok := s.paperSnapshot()
	if !ok {
		return portfolioResponse{}, false
	}
	resp := portfolioResponse{
		Cash:        snapshot.Cash,
		RealizedPnL: snapshot.RealizedPnL,
		Fees:        snapshot.Fees,
		MarginPct:   100,
		Positions:   make([]positionDTO, 0, len(snapshot.Positions)),
		UpdatedAt:   utc(snapshot.UpdatedAt),
	}
	total := snapshot.Cash
	var dayChange float64
	for _, pos := range snapshot.Positions {
		item := positionDTO{
			Symbol:      pos.Symbol,
			Qty:         pos.Qty,
			AvgPrice:    pos.AvgPrice,
			Price:       pos.AvgPrice,
			RealizedPnL: pos.RealizedPnL,
		}
		if inst, ok := s.deps.Feed.Instrument(pos.Symbol); ok {
			item.Name = inst.Name
		}
		if quote, ok := s.deps.Feed.Quote(pos.Symbol); ok && quote.Last > 0 {
			item.Price = quote.Last
			item.DayChangePct = quote.ChangePct
		}
		item.Value = item.Price * float64(pos.Qty)
		item.Profit = (item.Price - pos.AvgPrice) * float64(pos.Qty)
		if pos.AvgPrice > 0 {
			item.ProfitPct = (item.Price/pos.AvgPrice - 1) * 100
		}
		total += item.Value
		dayChange += item.Value * item.DayChangePct / 100
		resp.Positions = append(resp.Positions, item)
	}
	resp.TotalValue = total
	resp.DayChange = dayChange
	if base := total - dayChange; base != 0 {
		resp.DayChangePct = dayChange / base * 100
	}
	return resp, true
}

// --- DTO mappers -----------------------------------------------------------

func instrumentToDTO(inst feed.Instrument) instrumentDTO {
	spark := inst.Spark
	if spark == nil {
		spark = []float64{}
	}
	return instrumentDTO{
		Symbol:    inst.Symbol,
		Board:     inst.Board,
		SecCode:   inst.SecCode,
		Name:      inst.Name,
		ShortName: inst.ShortName,
		Currency:  inst.Currency,
		Decimals:  inst.Decimals,
		LotSize:   inst.LotSize,
		MinStep:   inst.MinStep,
		PrevClose: inst.PrevClose,
		Last:      inst.Last,
		Change:    inst.Change,
		ChangePct: inst.ChangePct,
		Open:      inst.Open,
		High:      inst.High,
		Low:       inst.Low,
		Volume:    inst.Volume,
		Bid:       inst.Bid,
		Ask:       inst.Ask,
		Spark:     spark,
		UpdatedAt: utc(inst.UpdatedAt),
	}
}

func orderToDTO(order paper.Order) orderDTO {
	return orderDTO{
		ID:           order.ID,
		Symbol:       order.Symbol,
		Side:         string(order.Side),
		Type:         string(order.Type),
		Qty:          order.Qty,
		Filled:       order.Filled,
		Price:        order.Price,
		AvgFillPrice: order.AvgFillPrice,
		Status:       string(order.Status),
		CreatedAt:    utc(order.CreatedAt),
		UpdatedAt:    utc(order.UpdatedAt),
		Source:       order.Source,
		Message:      order.Message,
	}
}

func tradeToDTO(trade paper.Trade) tradeDTO {
	return tradeDTO{
		ID:      trade.ID,
		OrderID: trade.OrderID,
		Symbol:  trade.Symbol,
		Side:    string(trade.Side),
		Qty:     trade.Qty,
		Price:   trade.Price,
		Fee:     trade.Fee,
		Time:    utc(trade.Time),
		Source:  trade.Source,
	}
}

// mkQuoteEventData maps a feed quote to the SSE payload.
func mkQuoteEventData(quote feed.Quote) quoteEventData {
	return quoteEventData{
		Symbol:    quote.Symbol,
		Last:      quote.Last,
		Change:    quote.Change,
		ChangePct: quote.ChangePct,
		Bid:       quote.Bid,
		Ask:       quote.Ask,
		Volume:    quote.Volume,
		Time:      utc(quote.Time),
	}
}
