package volatility_test

// Live ClickHouse integration test of the volatility oracle: the bars are
// synthetic (the demo market is the only live intraday source and is outside
// unit tests), but the releases and news come from the real point-in-time marts
// of the clickhouse-import-rosstat project. Run explicitly:
//
//	TXML_IT_CLICKHOUSE=1 TXML_CLICKHOUSE_URL=http://localhost:8123 \
//	  go test ./terminal/volatility -run Live -v

import (
	"context"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/chstore"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
	"github.com/kmlebedev/txmlconnector/terminal/volatility"
)

func liveVintage(t *testing.T) *vintage.ClickHouseStore {
	t.Helper()
	if os.Getenv("TXML_IT_CLICKHOUSE") != "1" {
		t.Skip("set TXML_IT_CLICKHOUSE=1 and TXML_CLICKHOUSE_URL to run the live checks")
	}
	raw := os.Getenv("TXML_CLICKHOUSE_URL")
	if raw == "" {
		raw = os.Getenv("CLICKHOUSE_URL")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		t.Skip("TXML_CLICKHOUSE_URL must be an HTTP ClickHouse endpoint")
	}
	return vintage.New(chstore.New(raw, os.Getenv("TXML_CLICKHOUSE_USER"), os.Getenv("TXML_CLICKHOUSE_PASSWORD"), nil))
}

// syntheticBars builds 90 complete trading days of 30 ten-minute bars each with
// a deterministic intraday random walk and a weekly volatility cycle.
func syntheticBars(end time.Time) []backtest.Bar {
	rng := rand.New(rand.NewSource(42))
	const barsPerDay = 30
	var bars []backtest.Bar
	price := 100.0
	for d := 0; d < 90; d++ {
		day := end.AddDate(0, 0, -(90 - d)).Truncate(24 * time.Hour)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		sigma := 0.0015 * (1 + 0.5*math.Sin(float64(d)/3))
		for b := 0; b < barsPerDay; b++ {
			ts := day.Add(8*time.Hour + time.Duration(b)*10*time.Minute)
			price *= math.Exp(rng.NormFloat64() * sigma)
			spread := price * sigma
			bars = append(bars, backtest.Bar{
				Time:   ts,
				Open:   price - spread,
				High:   price + spread,
				Low:    price - spread,
				Close:  price,
				Volume: 100 + int64(b),
			})
		}
	}
	return bars
}

// TestLiveOracleFitWithRealMacro checks Fit/Forecast against the real vintage
// releases and news of the rosstat layer: the model trains on the bars before
// asOf and returns a finite forecast with the documented regime.
func TestLiveOracleFitWithRealMacro(t *testing.T) {
	store := liveVintage(t)
	ctx := context.Background()
	// The news marts only exist for the recent feed history, so use a recent
	// asOf; the bars stay synthetic and deterministic for that date.
	asOf := time.Now().UTC().Truncate(24 * time.Hour)
	bars := syntheticBars(asOf)
	if len(bars) < 62*20 {
		t.Fatalf("synthetic bars = %d, want at least 62 complete days", len(bars))
	}
	releases, err := store.Releases(ctx, asOf.AddDate(0, 0, -120), asOf.AddDate(0, 0, 40))
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	news, err := store.News(ctx, asOf.AddDate(0, 0, -7), asOf)
	if err != nil {
		t.Fatalf("News: %v", err)
	}
	model, err := volatility.Fit(ctx, bars, releases, news, asOf)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	upcoming := releases
	synthetic := vintage.Release{At: asOf.Add(30 * time.Minute), Category: "cbr_rate", Title: "synthetic CBR decision", Country: "RU", Exact: true}
	upcoming = append(append([]vintage.Release(nil), releases...), synthetic)
	forecast := model.Forecast(asOf, time.Hour, upcoming, news)
	if math.IsNaN(forecast.SigmaPct) || math.IsInf(forecast.SigmaPct, 0) || forecast.SigmaPct <= 0 {
		t.Fatalf("SigmaPct = %v, want a positive finite value", forecast.SigmaPct)
	}
	if math.IsNaN(forecast.Multiplier) || math.IsInf(forecast.Multiplier, 0) || forecast.Multiplier <= 0 {
		t.Fatalf("Multiplier = %v, want a positive finite value", forecast.Multiplier)
	}
	switch forecast.Regime {
	case "calm", "normal", "event", "stress":
	default:
		t.Fatalf("regime = %q", forecast.Regime)
	}
	if len(forecast.Drivers) == 0 {
		t.Fatalf("forecast has no drivers: %+v", forecast)
	}
	t.Logf("forecast: sigma=%.3f%% multiplier=%.2f regime=%s drivers=%d releases=%d news=%d",
		forecast.SigmaPct, forecast.Multiplier, forecast.Regime, len(forecast.Drivers), len(releases), len(news))
}
