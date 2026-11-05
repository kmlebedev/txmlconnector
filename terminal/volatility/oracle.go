package volatility

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

// The forecast regimes of the plan Task 16 contract.
const (
	RegimeCalm   = "calm"
	RegimeNormal = "normal"
	RegimeEvent  = "event"
	RegimeStress = "stress"
)

// Regime thresholds on the variance multiplier: a release/news lift of at least
// 2x is a stress regime, 1.3x (or any active event driver) is an event regime,
// a calm market keeps the multiplier around or below 0.85.
const (
	stressMultiplier = 2.0
	eventMultiplier  = 1.3
	calmMultiplier   = 0.85
)

// Forecast is the oracle's volatility forecast for one instrument and horizon.
type Forecast struct {
	Symbol     string
	At         time.Time
	Horizon    time.Duration
	SigmaPct   float64 // forecast standard deviation of the horizon return, percent
	Multiplier float64 // predicted variance relative to the HAR baseline (>= 1 = elevated)
	Drivers    []Driver
	Regime     string // RegimeCalm|RegimeNormal|RegimeEvent|RegimeStress
}

// Driver is one log-variance contribution of the forecast. Kind is
// "release" (Ref is the release category), "news" (Ref "intensity") or "har"
// (Ref "daily"/"weekly"/"monthly"); the release driver example of the plan is
// "release:cbr_rate".
type Driver struct {
	Kind         string
	Ref          string
	Contribution float64
}

// Forecast predicts the volatility of the [at, at+horizon] window given the
// upcoming releases and the recent news. It never looks at the future: the news
// after at are ignored, and the HAR state is the trailing state saved by Fit.
func (m *Model) Forecast(at time.Time, horizon time.Duration, upcoming []vintage.Release, recentNews []vintage.NewsItem) Forecast {
	out := Forecast{At: at, Horizon: horizon}
	if m == nil {
		return out
	}
	out.Symbol = m.Symbol
	at = at.UTC()
	har := [3]float64{
		math.Log(math.Max(m.lastDailyRV, minDailyRV)),
		math.Log(math.Max(m.weekRV, minDailyRV)),
		math.Log(math.Max(m.monthRV, minDailyRV)),
	}
	drivers := make([]Driver, 0, len(upcoming)+4)

	// Release drivers: the categories whose window intersects the horizon.
	before, after := m.eventWindow()
	eventCats := make([]string, 0, 2)
	seen := make(map[string]bool, len(upcoming))
	if horizon > 0 {
		end := at.Add(horizon)
		for _, release := range upcoming {
			if release.At.IsZero() {
				continue
			}
			windowStart := release.At.Add(-before)
			windowEnd := release.At.Add(after)
			if windowEnd.Before(at) || windowStart.After(end) {
				continue
			}
			category := strings.TrimSpace(release.Category)
			if category == "" {
				category = "release"
			}
			if seen[category] {
				continue
			}
			seen[category] = true
			contribution := 0.0
			if m.withEvents {
				contribution = m.eventCoef[category]
				eventCats = append(eventCats, category)
			}
			drivers = append(drivers, Driver{Kind: "release", Ref: category, Contribution: contribution})
		}
	}

	// News driver: the decayed intensity known before at.
	newsContribution := 0.0
	intensity := newsIntensity(recentNews, at, m.newsTauOrDefault(), newsLookback)
	if m.withEvents {
		newsContribution = m.newsCoef * math.Log1p(intensity)
	}
	if intensity > 0 {
		drivers = append(drivers, Driver{Kind: "news", Ref: "intensity", Contribution: newsContribution})
	}

	// HAR components of the log variance (the baseline of the multiplier).
	drivers = append(drivers,
		Driver{Kind: "har", Ref: "daily", Contribution: m.harDaily * har[0]},
		Driver{Kind: "har", Ref: "weekly", Contribution: m.harWeek * har[1]},
		Driver{Kind: "har", Ref: "monthly", Contribution: m.harMonth * har[2]},
	)

	full := m.predictLogRV(har, eventCats, intensity)
	baseline := m.intercept + m.harDaily*har[0] + m.harWeek*har[1] + m.harMonth*har[2]
	out.Multiplier = math.Exp(full - baseline)
	if math.IsNaN(out.Multiplier) || math.IsInf(out.Multiplier, 0) {
		out.Multiplier = 1
	}
	out.SigmaPct = m.sigmaPct(full, horizon)
	out.Drivers = sortDrivers(drivers)
	out.Regime = regimeFor(out.Multiplier, len(eventCats) > 0 || intensity > 0)
	return out
}

// newsTauOrDefault returns the configured news decay constant.
func (m *Model) newsTauOrDefault() time.Duration {
	if m.newsTau > 0 {
		return m.newsTau
	}
	return newsTau
}

// sigmaPct converts the log daily variance prediction into the standard
// deviation of the horizon return in percent: the intraday variance is assumed
// uniform across the bars of a trading day.
func (m *Model) sigmaPct(logRV float64, horizon time.Duration) float64 {
	if horizon <= 0 || m.barsPerDay <= 0 || m.barInterval <= 0 {
		return 0
	}
	dailyVariance := math.Exp(logRV)
	if !(dailyVariance > 0) || math.IsInf(dailyVariance, 0) || math.IsNaN(dailyVariance) {
		return 0
	}
	perBarVariance := dailyVariance / m.barsPerDay
	bars := float64(horizon) / float64(m.barInterval)
	if bars <= 0 {
		return 0
	}
	return math.Sqrt(perBarVariance*bars) * 100
}

// regimeFor names the forecast regime of the variance multiplier.
func regimeFor(multiplier float64, hasDrivers bool) string {
	switch {
	case multiplier >= stressMultiplier:
		return RegimeStress
	case multiplier >= eventMultiplier || hasDrivers:
		return RegimeEvent
	case multiplier <= calmMultiplier:
		return RegimeCalm
	}
	return RegimeNormal
}

// sortDrivers returns the drivers strongest first (absolute log variance).
func sortDrivers(drivers []Driver) []Driver {
	sort.SliceStable(drivers, func(i, j int) bool {
		return math.Abs(drivers[i].Contribution) > math.Abs(drivers[j].Contribution)
	})
	return drivers
}
