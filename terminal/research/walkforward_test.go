package research

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

// --- deflated Sharpe -------------------------------------------------------

// TestDeflatedSharpePenalizesTrials is the plan Task 15 test: the same observed
// Sharpe is less convincing after many trials, because the expected maximum of
// independent trials grows with their count.
func TestDeflatedSharpePenalizesTrials(t *testing.T) {
	// Reference values computed with an independent implementation of the
	// Bailey & López de Prado formula.
	if got, want := DeflatedSharpe(0.05, 2, 500, 0, 3), 0.7246752593441893; math.Abs(got-want) > 1e-9 {
		t.Fatalf("DSR(0.05, 2 trials) = %.12f, want %.12f", got, want)
	}
	if got, want := DeflatedSharpe(0.05, 100, 500, 0, 3), 0.07885656756169687; math.Abs(got-want) > 1e-9 {
		t.Fatalf("DSR(0.05, 100 trials) = %.12f, want %.12f", got, want)
	}
	if got, want := DeflatedSharpe(0.1, 10, 500, -0.5, 5), 0.7390122485154356; math.Abs(got-want) > 1e-9 {
		t.Fatalf("DSR(0.1, 10 trials, skew/kurt) = %.12f, want %.12f", got, want)
	}

	few := DeflatedSharpe(0.1, 2, 500, 0, 3)
	many := DeflatedSharpe(0.1, 200, 500, 0, 3)
	if !(many < few) {
		t.Fatalf("DSR with 200 trials %.6f is not below DSR with 2 trials %.6f", many, few)
	}
	if high, low := DeflatedSharpe(0.3, 10, 500, 0, 3), DeflatedSharpe(0.05, 10, 500, 0, 3); !(high > low) {
		t.Fatalf("higher Sharpe did not raise DSR: %.6f vs %.6f", high, low)
	}
	for _, value := range []float64{few, many} {
		if value < 0 || value > 1 || math.IsNaN(value) {
			t.Fatalf("DSR = %v is outside [0, 1]", value)
		}
	}
	// Degenerate inputs are reported as "no evidence", not as a NaN.
	if got := DeflatedSharpe(0.1, 0, 500, 0, 3); got != 0 {
		t.Fatalf("DSR with zero trials = %v, want 0", got)
	}
	if got := DeflatedSharpe(0.1, 10, 1, 0, 3); got != 0 {
		t.Fatalf("DSR with one observation = %v, want 0", got)
	}
	if got := DeflatedSharpe(0.1, 1, 500, 0, 3); got < 0 || got > 1 {
		t.Fatalf("single-trial DSR = %v is outside [0, 1]", got)
	}
}

// TestMomentsOfReturns checks the moments used by the deflated Sharpe.
func TestMomentsOfReturns(t *testing.T) {
	// A symmetric two-point series: mean 0, positive variance, no skew.
	moments := MomentsOf([]float64{-1, 1, -1, 1})
	if moments.Count != 4 || math.Abs(moments.Mean) > 1e-12 || moments.Std <= 0 {
		t.Fatalf("moments = %+v", moments)
	}
	if math.Abs(moments.Skewness) > 1e-9 {
		t.Fatalf("symmetric series skew = %v, want 0", moments.Skewness)
	}
	// The raw kurtosis of a symmetric two-point distribution is 1, not 3: the
	// normal value is 3 only for a Gaussian sample.
	if math.Abs(moments.Kurtosis-1) > 1e-9 {
		t.Fatalf("symmetric two-point kurtosis = %v, want 1", moments.Kurtosis)
	}
	if got := SharpeRatio([]float64{0.01, 0.01, 0.01}); got != 0 {
		t.Fatalf("degenerate Sharpe = %v, want 0", got)
	}
}

// --- probability of backtest overfitting ----------------------------------

