package volatility

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

// Synthetic market: 96 five-minute bars per trading day (07:00–15:00 UTC), a
// lognormal close series with a fixed per-bar volatility. Event days carry a
// much larger volatility, which is exactly the effect the oracle must learn.
const (
	testBarsPerDay = 96
	testBarStep    = 5 * time.Minute
	testBaseSigma  = 0.001
	testEventSigma = 0.004
)

// testBase is the first synthetic trading day (Monday 10:00 MSK).
func testBase() time.Time {
	return time.Date(2026, 1, 5, 7, 0, 0, 0, time.UTC)
}

// testDayStart is the UTC midnight of synthetic day index d.
func testDayStart(d int) time.Time {
	base := testBase()
	return time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, d)
}

// testEventDays returns the synthetic CBR decision days: every 14 days from 7.
func testEventDays(days int) map[int]bool {
	out := map[int]bool{}
	for d := 7; d < days; d += 14 {
		out[d] = true
	}
	return out
}

// syntheticBars builds the intraday series starting at synthetic day startDay;
// eventDays raises the per-bar sigma (so the daily realized variance is
// multiplied by 16 on those days), sigmaAfter overrides it for a single day.
func syntheticBars(days, startDay int, eventDays map[int]bool, sigmaAfter map[int]float64, seed int64) []backtest.Bar {
	rng := rand.New(rand.NewSource(seed))
	base := testBase()
	price := 100.0
	bars := make([]backtest.Bar, 0, days*testBarsPerDay)
	for d := 0; d < days; d++ {
		dayIndex := startDay + d
		start := base.AddDate(0, 0, dayIndex)
		sigma := testBaseSigma
		if eventDays[dayIndex] {
			sigma = testEventSigma
		}
		if value, ok := sigmaAfter[dayIndex]; ok {
			sigma = value
		}
		for i := 0; i < testBarsPerDay; i++ {
			price *= math.Exp(sigma * rng.NormFloat64())
			bars = append(bars, backtest.Bar{
				Time:   start.Add(time.Duration(i) * testBarStep),
				Open:   price,
				High:   price * 1.0005,
				Low:    price * 0.9995,
				Close:  price,
				Volume: 1000,
			})
		}
	}
	return bars
}

// syntheticReleases builds one CBR key-rate decision per event day, at the
// canonical 13:30 MSK (10:30 UTC).
func syntheticReleases(eventDays map[int]bool) []vintage.Release {
	releases := make([]vintage.Release, 0, len(eventDays))
	for d := 0; d < 200; d++ {
		if !eventDays[d] {
			continue
		}
		releases = append(releases, vintage.Release{
			At:       testDayStart(d).Add(10*time.Hour + 30*time.Minute),
			Category: "cbr_rate",
			Title:    "СД ЦБ РФ: заседание по ключевой ставке",
			Country:  "RU",
			Exact:    true,
		})
	}
	return releases
}

// syntheticNews builds a few annotated stories on the non-event days.
func syntheticNews(days int) []vintage.NewsItem {
	news := make([]vintage.NewsItem, 0, days/7)
	for d := 3; d < days; d += 7 {
		sentiment := -0.6
		if d%2 == 0 {
			sentiment = 0.5
		}
		news = append(news, vintage.NewsItem{
			At:        testDayStart(d).Add(9 * time.Hour),
			Source:    "interfax",
			Title:     "Новость синтетического ряда",
			Sentiment: sentiment,
		})
	}
	return news
}

// TestOracleRaisesSigmaBeforeKeyRateDecision is the plan test: the model is
// trained on days where the CBR decision triples the realized variance; the
// forecast inside the decision window must lift the multiplier above 2, name the
// release driver and leave the calm regime.
func TestOracleRaisesSigmaBeforeKeyRateDecision(t *testing.T) {
	const days = 120
	eventDays := testEventDays(days)
	bars := syntheticBars(days, 0, eventDays, nil, 42)
	releases := syntheticReleases(eventDays)
	news := syntheticNews(days)
	asOf := testBase().AddDate(0, 0, 90)

	model, err := Fit(context.Background(), bars, releases, news, asOf)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	// The next decision is synthetic day 91 (7 + 14*6); forecast one hour before.
	releaseAt := testDayStart(91).Add(10*time.Hour + 30*time.Minute)
	forecast := model.Forecast(releaseAt.Add(-time.Hour), 8*time.Hour, []vintage.Release{{
		At: releaseAt, Category: "cbr_rate", Title: "СД ЦБ РФ: заседание по ключевой ставке", Country: "RU", Exact: true,
	}}, nil)
	if forecast.Multiplier <= 2 {
		t.Fatalf("multiplier = %v, want > 2 inside the CBR decision window (drivers %+v)", forecast.Multiplier, forecast.Drivers)
	}
	if forecast.Regime != "event" && forecast.Regime != "stress" {
		t.Fatalf("regime = %q, want event or stress", forecast.Regime)
	}
	if forecast.SigmaPct <= 0 || forecast.Horizon != 8*time.Hour {
		t.Fatalf("sigma/horizon = %v/%v", forecast.SigmaPct, forecast.Horizon)
	}
	found := false
	for _, driver := range forecast.Drivers {
		if driver.Kind == "release" && driver.Ref == "cbr_rate" && driver.Contribution > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("release driver cbr_rate missing: %+v", forecast.Drivers)
	}

	// Outside every window and without fresh news the multiplier stays near 1.
	calm := model.Forecast(releaseAt.Add(-12*time.Hour), time.Hour, nil, nil)
	if math.Abs(calm.Multiplier-1) > 1e-9 {
		t.Fatalf("calm multiplier = %v, want 1 without event/news drivers", calm.Multiplier)
	}
	if calm.Regime != "normal" && calm.Regime != "calm" {
		t.Fatalf("calm regime = %q", calm.Regime)
	}
}

