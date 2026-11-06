package research

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

// Walk-forward defaults and guard rails.
const (
	// walkForwardCash is the initial cash of every walk-forward replay.
	walkForwardCash = 1_000_000.0
	// maxWalkForwardTrials bounds one grid search so a tool call cannot spawn
	// an unbounded number of backtests.
	maxWalkForwardTrials = 1000
	// walkForwardPBOBlocks is the conventional CSCV block count (plan Task 15).
	walkForwardPBOBlocks = 16
)

// Fold is one walk-forward split: the train window [TrainFrom, TrainTo), the
// following test window [TestFrom, TestTo) and the grid params chosen on the
// train window.
type Fold struct {
	TrainFrom int                `json:"trainFrom"`
	TrainTo   int                `json:"trainTo"`
	TestFrom  int                `json:"testFrom"`
	TestTo    int                `json:"testTo"`
	Params    map[string]float64 `json:"params"`
	IS        backtest.Metrics   `json:"is"`
	OOS       backtest.Metrics   `json:"oos"`
}

// Runner replays one window. It lets the caller swap the frozen engine for a
// seam in tests or for a future replay backend; nil means backtest.Run.
type Runner func(backtest.Request) (backtest.Result, error)

// EventWindow is one excluded interval around a macro release: the release
// instant and ExcludeEventWindow after it. All instants are UTC, like
// vintage.Release.At.
type EventWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// WalkForwardRequest is one walk-forward grid search over a bar series.
//
// StepBars defaults to TestBars when it is zero. FeeBps and SlippageBps keep the
// backtest defaults (a zero means the documented 5/1 bps). Grid values are
// combined as a full cartesian product; an empty Grid evaluates the strategy
// defaults once. Run overrides the backtest engine (tests, adapters).
type WalkForwardRequest struct {
	Bars        []backtest.Bar
	Strategy    string
	Grid        map[string][]float64
	TrainBars   int
	TestBars    int
	StepBars    int
	FeeBps      float64
	SlippageBps float64
	// Events are scheduled macro releases (vintage.Releases). A bar whose instant
	// falls inside [At, At+ExcludeEventWindow] is dropped from the train and test
	// windows of every fold, so the caller can compare a run "with release
	// windows" to the same run without events.
	Events             []vintage.Release
	ExcludeEventWindow time.Duration
	Run                Runner `json:"-"`
}

// WalkForwardResult is the aggregate of all folds: the out-of-sample and
// in-sample metrics of the chained windows, the deflated Sharpe ratio of the
// out-of-sample returns (the number of trials is the grid size) and the CSCV
// probability of backtest overfitting over the same trials.
type WalkForwardResult struct {
	Folds          []Fold           `json:"folds"`
	OOS            backtest.Metrics `json:"oos"`
	IS             backtest.Metrics `json:"is"`
	DeflatedSharpe float64          `json:"deflatedSharpe"`
	PBO            float64          `json:"pbo"`
	Trials         int              `json:"trials"`
}

// wfConfig is a validated request plus the derived bar period.
type wfConfig struct {
	bars        []backtest.Bar
	strategy    string
	grid        map[string][]float64
	trainBars   int
	testBars    int
	stepBars    int
	feeBps      float64
	slippageBps float64
	periodSec   int
	windows     []EventWindow
	run         Runner
}

// gridOutcome is one grid combination replayed on both windows of one fold.
type gridOutcome struct {
	train backtest.Result
	test  backtest.Result
}

