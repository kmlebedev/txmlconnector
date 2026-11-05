// Package volatility is the event- and news-aware volatility oracle of the AI
// terminal (plan Task 16): a HAR-RV model over intraday realized variance with
// release-window dummies and a decaying news-intensity regressor. It is pure Go
// (standard library only, no network); Fit is point-in-time safe (only data
// before asOf is used) and Forecast explains its drivers for the UI.
package volatility

import (
	"math"
	"sort"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

const (
	// HAR windows in trading days: yesterday, last week, last month.
	weekWindow  = 5
	monthWindow = 22
	// minBarsPerDay is the minimum number of intraday bars of a complete day.
	minBarsPerDay = 20
	// minTrainDays is the minimum number of OLS samples after the HAR warm-up.
	minTrainDays = 40
	// The event window around a release instant: the volatility of the whole
	// day is affected, so a sample is "event" when the window overlaps the day.
	eventBefore = 2 * time.Hour
	eventAfter  = 2 * time.Hour
	// The news intensity is an exponentially decaying count with the given
	// decay constant and lookback.
	newsTau      = 24 * time.Hour
	newsLookback = 3 * 24 * time.Hour
	// minDailyRV is the variance floor of the logarithms.
	minDailyRV = 1e-12
	// ridge keeps the OLS normal equations invertible for rare event dummies.
	ridge = 1e-6
	// defaultBarInterval is the fallback bar step of a degenerate series.
	defaultBarInterval = 5 * time.Minute
)

// dailyStat is one complete trading day: the realized variance of the intraday
// bar returns and the bar count.
type dailyStat struct {
	Day  time.Time // UTC midnight
	RV   float64
	Bars int
}

// dailySeries groups the bars by UTC day and computes each day's realized
// variance. The return series restarts every day, so the overnight gap is not
// part of the realized variance. The returned interval is the median bar step.
func dailySeries(bars []backtest.Bar) ([]dailyStat, time.Duration) {
	byDay := make(map[time.Time][]backtest.Bar, len(bars)/minBarsPerDay+1)
	for _, bar := range bars {
		if bar.Partial || bar.Close <= 0 || bar.Time.IsZero() {
			continue
		}
		utc := bar.Time.UTC()
		day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
		byDay[day] = append(byDay[day], bar)
	}
	dayKeys := make([]time.Time, 0, len(byDay))
	for day := range byDay {
		dayKeys = append(dayKeys, day)
	}
	sort.Slice(dayKeys, func(i, j int) bool { return dayKeys[i].Before(dayKeys[j]) })
	days := make([]dailyStat, 0, len(dayKeys))
	intervals := make([]time.Duration, 0, len(bars))
	for _, day := range dayKeys {
		dayBars := byDay[day]
		sort.Slice(dayBars, func(i, j int) bool { return dayBars[i].Time.Before(dayBars[j].Time) })
		if len(dayBars) < minBarsPerDay {
			continue
		}
		rv := 0.0
		for i := 1; i < len(dayBars); i++ {
			prev, cur := dayBars[i-1], dayBars[i]
			if prev.Close <= 0 || cur.Close <= 0 {
				continue
			}
			returnValue := math.Log(cur.Close / prev.Close)
			rv += returnValue * returnValue
			if delta := cur.Time.Sub(prev.Time); delta > 0 && delta <= 24*time.Hour {
				intervals = append(intervals, delta)
			}
		}
		days = append(days, dailyStat{Day: day, RV: math.Max(rv, minDailyRV), Bars: len(dayBars)})
	}
	return days, medianDuration(intervals)
}

// medianDuration returns the median of the durations (defaultBarInterval when
// empty): the bar step of the series.
func medianDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return defaultBarInterval
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

// harFeatures returns the HAR regressors of the sample targeting days[i]: the
// log realized variance of yesterday and the means of the last week and month
// before it. ok is false before the month warm-up.
func harFeatures(days []dailyStat, i int) ([3]float64, bool) {
	var values [3]float64
	if i <= 0 || i > len(days) {
		return values, false
	}
	daily := days[i-1].RV
	week := windowMean(days, i, weekWindow)
	month := windowMean(days, i, monthWindow)
	return [3]float64{math.Log(daily), math.Log(week), math.Log(month)}, true
}

// windowMean is the mean realized variance of the size days before index end
// (the window is clamped at the series start).
func windowMean(days []dailyStat, end, size int) float64 {
	start := end - size
	if start < 0 {
		start = 0
	}
	if start >= end {
		return minDailyRV
	}
	sum := 0.0
	for i := start; i < end; i++ {
		sum += days[i].RV
	}
	return math.Max(sum/float64(end-start), minDailyRV)
}

// categoryOnDay returns the sorted release categories whose window overlaps the
// UTC day: a sample is an "event" day when any release window covers it.
func categoryOnDay(day time.Time, releases []vintage.Release, before, after time.Duration) []string {
	dayEnd := day.Add(24 * time.Hour)
	seen := make(map[string]bool, 2)
	out := make([]string, 0, 2)
	for _, release := range releases {
		if release.At.IsZero() || release.Category == "" {
			continue
		}
		windowStart := release.At.Add(-before)
		windowEnd := release.At.Add(after)
		if windowEnd.Before(day) || !windowStart.Before(dayEnd) {
			continue
		}
		if seen[release.Category] {
			continue
		}
		seen[release.Category] = true
		out = append(out, release.Category)
	}
	sort.Strings(out)
	return out
}

// newsIntensity is the exponentially decaying count of the news known before
// at: both positive and negative news add uncertainty, so the weight grows with
// the absolute sentiment.
func newsIntensity(news []vintage.NewsItem, at time.Time, tau, lookback time.Duration) float64 {
	if tau <= 0 || lookback <= 0 {
		return 0
	}
	intensity := 0.0
	for _, item := range news {
		if item.At.IsZero() || !item.At.Before(at) {
			continue
		}
		age := at.Sub(item.At)
		if age > lookback {
			continue
		}
		weight := 1 + math.Abs(item.Sentiment)
		intensity += math.Exp(-float64(age)/float64(tau)) * weight
	}
	return intensity
}

// eventCategories returns the sorted distinct categories of the releases.
func eventCategories(releases []vintage.Release) []string {
	seen := make(map[string]bool, len(releases))
	out := make([]string, 0, len(releases))
	for _, release := range releases {
		category := release.Category
		if category == "" || seen[category] {
			continue
		}
		seen[category] = true
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

// releasesBefore keeps the releases known strictly before asOf.
func releasesBefore(releases []vintage.Release, asOf time.Time) []vintage.Release {
	out := make([]vintage.Release, 0, len(releases))
	for _, release := range releases {
		if !release.At.IsZero() && release.At.Before(asOf) {
			out = append(out, release)
		}
	}
	return out
}

// newsBefore keeps the news known strictly before asOf.
func newsBefore(news []vintage.NewsItem, asOf time.Time) []vintage.NewsItem {
	out := make([]vintage.NewsItem, 0, len(news))
	for _, item := range news {
		if !item.At.IsZero() && item.At.Before(asOf) {
			out = append(out, item)
		}
	}
	return out
}

// containsString reports whether values holds want.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
