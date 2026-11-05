package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// Defaults frozen by docs/ai-terminal/ARCHITECTURE.md §9.
const (
	defaultLLMModel      = "deepseek-chat"
	defaultLLMTimeout    = 90 * time.Second
	defaultPeriodSec     = 300
	defaultFeeBps        = 5.0
	defaultBacktestCash  = 1_000_000.0
	defaultHistoryLimit  = 20
	defaultAutonomyLimit = 200_000.0
	defaultAutonomySpeed = 5.0
	maxHistoryBars       = 600
	maxBacktestBars      = 5000
	autonomyBars         = 500
	autonomyLogLimit     = 500
	maxToolIterations    = 8
)

// supportedPeriods are the candle periods accepted by the terminal.
var supportedPeriods = map[int]bool{60: true, 300: true, 900: true, 3600: true, 86400: true}

// Engine is the in-process AI agent.
type Engine struct {
	cfg    Config
	data   DataSource
	broker Broker

	// conversations keeps the bounded history of the chat dialogs (§9.2).
	conversations ConversationStore

	// Seams over the frozen backtest package: production wiring points them at
	// the real functions, package tests replace them with deterministic fakes.
	backtestRun   func(backtest.Request) (backtest.Result, error)
	newStrategy   func(string, map[string]float64) (backtest.Strategy, error)
	strategyInfos func() []backtest.StrategyInfo
	stepDelay     func(speed float64) time.Duration
	llmHTTPClient *http.Client

	mu        sync.Mutex
	subs      map[int]func(Event)
	nextSubID int
	backtests []backtest.Result
	tools     []ToolInfo
	autonomy  *autonomyRun
}

// New builds the agent engine. The data source is required; the paper broker
// may be nil when only market analytics are used.
func New(cfg Config, data DataSource, broker Broker) (*Engine, error) {
	if data == nil {
		return nil, errors.New("agent: data source is required")
	}
	cfg = withDefaults(cfg)
	e := &Engine{
		cfg:           cfg,
		data:          data,
		broker:        broker,
		conversations: newMemoryConversations(defaultConversationTurns, defaultConversationBytes),
		llmHTTPClient: &http.Client{Timeout: cfg.LLM.Timeout},
		backtestRun:   backtest.Run,
		newStrategy:   backtest.NewStrategy,
		strategyInfos: backtest.Strategies,
		stepDelay: func(speed float64) time.Duration {
			if speed <= 0 {
				return 0
			}
			d := time.Duration(float64(time.Second) / speed)
			if d <= 0 {
				d = time.Millisecond
			}
			return d
		},
		subs: make(map[int]func(Event)),
	}
	e.tools = buildTools()
	return e, nil
}

// withDefaults fills every unset Config/RiskLimits field with the frozen
// contract default.
func withDefaults(cfg Config) Config {
	risk := cfg.Risk
	if risk.MaxPositionPct <= 0 {
		risk.MaxPositionPct = 25
	}
	if risk.MaxOrderPct <= 0 {
		risk.MaxOrderPct = 20
	}
	if risk.StopLossPct <= 0 {
		risk.StopLossPct = 2
	}
	if risk.TakeProfitPct <= 0 {
		risk.TakeProfitPct = 5
	}
	if risk.DailyLossPct <= 0 {
		risk.DailyLossPct = 3
	}
	if risk.MaxTradesPerHour <= 0 {
		risk.MaxTradesPerHour = 30
	}
	cfg.Risk = risk

	if cfg.DefaultPeriodSec <= 0 {
		cfg.DefaultPeriodSec = defaultPeriodSec
	}
	if !supportedPeriods[cfg.DefaultPeriodSec] {
		cfg.DefaultPeriodSec = defaultPeriodSec
	}
	if cfg.FeeBps < 0 {
		cfg.FeeBps = 0
	}
	if cfg.FeeBps == 0 {
		cfg.FeeBps = defaultFeeBps
	}
	if cfg.BacktestHistory <= 0 {
		cfg.BacktestHistory = defaultHistoryLimit
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	llm := cfg.LLM
	if llm.Timeout <= 0 {
		llm.Timeout = defaultLLMTimeout
	}
	if llm.Model == "" {
		llm.Model = defaultLLMModel
	}
	llm.BaseURL = strings.TrimRight(strings.TrimSpace(llm.BaseURL), "/")
	// Enabled is computed, not caller-supplied (contract §9.2): without both a
	// base URL and a key the deterministic local analyst is used.
	llm.Enabled = llm.BaseURL != "" && llm.APIKey != ""
	cfg.LLM = llm
	return cfg
}

// now returns the engine clock (injectable in tests).
func (e *Engine) now() time.Time { return e.cfg.Now() }

// Status returns the autonomy and LLM status.
func (e *Engine) Status() Status {
	st := Status{LLM: e.llmStatus()}
	e.mu.Lock()
	run := e.autonomy
	e.mu.Unlock()
	if run != nil {
		st.Autonomy = run.snapshot()
	} else {
		st.Autonomy = AutonomyStatus{Log: []LogEntry{}}
	}
	return st
}

func (e *Engine) llmStatus() LLMStatus {
	if e.cfg.LLM.Enabled {
		return LLMStatus{Enabled: true, Model: e.cfg.LLM.Model, Note: "OpenAI-compatible LLM"}
	}
	return LLMStatus{Enabled: false, Model: e.cfg.LLM.Model, Note: "локальный аналитик (LLM не настроен)"}
}

// Backtests returns the recent backtest results kept for the UI.
func (e *Engine) Backtests() []backtest.Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]backtest.Result, len(e.backtests))
	copy(out, e.backtests)
	return out
}