// TestPBOFlagsOverfitting is the CSCV sanity check: a persistent edge is not
// flagged, a strategy that wins in-sample and loses out-of-sample is.
func TestPBOFlagsOverfitting(t *testing.T) {
	const blocks = 16
	const observations = 1024
	persistent := make([][]float64, 6)
	for trial := range persistent {
		persistent[trial] = make([]float64, observations)
	}
	for index := 0; index < observations; index++ {
		// Trial 0 has a positive mean in every block, the others are zero-mean
		// noise.
		persistent[0][index] = 0.01 + 0.001*math.Sin(float64(index))
		for trial := 1; trial < len(persistent); trial++ {
			persistent[trial][index] = 0.003 * math.Sin(float64(index*7+trial*13))
		}
	}
	if got := PBO(persistent, blocks); got != 0 {
		t.Fatalf("persistent edge PBO = %v, want 0", got)
	}

	// Trial 0 is the best only in the first half of the sample, trial 1 only in
	// the second half: whichever wins in-sample must disappoint out-of-sample.
	overfit := make([][]float64, 8)
	for trial := range overfit {
		overfit[trial] = make([]float64, observations)
	}
	half := observations / 2
	for index := 0; index < observations; index++ {
		switch {
		case index < half:
			overfit[0][index] = 0.02 + 0.001*math.Sin(float64(index))
			overfit[1][index] = -0.02 + 0.001*math.Sin(float64(index))
		default:
			overfit[0][index] = -0.02 + 0.001*math.Sin(float64(index))
			overfit[1][index] = 0.02 + 0.001*math.Sin(float64(index))
		}
		for trial := 2; trial < len(overfit); trial++ {
			overfit[trial][index] = 0.003 * math.Sin(float64(index*5+trial*11))
		}
	}
	flagged := PBO(overfit, blocks)
	if flagged <= 0.4 {
		t.Fatalf("overfit matrix PBO = %v, want it flagged (> 0.4)", flagged)
	}
	if flagged > 1 || flagged < 0 {
		t.Fatalf("PBO = %v is outside [0, 1]", flagged)
	}

	if got := PBO([][]float64{{1, 2, 3}}, 16); got != 0.5 {
		t.Fatalf("PBO with a single trial = %v, want 0.5", got)
	}
	if got := PBO(nil, 16); got != 0.5 {
		t.Fatalf("PBO with no data = %v, want 0.5", got)
	}
}

// --- walk-forward ----------------------------------------------------------

// wfTestBars builds an oscillating series usable by the catalog strategies:
// fast SMA crosses produce a non-trivial number of trades inside every fold.
func wfTestBars(n int) []backtest.Bar {
	bars := make([]backtest.Bar, n)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		closePrice := 100 + 8*math.Sin(float64(i)/5.0) + 0.05*float64(i)
		open := closePrice
		if i > 0 {
			open = bars[i-1].Close
		}
		high, low := open, closePrice
		if closePrice > open {
			high = closePrice
		} else {
			low = closePrice
		}
		bars[i] = backtest.Bar{
			Time:   start.Add(time.Duration(i) * 24 * time.Hour),
			Open:   open,
			High:   high,
			Low:    low,
			Close:  closePrice,
			Volume: int64(1000 + i),
		}
	}
	return bars
}

func wfRequest(bars []backtest.Bar) WalkForwardRequest {
	return WalkForwardRequest{
		Bars:        bars,
		Strategy:    "sma_cross",
		Grid:        map[string][]float64{"fast": {2, 3}, "slow": {8, 13}},
		TrainBars:   120,
		TestBars:    40,
		StepBars:    40,
		FeeBps:      5,
		SlippageBps: 1,
	}
}

