package backtest

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

func TestRunSMACrossKnownTrade(t *testing.T) {
	closes := knownCrossCloses()
	bars := closeSeries(closes)
	const initialCash = 100000.0
	req := Request{
		Symbol:      "TQBR:SBER",
		PeriodSec:   300,
		Bars:        bars,
		Strategy:    "sma_cross",
		Params:      map[string]float64{"fast": 2, "slow": 3},
		InitialCash: initialCash,
	}
	res, err := Run(req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Metrics.Trades != 1 {
		t.Fatalf("trades = %d, want 1 (trades: %+v)", res.Metrics.Trades, res.Trades)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("len(Trades) = %d, want 1", len(res.Trades))
	}
	trade := res.Trades[0]

	// Documented defaults: 5 bps fee and 1 bps slippage (zero means "default").
	const feeRate = 5.0 / 10000
	const slipRate = 1.0 / 10000
	entryIdx, exitIdx := 6, 13
	wantEntry := bars[entryIdx].Open * (1 + slipRate)
	wantExit := bars[exitIdx].Open * (1 - slipRate)
	wantQty := int64(math.Min(
		math.Floor(initialCash/wantEntry),
		math.Floor(initialCash/(wantEntry*(1+feeRate))),
	))
	wantPnL := float64(wantQty)*wantExit*(1-feeRate) - float64(wantQty)*wantEntry*(1+feeRate)
	wantEntryCost := float64(wantQty) * wantEntry * (1 + feeRate)

	if trade.Side != "long" {
		t.Fatalf("trade side = %q, want long", trade.Side)
	}
	assertClose(t, "entry price", trade.EntryPrice, wantEntry)
	assertClose(t, "exit price", trade.ExitPrice, wantExit)
	if trade.Qty != wantQty {
		t.Fatalf("qty = %d, want %d", trade.Qty, wantQty)
	}
	assertClose(t, "pnl", trade.PnL, wantPnL)
	assertClose(t, "pnl pct", trade.PnLPct, wantPnL/wantEntryCost*100)
	if !trade.EntryTime.Equal(bars[entryIdx].Time) {
		t.Fatalf("entry time = %v, want %v (fill must be on bar %d)", trade.EntryTime, bars[entryIdx].Time, entryIdx)
	}
	if !trade.ExitTime.Equal(bars[exitIdx].Time) {
		t.Fatalf("exit time = %v, want %v (fill must be on bar %d)", trade.ExitTime, bars[exitIdx].Time, exitIdx)
	}
	if trade.BarsHeld != exitIdx-entryIdx {
		t.Fatalf("bars held = %d, want %d", trade.BarsHeld, exitIdx-entryIdx)
	}

	assertClose(t, "initial cash", res.Metrics.InitialCash, initialCash)
	assertClose(t, "final equity", res.Metrics.FinalEquity, initialCash+wantPnL)
	assertClose(t, "total return pct", res.Metrics.TotalReturnPct, wantPnL/initialCash*100)
	assertClose(t, "benchmark return pct", res.Metrics.BenchmarkReturnPct, (closes[len(closes)-1]/closes[0]-1)*100)
	// The position is open from the close of bar 6 through the close of bar 12.
	assertClose(t, "exposure pct", res.Metrics.ExposurePct, 7.0/14.0*100)
	assertClose(t, "win rate pct", res.Metrics.WinRatePct, 100)
	if !isFinite(res.Metrics.ProfitFactor) {
		t.Fatalf("profit factor = %v, want a finite number", res.Metrics.ProfitFactor)
	}
	if !isFinite(res.Metrics.Sharpe) {
		t.Fatalf("sharpe = %v, want a finite number", res.Metrics.Sharpe)
	}
	if res.Metrics.Bars != len(bars) {
		t.Fatalf("bars = %d, want %d", res.Metrics.Bars, len(bars))
	}
	if res.Symbol != "TQBR:SBER" || res.Strategy != "sma_cross" || res.PeriodSec != 300 {
		t.Fatalf("result header = %+v", res)
	}
	assertParams(t, res.Params, map[string]float64{"fast": 2, "slow": 3})

	if len(res.Equity) != len(bars) {
		t.Fatalf("len(Equity) = %d, want %d", len(res.Equity), len(bars))
	}
	if res.Equity[0].Time != bars[0].Time && !res.Equity[0].Time.Equal(bars[0].Time) {
		t.Fatalf("equity[0].Time = %v, want %v", res.Equity[0].Time, bars[0].Time)
	}
	assertClose(t, "equity[0]", res.Equity[0].Equity, initialCash)
	assertClose(t, "last equity", res.Equity[len(res.Equity)-1].Equity, res.Metrics.FinalEquity)

	// 14 bars is below the recommended 50: the engine must warn, not fail.
	if !hasWarning(res.Warnings, "баров") {
		t.Fatalf("warnings = %v, want a short-history warning", res.Warnings)
	}
}

// TestRunFillsAtNextBarOpen pins the "signal on close of bar i, fill at open of
// bar i+1" rule: the entry price must use the next bar's open, never the close
// of the signal bar.
func TestRunFillsAtNextBarOpen(t *testing.T) {
	bars := closeSeries(knownCrossCloses())
	entryIdx := 6
	bars[entryIdx].Open = bars[entryIdx-1].Close + 25
	bars[entryIdx].High = bars[entryIdx].Open + 1
	res, err := Run(Request{
		Symbol:      "TEST",
		PeriodSec:   300,
		Bars:        bars,
		Strategy:    "sma_cross",
		Params:      map[string]float64{"fast": 2, "slow": 3},
		InitialCash: 100000,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("len(Trades) = %d, want 1", len(res.Trades))
	}
	trade := res.Trades[0]
	if trade.EntryPrice <= bars[entryIdx-1].Close {
		t.Fatalf("entry price %v used the signal-bar close %v instead of the next open %v",
			trade.EntryPrice, bars[entryIdx-1].Close, bars[entryIdx].Open)
	}
	assertClose(t, "entry price", trade.EntryPrice, bars[entryIdx].Open*(1+1.0/10000))
}

// TestRunNoLookaheadPrefixConsistency proves the engine is causal: running it on
// a prefix of the bars must reproduce exactly the same equity path prefix.
func TestRunNoLookaheadPrefixConsistency(t *testing.T) {
	bars := wavyBars(120)
	req := Request{
		Symbol:      "TEST",
		PeriodSec:   300,
		Bars:        bars,
		Strategy:    "sma_cross",
		Params:      map[string]float64{"fast": 3, "slow": 8},
		InitialCash: 1000000,
	}
	full, err := Run(req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if full.Metrics.Trades == 0 {
		t.Fatalf("fixture produced no trades, the test would be vacuous")
	}
	for _, k := range []int{5, 12, 30, 61, 119} {
		prefix := req
		prefix.Bars = bars[:k]
		res, err := Run(prefix)
		if err != nil {
			t.Fatalf("Run(prefix %d): %v", k, err)
		}
		if res.Metrics.Bars != k {
			t.Fatalf("prefix %d: bars = %d", k, res.Metrics.Bars)
		}
		for j := 0; j < k; j++ {
			assertClose(t, fmt.Sprintf("prefix %d equity[%d]", k, j), res.Equity[j].Equity, full.Equity[j].Equity)
			assertClose(t, fmt.Sprintf("prefix %d drawdown[%d]", k, j), res.Equity[j].DrawdownPct, full.Equity[j].DrawdownPct)
			assertClose(t, fmt.Sprintf("prefix %d benchmark[%d]", k, j), res.Equity[j].Benchmark, full.Equity[j].Benchmark)
		}
		assertClose(t, fmt.Sprintf("prefix %d final equity", k), res.Metrics.FinalEquity, full.Equity[k-1].Equity)
	}
}

func TestRunFeeAndSlippageReduceEquity(t *testing.T) {
	bars := closeSeries(knownCrossCloses())
	run := func(feeBps, slippageBps float64) Metrics {
		t.Helper()
		res, err := Run(Request{
			Symbol:      "TEST",
			PeriodSec:   300,
			Bars:        bars,
			Strategy:    "sma_cross",
			Params:      map[string]float64{"fast": 2, "slow": 3},
			InitialCash: 100000,
			FeeBps:      feeBps,
			SlippageBps: slippageBps,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.Metrics.Trades != 1 {
			t.Fatalf("trades = %d, want 1", res.Metrics.Trades)
		}
		return res.Metrics
	}
	base := run(1, 1)
	highFee := run(20, 1)
	highSlippage := run(1, 10)
	defaults := run(0, 0) // zero means "use the documented default": fee 5, slippage 1

	if !(highFee.FinalEquity < base.FinalEquity) {
		t.Fatalf("higher fee equity %v is not below baseline %v", highFee.FinalEquity, base.FinalEquity)
	}
	if !(highSlippage.FinalEquity < base.FinalEquity) {
		t.Fatalf("higher slippage equity %v is not below baseline %v", highSlippage.FinalEquity, base.FinalEquity)
	}
	if !(defaults.FinalEquity < base.FinalEquity) {
		t.Fatalf("default 5 bps fee equity %v is not below 1 bps baseline %v", defaults.FinalEquity, base.FinalEquity)
	}
	if !(defaults.FinalEquity > highFee.FinalEquity) {
		t.Fatalf("default fee equity %v should sit between 1 and 20 bps", defaults.FinalEquity)
	}
}

func TestRunEmptyAndShortBarsWarn(t *testing.T) {
	for _, n := range []int{0, 1, 10} {
		res, err := Run(Request{
			Symbol:    "TEST",
			PeriodSec: 300,
			Bars:      wavyBars(n),
			Strategy:  "sma_cross",
		})
		if err != nil {
			t.Fatalf("n=%d: Run returned error: %v", n, err)
		}
		if res.Metrics.Bars != n {
			t.Fatalf("n=%d: bars = %d", n, res.Metrics.Bars)
		}
		if res.Metrics.Trades != 0 || len(res.Trades) != 0 {
			t.Fatalf("n=%d: trades = %d, want 0", n, res.Metrics.Trades)
		}
		if len(res.Warnings) == 0 {
			t.Fatalf("n=%d: warnings are empty, want a short-history warning", n)
		}
		if math.Abs(res.Metrics.FinalEquity-res.Metrics.InitialCash) > 1e-9 {
			t.Fatalf("n=%d: final equity = %v, want initial cash %v", n, res.Metrics.FinalEquity, res.Metrics.InitialCash)
		}
		if _, err := json.Marshal(res); err != nil {
			t.Fatalf("n=%d: json.Marshal: %v", n, err)
		}
	}
}

func TestRunNoTradesWarns(t *testing.T) {
	res, err := Run(Request{
		Symbol:    "TEST",
		PeriodSec: 60,
		Bars:      closeSeries(constCloses(100, 60)),
		Strategy:  "sma_cross",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Metrics.Trades != 0 {
		t.Fatalf("trades = %d, want 0", res.Metrics.Trades)
	}
	if !hasWarning(res.Warnings, "сдел") {
		t.Fatalf("warnings = %v, want a no-trades warning", res.Warnings)
	}
	if res.Metrics.TotalReturnPct != 0 || res.Metrics.WinRatePct != 0 || res.Metrics.ProfitFactor != 0 ||
		res.Metrics.AvgTradePct != 0 || res.Metrics.MaxDrawdownPct != 0 || res.Metrics.Sharpe != 0 {
		t.Fatalf("flat market metrics must be zero, got %+v", res.Metrics)
	}
}

func TestRunOpenPositionMarkedToMarket(t *testing.T) {
	closes := make([]float64, 0, 65)
	for i := 0; i < 5; i++ {
		closes = append(closes, 100)
	}
	for i := 0; i < 60; i++ {
		closes = append(closes, closes[len(closes)-1]*1.01)
	}
	res, err := Run(Request{
		Symbol:      "TEST",
		PeriodSec:   300,
		Bars:        closeSeries(closes),
		Strategy:    "sma_cross",
		Params:      map[string]float64{"fast": 2, "slow": 3},
		InitialCash: 1000000,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Metrics.Trades != 0 {
		t.Fatalf("trades = %d, want 0: the position is still open at the end", res.Metrics.Trades)
	}
	if !(res.Metrics.FinalEquity > res.Metrics.InitialCash) {
		t.Fatalf("final equity %v does not include the open position mark-to-market", res.Metrics.FinalEquity)
	}
	if res.Metrics.ExposurePct <= 0 {
		t.Fatalf("exposure = %v, want > 0", res.Metrics.ExposurePct)
	}
	if !hasWarning(res.Warnings, "позиц") {
		t.Fatalf("warnings = %v, want an open-position warning", res.Warnings)
	}
	assertClose(t, "final equity", res.Metrics.FinalEquity, res.Equity[len(res.Equity)-1].Equity)
}

func TestRunRiskPctLimitsPositionSize(t *testing.T) {
	bars := closeSeries(knownCrossCloses())
	run := func(riskPct float64) Result {
		t.Helper()
		res, err := Run(Request{
			Symbol:      "TEST",
			PeriodSec:   300,
			Bars:        bars,
			Strategy:    "sma_cross",
			Params:      map[string]float64{"fast": 2, "slow": 3},
			InitialCash: 100000,
			RiskPct:     riskPct,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res
	}
	half := run(50)
	full := run(100)
	if len(half.Trades) != 1 || len(full.Trades) != 1 {
		t.Fatalf("trades = %d/%d, want 1/1", len(half.Trades), len(full.Trades))
	}
	notional := float64(half.Trades[0].Qty) * half.Trades[0].EntryPrice
	if notional > 100000*0.5+1e-6 {
		t.Fatalf("riskPct=50 position notional = %v, want <= 50000", notional)
	}
	if half.Trades[0].Qty >= full.Trades[0].Qty {
		t.Fatalf("riskPct=50 qty %d is not below riskPct=100 qty %d", half.Trades[0].Qty, full.Trades[0].Qty)
	}
	over := run(150) // risk above 100% is clamped: long-only cash account
	if len(over.Trades) != 1 {
		t.Fatalf("riskPct=150 trades = %d, want 1", len(over.Trades))
	}
	if over.Trades[0].Qty != full.Trades[0].Qty {
		t.Fatalf("riskPct=150 qty %d, want it clamped to riskPct=100 qty %d", over.Trades[0].Qty, full.Trades[0].Qty)
	}
}

func TestRunRejectsInvalidRequest(t *testing.T) {
	newReq := func() Request {
		return Request{
			Symbol:    "TEST",
			PeriodSec: 300,
			Bars:      wavyBars(60),
			Strategy:  "sma_cross",
		}
	}
	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"unknown strategy", func(r *Request) { r.Strategy = "no_such_strategy" }},
		{"negative fee", func(r *Request) { r.FeeBps = -1 }},
		{"negative slippage", func(r *Request) { r.SlippageBps = -1 }},
		{"negative cash", func(r *Request) { r.InitialCash = -1 }},
		{"huge fee", func(r *Request) { r.FeeBps = 10000 }},
		{"huge slippage", func(r *Request) { r.SlippageBps = 20000 }},
		{"negative period", func(r *Request) { r.PeriodSec = -1 }},
		{"negative risk", func(r *Request) { r.RiskPct = -1 }},
		{"zero close", func(r *Request) { r.Bars[3].Close = 0 }},
		{"nan open", func(r *Request) { r.Bars[2].Open = math.NaN() }},
		{"infinite high", func(r *Request) { r.Bars[4].High = math.Inf(1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newReq()
			tc.mutate(&req)
			if _, err := Run(req); err == nil {
				t.Fatalf("Run accepted an invalid request (%s)", tc.name)
			}
		})
	}
}

func TestResultJSONShape(t *testing.T) {
	res, err := Run(Request{
		Symbol:      "TQBR:SBER",
		PeriodSec:   300,
		Bars:        closeSeries(knownCrossCloses()),
		Strategy:    "sma_cross",
		Params:      map[string]float64{"fast": 2, "slow": 3},
		InitialCash: 100000,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal(Result): %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for _, key := range []string{"symbol", "strategy", "periodSec", "params", "metrics", "equity", "trades", "warnings"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("result JSON misses key %q", key)
		}
	}
	var metrics map[string]json.RawMessage
	if err := json.Unmarshal(doc["metrics"], &metrics); err != nil {
		t.Fatalf("metrics JSON: %v", err)
	}
	for _, key := range []string{
		"initialCash", "finalEquity", "totalReturnPct", "benchmarkReturnPct", "maxDrawdownPct",
		"sharpe", "winRatePct", "profitFactor", "avgTradePct", "exposurePct", "trades", "bars", "from", "to",
	} {
		if _, ok := metrics[key]; !ok {
			t.Fatalf("metrics JSON misses key %q", key)
		}
	}
	var equity []map[string]json.RawMessage
	if err := json.Unmarshal(doc["equity"], &equity); err != nil {
		t.Fatalf("equity JSON: %v", err)
	}
	if len(equity) != len(res.Equity) || len(equity) == 0 {
		t.Fatalf("equity JSON length = %d, want %d", len(equity), len(res.Equity))
	}
	for _, key := range []string{"t", "value", "benchmark", "drawdownPct"} {
		if _, ok := equity[0][key]; !ok {
			t.Fatalf("equity JSON misses key %q", key)
		}
	}
	var trades []map[string]json.RawMessage
	if err := json.Unmarshal(doc["trades"], &trades); err != nil {
		t.Fatalf("trades JSON: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("trades JSON length = %d, want 1", len(trades))
	}
	for _, key := range []string{"entryTime", "exitTime", "side", "entryPrice", "exitPrice", "qty", "pnl", "pnlPct", "barsHeld", "reason"} {
		if _, ok := trades[0][key]; !ok {
			t.Fatalf("trade JSON misses key %q", key)
		}
	}
}

func TestRunZeroPeriodWarnsAndKeepsSharpeFinite(t *testing.T) {
	res, err := Run(Request{
		Symbol:   "TEST",
		Bars:     wavyBars(60),
		Strategy: "sma_cross",
		Params:   map[string]float64{"fast": 3, "slow": 8},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasWarning(res.Warnings, "periodSec") {
		t.Fatalf("warnings = %v, want a periodSec warning", res.Warnings)
	}
	if res.Metrics.Sharpe != 0 {
		t.Fatalf("sharpe = %v, want 0 when periodSec is unknown", res.Metrics.Sharpe)
	}
	if !isFinite(res.Metrics.BenchmarkReturnPct) || !isFinite(res.Metrics.MaxDrawdownPct) {
		t.Fatalf("metrics are not finite: %+v", res.Metrics)
	}
}

func TestRunSkipsUnfundedEntries(t *testing.T) {
	res, err := Run(Request{
		Symbol:      "TEST",
		PeriodSec:   300,
		Bars:        closeSeries(knownCrossCloses()),
		Strategy:    "sma_cross",
		Params:      map[string]float64{"fast": 2, "slow": 3},
		InitialCash: 1, // cheaper than a single unit of the instrument
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Metrics.Trades != 0 {
		t.Fatalf("trades = %d, want 0 without enough cash", res.Metrics.Trades)
	}
	if !hasWarning(res.Warnings, "нехватки средств") {
		t.Fatalf("warnings = %v, want a skipped-entry warning", res.Warnings)
	}
	if math.Abs(res.Metrics.FinalEquity-res.Metrics.InitialCash) > 1e-9 {
		t.Fatalf("final equity = %v, want untouched initial cash %v", res.Metrics.FinalEquity, res.Metrics.InitialCash)
	}
}
