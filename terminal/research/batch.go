// Package research implements multi-symbol research over the frozen backtest
// engine: batch comparisons against buy & hold, sample-size and cost warnings,
// and stable backtest IDs the bot runtime can reference.
//
// Every symbol of a batch is replayed independently in parallel (at most
// GOMAXPROCS goroutines), so one slow or failing ticker never blocks the rest
// and the result keeps the requested order.
package research

import (
	"context"
	"fmt"
	"hash/fnv"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
)

// HistoryFunc loads one candle series. In the wiring it is
// agent.DataSource.History over native TRANSAQ history.
type HistoryFunc func(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error)

// BatchRequest is one multi-symbol comparison.
//
// Either Strategy/Params (a catalog strategy) or Rules (a declarative
// rules.Spec) must be set, never both. FeeBps and SlippageBps are literal: the
// batch always sets backtest.Request.ExplicitCosts, so zero really means zero
// and produces the no_costs warning.
type BatchRequest struct {
	Symbols     []string           `json:"symbols"`
	PeriodSec   int                `json:"periodSec"`
	Count       int                `json:"count,omitempty"`
	Strategy    string             `json:"strategy,omitempty"`
	Params      map[string]float64 `json:"params,omitempty"`
	Rules       *rules.Spec        `json:"rules,omitempty"`
	FeeBps      float64            `json:"feeBps,omitempty"`
	SlippageBps float64            `json:"slippageBps,omitempty"`
}