// WalkForward runs a walk-forward grid search: every fold trains on TrainBars
// bars, picks the best grid params by in-sample Sharpe and replays the following
// TestBars bars out of sample. Fold and grid work is parallel over
// runtime.GOMAXPROCS. The bars beyond a fold window are never visible to that
// fold, so the strategy can only see its own train+test slice.
func WalkForward(ctx context.Context, req WalkForwardRequest) (WalkForwardResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return WalkForwardResult{}, err
	}
	cfg, err := normalizeWalkForward(req)
	if err != nil {
		return WalkForwardResult{}, err
	}
	combos, err := expandGrid(cfg.grid)
	if err != nil {
		return WalkForwardResult{}, err
	}
	if _, err := backtest.NewStrategy(cfg.strategy, nil); err != nil {
		return WalkForwardResult{}, fmt.Errorf("research: %w", err)
	}

	folds := make([]Fold, 0, (len(cfg.bars)-cfg.trainBars-cfg.testBars)/cfg.stepBars+1)
	var (
		oosReturns   []float64
		isReturns    []float64
		oosTimes     []time.Time
		isTimes      []time.Time
		oosBenchmark []float64
		oosTrades    []backtest.Trade
		isTrades     []backtest.Trade
		oosExposure  float64
		isExposure   float64
		oosPoints    int
		isPoints     int
	)
	trialReturns := make([][]float64, len(combos))
	for foldIndex := 0; ; foldIndex++ {
		testEnd := cfg.trainBars + cfg.testBars + foldIndex*cfg.stepBars
		if testEnd > len(cfg.bars) {
			break
		}
		trainFrom := testEnd - cfg.testBars - cfg.trainBars
		trainTo := testEnd - cfg.testBars
		trainBars := cfg.bars[trainFrom:trainTo]
		testBars := cfg.bars[trainTo:testEnd]
		if len(cfg.windows) > 0 {
			trainBars = excludeEventBars(trainBars, cfg.windows)
			testBars = excludeEventBars(testBars, cfg.windows)
			if len(trainBars) < 2 || len(testBars) < 2 {
				return WalkForwardResult{}, fmt.Errorf(
					"research: event windows leave less than 2 bars in fold %d (train %d, test %d)",
					foldIndex, len(trainBars), len(testBars))
			}
		}
		outcomes, err := runWalkForwardGrid(ctx, cfg, combos, trainBars, testBars)
		if err != nil {
			return WalkForwardResult{}, err
		}
		best := bestTrainCombo(outcomes)
		fold := Fold{
			TrainFrom: trainFrom,
			TrainTo:   trainTo,
			TestFrom:  trainTo,
			TestTo:    testEnd,
			Params:    combos[best],
			IS:        outcomes[best].train.Metrics,
			OOS:       outcomes[best].test.Metrics,
		}
		folds = append(folds, fold)
		oosReturns = append(oosReturns, equityReturns(outcomes[best].test.Equity)...)
		isReturns = append(isReturns, equityReturns(outcomes[best].train.Equity)...)
		oosBenchmark = append(oosBenchmark, benchmarkReturns(outcomes[best].test.Equity)...)
		oosTrades = append(oosTrades, outcomes[best].test.Trades...)
		isTrades = append(isTrades, outcomes[best].train.Trades...)
		for _, point := range outcomes[best].test.Equity[1:] {
			oosTimes = append(oosTimes, point.Time)
		}
		for _, point := range outcomes[best].train.Equity[1:] {
			isTimes = append(isTimes, point.Time)
		}
		oosExposure += outcomes[best].test.Metrics.ExposurePct * float64(len(outcomes[best].test.Equity))
		isExposure += outcomes[best].train.Metrics.ExposurePct * float64(len(outcomes[best].train.Equity))
		oosPoints += len(outcomes[best].test.Equity)
		isPoints += len(outcomes[best].train.Equity)
		for comboIndex := range combos {
			trialReturns[comboIndex] = append(trialReturns[comboIndex], equityReturns(outcomes[comboIndex].test.Equity)...)
		}
	}
	if len(folds) == 0 {
		return WalkForwardResult{}, fmt.Errorf("research: walk-forward produced no folds")
	}
	result := WalkForwardResult{Folds: folds, Trials: len(combos)}
	if oosPoints > 0 {
		oosExposure /= float64(oosPoints)
	}
	if isPoints > 0 {
		isExposure /= float64(isPoints)
	}
	result.OOS = aggregateWalkForwardMetrics(oosReturns, oosBenchmark, oosTimes, oosTrades, cfg.periodSec, oosExposure)
	result.IS = aggregateWalkForwardMetrics(isReturns, nil, isTimes, isTrades, cfg.periodSec, isExposure)
	moments := MomentsOf(oosReturns)
	result.DeflatedSharpe = DeflatedSharpe(moments.Sharpe, len(combos), moments.Count, moments.Skewness, moments.Kurtosis)
	result.PBO = PBO(trialReturns, walkForwardPBOBlocks)
	return result, nil
}

