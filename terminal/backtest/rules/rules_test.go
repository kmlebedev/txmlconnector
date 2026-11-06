package rules

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

var testStart = time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)

// --- fixtures ---------------------------------------------------------------

// closeSeries builds no-gap bars from closes: open is the previous close and
// high/low are the extremes of open and close.
func closeSeries(closes []float64) []backtest.Bar {
	bars := make([]backtest.Bar, len(closes))
	for i, c := range closes {
		open := c
		if i > 0 {
			open = closes[i-1]
		}
		high, low := open, c
		if c > open {
			high = c
		} else {
			low = c
		}
		bars[i] = backtest.Bar{
			Time:   testStart.Add(time.Duration(i) * 5 * time.Minute),
			Open:   open,
			High:   high,
			Low:    low,
			Close:  c,
			Volume: 1000 + int64(i),
		}
	}
	return bars
}

// publicationCloses is the deterministic series of the publication example:
// long up legs (that keep close above SMA200) followed by sharp down legs that
// drive Wilder's RSI below 20, so the rsi_example.json fixture trades.
func publicationCloses() []float64 {
	closes := []float64{100}
	for cycle := 0; cycle < 3; cycle++ {
		for i := 0; i < 80; i++ {
			closes = append(closes, closes[len(closes)-1]*1.003)
		}
		for i := 0; i < 20; i++ {
			closes = append(closes, closes[len(closes)-1]*0.99)
		}
		for i := 0; i < 80; i++ {
			closes = append(closes, closes[len(closes)-1]*1.003)
		}
	}
	return closes
}

func publicationBars() []backtest.Bar { return closeSeries(publicationCloses()) }

func cloneBars(bars []backtest.Bar) []backtest.Bar {
	out := make([]backtest.Bar, len(bars))
	copy(out, bars)
	return out
}

func loadFixture(t *testing.T) Spec {
	t.Helper()
	raw, err := os.ReadFile("testdata/rsi_example.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return spec
}

func mustCompile(t *testing.T, spec Spec) *Strategy {
	t.Helper()
	strategy, err := Compile(spec)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	compiled, ok := strategy.(*Strategy)
	if !ok {
		t.Fatalf("Compile returned %T, want *Strategy", strategy)
	}
	return compiled
}

// --- reference implementations (independent of the package code) -----------

// referenceRSI is Wilder's RSI with the classic seed: the first value at index
// period is the mean of the first period changes; the rest is Wilder-smoothed.
// NaN marks the warm-up bars.
func referenceRSI(closes []float64, period int) []float64 {
	out := make([]float64, len(closes))
	for i := range out {
		out[i] = math.NaN()
	}
	if len(closes) <= period {
		return out
	}
	var gain, loss float64
	for j := 1; j <= period; j++ {
		change := closes[j] - closes[j-1]
		if change > 0 {
			gain += change
		} else {
			loss -= change
		}
	}
	avgGain, avgLoss := gain/float64(period), loss/float64(period)
	out[period] = referenceRSIValue(avgGain, avgLoss)
	for j := period + 1; j < len(closes); j++ {
		change := closes[j] - closes[j-1]
		var up, down float64
		if change > 0 {
			up = change
		} else {
			down = -change
		}
		avgGain = (avgGain*float64(period-1) + up) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + down) / float64(period)
		out[j] = referenceRSIValue(avgGain, avgLoss)
	}
	return out
}

func referenceRSIValue(avgGain, avgLoss float64) float64 {
	switch {
	case avgLoss == 0 && avgGain == 0:
		return 50
	case avgLoss == 0:
		return 100
	}
	return 100 - 100/(1+avgGain/avgLoss)
}

func referenceSMA(closes []float64, period int) []float64 {
	out := make([]float64, len(closes))
	for i := range out {
		out[i] = math.NaN()
	}
	var sum float64
	for i, c := range closes {
		sum += c
		if i >= period {
			sum -= closes[i-period]
		}
		if i >= period-1 {
			out[i] = sum / float64(period)
		}
	}
	return out
}

// --- TestRSIWilderMatchesReference (plan Task 23, step 1) -------------------

