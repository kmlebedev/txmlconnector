// Package bot is the rule-bot runtime of the AI terminal: a declarative
// rules.Spec backtested once becomes a bot that can be checked against history,
// watched in dry-run and then traded on its own paper account on live closed
// candles. Signals are computed on closed bars only.
package bot

import (
	"context"
	"errors"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// Stage is the lifecycle stage of a bot. "live" does not exist in iteration 1:
// Promote to it returns ErrLiveDisabled.
type Stage string

// Bot stages.
const (
	StageCheck   Stage = "check"
	StageDryRun  Stage = "dry_run"
	StagePaper   Stage = "paper"
	StageStopped Stage = "stopped"
)

// Journal kinds (frozen by the plan).
const (
	KindClosedBar = "closed_bar"
	KindSignal    = "signal"
	KindRiskCheck = "risk_check"
	KindOrder     = "order"
	KindFill      = "fill"
	KindReconnect = "reconnect"
	KindError     = "error"
	KindStage     = "stage"
)

// Signals of one closed bar.
const (
	SignalNone  = "none"
	SignalEntry = "entry"
	SignalExit  = "exit"
)

// Errors of the bot runtime.
var (
	// ErrNotFound marks an unknown bot id.
	ErrNotFound = errors.New("bot: bot not found")
	// ErrLiveDisabled is returned by Promote for the live stage: iteration 1
	// supports only check, dry-run, paper and stopped.
	ErrLiveDisabled = errors.New("bot: live trading is not implemented: live stage is disabled")
	// ErrApprovalMissing is returned by Promote without a configured approver.
	ErrApprovalMissing = errors.New("bot: promotion to paper requires an approval")
	// ErrInvalidStage marks an unsupported stage or transition.
	ErrInvalidStage = errors.New("bot: unsupported stage")
	// ErrInvalidBot marks a malformed bot definition.
	ErrInvalidBot = errors.New("bot: invalid bot definition")
	// ErrNotConfigured marks a Manager without a bar stream or broker.
	ErrNotConfigured = errors.New("bot: runtime is not configured")
)

// Bot is one rule bot created from a backtest result.
type Bot struct {
	ID           string     `json:"id"`
	Symbol       string     `json:"symbol"`
	PeriodSec    int        `json:"periodSec"`
	Spec         rules.Spec `json:"spec"`
	Qty          int64      `json:"qty"`
	Stage        Stage      `json:"stage"`
	FromBacktest string     `json:"fromBacktest"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// JournalEntry is one journal record of a bot.
type JournalEntry struct {
	Seq        uint64             `json:"seq"`
	Time       time.Time          `json:"time"`
	BarTime    time.Time          `json:"barTime,omitempty"`
	Kind       string             `json:"kind"`
	Close      float64            `json:"close,omitempty"`
	Indicators map[string]float64 `json:"indicators,omitempty"`
	Signal     string             `json:"signal"`
	Message    string             `json:"message"`
	Data       map[string]any     `json:"data,omitempty"`
}

// BotEvent is one SSE event of the bot runtime (BotID + journal entry).
type BotEvent struct {
	BotID string       `json:"botId"`
	Entry JournalEntry `json:"entry"`
}

// RiskLimits are the bot guard rails (the task-2 limits subset that applies to
// one bot order).
type RiskLimits struct {
	MaxOrderPct      float64
	MaxPositionPct   float64
	MaxTradesPerHour int
}

// BarStream is the closed-candle market source. The live implementation is
// *feed.Service (SubscribeClosedBars); tests supply a fake.
type BarStream interface {
	History(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error)
	SubscribeClosedBars(ctx context.Context, symbol string, periodSec int) (<-chan backtest.Bar, error)
}

// Broker is the paper-broker view the bot runtime needs. *paper.Broker
// implements it; the per-bot account is "bot:<id>".
type Broker interface {
	PlaceAt(req paper.OrderRequest, fill paper.QuoteView, at time.Time, account string) (paper.Order, error)
	Snapshot() paper.Snapshot
	Allocate(account string, amount float64) error
}

// Approver is implemented by agent.Engine: it turns a mutating action into a
// pending proposal (task 7). The bot package does not import agent, so the
// dependency stays one-way (agent -> bot for the bot_* tools).
type Approver interface {
	Propose(tool string, args map[string]any, summary string) (id string, err error)
}

const (
	// defaultPaperBudget funds the bot paper account on the first paper start;
	// Manager.SetBudget overrides it.
	defaultPaperBudget = 100_000.0
	// journalLimit is the bounded in-memory journal of one bot.
	journalLimit = 5000
	// historyCount is how many bars Check reads for its one-shot calculation.
	historyCount = 300
	// barHistoryLimit bounds the replay window kept by a running bot; the
	// strategy is replayed over this window on every new bar, so it stays small.
	barHistoryLimit = 500
	// maxQty bounds the bot order quantity.
	maxQty = 1_000_000
)