// normalizeWalkForward validates the request and fills the defaults.
func normalizeWalkForward(req WalkForwardRequest) (wfConfig, error) {
	if len(req.Bars) < 2 {
		return wfConfig{}, fmt.Errorf("research: walk-forward needs at least 2 bars, got %d", len(req.Bars))
	}
	strategy := fmt.Sprintf("%s", req.Strategy)
	strategy = trimSpace(strategy)
	if strategy == "" {
		return wfConfig{}, fmt.Errorf("research: walk-forward strategy is required")
	}
	if req.TrainBars <= 0 {
		return wfConfig{}, fmt.Errorf("research: trainBars must be positive: %d", req.TrainBars)
	}
	if req.TestBars <= 0 {
		return wfConfig{}, fmt.Errorf("research: testBars must be positive: %d", req.TestBars)
	}
	step := req.StepBars
	if step <= 0 {
		step = req.TestBars
	}
	if step <= 0 {
		return wfConfig{}, fmt.Errorf("research: stepBars must be positive: %d", req.StepBars)
	}
	if req.FeeBps < 0 || req.FeeBps >= 10_000 {
		return wfConfig{}, fmt.Errorf("research: feeBps must be in [0, 10000): %v", req.FeeBps)
	}
	if req.SlippageBps < 0 || req.SlippageBps >= 10_000 {
		return wfConfig{}, fmt.Errorf("research: slippageBps must be in [0, 10000): %v", req.SlippageBps)
	}
	if len(req.Bars) < req.TrainBars+req.TestBars {
		return wfConfig{}, fmt.Errorf("research: %d bars are not enough for trainBars %d + testBars %d",
			len(req.Bars), req.TrainBars, req.TestBars)
	}
	if req.ExcludeEventWindow < 0 {
		return wfConfig{}, fmt.Errorf("research: excludeEventWindow must not be negative: %v", req.ExcludeEventWindow)
	}
	if len(req.Events) > 0 && req.ExcludeEventWindow <= 0 {
		return wfConfig{}, fmt.Errorf("research: excludeEventWindow must be positive when events are set")
	}
	return wfConfig{
		bars:        req.Bars,
		strategy:    strategy,
		grid:        req.Grid,
		trainBars:   req.TrainBars,
		testBars:    req.TestBars,
		stepBars:    step,
		feeBps:      req.FeeBps,
		slippageBps: req.SlippageBps,
		periodSec:   barsPeriodSec(req.Bars),
		windows:     EventWindows(req.Events, req.ExcludeEventWindow),
		run:         req.Run,
	}, nil
}

// runWalkForwardGrid replays every grid combination on the train and the test
// window of one fold, at most GOMAXPROCS combinations at a time.
func runWalkForwardGrid(ctx context.Context, cfg wfConfig, combos []map[string]float64, trainBars, testBars []backtest.Bar) ([]gridOutcome, error) {
	outcomes := make([]gridOutcome, len(combos))
	errs := make([]error, len(combos))
	workers := runtime.GOMAXPROCS(0)
	if workers > len(combos) {
		workers = len(combos)
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			for index := range jobs {
				if err := ctx.Err(); err != nil {
					errs[index] = err
					continue
				}
				train, err := runWindow(cfg, trainBars, combos[index])
				if err != nil {
					errs[index] = err
					continue
				}
				test, err := runWindow(cfg, testBars, combos[index])
				if err != nil {
					errs[index] = err
					continue
				}
				outcomes[index] = gridOutcome{train: train, test: test}
			}
		}()
	}
	for index := range combos {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("research: walk-forward grid trial %d: %w", index, err)
		}
	}
	return outcomes, nil
}

// runWindow replays one grid combination on one window through the configured
// runner (the frozen engine by default).
func runWindow(cfg wfConfig, bars []backtest.Bar, params map[string]float64) (backtest.Result, error) {
	request := backtest.Request{
		Symbol:      "WF",
		PeriodSec:   cfg.periodSec,
		Bars:        bars,
		Strategy:    cfg.strategy,
		Params:      params,
		InitialCash: walkForwardCash,
		FeeBps:      cfg.feeBps,
		SlippageBps: cfg.slippageBps,
	}
	if cfg.run != nil {
		return cfg.run(request)
	}
	return backtest.Run(request)
}