// BatchRow is one instrument of a batch comparison. BacktestID identifies the
// exact replay (symbol, strategy/Rules hash, costs and the closed data window),
// so the bot runtime can recreate a bot from one row.
type BatchRow struct {
	Symbol         string    `json:"symbol"`
	Trades         int       `json:"trades"`
	ReturnPct      float64   `json:"returnPct"`
	BuyHoldPct     float64   `json:"buyHoldPct"`
	MaxDrawdownPct float64   `json:"maxDrawdownPct"`
	Sharpe         float64   `json:"sharpe"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	BacktestID     string    `json:"backtestId"`
	Warnings       []string  `json:"warnings,omitempty"`
}

// BatchDetail is one computed batch symbol: its summary row plus the replay
// data the UI needs for a chart (the closed bars, the full backtest result and,
// for a rules strategy, the indicator series aligned with those bars).
type BatchDetail struct {
	Row    BatchRow
	Bars   []backtest.Bar
	Result backtest.Result
	Series map[string][]float64
}

// Batch replays the strategy or Rules over every symbol in parallel and returns
// one row per symbol in the requested order. A failing symbol produces a row
// whose Warnings start with "error: " and does not fail the whole batch; a
// batch where every symbol failed returns an error.
func Batch(ctx context.Context, hist HistoryFunc, req BatchRequest) ([]BatchRow, error) {
	details, err := BatchDetailed(ctx, hist, req)
	if err != nil {
		return nil, err
	}
	rows := make([]BatchRow, len(details))
	for i := range details {
		rows[i] = details[i].Row
	}
	return rows, nil
}

// BatchDetailed is Batch plus the per-symbol chart data (bars, full result and
// the indicator series of a rules strategy). The agent tool uses it to fill the
// UI artifact, while plain callers use Batch.
func BatchDetailed(ctx context.Context, hist HistoryFunc, req BatchRequest) ([]BatchDetail, error) {
	if hist == nil {
		return nil, fmt.Errorf("research: history function is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	rulesHash, err := rulesHash(req.Rules)
	if err != nil {
		return nil, err
	}

	workers := runtime.GOMAXPROCS(0)
	if workers > len(req.Symbols) {
		workers = len(req.Symbols)
	}
	if workers < 1 {
		workers = 1
	}
	details := make([]BatchDetail, len(req.Symbols))
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			for index := range jobs {
				details[index] = runSymbol(ctx, hist, req, req.Symbols[index], rulesHash)
			}
		}()
	}

	interrupted := false
	for index := range req.Symbols {
		select {
		case jobs <- index:
		case <-ctx.Done():
			interrupted = true
		}
		if interrupted {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if interrupted {
		return nil, ctx.Err()
	}

	failed := 0
	for _, detail := range details {
		if detail.Row.BacktestID == "" {
			failed++
		}
	}
	if failed == len(details) {
		failures := make([]string, 0, len(details))
		for _, detail := range details {
			reason := "no result"
			if len(detail.Row.Warnings) > 0 {
				reason = detail.Row.Warnings[0]
			}
			failures = append(failures, detail.Row.Symbol+": "+reason)
		}
		return nil, fmt.Errorf("research: every symbol failed: %s", strings.Join(failures, "; "))
	}
	return details, nil
}

// validate checks the request before any history is loaded.
func (req BatchRequest) validate() error {
	if len(req.Symbols) == 0 {
		return fmt.Errorf("research: at least one symbol is required")
	}
	for index, symbol := range req.Symbols {
		if strings.TrimSpace(symbol) == "" {
			return fmt.Errorf("research: symbol %d is empty", index+1)
		}
	}
	if req.PeriodSec < 0 {
		return fmt.Errorf("research: periodSec must not be negative: %d", req.PeriodSec)
	}
	if req.Count < 0 {
		return fmt.Errorf("research: count must not be negative: %d", req.Count)
	}
	if req.FeeBps < 0 || req.FeeBps >= 10_000 {
		return fmt.Errorf("research: feeBps must be in [0, 10000): %v", req.FeeBps)
	}
	if req.SlippageBps < 0 || req.SlippageBps >= 10_000 {
		return fmt.Errorf("research: slippageBps must be in [0, 10000): %v", req.SlippageBps)
	}
	hasStrategy := strings.TrimSpace(req.Strategy) != ""
	switch {
	case hasStrategy && req.Rules != nil:
		return fmt.Errorf("research: set either strategy/params or rules, not both")
	case !hasStrategy && req.Rules == nil:
		return fmt.Errorf("research: strategy or rules are required")
	case !hasStrategy:
		return nil
	}
	if _, err := backtest.NewStrategy(req.Strategy, req.Params); err != nil {
		return fmt.Errorf("research: %w", err)
	}
	return nil
}

// rulesHash compiles the Spec once (validating it up front) and returns the
// normalized Spec hash used in BacktestID.
func rulesHash(spec *rules.Spec) (uint64, error) {
	if spec == nil {
		return 0, nil
	}
	strategy, err := rules.Compile(*spec)
	if err != nil {
		return 0, fmt.Errorf("research: %w", err)
	}
	if hasher, ok := strategy.(interface{ Hash() uint64 }); ok {
		return hasher.Hash(), nil
	}
	return 0, nil
}

// runSymbol replays one symbol and fills its detail. All failures are reported
// in the row warnings; the batch keeps going.
func runSymbol(ctx context.Context, hist HistoryFunc, req BatchRequest, symbol string, rulesHash uint64) BatchDetail {
	detail := BatchDetail{Row: BatchRow{Symbol: symbol}}
	row := &detail.Row
	if err := ctx.Err(); err != nil {
		row.Warnings = append(row.Warnings, "error: "+err.Error())
		return detail
	}
	bars, err := hist(ctx, symbol, req.PeriodSec, req.Count)
	if err != nil {
		row.Warnings = append(row.Warnings, "error: "+err.Error())
		return detail
	}
	closed := closedBars(bars)
	if len(closed) < 2 {
		row.Warnings = append(row.Warnings,
			fmt.Sprintf("error: not enough closed bars for %s (%d)", symbol, len(closed)))
		return detail
	}
	result, strategy, err := replay(req, symbol, bars)
	if err != nil {
		row.Warnings = append(row.Warnings, "error: "+err.Error())
		return detail
	}
	row.Trades = result.Metrics.Trades
	row.ReturnPct = result.Metrics.TotalReturnPct
	row.BuyHoldPct = (closed[len(closed)-1].Close/closed[0].Close - 1) * 100
	row.MaxDrawdownPct = result.Metrics.MaxDrawdownPct
	row.Sharpe = result.Metrics.Sharpe
	row.From = result.Metrics.From
	row.To = result.Metrics.To
	row.BacktestID = backtestID(req, symbol, closed, rulesHash)
	row.Warnings = append(row.Warnings, result.Warnings...)
	if warning := backtest.SampleWarning(row.Trades, row.From, row.To, req.PeriodSec); warning != "" {
		row.Warnings = append(row.Warnings, warning)
	}
	detail.Bars = bars
	detail.Result = result
	if strategy != nil {
		if provider, ok := strategy.(interface {
			Series([]backtest.Bar) map[string][]float64
		}); ok {
			detail.Series = provider.Series(bars)
		}
	}
	return detail
}

// replay runs the frozen engine with explicit costs. Partial (still forming)
// bars are dropped by the engine itself; the closed slice exists only for the
// buy & hold baseline and the data-window fingerprint. The compiled rules
// strategy is returned for the chart series; catalog strategies have no series.
func replay(req BatchRequest, symbol string, bars []backtest.Bar) (backtest.Result, backtest.Strategy, error) {
	request := backtest.Request{
		Symbol:        symbol,
		PeriodSec:     req.PeriodSec,
		Bars:          bars,
		FeeBps:        req.FeeBps,
		SlippageBps:   req.SlippageBps,
		ExplicitCosts: true,
	}
	if req.Rules != nil {
		strategy, err := rules.Compile(*req.Rules)
		if err != nil {
			return backtest.Result{}, nil, err
		}
		request.Strategy = strategy.ID()
		result, err := backtest.RunStrategy(request, strategy)
		return result, strategy, err
	}
	request.Strategy = req.Strategy
	request.Params = req.Params
	result, err := backtest.Run(request)
	return result, nil, err
}

// closedBars returns the bars that are actually closed, skipping the still
// forming candles a live feed may append.
func closedBars(bars []backtest.Bar) []backtest.Bar {
	closed := make([]backtest.Bar, 0, len(bars))
	for _, bar := range bars {
		if bar.Partial {
			continue
		}
		closed = append(closed, bar)
	}
	return closed
}

// backtestID fingerprints one replay: the symbol, the strategy or Rules hash,
// the costs and the closed data window. It is deterministic, so the same batch
// row always yields the same ID and the bot runtime can recreate the replay.
func backtestID(req BatchRequest, symbol string, closed []backtest.Bar, rulesHash uint64) string {
	hasher := fnv.New64a()
	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = hasher.Write([]byte(part))
			_, _ = hasher.Write([]byte{0})
		}
	}
	first, last := closed[0], closed[len(closed)-1]
	write(symbol, strconv.Itoa(req.PeriodSec), strconv.Itoa(req.Count))
	if req.Rules != nil {
		write("rules", strconv.FormatUint(rulesHash, 10))
	} else {
		write(req.Strategy, paramsFingerprint(req.Params))
	}
	write(
		strconv.FormatFloat(req.FeeBps, 'g', -1, 64),
		strconv.FormatFloat(req.SlippageBps, 'g', -1, 64),
		first.Time.UTC().Format(time.RFC3339Nano),
		last.Time.UTC().Format(time.RFC3339Nano),
		strconv.Itoa(len(closed)),
	)
	return fmt.Sprintf("bt_%016x", hasher.Sum64())
}

// paramsFingerprint renders strategy params in a stable key order.
func paramsFingerprint(params map[string]float64) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(strconv.FormatFloat(params[key], 'g', -1, 64))
		builder.WriteByte(';')
	}
	return builder.String()
}