// TestOracleFitUsesOnlyDataBeforeAsOf is the plan test: bars, releases and news
// after asOf must not change the fitted model (no lookahead into the training).
func TestOracleFitUsesOnlyDataBeforeAsOf(t *testing.T) {
	const days = 120
	eventDays := testEventDays(days)
	asOf := testBase().AddDate(0, 0, 90)
	trainingBars := syntheticBars(90, 0, eventDays, nil, 7)
	// A strong regime change after asOf: if Fit leaked it, the coefficients
	// would move. The shock days start at day 90, i.e. at or after asOf.
	shock := map[int]float64{}
	for d := 90; d < days; d++ {
		shock[d] = 0.02
	}
	fullBars := append(append([]backtest.Bar(nil), trainingBars...), syntheticBars(days-90, 90, nil, shock, 9)...)

	trainingReleases := syntheticReleases(eventDays)
	allReleases := append(append([]vintage.Release(nil), trainingReleases...),
		vintage.Release{At: asOf.Add(72 * time.Hour), Category: "fomc", Title: "ФРС", Country: "US", Exact: true})
	trainingNews := syntheticNews(90)
	allNews := append(append([]vintage.NewsItem(nil), trainingNews...),
		vintage.NewsItem{At: asOf.Add(48 * time.Hour), Source: "rbc", Title: "будущая новость", Sentiment: 1})

	truncated, err := Fit(context.Background(), trainingBars, trainingReleases, trainingNews, asOf)
	if err != nil {
		t.Fatalf("Fit(truncated): %v", err)
	}
	full, err := Fit(context.Background(), fullBars, allReleases, allNews, asOf)
	if err != nil {
		t.Fatalf("Fit(full): %v", err)
	}
	if truncated.intercept != full.intercept || truncated.harDaily != full.harDaily ||
		truncated.harWeek != full.harWeek || truncated.harMonth != full.harMonth ||
		truncated.newsCoef != full.newsCoef || truncated.lastDailyRV != full.lastDailyRV {
		t.Fatalf("model coefficients changed with post-asOf data:\ntruncated=%+v\nfull=%+v", truncated, full)
	}
	if len(truncated.eventCoef) != len(full.eventCoef) {
		t.Fatalf("event categories = %v vs %v", truncated.eventCoef, full.eventCoef)
	}
	for category, coef := range truncated.eventCoef {
		if full.eventCoef[category] != coef {
			t.Fatalf("event coefficient %q changed: %v vs %v", category, coef, full.eventCoef[category])
		}
	}
}

// TestQLIKEBeatsHARonSynthetic is the plan test: on data containing the release
// effect the oracle's QLIKE is lower than the naive HAR-only model's one.
func TestQLIKEBeatsHARonSynthetic(t *testing.T) {
	const days = 150
	eventDays := testEventDays(days)
	bars := syntheticBars(days, 0, eventDays, nil, 11)
	releases := syntheticReleases(eventDays)
	news := syntheticNews(days)
	asOf := testBase().AddDate(0, 0, 110)

	model, err := Fit(context.Background(), bars, releases, news, asOf)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	harOnly, err := fitHAROnly(context.Background(), bars, releases, news, asOf)
	if err != nil {
		t.Fatalf("fitHAROnly: %v", err)
	}
	from := testDayStart(111)
	to := testDayStart(145)
	oracleQLIKE, oracleDays := model.QLIKE(bars, releases, news, from, to)
	harQLIKE, harDays := harOnly.QLIKE(bars, releases, news, from, to)
	if oracleDays == 0 || oracleDays != harDays {
		t.Fatalf("evaluated days = %d/%d, want the same non-zero window", oracleDays, harDays)
	}
	if !(oracleQLIKE < harQLIKE) {
		t.Fatalf("QLIKE oracle %v is not better than naive HAR %v", oracleQLIKE, harQLIKE)
	}
	if oracleQLIKE > 0.9*harQLIKE {
		t.Fatalf("QLIKE improvement is too small: oracle %v vs HAR %v", oracleQLIKE, harQLIKE)
	}
}
