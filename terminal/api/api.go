// Package api is the HTTP/JSON/SSE surface of the AI terminal: REST endpoints
// for market data, the paper portfolio, backtests and the AI agent, plus the
// /api/events server-sent event stream and the embedded SPA. The handlers talk
// to interfaces (FeedAPI, PaperAPI, AgentAPI) so they are testable with fakes;
// cmd/terminal wires the real feed/paper/agent packages in.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// Config configures the HTTP server.
type Config struct {
	Version            string
	LiveTradingAllowed bool // -live-trading && TERMINAL_LIVE_TRADING=1
	PaperInitialCash   float64
	SeedGeneratedAt    time.Time
	Web                fs.FS // SPA root; nil disables static files
}

// FeedAPI is the market-data interface the handlers use (implemented by
// *feed.Service).
type FeedAPI interface {
	Status() feed.Status
	Instruments() []feed.Instrument
	Instrument(symbol string) (feed.Instrument, bool)
	Quote(symbol string) (feed.Quote, bool)
	Candles(ctx context.Context, symbol string, periodSec, count int) (feed.History, error)
	OrderBook(ctx context.Context, symbol string) (feed.OrderBook, error)
	Trades(ctx context.Context, symbol string, limit int) ([]feed.Trade, error)
	Subscribe(fn func(feed.Event)) (cancel func())
}

// PaperAPI is the paper-broker interface the handlers use (implemented by
// *paper.Broker).
type PaperAPI interface {
	Place(req paper.OrderRequest, q paper.QuoteView) (paper.Order, error)
	Cancel(id string) (paper.Order, error)
	OnQuote(q paper.QuoteView) []paper.Event
	Snapshot() paper.Snapshot
}

// AgentAPI is the AI-agent interface the handlers use (implemented by
// *agent.Engine).
type AgentAPI interface {
	Chat(ctx context.Context, req agent.ChatRequest) (agent.ChatResponse, error)
	Status() agent.Status
	StartAutonomy(ctx context.Context, cfg agent.AutonomyConfig) error
	StopAutonomy() error
	Subscribe(fn func(agent.Event)) (cancel func())
	Backtests() []backtest.Result
	Hints(ctx context.Context, symbol string, periodSec int) (agent.MarketHints, error)
	Tools() []agent.ToolInfo
}

// Deps are the backend dependencies of the HTTP layer.
type Deps struct {
	Feed     FeedAPI
	Paper    PaperAPI
	Agent    AgentAPI
	Backtest func(backtest.Request) (backtest.Result, error) // HTTP backtest runner
}

// Server is the HTTP API server. It owns the SSE hub and the background event
// loop; create it with New and stop it with Close.
type Server struct {
	cfg  Config
	deps Deps
	hub  *hub
	mux  *http.ServeMux

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	startOnce sync.Once

	// Event-loop intervals, overridable in tests before New returns.
	quoteInterval     time.Duration
	statusInterval    time.Duration
	heartbeatInterval time.Duration
}

// Event-loop defaults (docs/ai-terminal/ARCHITECTURE.md §10.3).
const (
	defaultQuoteInterval     = 1 * time.Second
	defaultStatusInterval    = 5 * time.Second
	defaultHeartbeatInterval = 15 * time.Second
	// hubBuffer is the per-client SSE queue size; a client that cannot keep up
	// is dropped instead of blocking the feed.
	hubBuffer = 256
)

// Request timeouts per endpoint class.
const (
	defaultRequestTimeout  = 15 * time.Second
	backtestRequestTimeout = 60 * time.Second
	agentRequestTimeout    = 120 * time.Second
	maxBodyBytes           = 1 << 20
)

// Error codes of the API (docs/ai-terminal/ARCHITECTURE.md §10).
const (
	codeBadRequest          = "bad_request"
	codeNotFound            = "not_found"
	codeConflict            = "conflict"
	codeUnavailable         = "unavailable"
	codeLiveTradingDisabled = "live_trading_disabled"
	codeNotImplemented      = "not_implemented"
	codeInternal            = "internal"
)