// TestRSIWilderMatchesReference pins the Wilder RSI(14) formula to the classic
// reference series (the values below are the canonical TTR/quantmod RSI
// example) with a 1e-9 tolerance.
func TestRSIWilderMatchesReference(t *testing.T) {
	closes := []float64{
		44.34, 44.09, 44.15, 43.61, 44.33, 44.83, 45.10, 45.42, 45.84, 46.08,
		45.89, 46.03, 45.61, 46.28, 46.28, 46.00, 46.03, 46.41, 46.22, 45.64,
		46.21, 46.25, 45.71, 46.45, 45.78, 45.35, 44.03, 44.18, 44.22, 44.57,
		43.42, 42.66, 43.13,
	}
	want := map[int]float64{
		14: 70.46413502109705,
		15: 66.24961855355505,
		16: 66.48094183471265,
		17: 69.34685316290866,
		18: 66.29471265892624,
		19: 57.91502067008556,
		20: 62.880718309962404,
		21: 63.20878871828778,
		22: 56.01158478954757,
		23: 62.33992931089789,
		24: 54.67097137765516,
		25: 50.386815195114224,
		26: 40.01942379131357,
		27: 41.49263540422282,
		28: 41.90242967845811,
		29: 45.499497238680405,
		30: 37.322778313379956,
		31: 33.09048257272339,
		32: 37.788771982057824,
	}
	spec := Spec{
		Indicators: map[string]Indicator{"rsi": {Kind: KindRSIWilder, Period: 14}},
		Entry:      []Condition{{Left: "rsi", Op: OpGT, Right: "0"}},
		Exit:       []Condition{{Left: "rsi", Op: OpGT, Right: "1000"}},
	}
	strategy := mustCompile(t, spec)
	bars := closeSeries(closes)
	for i := range bars {
		strategy.prepare(bars, i)
		ind := strategy.inds["rsi"]
		got, ok := ind.at(i)
		wantValue, wantOK := want[i]
		if !wantOK {
			if ok && i < 14 {
				t.Fatalf("RSI[%d] = %v, want warm-up NaN", i, got)
			}
			continue
		}
		if !ok {
			t.Fatalf("RSI[%d] is not defined, want %v", i, wantValue)
		}
		if math.Abs(got-wantValue) > 1e-9 {
			t.Fatalf("RSI[%d] = %.12f, want %.12f", i, got, wantValue)
		}
	}
}

// --- TestRulesPublicationExampleSignals (plan Task 23, step 1) --------------

// TestRulesPublicationExampleSignals loads testdata/rsi_example.json (RSI(14)
// scaled to 0..1, entry rsi < 0.2, exit rsi > 0.8, filter close > SMA200) and
// asserts every entry happens only when the reference RSI is below 20 and the
// strategy is flat, and every exit only when the reference RSI is above 80.
func TestRulesPublicationExampleSignals(t *testing.T) {
	spec := loadFixture(t)
	if got := spec.Indicators["rsi"]; got.Kind != KindRSIWilder || got.Period != 14 || got.Scale != 0.01 {
		t.Fatalf("fixture rsi indicator = %+v", got)
	}
	if got := spec.Indicators["sma200"]; got.Kind != KindSMA || got.Period != 200 {
		t.Fatalf("fixture sma200 indicator = %+v", got)
	}

	closes := publicationCloses()
	bars := closeSeries(closes)
	rsi := referenceRSI(closes, 14)
	sma200 := referenceSMA(closes, 200)

	strategy := mustCompile(t, spec)
	var entries, exits []int
	inPosition := false
	for i := range bars {
		switch action := strategy.OnBar(bars, i); action {
		case backtest.Buy:
			entries = append(entries, i)
			if inPosition {
				t.Fatalf("bar %d: Buy while a position is already open", i)
			}
			if !(rsi[i]/100 < 0.2) {
				t.Fatalf("bar %d: Buy with reference RSI %.6f >= 20", i, rsi[i])
			}
			if !(closes[i] > sma200[i]) {
				t.Fatalf("bar %d: Buy while close %.4f <= SMA200 %.4f", i, closes[i], sma200[i])
			}
			inPosition = true
		case backtest.Sell:
			exits = append(exits, i)
			if !inPosition {
				t.Fatalf("bar %d: Sell without an open position", i)
			}
			if !(rsi[i]/100 > 0.8) {
				t.Fatalf("bar %d: Sell with reference RSI %.6f <= 80", i, rsi[i])
			}
			inPosition = false
		}
	}
	if len(entries) == 0 || len(exits) == 0 {
		t.Fatalf("fixture produced no round trips: entries=%v exits=%v", entries, exits)
	}
	if len(entries) != len(exits) {
		t.Fatalf("entries=%v exits=%v are not paired", entries, exits)
	}
	for k := range entries {
		if entries[k] >= exits[k] {
			t.Fatalf("entry %d is not before its exit %d", entries[k], exits[k])
		}
	}

	// The same rules must produce the same round trips in the engine.
	result := runRules(t, spec, bars)
	if result.Metrics.Trades != len(entries) {
		t.Fatalf("engine trades = %d, want %d", result.Metrics.Trades, len(entries))
	}
	if result.Strategy != ID {
		t.Fatalf("result strategy = %q, want %q", result.Strategy, ID)
	}
}