// TestWalkForwardNoLookahead proves every fold is replayed on exactly its own
// train/test window: the fold metrics match a standalone backtest on the same
// window and poisoned bars after the window do not change the fold.
func TestWalkForwardNoLookahead(t *testing.T) {
	bars := wfTestBars(320)
	req := wfRequest(bars)
	result, err := WalkForward(context.Background(), req)
	if err != nil {
		t.Fatalf("WalkForward: %v", err)
	}
	if len(result.Folds) == 0 {
		t.Fatal("no folds produced")
	}
	run := func(window []backtest.Bar, params map[string]float64) backtest.Metrics {
		t.Helper()
		res, err := backtest.Run(backtest.Request{
			Symbol:      "TEST",
			PeriodSec:   periodSecOf(bars),
			Bars:        window,
			Strategy:    req.Strategy,
			Params:      params,
			FeeBps:      req.FeeBps,
			SlippageBps: req.SlippageBps,
		})
		if err != nil {
			t.Fatalf("standalone backtest: %v", err)
		}
		return res.Metrics
	}
	for index, fold := range result.Folds {
		if fold.TestTo-fold.TestFrom != req.TestBars || fold.TrainTo-fold.TrainFrom != req.TrainBars {
			t.Fatalf("fold %d has windows train=%d..%d test=%d..%d",
				index, fold.TrainFrom, fold.TrainTo, fold.TestFrom, fold.TestTo)
		}
		wantIS := run(bars[fold.TrainFrom:fold.TrainTo], fold.Params)
		if !reflectMetrics(fold.IS, wantIS) {
			t.Fatalf("fold %d IS metrics %+v != standalone %+v", index, fold.IS, wantIS)
		}
		wantOOS := run(bars[fold.TestFrom:fold.TestTo], fold.Params)
		if !reflectMetrics(fold.OOS, wantOOS) {
			t.Fatalf("fold %d OOS metrics %+v != standalone %+v", index, fold.OOS, wantOOS)
		}
	}

	// Poison every bar after the end of the first fold: the first fold must not
	// notice (its train and test windows are before it).
	poisoned := make([]backtest.Bar, len(bars))
	copy(poisoned, bars)
	firstEnd := result.Folds[0].TestTo
	for i := firstEnd; i < len(poisoned); i++ {
		poisoned[i].Open *= 4
		poisoned[i].High *= 4
		poisoned[i].Low *= 4
		poisoned[i].Close *= 4
	}
	poisonedReq := wfRequest(poisoned)
	poisonedResult, err := WalkForward(context.Background(), poisonedReq)
	if err != nil {
		t.Fatalf("WalkForward(poisoned): %v", err)
	}
	if !reflectMetrics(poisonedResult.Folds[0].OOS, result.Folds[0].OOS) ||
		!reflectMetrics(poisonedResult.Folds[0].IS, result.Folds[0].IS) {
		t.Fatalf("first fold changed after poisoning later bars")
	}
}

// TestWalkForwardGridAndAggregates checks the grid search, fold accounting and
// the walk-forward statistics.
func TestWalkForwardGridAndAggregates(t *testing.T) {
	req := wfRequest(wfTestBars(320))
	result, err := WalkForward(context.Background(), req)
	if err != nil {
		t.Fatalf("WalkForward: %v", err)
	}
	wantTrials := len(req.Grid["fast"]) * len(req.Grid["slow"])
	if result.Trials != wantTrials {
		t.Fatalf("trials = %d, want %d", result.Trials, wantTrials)
	}
	wantFolds := (len(req.Bars)-req.TrainBars-req.TestBars)/req.StepBars + 1
	if len(result.Folds) != wantFolds {
		t.Fatalf("folds = %d, want %d", len(result.Folds), wantFolds)
	}
	trades := 0
	for index, fold := range result.Folds {
		if len(fold.Params) != 2 || fold.Params["fast"] == 0 || fold.Params["slow"] == 0 {
			t.Fatalf("fold %d params = %v", index, fold.Params)
		}
		trades += fold.OOS.Trades
		if fold.OOS.Bars != req.TestBars {
			t.Fatalf("fold %d OOS bars = %d, want %d", index, fold.OOS.Bars, req.TestBars)
		}
	}
	if result.OOS.Trades != trades {
		t.Fatalf("aggregate OOS trades = %d, want %d", result.OOS.Trades, trades)
	}
	// The chained equity curve has one starting point plus the per-bar returns
	// of every fold.
	if wantBars := len(result.Folds)*(req.TestBars-1) + 1; result.OOS.Bars != wantBars {
		t.Fatalf("aggregate OOS bars = %d, want %d", result.OOS.Bars, wantBars)
	}
	if result.OOS.InitialCash <= 0 || result.OOS.FinalEquity <= 0 || result.OOS.From.IsZero() || result.OOS.To.IsZero() {
		t.Fatalf("aggregate OOS metrics are empty: %+v", result.OOS)
	}
	if result.DeflatedSharpe < 0 || result.DeflatedSharpe > 1 {
		t.Fatalf("deflated Sharpe = %v", result.DeflatedSharpe)
	}
	if result.PBO < 0 || result.PBO > 1 {
		t.Fatalf("PBO = %v", result.PBO)
	}
	if result.IS.Trades == 0 && result.OOS.Trades == 0 {
		t.Fatalf("fixture produced no trades at all: IS %+v OOS %+v", result.IS, result.OOS)
	}
}

