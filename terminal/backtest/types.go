// Package backtest is the vectorized bar-replay backtesting engine of the AI
// terminal. It is pure Go (no network, no clock): the caller supplies bars and
// receives metrics, equity and trades.
//
// This file is the frozen contract scaffold created by the Lead; the
// implementation lives in the package files owned by the backtest task.
package backtest

import (
	"errors"
	"time"
)

// Bar is one OHLCV bar supplied by the market-data feed.
type Bar struct {
	Time   time.Time `json:"t"`
	Open   float64   `json:"o"`
	High   float64   `json:"h"`
	Low    float64   `json:"l"`
	Close  float64   `json:"c"`
	Volume int64     `json:"v"`
}

// Param describes one tunable strategy parameter for the UI and the agent.
type Param struct {
	Key     string  `json:"key"`
	Title   string  `json:"title"`
	Default float64 `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Step    float64 `json:"step"`
}

// StrategyInfo is one entry of the strategy catalog.
type StrategyInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Params      []Param `json:"params"`
}

// Action is the decision of a strategy on the close of one bar.
type Action int

const (
	// Hold keeps the current position.
	Hold Action = iota
	// Buy asks to open a long position.
	Buy
	// Sell asks to close the long position.
	Sell
)

// Strategy is an incremental, lookahead-free strategy. OnBar may only read
// bars[0..i]; the engine fills the decision at Open[i+1].
type Strategy interface {
	ID() string
	Params() map[string]float64
	OnBar(bars []Bar, i int) Action
}

// Request is one backtest run.
type Request struct {
	Symbol      string             `json:"symbol"`
	PeriodSec   int                `json:"periodSec"`
	Bars        []Bar              `json:"-"`
	Strategy    string             `json:"strategy"`
	Params      map[string]float64 `json:"params,omitempty"`
	InitialCash float64            `json:"initialCash,omitempty"`
	FeeBps      float64            `json:"feeBps,omitempty"`
	SlippageBps float64            `json:"slippageBps,omitempty"`
	RiskPct     float64            `json:"riskPct,omitempty"`
}

// Trade is one closed round trip.
type Trade struct {
	EntryTime  time.Time `json:"entryTime"`
	ExitTime   time.Time `json:"exitTime"`
	Side       string    `json:"side"`
	EntryPrice float64   `json:"entryPrice"`
	ExitPrice  float64   `json:"exitPrice"`
	Qty        int64     `json:"qty"`
	PnL        float64   `json:"pnl"`
	PnLPct     float64   `json:"pnlPct"`
	BarsHeld   int       `json:"barsHeld"`
	Reason     string    `json:"reason"`
}

// EquityPoint is one point of the equity/benchmark curve.
type EquityPoint struct {
	Time        time.Time `json:"t"`
	Equity      float64   `json:"value"`
	Benchmark   float64   `json:"benchmark"`
	DrawdownPct float64   `json:"drawdownPct"`
}

// Metrics are the summary statistics of one run.
type Metrics struct {
	InitialCash        float64   `json:"initialCash"`
	FinalEquity        float64   `json:"finalEquity"`
	TotalReturnPct     float64   `json:"totalReturnPct"`
	BenchmarkReturnPct float64   `json:"benchmarkReturnPct"`
	MaxDrawdownPct     float64   `json:"maxDrawdownPct"`
	Sharpe             float64   `json:"sharpe"`
	WinRatePct         float64   `json:"winRatePct"`
	ProfitFactor       float64   `json:"profitFactor"`
	AvgTradePct        float64   `json:"avgTradePct"`
	ExposurePct        float64   `json:"exposurePct"`
	Trades             int       `json:"trades"`
	Bars               int       `json:"bars"`
	From               time.Time `json:"from"`
	To                 time.Time `json:"to"`
}

// Result is one complete backtest run, JSON-serializable for the HTTP/agent
// API (keys are frozen by docs/ai-terminal/ARCHITECTURE.md §8).
type Result struct {
	Symbol    string             `json:"symbol"`
	Strategy  string             `json:"strategy"`
	PeriodSec int                `json:"periodSec"`
	Params    map[string]float64 `json:"params"`
	Metrics   Metrics            `json:"metrics"`
	Equity    []EquityPoint      `json:"equity"`
	Trades    []Trade            `json:"trades"`
	Warnings  []string           `json:"warnings"`
}

// ErrNotImplemented is returned by the contract scaffold until the backtest
// task fills the implementation.
var ErrNotImplemented = errors.New("backtest: not implemented yet")

// Strategies returns the strategy catalog.
func Strategies() []StrategyInfo { return nil }

// NewStrategy validates params and builds an incremental strategy.
func NewStrategy(id string, params map[string]float64) (Strategy, error) {
	return nil, ErrNotImplemented
}

// Run replays bars through the strategy and returns the result.
func Run(req Request) (Result, error) { return Result{}, ErrNotImplemented }
