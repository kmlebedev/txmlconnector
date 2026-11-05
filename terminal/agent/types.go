// Package agent is the in-process AI agent of the trading terminal: an
// OpenAI-compatible tool-calling loop, a deterministic local analyst used when
// no LLM is configured, technical "hint" computations and a paper autonomy
// runner. It never speaks MCP: every tool is a Go call in this process.
//
// The exported API, the JSON tags and the JSON shapes of this package are
// frozen by docs/ai-terminal/ARCHITECTURE.md §9. Implementation files:
// engine.go, tools.go, llm.go, analyst.go, hints.go, autonomy.go, risk.go.
package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// LLMConfig configures the optional OpenAI-compatible provider.
type LLMConfig struct {
	BaseURL string        `json:"baseUrl"`
	APIKey  string        `json:"-"`
	Model   string        `json:"model"`
	Timeout time.Duration `json:"-"`
	Enabled bool          `json:"enabled"`
}

// RiskLimits are the default guard rails of paper trading and autonomy.
type RiskLimits struct {
	MaxPositionPct   float64 `json:"maxPositionPct"`
	MaxOrderPct      float64 `json:"maxOrderPct"`
	StopLossPct      float64 `json:"stopLossPct"`
	TakeProfitPct    float64 `json:"takeProfitPct"`
	DailyLossPct     float64 `json:"dailyLossPct"`
	MaxTradesPerHour float64 `json:"maxTradesPerHour"`
}

// Config configures the agent engine.
type Config struct {
	LLM              LLMConfig        `json:"llm"`
	Universe         []string         `json:"universe"`
	DefaultSymbol    string           `json:"defaultSymbol"`
	DefaultPeriodSec int              `json:"defaultPeriodSec"`
	Risk             RiskLimits       `json:"risk"`
	FeeBps           float64          `json:"feeBps"`
	BacktestHistory  int              `json:"backtestHistory"`
	Now              func() time.Time `json:"-"`
}

// InstrumentRef is the agent's view of a tradable instrument.
type InstrumentRef struct {
	Symbol    string  `json:"symbol"`
	Name      string  `json:"name"`
	Last      float64 `json:"last"`
	ChangePct float64 `json:"changePct"`
}

// Quote is the agent's view of one quote.
type Quote struct {
	Symbol    string    `json:"symbol"`
	Last      float64   `json:"last"`
	Bid       float64   `json:"bid"`
	Ask       float64   `json:"ask"`
	ChangePct float64   `json:"changePct"`
	Time      time.Time `json:"time"`
}

// Level is one order-book level.
type Level struct {
	Price float64 `json:"price"`
	Qty   float64 `json:"qty"`
}

// OrderBook is the agent's view of a price ladder.
type OrderBook struct {
	Symbol string  `json:"symbol"`
	Bids   []Level `json:"bids"`
	Asks   []Level `json:"asks"`
	Last   float64 `json:"last"`
}

// DataSource is implemented by the cmd/terminal feed adapter.
type DataSource interface {
	Instruments() []InstrumentRef
	Quote(symbol string) (Quote, bool)
	OrderBook(symbol string) (OrderBook, error)
	History(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error)
}

// PlaceOrder is a paper order request made by a tool.
type PlaceOrder struct {
	Symbol string  `json:"symbol"`
	Side   string  `json:"side"`
	Qty    int64   `json:"qty"`
	Type   string  `json:"type"`
	Price  float64 `json:"price"`
}

// OrderView is the agent-facing paper order.
type OrderView struct {
	ID           string    `json:"id"`
	Symbol       string    `json:"symbol"`
	Side         string    `json:"side"`
	Type         string    `json:"type"`
	Status       string    `json:"status"`
	Qty          int64     `json:"qty"`
	Filled       int64     `json:"filled"`
	Price        float64   `json:"price"`
	AvgFillPrice float64   `json:"avgFillPrice"`
	CreatedAt    time.Time `json:"createdAt"`
}