// recordBacktest keeps the last cfg.BacktestHistory results.
func (e *Engine) recordBacktest(res backtest.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.backtests = append(e.backtests, res)
	if lim := e.cfg.BacktestHistory; len(e.backtests) > lim {
		e.backtests = append([]backtest.Result(nil), e.backtests[len(e.backtests)-lim:]...)
	}
}

// Subscribe registers an agent event sink and returns its cancel function.
func (e *Engine) Subscribe(fn func(Event)) (cancel func()) {
	if fn == nil {
		return func() {}
	}
	e.mu.Lock()
	id := e.nextSubID
	e.nextSubID++
	e.subs[id] = fn
	e.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			delete(e.subs, id)
			e.mu.Unlock()
		})
	}
}

// publish fans an event out to every subscriber. A panicking subscriber must
// not take the engine down, a slow one must not block tool execution: sinks
// are expected to be non-blocking (the API hub buffers them).
func (e *Engine) publish(ev Event) {
	e.mu.Lock()
	sinks := make([]func(Event), 0, len(e.subs))
	for _, fn := range e.subs {
		sinks = append(sinks, fn)
	}
	e.mu.Unlock()
	for _, fn := range sinks {
		func() {
			defer func() { _ = recover() }()
			fn(ev)
		}()
	}
}

// resolveSymbol normalizes a tool argument: empty falls back to the configured
// default, a bare ticker is matched against the universe and expanded to
// "BOARD:SECCODE" when known.
func (e *Engine) resolveSymbol(symbol string) string {
	s := strings.TrimSpace(symbol)
	if s == "" {
		return e.cfg.DefaultSymbol
	}
	if strings.Contains(s, ":") {
		return s
	}
	up := strings.ToUpper(s)
	for _, ins := range e.data.Instruments() {
		if strings.EqualFold(ins.Symbol, up) || strings.EqualFold(secCode(ins.Symbol), up) {
			return ins.Symbol
		}
	}
	return up
}

// defaultSymbol resolves the empty-symbol fallback using the universe.
func (e *Engine) defaultSymbol() string {
	if e.cfg.DefaultSymbol != "" {
		return e.resolveSymbol(e.cfg.DefaultSymbol)
	}
	for _, ins := range e.data.Instruments() {
		if ins.Symbol != "" {
			return ins.Symbol
		}
	}
	return ""
}

// normalizePeriod validates a candle period and falls back to the configured
// default when it is missing or unsupported.
func (e *Engine) normalizePeriod(periodSec int) int {
	if supportedPeriods[periodSec] {
		return periodSec
	}
	return e.cfg.DefaultPeriodSec
}

// feeBps is the effective paper-trading commission.
func (e *Engine) feeBps() float64 {
	if e.cfg.FeeBps > 0 {
		return e.cfg.FeeBps
	}
	return defaultFeeBps
}

// strategyCatalog returns the backtest strategy catalog (test seam).
func (e *Engine) strategyCatalog() []backtest.StrategyInfo {
	if e.strategyInfos == nil {
		return backtest.Strategies()
	}
	return e.strategyInfos()
}

// runBacktest executes one backtest through the frozen engine (test seam).
func (e *Engine) runBacktest(req backtest.Request) (backtest.Result, error) {
	if e.backtestRun == nil {
		return backtest.Result{}, fmt.Errorf("agent: backtest engine is not configured")
	}
	return e.backtestRun(req)
}

// newBacktestStrategy builds one strategy through the frozen engine (test seam).
func (e *Engine) newBacktestStrategy(id string, params map[string]float64) (backtest.Strategy, error) {
	if e.newStrategy == nil {
		return backtest.NewStrategy(id, params)
	}
	return e.newStrategy(id, params)
}

// secCode returns the seccode part of a "BOARD:SECCODE" symbol.
func secCode(symbol string) string {
	if i := strings.LastIndex(symbol, ":"); i >= 0 && i+1 < len(symbol) {
		return symbol[i+1:]
	}
	return symbol
}

// clampPeriodCount bounds a history/backtest bar count.
func clampCount(n, max int) int {
	if n <= 0 {
		return max
	}
	if n > max {
		return max
	}
	return n
}

// hasAny reports whether the lowercase text contains any of the substrings.
func hasAny(text string, subs ...string) bool {
	for _, s := range subs {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// contextErr maps a canceled/expired context to a stable tool error.
func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
