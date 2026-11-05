// Package skills writes and promotes the SKILL-pattern artifacts of the AI
// terminal research loop (plan Task 19): a pattern is a draft SKILL.md with its
// out-of-sample evidence, and promotion turns it into a validated skill only
// after the evidence passes the promotion policy. The package is pure data and
// file IO: no LLM, no network, no exchange.
package skills

import (
	"fmt"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/fingerprint"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
	"github.com/kmlebedev/txmlconnector/terminal/research"
	"github.com/kmlebedev/txmlconnector/terminal/volatility"
)

// Pattern statuses of the draft → validated → retired lifecycle.
const (
	// StatusDraft is a pattern with evidence that is not promoted yet: the
	// agent can read it back, but it is not part of its visible skills.
	StatusDraft = "draft"
	// StatusValidated is a pattern that passed the promotion policy. It makes
	// the skill visible to the agent; it never enables live trading.
	StatusValidated = "validated"
	// StatusRetired is a pattern a human retired; it cannot be promoted again.
	StatusRetired = "retired"
)

// Evidence sources.
const (
	// SourceBars marks a bar-based walk-forward / vectorized backtest.
	SourceBars = "bars"
	// SourceLOB marks an order-book replay (lobreplay) result.
	SourceLOB = "lob"
)

// Pattern is one trading pattern drafted from a research result.
type Pattern struct {
	ID          string
	Title       string
	Hypothesis  string
	Instruments []string
	Conditions  []string
	Entry       string
	Exit        string
	Risk        string
	Evidence    Evidence
	Status      string
	CreatedAt   time.Time
}

// Evidence is the auditable evidence block of a pattern: the point-in-time data
// window, the out-of-sample metrics and the multiple-testing corrections
// (deflated Sharpe, PBO). It is what promotion checks (Review Focus 4).
type Evidence struct {
	DataFrom time.Time
	DataTo   time.Time
	AsOf     time.Time
	Source   string // SourceBars | SourceLOB
	OOS      backtest.Metrics
	// DeflatedSharpe is the probability that the OOS Sharpe beats the expected
	// maximum of the trials (Bailey & López de Prado); PBO is the CSCV
	// probability of backtest overfitting over the same Trials.
	DeflatedSharpe float64
	PBO            float64
	Trials         int
	OOSTrades      int
	DataHash       string
}

// OOSWindow returns the out-of-sample window of the evidence (zero when the
// evidence carries none).
func (e Evidence) OOSWindow() (from, to time.Time) {
	return e.OOS.From, e.OOS.To
}

// OOSTradeCount returns the number of OOS trades: the explicit counter wins,
// the metrics fallback keeps older callers working.
func (e Evidence) OOSTradeCount() int {
	if e.OOSTrades > 0 {
		return e.OOSTrades
	}
	return e.OOS.Trades
}

// EvidenceFromWalkForward converts a walk-forward result into the evidence
// block: the OOS metrics are the chained out-of-sample window, the data window
// spans the train and test ranges, and the trials are the grid size.
func EvidenceFromWalkForward(result research.WalkForwardResult, asOf time.Time, dataHash string) Evidence {
	from, to := dataRange(result.IS, result.OOS)
	return Evidence{
		DataFrom:       from,
		DataTo:         to,
		AsOf:           asOf.UTC(),
		Source:         SourceBars,
		OOS:            result.OOS,
		DeflatedSharpe: result.DeflatedSharpe,
		PBO:            result.PBO,
		Trials:         result.Trials,
		OOSTrades:      result.OOS.Trades,
		DataHash:       dataHash,
	}
}

// EvidenceFromLOB converts an order-book replay result into the evidence block.
// A single replay is not a walk-forward study: DeflatedSharpe/PBO/Trials stay
// zero, and the promotion policy requires a bar-based OOS study for validation.
func EvidenceFromLOB(result lobreplay.Result, asOf time.Time, dataHash string) Evidence {
	metrics := result.Metrics
	return Evidence{
		DataFrom:  metrics.From,
		DataTo:    metrics.To,
		AsOf:      asOf.UTC(),
		Source:    SourceLOB,
		OOS:       metrics,
		OOSTrades: metrics.Trades,
		DataHash:  dataHash,
	}
}

// dataRange returns the union of the two metric windows.
func dataRange(is, oos backtest.Metrics) (from, to time.Time) {
	from, to = oos.From, oos.To
	if !is.From.IsZero() && (from.IsZero() || is.From.Before(from)) {
		from = is.From
	}
	if is.To.After(to) {
		to = is.To
	}
	return from, to
}

// MarketContext turns the volatility oracle forecast and the execution
// fingerprints into human-readable condition lines of a draft pattern: the
// draft_skill_pattern tool appends them to Pattern.Conditions so the hypothesis
// records the market context it was found in.
func MarketContext(forecast volatility.Forecast, footprints []fingerprint.Footprint) []string {
	lines := make([]string, 0, 1+len(footprints))
	if forecast.Regime != "" || forecast.Multiplier > 0 {
		parts := make([]string, 0, 3)
		if forecast.Regime != "" {
			parts = append(parts, "режим "+forecast.Regime)
		}
		if forecast.Multiplier > 0 {
			parts = append(parts, fmt.Sprintf("множитель дисперсии %.2f", forecast.Multiplier))
		}
		if forecast.Horizon > 0 {
			parts = append(parts, "горизонт "+forecast.Horizon.String())
		}
		lines = append(lines, "волатильность: "+strings.Join(parts, ", "))
	}
	for _, footprint := range footprints {
		if footprint.Kind == "" || footprint.Kind == "unknown_algo" {
			continue
		}
		line := fmt.Sprintf("исполнение: отпечаток %s", footprint.Kind)
		if footprint.Side != "" {
			line += " " + footprint.Side
		}
		if footprint.Confidence > 0 {
			line += fmt.Sprintf(", уверенность %.2f", footprint.Confidence)
		}
		lines = append(lines, line)
	}
	return lines
}