// TestWalkForwardValidation rejects unusable requests with clear errors.
func TestWalkForwardValidation(t *testing.T) {
	bars := wfTestBars(200)
	cases := []struct {
		name   string
		mutate func(*WalkForwardRequest)
	}{
		{"no bars", func(r *WalkForwardRequest) { r.Bars = nil }},
		{"no strategy", func(r *WalkForwardRequest) { r.Strategy = "" }},
		{"unknown strategy", func(r *WalkForwardRequest) { r.Strategy = "no_such_strategy" }},
		{"zero train", func(r *WalkForwardRequest) { r.TrainBars = 0 }},
		{"zero test", func(r *WalkForwardRequest) { r.TestBars = 0 }},
		{"too short", func(r *WalkForwardRequest) { r.TrainBars = 150; r.TestBars = 150 }},
		{"negative fee", func(r *WalkForwardRequest) { r.FeeBps = -1 }},
		{"grid too large", func(r *WalkForwardRequest) {
			r.Grid = map[string][]float64{"fast": make([]float64, 60), "slow": make([]float64, 60)}
		}},
		{"events without window", func(r *WalkForwardRequest) {
			r.Events = []vintage.Release{{At: time.Date(2026, time.March, 10, 12, 30, 0, 0, time.UTC)}}
			r.ExcludeEventWindow = 0
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := wfRequest(bars)
			tc.mutate(&req)
			if _, err := WalkForward(context.Background(), req); err == nil {
				t.Fatalf("WalkForward accepted an invalid request (%s)", tc.name)
			}
		})
	}
}

// TestWalkForwardCancelled proves a cancelled context stops the search.
func TestWalkForwardCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WalkForward(ctx, wfRequest(wfTestBars(320))); err == nil {
		t.Fatal("WalkForward succeeded with a cancelled context")
	}
}

// periodSecOf derives the bar period from the series (walk-forward request has
// no explicit period).
func periodSecOf(bars []backtest.Bar) int {
	if len(bars) < 2 {
		return 0
	}
	return int(bars[1].Time.Sub(bars[0].Time) / time.Second)
}

// reflectMetrics compares the metric fields that a replay of the same window
// must reproduce exactly.
func reflectMetrics(got, want backtest.Metrics) bool {
	return got.Trades == want.Trades &&
		got.Bars == want.Bars &&
		got.InitialCash == want.InitialCash &&
		got.FinalEquity == want.FinalEquity &&
		got.TotalReturnPct == want.TotalReturnPct &&
		got.MaxDrawdownPct == want.MaxDrawdownPct &&
		got.Sharpe == want.Sharpe &&
		got.WinRatePct == want.WinRatePct &&
		got.From.Equal(want.From) && got.To.Equal(want.To)
}