// PositionView is one portfolio position.
type PositionView struct {
	Symbol    string  `json:"symbol"`
	Qty       int64   `json:"qty"`
	AvgPrice  float64 `json:"avgPrice"`
	Price     float64 `json:"price"`
	Value     float64 `json:"value"`
	Profit    float64 `json:"profit"`
	ProfitPct float64 `json:"profitPct"`
}

// PortfolioView is the paper portfolio snapshot.
type PortfolioView struct {
	TotalValue   float64        `json:"totalValue"`
	Cash         float64        `json:"cash"`
	DayChange    float64        `json:"dayChange"`
	DayChangePct float64        `json:"dayChangePct"`
	Positions    []PositionView `json:"positions"`
}

// Broker is implemented by the cmd/terminal paper adapter.
type Broker interface {
	Place(PlaceOrder) (OrderView, error)
	Cancel(id string) (OrderView, error)
	Orders() []OrderView
	Portfolio() PortfolioView
	Cash() float64
}

// ToolInfo describes one in-process agent tool.
type ToolInfo struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
}

// ToolCall is one executed tool call (visible in the UI and SSE).
type ToolCall struct {
	Name       string         `json:"name"`
	Args       map[string]any `json:"args"`
	Result     any            `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	DurationMs int64          `json:"durationMs"`
}

// Artifact is a structured UI card produced by a tool (backtest chart etc.).
type Artifact struct {
	Kind    string `json:"kind"`
	Payload any    `json:"payload"`
}

// ChatRequest is one user message to the agent.
type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversationId,omitempty"`
}

// ChatResponse is the final agent answer plus the executed tool calls.
type ChatResponse struct {
	Answer    string     `json:"answer"`
	ToolCalls []ToolCall `json:"toolCalls"`
	Artifacts []Artifact `json:"artifacts"`
	LLM       bool       `json:"llm"`
	ElapsedMs int64      `json:"elapsedMs"`
}

// Event is one agent SSE event.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// AutonomyConfig configures the paper replay autonomy runner.
type AutonomyConfig struct {
	Symbol         string             `json:"symbol"`
	PeriodSec      int                `json:"periodSec"`
	Strategy       string             `json:"strategy"`
	Params         map[string]float64 `json:"params,omitempty"`
	Speed          float64            `json:"speed"`
	Budget         float64            `json:"budget"`
	MaxPositionPct float64            `json:"maxPositionPct"`
	StopLossPct    float64            `json:"stopLossPct"`
	TakeProfitPct  float64            `json:"takeProfitPct"`
	DailyLossPct   float64            `json:"dailyLossPct"`
	Live           bool               `json:"live"`
}

// LogEntry is one autonomy log line.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	Data    any       `json:"data,omitempty"`
}

// AutonomyStatus is the current state of the paper autonomy runner.
type AutonomyStatus struct {
	Running      bool           `json:"running"`
	Config       AutonomyConfig `json:"config"`
	StartedAt    time.Time      `json:"startedAt"`
	LastActionAt time.Time      `json:"lastActionAt"`
	Trades       int            `json:"trades"`
	PnL          float64        `json:"pnl"`
	PnLPct       float64        `json:"pnlPct"`
	Log          []LogEntry     `json:"log"`
}

// LLMStatus reports whether the LLM provider is configured.
type LLMStatus struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
	Note    string `json:"note"`
}

// Status is the aggregate agent status.
type Status struct {
	Autonomy AutonomyStatus `json:"autonomy"`
	LLM      LLMStatus      `json:"llm"`
}

// MarketHints are computed technical hints ("подсказки") for the UI/agent.
type MarketHints struct {
	Symbol        string         `json:"symbol"`
	PeriodSec     int            `json:"periodSec"`
	AsOf          time.Time      `json:"asOf"`
	SMA20         float64        `json:"sma20"`
	SMA50         float64        `json:"sma50"`
	RSI14         float64        `json:"rsi14"`
	ATR14         float64        `json:"atr14"`
	VolatilityPct float64        `json:"volatilityPct"`
	Trend         string         `json:"trend"`
	Support       float64        `json:"support"`
	Resistance    float64        `json:"resistance"`
	Breadth       map[string]int `json:"breadth"`
	Note          string         `json:"note"`
}
