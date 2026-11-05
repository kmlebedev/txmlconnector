package research

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
)

var testStart = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// --- fixtures ---------------------------------------------------------------

// sineBars builds deterministic oscillating bars with the given spacing. The
// oscillation produces many SMA crosses, so the backtest has a real sample.
func sineBars(n int, step time.Duration, base float64) []backtest.Bar {
	bars := make([]backtest.Bar, n)
	for i := 0; i < n; i++ {
		closePrice := base + 10*math.Sin(float64(i)/6.0) + 0.01*float64(i)
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
			Time:   testStart.Add(time.Duration(i) * step),
			Open:   open,
			High:   high,
			Low:    low,
			Close:  closePrice,
			Volume: int64(1000 + i),
		}
	}
	return bars
}

func flatHistory(series map[string][]backtest.Bar) HistoryFunc {
	return func(_ context.Context, symbol string, _, _ int) ([]backtest.Bar, error) {
		bars, ok := series[symbol]
		if !ok {
			return nil, fmt.Errorf("no history for %s", symbol)
		}
		out := make([]backtest.Bar, len(bars))
		copy(out, bars)
		return out, nil
	}
}

func hasWarning(warnings []string, substr string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, substr) {
			return true
		}
	}
	return false
}

func rowBySymbol(t *testing.T, rows []BatchRow, symbol string) BatchRow {
	t.Helper()
	for _, row := range rows {
		if row.Symbol == symbol {
			return row
		}
	}
	t.Fatalf("row for %q is missing: %+v", symbol, rows)
	return BatchRow{}
}

// --- TestBatchBacktestFlagsSmallSample (Review Focus 6) --------------------