// New builds the API server and starts its background event loop.
func New(cfg Config, deps Deps) (*Server, error) {
	if deps.Feed == nil {
		return nil, errors.New("api: не передан источник рыночных данных (feed)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:               cfg,
		deps:              deps,
		hub:               newHub(),
		ctx:               ctx,
		cancel:            cancel,
		quoteInterval:     defaultQuoteInterval,
		statusInterval:    defaultStatusInterval,
		heartbeatInterval: defaultHeartbeatInterval,
	}
	s.mux = s.routes()
	return s, nil
}

// Close stops the event loop and drops every SSE client. It is idempotent.
func (s *Server) Close() {
	s.cancel()
	s.wg.Wait()
	s.hub.closeAll()
}

// Handler returns the HTTP handler of the API (REST + SSE + SPA). The first
// call starts the background event loop, so tests may adjust the publishing
// intervals before serving.
func (s *Server) Handler() http.Handler {
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.eventLoop()
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(w, http.StatusInternalServerError, codeInternal, "внутренняя ошибка сервера")
			}
		}()
		s.mux.ServeHTTP(w, r)
	})
}

// routes registers every endpoint of the frozen contract (§10.1).
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/instruments", s.handleInstruments)
	mux.HandleFunc("GET /api/instruments/{symbol}/candles", s.handleCandles)
	mux.HandleFunc("GET /api/instruments/{symbol}/orderbook", s.handleOrderBook)
	mux.HandleFunc("GET /api/instruments/{symbol}/trades", s.handleTrades)
	mux.HandleFunc("GET /api/instruments/{symbol}/hints", s.handleHints)
	mux.HandleFunc("GET /api/portfolio", s.handlePortfolio)
	mux.HandleFunc("GET /api/orders", s.handleOrders)
	mux.HandleFunc("GET /api/trades", s.handleTradeHistory)
	mux.HandleFunc("POST /api/orders", s.handlePlaceOrder)
	mux.HandleFunc("DELETE /api/orders/{id}", s.handleCancelOrder)
	mux.HandleFunc("GET /api/backtest/strategies", s.handleStrategies)
	mux.HandleFunc("POST /api/backtest", s.handleBacktest)
	mux.HandleFunc("GET /api/agent/tools", s.handleAgentTools)
	mux.HandleFunc("POST /api/agent/chat", s.handleAgentChat)
	mux.HandleFunc("GET /api/agent/status", s.handleAgentStatus)
	mux.HandleFunc("POST /api/agent/autonomy", s.handleAutonomy)
	mux.HandleFunc("GET /api/agent/autonomy/log", s.handleAutonomyLog)
	mux.HandleFunc("GET /api/agent/backtests", s.handleAgentBacktests)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("/api/", s.handleAPINotFound)
	mux.HandleFunc("/", s.handleSPA)
	return mux
}

// handleAPINotFound answers unknown /api routes with the JSON error envelope.
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, codeNotFound, "метод API не найден: "+r.URL.Path)
}

// --- small helpers ---------------------------------------------------------

// utc normalizes a timestamp for JSON (RFC3339 UTC).
func utc(t time.Time) time.Time { return t.UTC() }

// writeJSON writes one JSON response with the frozen content type.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// apiError is the JSON error envelope.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeError writes one JSON error envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	var body apiError
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}

// writeInternal writes the generic 500 error.
func writeInternal(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, codeInternal, "внутренняя ошибка сервера")
}

// writeUnavailable writes a 503 with a Russian explanation.
func writeUnavailable(w http.ResponseWriter, message string) {
	writeError(w, http.StatusServiceUnavailable, codeUnavailable, message)
}

// decodeBody decodes a bounded JSON request body into dst.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректный JSON в запросе: "+err.Error())
		return false
	}
	return true
}