// TestWalkForwardRunnerSeam proves the Run seam receives exactly the fold
// windows (never the whole series) and that its results drive the search.
func TestWalkForwardRunnerSeam(t *testing.T) {
	bars := wfTestBars(320)
	req := wfRequest(bars)
	trainLen, testLen := req.TrainBars, req.TestBars
	var calls atomic.Int64
	req.Run = func(request backtest.Request) (backtest.Result, error) {
		call := calls.Add(1)
		if len(request.Bars) != trainLen && len(request.Bars) != testLen {
			t.Fatalf("runner got %d bars, want %d (train) or %d (test)", len(request.Bars), trainLen, testLen)
		}
		// The window must be a contiguous slice of the full series.
		found := false
		for start := 0; start+len(request.Bars) <= len(bars); start++ {
			if bars[start].Time.Equal(request.Bars[0].Time) &&
				bars[start+len(request.Bars)-1].Time.Equal(request.Bars[len(request.Bars)-1].Time) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("runner window %v..%v is not a slice of the series",
				request.Bars[0].Time, request.Bars[len(request.Bars)-1].Time)
		}
		result, err := backtest.Run(request)
		if err != nil {
			return backtest.Result{}, err
		}
		// Give later windows a better return so the aggregate reflects the seam.
		// A non-flat curve: later calls grow faster, so the aggregate must
		// reflect the seam's results rather than the raw engine ones.
		for index := range result.Equity {
			result.Equity[index].Equity = walkForwardCash * (1 + 0.001*float64(call)*float64(index))
		}
		return result, nil
	}
	result, err := WalkForward(context.Background(), req)
	if err != nil {
		t.Fatalf("WalkForward: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("the Run seam was not used")
	}
	if passes := int64(2 * len(result.Folds) * result.Trials); calls.Load() != passes {
		t.Fatalf("runner calls = %d, want %d (train+test per fold per trial)", calls.Load(), passes)
	}
	if result.OOS.FinalEquity == walkForwardCash {
		t.Fatalf("seam results did not reach the aggregate: %+v", result.OOS)
	}
}

// TestEventWindowAcrossUSDST is Review Focus 2: a US release at 8:30 ET maps to
// the right Moscow instant on both sides of the US daylight-saving switch.
func TestEventWindowAcrossUSDST(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		// Fall back to the fixed MSK offset, like terminal/vintage does.
		moscow = time.FixedZone("MSK", 3*60*60)
	}
	// 10.03.2026: the US already switched to summer time on 08.03 — 8:30 EDT
	// is 12:30 UTC and 15:30 MSK.
	march := time.Date(2026, time.March, 10, 8, 30, 0, 0, newYork)
	// 03.11.2026: the US already switched back on 01.11 — 8:30 EST is 13:30 UTC
	// and 16:30 MSK.
	november := time.Date(2026, time.November, 3, 8, 30, 0, 0, newYork)
	if got, want := march.UTC(), time.Date(2026, time.March, 10, 12, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("march release UTC = %v, want %v", got, want)
	}
	if got, want := november.UTC(), time.Date(2026, time.November, 3, 13, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("november release UTC = %v, want %v", got, want)
	}

	windows := EventWindows([]vintage.Release{
		{At: november, Category: "fomc", Country: "US"},
		{At: march, Category: "us_cpi", Country: "US"},
	}, 30*time.Minute)
	if len(windows) != 2 {
		t.Fatalf("windows = %+v, want 2", windows)
	}
	check := func(index int, wantStart time.Time) {
		t.Helper()
		got := windows[index].Start.In(moscow)
		if !got.Equal(wantStart) {
			t.Fatalf("window %d start = %v, want %v", index, got, wantStart)
		}
		if span := windows[index].End.Sub(windows[index].Start); span != 30*time.Minute {
			t.Fatalf("window %d span = %v, want 30m", index, span)
		}
	}
	// EventWindows sorts by Start: March first, November second.
	check(0, time.Date(2026, time.March, 10, 15, 30, 0, 0, moscow))
	check(1, time.Date(2026, time.November, 3, 16, 30, 0, 0, moscow))
}

// TestWalkForwardExcludesEventWindows proves the excluded release windows
// remove exactly the covered bars from the out-of-sample evaluation.
func TestWalkForwardExcludesEventWindows(t *testing.T) {
	bars := wfTestBars(320)
	req := wfRequest(bars)
	base, err := WalkForward(context.Background(), req)
	if err != nil {
		t.Fatalf("WalkForward base: %v", err)
	}
	// The first two test windows are [160,200) and [200,240): one bar in each is
	// covered by a release window.
	req.Events = []vintage.Release{
		{At: bars[170].Time, Category: "us_cpi", Country: "US"},
		{At: bars[210].Time, Category: "fomc", Country: "US"},
	}
	req.ExcludeEventWindow = time.Minute
	withEvents, err := WalkForward(context.Background(), req)
	if err != nil {
		t.Fatalf("WalkForward with events: %v", err)
	}
	if want := base.OOS.Bars - 2; withEvents.OOS.Bars != want {
		t.Fatalf("OOS bars with events = %d, want %d", withEvents.OOS.Bars, want)
	}
	if withEvents.DeflatedSharpe < 0 || withEvents.DeflatedSharpe > 1 {
		t.Fatalf("deflated Sharpe = %v", withEvents.DeflatedSharpe)
	}
	if withEvents.PBO < 0 || withEvents.PBO > 1 {
		t.Fatalf("PBO = %v", withEvents.PBO)
	}
	if len(withEvents.Folds) != len(base.Folds) {
		t.Fatalf("fold count changed by events: %d vs %d", len(withEvents.Folds), len(base.Folds))
	}
}
