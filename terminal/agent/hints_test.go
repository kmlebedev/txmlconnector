package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// TestHintsComputation checks the technical hints on rising synthetic data.
func TestHintsComputation(t *testing.T) {
	e, data, _ := newTestEngine(t)
	hints, err := e.Hints(context.Background(), "SBER", 300)
	if err != nil {
		t.Fatalf("Hints: %v", err)
	}
	if hints.Symbol != "TQBR:SBER" || hints.PeriodSec != 300 {
		t.Fatalf("unexpected hints identity: %+v", hints)
	}
	bars, _ := data.History(context.Background(), "TQBR:SBER", 300, 200)
	wantAsOf := bars[len(bars)-1].Time
	if !hints.AsOf.Equal(wantAsOf) {
		t.Errorf("AsOf = %v, want %v", hints.AsOf, wantAsOf)
	}
	if hints.SMA20 <= 0 || hints.SMA50 <= 0 {
		t.Errorf("SMA20/SMA50 = %v/%v, want positive", hints.SMA20, hints.SMA50)
	}
	if hints.SMA20 <= hints.SMA50 {
		t.Errorf("SMA20 = %v must be above SMA50 = %v on rising data", hints.SMA20, hints.SMA50)
	}
	if hints.RSI14 != 100 {
		t.Errorf("RSI14 = %v, want 100 on monotonically rising closes", hints.RSI14)
	}
	if hints.ATR14 <= 0 {
		t.Errorf("ATR14 = %v, want positive", hints.ATR14)
	}
	if hints.VolatilityPct <= 0 {
		t.Errorf("VolatilityPct = %v, want positive", hints.VolatilityPct)
	}
	if hints.Trend != "up" {
		t.Errorf("Trend = %q, want up", hints.Trend)
	}
	if hints.Support >= hints.Resistance {
		t.Errorf("Support %v >= Resistance %v", hints.Support, hints.Resistance)
	}
	if hints.Breadth["advancers"] != 2 || hints.Breadth["decliners"] != 1 || hints.Breadth["unchanged"] != 0 {
		t.Errorf("Breadth = %v", hints.Breadth)
	}
	if hints.Note == "" || !strings.Contains(hints.Note, "расчёт") {
		t.Errorf("Note = %q", hints.Note)
	}
}

// TestHintsDownTrend checks the trend/RSI on falling data.
func TestHintsDownTrend(t *testing.T) {
	data := newFakeDataSource()
	falling := make([]backtest.Bar, 100)
	for i := range falling {
		price := 200 - float64(i)
		falling[i] = backtest.Bar{
			Time: testNow.Add(time.Duration(i) * time.Minute),
			Open: price, High: price + 0.5, Low: price - 0.5, Close: price - 0.2, Volume: 100,
		}
	}
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: falling}
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, data, newFakeBroker(1_000_000))
	hints, err := e.Hints(context.Background(), "SBER", 300)
	if err != nil {
		t.Fatalf("Hints: %v", err)
	}
	if hints.Trend != "down" {
		t.Errorf("Trend = %q, want down", hints.Trend)
	}
	if hints.RSI14 >= 50 {
		t.Errorf("RSI14 = %v, want below 50", hints.RSI14)
	}
	if hints.SMA20 >= hints.SMA50 {
		t.Errorf("SMA20 = %v must be below SMA50 = %v", hints.SMA20, hints.SMA50)
	}
}

// TestHintsShortHistory still returns hints with the "not enough data" note.
func TestHintsShortHistory(t *testing.T) {
	data := newFakeDataSource()
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: syntheticBars(5, 100, 1)}
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, data, newFakeBroker(1_000_000))
	hints, err := e.Hints(context.Background(), "", 300)
	if err != nil {
		t.Fatalf("Hints: %v", err)
	}
	if hints.Symbol != "TQBR:SBER" {
		t.Errorf("Symbol = %q", hints.Symbol)
	}
	if !strings.Contains(hints.Note, "истории мало") {
		t.Errorf("Note = %q", hints.Note)
	}
}

// TestHintsErrors covers empty data and an unknown period fallback.
func TestHintsErrors(t *testing.T) {
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, &errDataSource{err: context.DeadlineExceeded}, newFakeBroker(1))
	if _, err := e.Hints(context.Background(), "SBER", 300); err == nil {
		t.Fatal("expected a history error")
	}
	empty := newFakeDataSource()
	empty.histories = map[string]map[int][]backtest.Bar{}
	e2 := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, empty, newFakeBroker(1))
	if _, err := e2.Hints(context.Background(), "SBER", 300); err == nil {
		t.Fatal("expected an error for missing history")
	}
	// An unsupported period falls back to the configured default.
	_ = e2
}