// runRules compiles the spec into a fresh strategy and replays the bars through
// the frozen backtest engine.
func runRules(t *testing.T, spec Spec, bars []backtest.Bar) backtest.Result {
	t.Helper()
	strategy, err := Compile(spec)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	result, err := backtest.RunStrategy(backtest.Request{
		Symbol:      "TEST",
		PeriodSec:   300,
		Bars:        bars,
		Strategy:    strategy.ID(),
		InitialCash: 1_000_000,
	}, strategy)
	if err != nil {
		t.Fatalf("RunStrategy: %v", err)
	}
	return result
}

// --- TestRulesIgnorePartialBar (Review Focus 7) -----------------------------

// TestRulesIgnorePartialBar pins the closed-bars-only rule: a bar marked
// Partial is never handed to the strategy and is dropped from the replay, so an
// unclosed candle can neither produce a signal nor change the metrics.
func TestRulesIgnorePartialBar(t *testing.T) {
	spec := loadFixture(t)
	bars := publicationBars()

	// 1. The strategy itself must refuse to decide on a partial bar. Find the
	// first bar that normally opens a position.
	probe := mustCompile(t, spec)
	firstBuy := -1
	for i := range bars {
		if probe.OnBar(bars, i) == backtest.Buy {
			firstBuy = i
			break
		}
	}
	if firstBuy <= 0 || firstBuy >= len(bars)-1 {
		t.Fatalf("fixture has no usable entry bar (firstBuy=%d)", firstBuy)
	}
	withPartial := cloneBars(bars)
	withPartial[firstBuy].Partial = true
	// Walk both strategies in lockstep up to the decision bar: the partial one
	// must stay flat and hold, the closed one must buy.
	closedRun := mustCompile(t, spec)
	partialRun := mustCompile(t, spec)
	for i := 0; i <= firstBuy; i++ {
		closedAction := closedRun.OnBar(bars, i)
		partialAction := partialRun.OnBar(withPartial, i)
		if i < firstBuy && closedAction != partialAction {
			t.Fatalf("bar %d: action changed by a future partial bar: %v vs %v", i, closedAction, partialAction)
		}
		if i == firstBuy {
			if closedAction != backtest.Buy {
				t.Fatalf("bar %d: closed bar action = %v, want Buy", i, closedAction)
			}
			if partialAction != backtest.Hold {
				t.Fatalf("bar %d: partial bar action = %v, want Hold", i, partialAction)
			}
		}
	}

	// 2. The engine must drop a trailing partial bar: the replay of the closed
	// series and of the same series plus a wild unclosed candle are identical.
	closed := publicationBars()
	unclosed := cloneBars(closed)
	wild := closed[len(closed)-1].Time.Add(5 * time.Minute)
	unclosed = append(unclosed, backtest.Bar{
		Time:    wild,
		Open:    closed[len(closed)-1].Close,
		High:    closed[len(closed)-1].Close * 2,
		Low:     closed[len(closed)-1].Close / 2,
		Close:   closed[len(closed)-1].Close * 0.5,
		Volume:  1,
		Partial: true,
	})
	closedResult := runRules(t, spec, closed)
	partialResult := runRules(t, spec, unclosed)
	if !reflect.DeepEqual(closedResult.Metrics, partialResult.Metrics) {
		t.Fatalf("metrics changed by the trailing partial bar:\nclosed  %+v\npartial %+v",
			closedResult.Metrics, partialResult.Metrics)
	}
	if !reflect.DeepEqual(closedResult.Trades, partialResult.Trades) {
		t.Fatalf("trades changed by the trailing partial bar:\nclosed  %+v\npartial %+v",
			closedResult.Trades, partialResult.Trades)
	}
	if !reflect.DeepEqual(closedResult.Equity, partialResult.Equity) {
		t.Fatal("equity changed by the trailing partial bar")
	}
	if !hasWarning(partialResult.Warnings, "незакрыт") {
		t.Fatalf("warnings = %v, want a dropped-partial-bar warning", partialResult.Warnings)
	}
}