// TestBatchBacktestFlagsSmallSample proves the sample-size warning: fewer than
// 30 trades, fewer than 5 years of daily history or fewer than 1 year of
// intraday history are all reported as small_sample.
func TestBatchBacktestFlagsSmallSample(t *testing.T) {
	const hourly = 3600
	const daily = 86400
	params := map[string]float64{"fast": 2, "slow": 3}
	small := map[string][]backtest.Bar{
		"TQBR:FEWTRADES": sineBars(50, 24*time.Hour, 100), // ~1% of the required daily span
	}

	// Too few trades on a very long daily history: the position is never opened.
	neverRules := rules.Spec{
		Entry: []rules.Condition{{Left: "close", Op: rules.OpGT, Right: "100000000"}},
		Exit:  []rules.Condition{{Left: "close", Op: rules.OpLT, Right: "0"}},
	}
	hist := histogram(small)
	rows, err := Batch(context.Background(), hist, BatchRequest{
		Symbols:   []string{"TQBR:FEWTRADES"},
		PeriodSec: daily,
		Strategy:  "sma_cross",
		Params:    params,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !hasWarning(rows[0].Warnings, backtest.WarningSmallSample) {
		t.Fatalf("short history warnings = %v, want %s", rows[0].Warnings, backtest.WarningSmallSample)
	}

	longDaily := sineBars(2000, 24*time.Hour, 100) // ~5.5 years, many SMA crosses
	rows, err = Batch(context.Background(), histogram(map[string][]backtest.Bar{"TQBR:SBER": longDaily}), BatchRequest{
		Symbols:   []string{"TQBR:SBER"},
		PeriodSec: daily,
		Strategy:  "sma_cross",
		Params:    params,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if rows[0].Trades < 30 {
		t.Fatalf("long daily fixture has %d trades, the test would be vacuous", rows[0].Trades)
	}
	if hasWarning(rows[0].Warnings, backtest.WarningSmallSample) {
		t.Fatalf("large daily sample warned: %v", rows[0].Warnings)
	}

	shortIntraday := sineBars(500, time.Hour, 100) // ~20 days
	rows, err = Batch(context.Background(), histogram(map[string][]backtest.Bar{"TQBR:GAZP": shortIntraday}), BatchRequest{
		Symbols:   []string{"TQBR:GAZP"},
		PeriodSec: hourly,
		Strategy:  "sma_cross",
		Params:    params,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !hasWarning(rows[0].Warnings, backtest.WarningSmallSample) {
		t.Fatalf("short intraday history warnings = %v, want %s", rows[0].Warnings, backtest.WarningSmallSample)
	}

	longIntraday := sineBars(11000, time.Hour, 100) // ~458 days, many SMA crosses
	rows, err = Batch(context.Background(), histogram(map[string][]backtest.Bar{"TQBR:LKOH": longIntraday}), BatchRequest{
		Symbols:   []string{"TQBR:LKOH"},
		PeriodSec: hourly,
		Strategy:  "sma_cross",
		Params:    params,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if rows[0].Trades < 30 {
		t.Fatalf("long intraday fixture has %d trades, the test would be vacuous", rows[0].Trades)
	}
	if hasWarning(rows[0].Warnings, backtest.WarningSmallSample) {
		t.Fatalf("large intraday sample warned: %v", rows[0].Warnings)
	}

	// A long history with no signals at all: trades < 30 dominates.
	rows, err = Batch(context.Background(), histogram(map[string][]backtest.Bar{"TQBR:YNDX": longDaily}), BatchRequest{
		Symbols:   []string{"TQBR:YNDX"},
		PeriodSec: daily,
		Rules:     &neverRules,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if rows[0].Trades != 0 {
		t.Fatalf("no-signal fixture traded %d times", rows[0].Trades)
	}
	if !hasWarning(rows[0].Warnings, backtest.WarningSmallSample) {
		t.Fatalf("no-signal warnings = %v, want %s", rows[0].Warnings, backtest.WarningSmallSample)
	}
}

// histogram adapts a symbol map to HistoryFunc.
func histogram(series map[string][]backtest.Bar) HistoryFunc { return flatHistory(series) }

// --- TestBatchBuyHoldMatchesFirstLastClose ---------------------------------

// TestBatchBuyHoldMatchesFirstLastClose pins the buy & hold baseline of a row:
// (lastClose/firstClose - 1) * 100, computed from the same closed bars.
func TestBatchBuyHoldMatchesFirstLastClose(t *testing.T) {
	series := map[string][]backtest.Bar{
		"TQBR:GAZP": sineBars(400, 24*time.Hour, 100),
		"TQBR:SBER": sineBars(400, 24*time.Hour, 250),
	}
	rows, err := Batch(context.Background(), histogram(series), BatchRequest{
		Symbols:   []string{"TQBR:GAZP", "TQBR:SBER"},
		PeriodSec: 86400,
		Strategy:  "sma_cross",
		Params:    map[string]float64{"fast": 2, "slow": 3},
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		bars := series[row.Symbol]
		first, last := bars[0].Close, bars[len(bars)-1].Close
		want := (last/first - 1) * 100
		if math.Abs(row.BuyHoldPct-want) > 1e-9 {
			t.Fatalf("%s: buyHoldPct = %.12f, want %.12f", row.Symbol, row.BuyHoldPct, want)
		}
		if !row.From.Equal(bars[0].Time) || !row.To.Equal(bars[len(bars)-1].Time) {
			t.Fatalf("%s: from/to = %v/%v, want %v/%v", row.Symbol, row.From, row.To, bars[0].Time, bars[len(bars)-1].Time)
		}
		if row.ReturnPct == 0 && row.Trades == 0 && row.MaxDrawdownPct == 0 {
			// A zero-trade row is allowed, but the fields must still be filled
			// by the engine rather than left untouched.
			if row.From.IsZero() {
				t.Fatalf("%s: row is not filled: %+v", row.Symbol, row)
			}
		}
	}
}

// --- TestBatchWarnsNoCosts --------------------------------------------------

// TestBatchWarnsNoCosts proves the cost warning appears when both cost fields
// are left at zero and disappears when the caller passes explicit costs.
func TestBatchWarnsNoCosts(t *testing.T) {
	hist := histogram(map[string][]backtest.Bar{"TQBR:SBER": sineBars(400, 24*time.Hour, 100)})
	rows, err := Batch(context.Background(), hist, BatchRequest{
		Symbols:   []string{"TQBR:SBER"},
		PeriodSec: 86400,
		Strategy:  "sma_cross",
		Params:    map[string]float64{"fast": 2, "slow": 3},
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !hasWarning(rows[0].Warnings, backtest.WarningNoCosts) {
		t.Fatalf("warnings = %v, want %s", rows[0].Warnings, backtest.WarningNoCosts)
	}

	rows, err = Batch(context.Background(), hist, BatchRequest{
		Symbols:     []string{"TQBR:SBER"},
		PeriodSec:   86400,
		Strategy:    "sma_cross",
		Params:      map[string]float64{"fast": 2, "slow": 3},
		FeeBps:      5,
		SlippageBps: 1,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if hasWarning(rows[0].Warnings, backtest.WarningNoCosts) {
		t.Fatalf("explicit costs warned: %v", rows[0].Warnings)
	}
}

// --- TestBatchContinuesOnOneSymbolError ------------------------------------

// TestBatchContinuesOnOneSymbolError proves one failing ticker produces a row
// with an error warning while the other tickers are still computed, in the
// requested order.
func TestBatchContinuesOnOneSymbolError(t *testing.T) {
	series := map[string][]backtest.Bar{
		"TQBR:GAZP": sineBars(400, 24*time.Hour, 100),
		"TQBR:SBER": sineBars(400, 24*time.Hour, 250),
	}
	hist := func(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error) {
		if symbol == "TQBR:FAIL" {
			return nil, errors.New("backend unavailable")
		}
		return flatHistory(series)(ctx, symbol, periodSec, count)
	}
	rows, err := Batch(context.Background(), hist, BatchRequest{
		Symbols:   []string{"TQBR:GAZP", "TQBR:FAIL", "TQBR:SBER"},
		PeriodSec: 86400,
		Strategy:  "sma_cross",
		Params:    map[string]float64{"fast": 2, "slow": 3},
	})
	if err != nil {
		t.Fatalf("Batch returned an error for one failed symbol: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Symbol != "TQBR:GAZP" || rows[1].Symbol != "TQBR:FAIL" || rows[2].Symbol != "TQBR:SBER" {
		t.Fatalf("rows are not in the requested order: %+v", rows)
	}
	failed := rows[1]
	if !hasWarning(failed.Warnings, "error:") || failed.From.IsZero() != true {
		t.Fatalf("failed row = %+v, want only an error warning", failed)
	}
	for _, index := range []int{0, 2} {
		row := rows[index]
		if hasWarning(row.Warnings, "error:") {
			t.Fatalf("%s reported an error: %v", row.Symbol, row.Warnings)
		}
		if row.From.IsZero() || row.To.IsZero() {
			t.Fatalf("%s: from/to are empty: %+v", row.Symbol, row)
		}
	}
}

// TestBatchAllSymbolsFail proves a totally failed batch is an error, not a
// silent list of empty rows.
func TestBatchAllSymbolsFail(t *testing.T) {
	hist := func(context.Context, string, int, int) ([]backtest.Bar, error) {
		return nil, errors.New("down")
	}
	if _, err := Batch(context.Background(), hist, BatchRequest{
		Symbols:   []string{"TQBR:GAZP", "TQBR:SBER"},
		PeriodSec: 86400,
		Strategy:  "sma_cross",
	}); err == nil {
		t.Fatal("Batch succeeded although every symbol failed")
	}
}

// TestBatchRejectsInvalidRequest checks request validation.
func TestBatchRejectsInvalidRequest(t *testing.T) {
	hist := histogram(map[string][]backtest.Bar{"TQBR:SBER": sineBars(50, 24*time.Hour, 100)})
	valid := BatchRequest{Symbols: []string{"TQBR:SBER"}, PeriodSec: 86400, Strategy: "sma_cross"}
	cases := []struct {
		name   string
		mutate func(*BatchRequest)
	}{
		{"no symbols", func(r *BatchRequest) { r.Symbols = nil }},
		{"no strategy", func(r *BatchRequest) { r.Strategy = "" }},
		{"negative period", func(r *BatchRequest) { r.PeriodSec = -1 }},
		{"negative count", func(r *BatchRequest) { r.Count = -1 }},
		{"negative fee", func(r *BatchRequest) { r.FeeBps = -1 }},
		{"negative slippage", func(r *BatchRequest) { r.SlippageBps = -1 }},
		{"huge fee", func(r *BatchRequest) { r.FeeBps = 10000 }},
		{"unknown strategy", func(r *BatchRequest) { r.Strategy = "no_such_strategy" }},
		{"strategy and rules", func(r *BatchRequest) {
			r.Strategy = ""
			r.Rules = &rules.Spec{
				Entry: []rules.Condition{{Left: "close", Op: rules.OpGT, Right: "0"}},
				Exit:  []rules.Condition{{Left: "close", Op: rules.OpLT, Right: "0"}},
			}
			r.Strategy = "sma_cross"
		}},
		{"invalid rules", func(r *BatchRequest) {
			r.Strategy = ""
			r.Rules = &rules.Spec{
				Indicators: map[string]rules.Indicator{"rsi": {Kind: "supertrend", Period: 14}},
				Entry:      []rules.Condition{{Left: "rsi", Op: rules.OpLT, Right: "20"}},
				Exit:       []rules.Condition{{Left: "rsi", Op: rules.OpGT, Right: "80"}},
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			req.Symbols = append([]string(nil), valid.Symbols...)
			tc.mutate(&req)
			if _, err := Batch(context.Background(), hist, req); err == nil {
				t.Fatalf("Batch accepted an invalid request (%s)", tc.name)
			}
		})
	}
}

// TestBatchParallelButBounded proves the symbols are loaded in parallel while
// the number of concurrent history calls never exceeds GOMAXPROCS, and that the
// result keeps the requested order.
func TestBatchParallelButBounded(t *testing.T) {
	symbols := []string{"A", "B", "C", "D", "E", "F"}
	limit := int64(runtime.GOMAXPROCS(0))
	if limit < 1 {
		limit = 1
	}
	var active, peak int64
	bars := sineBars(200, 24*time.Hour, 100)
	hist := func(_ context.Context, symbol string, _, _ int) ([]backtest.Bar, error) {
		current := atomic.AddInt64(&active, 1)
		for {
			seen := atomic.LoadInt64(&peak)
			if current <= seen || atomic.CompareAndSwapInt64(&peak, seen, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt64(&active, -1)
		out := make([]backtest.Bar, len(bars))
		copy(out, bars)
		return out, nil
	}
	rows, err := Batch(context.Background(), hist, BatchRequest{
		Symbols:   symbols,
		PeriodSec: 86400,
		Strategy:  "sma_cross",
		Params:    map[string]float64{"fast": 2, "slow": 3},
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if got := atomic.LoadInt64(&peak); got > limit {
		t.Fatalf("peak parallel history calls = %d, want <= GOMAXPROCS %d", got, limit)
	}
	if got := atomic.LoadInt64(&peak); got < 2 && limit > 1 {
		t.Fatalf("peak parallel history calls = %d, want parallel execution", got)
	}
	for i, row := range rows {
		if row.Symbol != symbols[i] {
			t.Fatalf("rows[%d].Symbol = %q, want %q", i, row.Symbol, symbols[i])
		}
	}
}

// TestBatchBacktestIDStable proves BacktestID is deterministic and unique per
// symbol/strategy, so a bot can be created from a batch row.
func TestBatchBacktestIDStable(t *testing.T) {
	series := map[string][]backtest.Bar{
		"TQBR:GAZP": sineBars(400, 24*time.Hour, 100),
		"TQBR:SBER": sineBars(400, 24*time.Hour, 250),
	}
	req := BatchRequest{
		Symbols:   []string{"TQBR:GAZP", "TQBR:SBER"},
		PeriodSec: 86400,
		Strategy:  "sma_cross",
		Params:    map[string]float64{"fast": 2, "slow": 3},
	}
	first, err := Batch(context.Background(), histogram(series), req)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	second, err := Batch(context.Background(), histogram(series), req)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	for i := range first {
		if first[i].BacktestID == "" {
			t.Fatalf("%s: empty BacktestID", first[i].Symbol)
		}
		if !strings.HasPrefix(first[i].BacktestID, "bt_") {
			t.Fatalf("%s: BacktestID = %q, want the bt_ prefix", first[i].Symbol, first[i].BacktestID)
		}
		if first[i].BacktestID == first[1-i].BacktestID {
			t.Fatalf("BacktestID is not unique per symbol: %q", first[i].BacktestID)
		}
		if first[i].BacktestID != second[i].BacktestID {
			t.Fatalf("%s: BacktestID is not deterministic: %q vs %q",
				first[i].Symbol, first[i].BacktestID, second[i].BacktestID)
		}
	}
}

// TestBatchRulesPath proves a declarative rules.Spec can drive the batch.
func TestBatchRulesPath(t *testing.T) {
	spec := rules.Spec{
		Indicators: map[string]rules.Indicator{"sma3": {Kind: rules.KindSMA, Period: 3}},
		Entry:      []rules.Condition{{Left: "close", Op: rules.OpCrossAbove, Right: "sma3"}},
		Exit:       []rules.Condition{{Left: "close", Op: rules.OpCrossBelow, Right: "sma3"}},
	}
	hist := histogram(map[string][]backtest.Bar{"TQBR:SBER": sineBars(400, 24*time.Hour, 100)})
	rows, err := Batch(context.Background(), hist, BatchRequest{
		Symbols:   []string{"TQBR:SBER"},
		PeriodSec: 86400,
		Rules:     &spec,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if rows[0].Trades <= 0 || rows[0].BacktestID == "" {
		t.Fatalf("rules batch row = %+v, want trades and an ID", rows[0])
	}

	// The same spec hash must produce the same BacktestID; a different spec a
	// different one.
	other := spec
	other.Indicators["sma3"] = rules.Indicator{Kind: rules.KindSMA, Period: 5}
	otherRows, err := Batch(context.Background(), hist, BatchRequest{
		Symbols:   []string{"TQBR:SBER"},
		PeriodSec: 86400,
		Rules:     &other,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if otherRows[0].BacktestID == rows[0].BacktestID {
		t.Fatalf("different rules share the BacktestID %q", rows[0].BacktestID)
	}
}

// TestBatchCancelledContext proves a cancelled context stops the batch with an
// error instead of computing every symbol.
func TestBatchCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hist := histogram(map[string][]backtest.Bar{"TQBR:SBER": sineBars(50, 24*time.Hour, 100)})
	if _, err := Batch(ctx, hist, BatchRequest{
		Symbols:   []string{"TQBR:SBER"},
		PeriodSec: 86400,
		Strategy:  "sma_cross",
	}); err == nil {
		t.Fatal("Batch succeeded with a cancelled context")
	}
}

var _ = sync.Mutex{}