// bestTrainCombo picks the in-sample winner: higher Sharpe, then higher total
// return, then more trades, then a shallower drawdown; ties keep the first grid
// combination, which keeps the search deterministic.
func bestTrainCombo(outcomes []gridOutcome) int {
	best := 0
	for index := 1; index < len(outcomes); index++ {
		if betterTrainMetrics(outcomes[index].train.Metrics, outcomes[best].train.Metrics) {
			best = index
		}
	}
	return best
}

// betterTrainMetrics reports whether a is a better in-sample result than b.
func betterTrainMetrics(a, b backtest.Metrics) bool {
	if a.Sharpe != b.Sharpe {
		return a.Sharpe > b.Sharpe
	}
	if a.TotalReturnPct != b.TotalReturnPct {
		return a.TotalReturnPct > b.TotalReturnPct
	}
	if a.Trades != b.Trades {
		return a.Trades > b.Trades
	}
	return a.MaxDrawdownPct > b.MaxDrawdownPct
}

// expandGrid turns the parameter grid into the full cartesian product of
// combinations. Keys are sorted so the trial order (and therefore PBO) is
// deterministic. An empty or missing grid means one trial with default params.
func expandGrid(grid map[string][]float64) ([]map[string]float64, error) {
	keys := make([]string, 0, len(grid))
	for key, values := range grid {
		if trimSpace(key) == "" {
			return nil, fmt.Errorf("research: grid parameter name must not be empty")
		}
		if len(values) == 0 {
			continue
		}
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("research: grid parameter %q has a non-finite value", key)
			}
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	combos := []map[string]float64{{}}
	for _, key := range keys {
		values := grid[key]
		next := make([]map[string]float64, 0, len(combos)*len(values))
		for _, combo := range combos {
			for _, value := range values {
				clone := make(map[string]float64, len(combo)+1)
				for existingKey, existingValue := range combo {
					clone[existingKey] = existingValue
				}
				clone[key] = value
				next = append(next, clone)
			}
		}
		combos = next
		if len(combos) > maxWalkForwardTrials {
			return nil, fmt.Errorf("research: grid has more than %d combinations", maxWalkForwardTrials)
		}
	}
	return combos, nil
}

// equityReturns returns the per-bar returns of a backtest equity curve.
func equityReturns(equity []backtest.EquityPoint) []float64 {
	if len(equity) < 2 {
		return nil
	}
	returns := make([]float64, 0, len(equity)-1)
	for index := 1; index < len(equity); index++ {
		previous := equity[index-1].Equity
		if previous <= 0 {
			returns = append(returns, 0)
			continue
		}
		returns = append(returns, equity[index].Equity/previous-1)
	}
	return returns
}

// benchmarkReturns returns the per-bar returns of the benchmark curve.
func benchmarkReturns(equity []backtest.EquityPoint) []float64 {
	if len(equity) < 2 {
		return nil
	}
	returns := make([]float64, 0, len(equity)-1)
	for index := 1; index < len(equity); index++ {
		previous := equity[index-1].Benchmark
		if previous <= 0 {
			returns = append(returns, 0)
			continue
		}
		returns = append(returns, equity[index].Benchmark/previous-1)
	}
	return returns
}