// --- TestCompileRejectsUnknownIndicator (plan Task 23, step 1) --------------

// TestCompileRejectsUnknownIndicator proves Compile validates the names it is
// given with errors a model can act on.
func TestCompileRejectsUnknownIndicator(t *testing.T) {
	base := func() Spec {
		return Spec{
			Indicators: map[string]Indicator{"rsi": {Kind: KindRSIWilder, Period: 14, Scale: 0.01}},
			Entry:      []Condition{{Left: "rsi", Op: OpLT, Right: "0.2"}},
			Exit:       []Condition{{Left: "rsi", Op: OpGT, Right: "0.8"}},
		}
	}
	cases := []struct {
		name   string
		mutate func(*Spec)
		want   string
	}{
		{"unknown kind", func(s *Spec) { s.Indicators["rsi"] = Indicator{Kind: "supertrend", Period: 14} }, "supertrend"},
		{"unknown operand", func(s *Spec) { s.Entry[0].Left = "vwap" }, "vwap"},
		{"unknown operator", func(s *Spec) { s.Entry[0].Op = "==" }, "=="},
		{"zero period", func(s *Spec) { s.Indicators["rsi"] = Indicator{Kind: KindRSIWilder, Period: 0} }, "period"},
		{"unknown source", func(s *Spec) {
			s.Indicators["rsi"] = Indicator{Kind: KindSMA, Period: 14, Source: "typical"}
		}, "typical"},
		{"unsupported side", func(s *Spec) { s.Side = "short" }, "short"},
		{"empty entry", func(s *Spec) { s.Entry = nil }, "entry"},
		{"empty exit", func(s *Spec) { s.Exit = nil }, "exit"},
		{"negative scale", func(s *Spec) {
			s.Indicators["rsi"] = Indicator{Kind: KindRSIWilder, Period: 14, Scale: -1}
		}, "scale"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := base()
			tc.mutate(&spec)
			_, err := Compile(spec)
			if err == nil {
				t.Fatalf("Compile accepted an invalid spec (%s)", tc.name)
			}
			if !strings.HasPrefix(err.Error(), "rules: ") {
				t.Fatalf("error %q does not start with \"rules: \"", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// --- TestRulesNoLookahead (plan Task 23, step 1) ---------------------------

// TestRulesNoLookahead proves the decision on bar i depends only on bars[0..i]:
// poisoning every later bar does not change any action, and replaying a shorter
// prefix reproduces the same transcript.
func TestRulesNoLookahead(t *testing.T) {
	spec := loadFixture(t)
	bars := publicationBars()
	const cut = 300

	poisoned := cloneBars(bars)
	for i := cut + 1; i < len(poisoned); i++ {
		poisoned[i].Open *= 1.5
		poisoned[i].High *= 1.5
		poisoned[i].Low *= 1.5
		poisoned[i].Close *= 1.5
		poisoned[i].Volume *= 10
	}
	clean := mustCompile(t, spec)
	dirty := mustCompile(t, spec)
	for i := 0; i <= cut; i++ {
		if got, want := dirty.OnBar(poisoned, i), clean.OnBar(bars, i); got != want {
			t.Fatalf("bar %d: action changed by future bars: %v vs %v", i, got, want)
		}
	}

	prefix := bars[:cut+1]
	full := mustCompile(t, spec)
	short := mustCompile(t, spec)
	for i := 0; i <= cut; i++ {
		if got, want := short.OnBar(prefix, i), full.OnBar(bars, i); got != want {
			t.Fatalf("bar %d: action differs on the prefix replay: %v vs %v", i, got, want)
		}
	}
}

// --- indicator and operator coverage ---------------------------------------

// symmetricBars builds bars with High=Close+0.5 and Low=Close-0.5, which makes
// the hand-computed indicator expectations below easy to follow.
func symmetricBars(closes []float64) []backtest.Bar {
	bars := make([]backtest.Bar, len(closes))
	for i, c := range closes {
		bars[i] = backtest.Bar{
			Time:   testStart.Add(time.Duration(i) * 5 * time.Minute),
			Open:   c,
			High:   c + 0.5,
			Low:    c - 0.5,
			Close:  c,
			Volume: int64(100 + i),
		}
	}
	return bars
}

// TestIndicatorsMatchReferences checks every supported indicator kind against
// hand-computed values on a simple series.
func TestIndicatorsMatchReferences(t *testing.T) {
	closes := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	bars := symmetricBars(closes)

	spec := Spec{
		Indicators: map[string]Indicator{
			"sma3":      {Kind: KindSMA, Period: 3},
			"ema3":      {Kind: KindEMA, Period: 3},
			"roc3":      {Kind: KindROC, Period: 3},
			"donHigh3":  {Kind: KindDonchianHigh, Period: 3},
			"donLow3":   {Kind: KindDonchianLow, Period: 3},
			"atr2":      {Kind: KindATR, Period: 2},
			"volSma3":   {Kind: KindSMA, Period: 3, Source: SourceVolume},
			"scaledRoc": {Kind: KindROC, Period: 3, Scale: 0.01},
		},
		Entry: []Condition{{Left: "sma3", Op: OpGT, Right: "0"}},
		Exit:  []Condition{{Left: "sma3", Op: OpLT, Right: "0"}},
	}
	strategy := mustCompile(t, spec)

	value := func(name string, i int) (float64, bool) {
		t.Helper()
		strategy.prepare(bars, i)
		return strategy.inds[name].at(i)
	}
	check := func(name string, i int, want float64) {
		t.Helper()
		got, ok := value(name, i)
		if !ok {
			t.Fatalf("%s[%d] is not defined, want %v", name, i, want)
		}
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s[%d] = %.12f, want %.12f", name, i, got, want)
		}
	}

	// SMA(3): mean of closes 1..3 = 2 at index 2, mean of 8..10 = 9 at index 9.
	check("sma3", 2, 2)
	check("sma3", 9, 9)
	if _, ok := value("sma3", 1); ok {
		t.Fatal("sma3 is defined during the warm-up")
	}
	// EMA(3): seeded with SMA(3)=2 at index 2, then alpha=0.5: 3, 4, ...
	check("ema3", 2, 2)
	check("ema3", 3, 3)
	check("ema3", 9, 9)
	// ROC(3) in percent: (close[i]/close[i-3] - 1) * 100.
	check("roc3", 3, 300)
	check("roc3", 9, (10.0/7.0-1)*100)
	check("scaledRoc", 3, 3)
	// Donchian channel of the previous 3 bars (high/low +/-0.5).
	check("donHigh3", 3, 3.5)
	check("donLow3", 3, 0.5)
	check("donHigh3", 9, 9.5)
	check("donLow3", 9, 6.5)
	// ATR(2): TR is max(high-low, |high-prevClose|, |low-prevClose|) = 1.5 on a
	// series that grows by 1 per bar; the first value at index 2 averages TR(1)
	// and TR(2).
	check("atr2", 2, 1.5)
	check("atr2", 9, 1.5)
	// Volume source: volume(i) = 100 + i, so SMA(3) at index 9 is 108.
	check("volSma3", 9, 108)
}

// TestRulesCrossOperators covers cross_above / cross_below.
func TestRulesCrossOperators(t *testing.T) {
	bars := closeSeries([]float64{99, 101, 99})
	up := mustCompile(t, Spec{
		Entry: []Condition{{Left: "close", Op: OpCrossAbove, Right: "100"}},
		Exit:  []Condition{{Left: "close", Op: OpCrossBelow, Right: "100"}},
	})
	if got := up.OnBar(bars, 0); got != backtest.Hold {
		t.Fatalf("OnBar(0) = %v, want Hold before a previous bar exists", got)
	}
	if got := up.OnBar(bars, 1); got != backtest.Buy {
		t.Fatalf("OnBar(1) = %v, want Buy on a cross above", got)
	}
	if got := up.OnBar(bars, 2); got != backtest.Sell {
		t.Fatalf("OnBar(2) = %v, want Sell on a cross below", got)
	}

	down := mustCompile(t, Spec{
		Entry: []Condition{{Left: "close", Op: OpCrossBelow, Right: "100"}},
		Exit:  []Condition{{Left: "close", Op: OpCrossAbove, Right: "100"}},
	})
	downBars := closeSeries([]float64{101, 99})
	if got := down.OnBar(downBars, 1); got != backtest.Buy {
		t.Fatalf("cross below entry action = %v, want Buy", got)
	}
}

// TestRulesStopLossTakeProfit covers the optional stop-loss / take-profit
// fields of the Spec.
func TestRulesStopLossTakeProfit(t *testing.T) {
	entry := []Condition{{Left: "close", Op: OpGE, Right: "100"}}
	never := []Condition{{Left: "close", Op: OpGT, Right: "1000000"}}

	stop := mustCompile(t, Spec{
		Entry: entry, Exit: never, StopLossPct: 5,
	})
	stopBars := closeSeries([]float64{100, 100, 94, 94})
	if got := stop.OnBar(stopBars, 0); got != backtest.Buy {
		t.Fatalf("stop: entry action = %v, want Buy", got)
	}
	if got := stop.OnBar(stopBars, 1); got != backtest.Hold {
		t.Fatalf("stop: action above the stop = %v, want Hold", got)
	}
	if got := stop.OnBar(stopBars, 2); got != backtest.Sell {
		t.Fatalf("stop: action after a 6%% drop = %v, want Sell", got)
	}
	if got := stop.OnBar(stopBars, 3); got != backtest.Hold {
		t.Fatalf("stop: action while flat = %v, want Hold", got)
	}

	take := mustCompile(t, Spec{
		Entry: entry, Exit: never, TakeProfitPct: 5,
	})
	takeBars := closeSeries([]float64{100, 101, 106})
	if got := take.OnBar(takeBars, 0); got != backtest.Buy {
		t.Fatalf("take: entry action = %v, want Buy", got)
	}
	if got := take.OnBar(takeBars, 1); got != backtest.Hold {
		t.Fatalf("take: action below the target = %v, want Hold", got)
	}
	if got := take.OnBar(takeBars, 2); got != backtest.Sell {
		t.Fatalf("take: action at the target = %v, want Sell", got)
	}
}

// TestRulesParamsHash proves the Spec is summarized deterministically in the
// frozen Result.Params map.
func TestRulesParamsHash(t *testing.T) {
	spec := loadFixture(t)
	first := mustCompile(t, spec)
	second := mustCompile(t, spec)
	withExplicitSide := loadFixture(t)
	withExplicitSide.Side = SideLong
	normalized := mustCompile(t, withExplicitSide)

	hash := func(s *Strategy) float64 {
		params := s.Params()
		if len(params) != 1 {
			t.Fatalf("Params() = %v, want exactly the spec hash", params)
		}
		value, ok := params[ParamSpecHash]
		if !ok {
			t.Fatalf("Params() = %v, want the %q key", params, ParamSpecHash)
		}
		return value
	}
	if got, want := hash(second), hash(first); got != want {
		t.Fatalf("same spec hashed differently: %v vs %v", got, want)
	}
	if got, want := hash(normalized), hash(first); got != want {
		t.Fatalf("implicit and explicit long sides hashed differently: %v vs %v", got, want)
	}
	if hash(first) == 0 {
		t.Fatal("spec hash is zero")
	}

	other := loadFixture(t)
	other.Indicators["rsi"] = Indicator{Kind: KindRSIWilder, Period: 21, Scale: 0.01}
	if got, want := hash(mustCompile(t, other)), hash(first); got == want {
		t.Fatalf("different specs share the hash %v", got)
	}
	if spec := first.Spec(); spec.Indicators["rsi"].Period != 14 || spec.Side != SideLong {
		t.Fatalf("Spec() = %+v, want the normalized copy", spec)
	}
}

// --- helpers ----------------------------------------------------------------

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