// mapError converts a backend error into the matching JSON error response.
func (s *Server) mapError(w http.ResponseWriter, err error) {
	var feedErr *feed.Error
	var reject *paper.RejectError
	switch {
	case errors.As(err, &feedErr):
		switch feedErr.Code {
		case feed.CodeBadPeriod:
			writeError(w, http.StatusBadRequest, codeBadRequest, feedErr.Message)
		case feed.CodeNotFound:
			writeError(w, http.StatusNotFound, codeNotFound, feedErr.Message)
		case feed.CodeUnavailable:
			writeError(w, http.StatusServiceUnavailable, codeUnavailable, feedErr.Message)
		default:
			writeError(w, http.StatusInternalServerError, codeInternal, feedErr.Message)
		}
	case errors.As(err, &reject):
		writeError(w, http.StatusBadRequest, codeBadRequest, reject.Error())
	case errors.Is(err, paper.ErrOrderNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, paper.ErrOrderNotCancellable):
		writeError(w, http.StatusConflict, codeConflict, err.Error())
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "превышено время ожидания ответа")
	default:
		writeError(w, http.StatusInternalServerError, codeInternal, "внутренняя ошибка сервера")
	}
}

// requestContext returns a bounded context for one non-SSE request.
func requestContext(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), timeout)
}

// symbolParam returns the URL symbol path value (Go decodes %3A to ':').
func symbolParam(r *http.Request) string {
	symbol := strings.TrimSpace(r.PathValue("symbol"))
	if symbol == "" {
		return ""
	}
	return symbol
}

// intParam parses an optional integer query parameter with a default; ok is
// false when the value is present but malformed.
func intParam(r *http.Request, name string, def int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	value := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
		value = value*10 + int(raw[i]-'0')
		if value > 1_000_000 {
			return 0, false
		}
	}
	return value, true
}

// feedStatus returns the feed status or a zero value when the feed is absent.
func (s *Server) feedStatus() feed.Status {
	if s.deps.Feed == nil {
		return feed.Status{}
	}
	return s.deps.Feed.Status()
}

// agentStatus returns the agent status or a zero value when the agent is absent.
func (s *Server) agentStatus() agent.Status {
	if s.deps.Agent == nil {
		return agent.Status{}
	}
	return s.deps.Agent.Status()
}

// --- static SPA ------------------------------------------------------------

// handleSPA serves the embedded single-page application with an index.html
// fallback for client-side routes.
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, codeBadRequest, "метод не поддерживается")
		return
	}
	if s.cfg.Web == nil {
		writeError(w, http.StatusNotFound, codeNotFound, "интерфейс не встроен в сборку")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == "." {
		s.serveIndex(w, r)
		return
	}
	if info, err := fs.Stat(s.cfg.Web, name); err == nil && !info.IsDir() {
		if strings.HasSuffix(name, ".html") {
			w.Header().Set("Cache-Control", "no-store")
		}
		http.ServeFileFS(w, r, s.cfg.Web, name)
		return
	}
	// Client-side route without a file: fall back to index.html, but keep
	// missing assets a JSON 404.
	if path.Ext(name) == "" {
		s.serveIndex(w, r)
		return
	}
	writeError(w, http.StatusNotFound, codeNotFound, "файл не найден: "+name)
}

// serveIndex serves the SPA entry point.
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	if _, err := fs.Stat(s.cfg.Web, "index.html"); err != nil {
		writeError(w, http.StatusNotFound, codeNotFound, "интерфейс не найден в сборке")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFileFS(w, r, s.cfg.Web, "index.html")
}

// version returns the effective build version.
func (s *Server) version() string {
	if s.cfg.Version == "" {
		return "dev"
	}
	return s.cfg.Version
}

// checkLiveOrderRequest enforces the live-trading gate of §10.4: real orders
// need both the server flag and the explicit "LIVE" confirmation, and the
// exchange routing itself is not implemented in iteration 1.
func (s *Server) checkLiveOrderRequest(w http.ResponseWriter, confirm string) bool {
	if confirm != "LIVE" {
		return true
	}
	if !s.cfg.LiveTradingAllowed {
		writeError(w, http.StatusForbidden, codeLiveTradingDisabled,
			"реальная торговля выключена: включите флаг -live-trading и TERMINAL_LIVE_TRADING=1")
		return false
	}
	writeError(w, http.StatusNotImplemented, codeNotImplemented,
		"реальная маршрутизация заявок на биржу в итерации 1 не реализована; доступен только бумажный счёт")
	return false
}