// aggregateWalkForwardMetrics chains the fold returns into one equity curve and
// derives the summary metrics. It mirrors the definitions of the frozen
// backtest engine (population variance, yearly Sharpe annualization, capped
// profit factor) so a walk-forward aggregate reads like a single run.
func aggregateWalkForwardMetrics(returns, benchmark []float64, times []time.Time, trades []backtest.Trade, periodSec int, exposurePct float64) backtest.Metrics {
	metrics := backtest.Metrics{
		InitialCash: walkForwardCash,
		Trades:      len(trades),
		ExposurePct: exposurePct,
	}
	if len(returns) == 0 {
		metrics.FinalEquity = walkForwardCash
		return metrics
	}
	equity := make([]float64, len(returns)+1)
	equity[0] = walkForwardCash
	for index, ret := range returns {
		equity[index+1] = equity[index] * (1 + ret)
	}
	metrics.Bars = len(equity)
	metrics.FinalEquity = equity[len(equity)-1]
	metrics.TotalReturnPct = (metrics.FinalEquity/walkForwardCash - 1) * 100

	peak := equity[0]
	for _, value := range equity {
		if value > peak {
			peak = value
		}
		if peak > 0 {
			drawdown := (value/peak - 1) * 100
			if drawdown < metrics.MaxDrawdownPct {
				metrics.MaxDrawdownPct = drawdown
			}
		}
	}
	metrics.Sharpe = walkForwardSharpe(returns, periodSec)

	if benchmark != nil {
		benchmarkEquity := make([]float64, len(benchmark)+1)
		benchmarkEquity[0] = walkForwardCash
		for index, ret := range benchmark {
			benchmarkEquity[index+1] = benchmarkEquity[index] * (1 + ret)
		}
		metrics.BenchmarkReturnPct = (benchmarkEquity[len(benchmarkEquity)-1]/walkForwardCash - 1) * 100
	}

	var wins, losses, pnlPctSum float64
	winners := 0
	for _, trade := range trades {
		pnlPctSum += trade.PnLPct
		switch {
		case trade.PnL > 0:
			winners++
			wins += trade.PnL
		case trade.PnL < 0:
			losses -= trade.PnL
		}
	}
	if len(trades) > 0 {
		metrics.WinRatePct = float64(winners) / float64(len(trades)) * 100
		metrics.AvgTradePct = pnlPctSum / float64(len(trades))
	}
	switch {
	case losses > 0:
		metrics.ProfitFactor = wins / losses
	case wins > 0:
		metrics.ProfitFactor = 1000
	}
	if len(times) >= len(equity) {
		metrics.From = times[0]
		metrics.To = times[len(equity)-2]
	} else if len(times) > 0 {
		metrics.From = times[0]
		metrics.To = times[len(times)-1]
	}
	return metrics
}

// walkForwardSharpe annualizes the mean/std of the per-bar returns the same way
// the frozen engine does.
func walkForwardSharpe(returns []float64, periodSec int) float64 {
	if periodSec <= 0 || len(returns) < 3 {
		return 0
	}
	var sum float64
	for _, ret := range returns {
		sum += ret
	}
	mean := sum / float64(len(returns))
	var variance float64
	for _, ret := range returns {
		diff := ret - mean
		variance += diff * diff
	}
	variance /= float64(len(returns))
	std := math.Sqrt(variance)
	if std == 0 {
		return 0
	}
	annualization := float64(365*24*3600) / float64(periodSec)
	return mean / std * math.Sqrt(annualization)
}

// EventWindows converts the scheduled releases into the excluded intervals
// [At, At+window]. The result is sorted by Start and UTC-normalized; a zero
// window or a release without an instant produces no window.
func EventWindows(releases []vintage.Release, window time.Duration) []EventWindow {
	if window <= 0 || len(releases) == 0 {
		return nil
	}
	windows := make([]EventWindow, 0, len(releases))
	for _, release := range releases {
		if release.At.IsZero() {
			continue
		}
		at := release.At.UTC()
		windows = append(windows, EventWindow{Start: at, End: at.Add(window)})
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].Start.Before(windows[j].Start) })
	return windows
}

// excludeEventBars drops the bars whose instant falls inside any event window.
// The intervals are sorted, so the lookup is a binary search.
func excludeEventBars(bars []backtest.Bar, windows []EventWindow) []backtest.Bar {
	if len(windows) == 0 {
		return bars
	}
	out := make([]backtest.Bar, 0, len(bars))
	for _, bar := range bars {
		if !inEventWindow(bar.Time.UTC(), windows) {
			out = append(out, bar)
		}
	}
	return out
}

// inEventWindow reports whether the instant is inside one of the sorted
// windows.
func inEventWindow(at time.Time, windows []EventWindow) bool {
	index := sort.Search(len(windows), func(i int) bool { return windows[i].End.After(at) })
	if index >= len(windows) {
		return false
	}
	return !at.Before(windows[index].Start) && at.Before(windows[index].End)
}

// barsPeriodSec derives the bar period in seconds from the series spacing.
func barsPeriodSec(bars []backtest.Bar) int {
	if len(bars) < 2 {
		return 0
	}
	seconds := int(bars[1].Time.Sub(bars[0].Time) / time.Second)
	if seconds < 0 {
		return 0
	}
	return seconds
}

// trimSpace is strings.TrimSpace without importing strings for one call.
func trimSpace(value string) string {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\t' || value[start] == '\n' || value[start] == '\r') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\t' || value[end-1] == '\n' || value[end-1] == '\r') {
		end--
	}
	return value[start:end]
}
