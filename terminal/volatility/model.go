package volatility

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

// Model is a fitted HAR-RV model with release-window dummies and a decaying
// news-intensity regressor. It is immutable after Fit and safe for concurrent
// Forecast/QLIKE calls. The type is the seam of plan Task 20: a trained ML model
// can be returned as *Model through Forecast-compatible accessors.
type Model struct {
	// Symbol is the instrument label copied into Forecast; Fit has no symbol
	// argument (the bars are already one instrument's series), so the caller sets
	// it after Fit when it needs the label in the forecast payload.
	Symbol string

	// categories are the release categories with a dummy column, in matrix order.
	categories []string

	intercept float64
	harDaily  float64
	harWeek   float64
	harMonth  float64
	newsCoef  float64
	eventCoef map[string]float64
	// withEvents is false for the naive HAR-only baseline of the QLIKE test.
	withEvents bool

	// Trailing state of the training window, used by Forecast for times after it.
	lastDailyRV   float64
	weekRV        float64
	monthRV       float64
	barsPerDay    float64
	barInterval   time.Duration
	asOf          time.Time
	residualSigma float64

	eventBefore, eventAfter time.Duration
	newsTau                 time.Duration
}

// Fit trains the oracle on the bars, releases and news known before asOf: only
// complete trading days ending before asOf are used, so the model is
// point-in-time safe (no lookahead into the training).
func Fit(ctx context.Context, bars []backtest.Bar, releases []vintage.Release, news []vintage.NewsItem, asOf time.Time) (*Model, error) {
	return fit(ctx, bars, releases, news, asOf, true)
}

// fitHAROnly is the naive HAR baseline of the quality comparison: no release
// dummies, no news term.
func fitHAROnly(ctx context.Context, bars []backtest.Bar, releases []vintage.Release, news []vintage.NewsItem, asOf time.Time) (*Model, error) {
	return fit(ctx, bars, releases, news, asOf, false)
}

// fit is the shared point-in-time OLS training of the two model variants.
func fit(ctx context.Context, bars []backtest.Bar, releases []vintage.Release, news []vintage.NewsItem, asOf time.Time, withEvents bool) (*Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if asOf.IsZero() {
		return nil, errors.New("volatility: asOf is required")
	}
	// Bars at or after asOf never reach the model.
	trainBars := make([]backtest.Bar, 0, len(bars))
	for _, bar := range bars {
		if bar.Time.Before(asOf) {
			trainBars = append(trainBars, bar)
		}
	}
	days, interval := dailySeries(trainBars)
	// The last day may be incomplete: require the whole day before asOf.
	for len(days) > 0 && !days[len(days)-1].Day.Add(24*time.Hour).Before(asOf) {
		days = days[:len(days)-1]
	}
	if interval <= 0 {
		interval = defaultBarInterval
	}
	if len(days) < monthWindow+minTrainDays {
		return nil, fmt.Errorf("volatility: not enough complete training days: %d (need %d)",
			len(days), monthWindow+minTrainDays)
	}
	rel := releasesBefore(releases, asOf)
	newsKnown := newsBefore(news, asOf)
	categories := eventCategories(rel)
	if !withEvents {
		categories = nil
	}

	rows := make([][]float64, 0, len(days)-monthWindow)
	targets := make([]float64, 0, len(days)-monthWindow)
	for i := monthWindow; i < len(days); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		har, ok := harFeatures(days, i)
		if !ok {
			continue
		}
		row := []float64{1, har[0], har[1], har[2]}
		if withEvents {
			known := categoryOnDay(days[i].Day, rel, eventBefore, eventAfter)
			for _, category := range categories {
				if containsString(known, category) {
					row = append(row, 1)
				} else {
					row = append(row, 0)
				}
			}
			row = append(row, math.Log1p(newsIntensity(newsKnown, days[i].Day, newsTau, newsLookback)))
		}
		rows = append(rows, row)
		targets = append(targets, math.Log(days[i].RV))
	}
	if len(rows) < minTrainDays {
		return nil, fmt.Errorf("volatility: not enough training samples: %d (need %d)", len(rows), minTrainDays)
	}
	coef, err := solveOLS(rows, targets)
	if err != nil {
		return nil, err
	}
	m := &Model{
		categories:  categories,
		intercept:   coef[0],
		harDaily:    coef[1],
		harWeek:     coef[2],
		harMonth:    coef[3],
		withEvents:  withEvents,
		barInterval: interval,
		asOf:        asOf,
		eventBefore: eventBefore,
		eventAfter:  eventAfter,
		newsTau:     newsTau,
	}
	if withEvents {
		m.eventCoef = make(map[string]float64, len(categories))
		for i, category := range categories {
			m.eventCoef[category] = coef[4+i]
		}
		m.newsCoef = coef[4+len(categories)]
	}
	// Residuals of the fitted model (all rows), for diagnostics.
	residual := 0.0
	for i, row := range rows {
		predicted := dot(row, coef)
		delta := predicted - targets[i]
		residual += delta * delta
	}
	m.residualSigma = math.Sqrt(residual / float64(len(rows)))

	last := len(days) - 1
	m.lastDailyRV = days[last].RV
	m.weekRV = windowMean(days, len(days), weekWindow)
	m.monthRV = windowMean(days, len(days), monthWindow)
	m.barsPerDay = medianBars(days)
	return m, nil
}

// predictLogRV is the log daily variance prediction of the model: HAR terms,
// plus the news term and the release dummies of the event-aware variant.
func (m *Model) predictLogRV(har [3]float64, eventCats []string, intensity float64) float64 {
	logRV := m.intercept + m.harDaily*har[0] + m.harWeek*har[1] + m.harMonth*har[2]
	if !m.withEvents {
		return logRV
	}
	logRV += m.newsCoef * math.Log1p(intensity)
	if len(eventCats) == 0 {
		return logRV
	}
	seen := make(map[string]bool, len(eventCats))
	for _, category := range eventCats {
		if seen[category] {
			continue
		}
		seen[category] = true
		logRV += m.eventCoef[category]
	}
	return logRV
}

// QLIKE evaluates the model's daily variance forecasts of the days in [from,to)
// and returns the mean QLIKE loss and the number of evaluated days:
//
//	QLIKE(actual, predicted) = actual/predicted - ln(actual/predicted) - 1
//
// The features of a day use only the data strictly before its start, so a
// window after the training asOf is an out-of-sample evaluation.
func (m *Model) QLIKE(bars []backtest.Bar, releases []vintage.Release, news []vintage.NewsItem, from, to time.Time) (float64, int) {
	if m == nil || from.IsZero() || to.IsZero() || !from.Before(to) {
		return 0, 0
	}
	days, _ := dailySeries(bars)
	before, after := m.eventWindow()
	tau := m.newsTau
	if tau <= 0 {
		tau = newsTau
	}
	total := 0.0
	count := 0
	for i := monthWindow; i < len(days); i++ {
		day := days[i].Day
		if day.Before(from) || !day.Before(to) {
			continue
		}
		har, ok := harFeatures(days, i)
		if !ok {
			continue
		}
		known := categoryOnDay(day, releases, before, after)
		intensity := newsIntensity(news, day, tau, newsLookback)
		logRV := m.predictLogRV(har, known, intensity)
		predicted := math.Exp(logRV)
		if !(predicted > 0) || math.IsInf(predicted, 0) || math.IsNaN(predicted) {
			continue
		}
		ratio := days[i].RV / predicted
		if !(ratio > 0) || math.IsInf(ratio, 0) || math.IsNaN(ratio) {
			continue
		}
		total += ratio - math.Log(ratio) - 1
		count++
	}
	if count == 0 {
		return 0, 0
	}
	return total / float64(count), count
}

// eventWindow returns the release window around an event (defaults applied).
func (m *Model) eventWindow() (time.Duration, time.Duration) {
	before, after := m.eventBefore, m.eventAfter
	if before <= 0 {
		before = eventBefore
	}
	if after <= 0 {
		after = eventAfter
	}
	return before, after
}

// medianBars returns the median bar count of a complete day.
func medianBars(days []dailyStat) float64 {
	if len(days) == 0 {
		return 1
	}
	counts := make([]int, len(days))
	for i, day := range days {
		counts[i] = day.Bars
	}
	sort.Ints(counts)
	return float64(counts[len(counts)/2])
}

// solveOLS fits the ordinary least squares model with a small ridge penalty on
// the non-intercept coefficients and returns the coefficient vector.
func solveOLS(rows [][]float64, targets []float64) ([]float64, error) {
	if len(rows) == 0 || len(rows[0]) == 0 || len(rows) != len(targets) {
		return nil, errors.New("volatility: invalid training matrix")
	}
	n := len(rows[0])
	xtx := make([][]float64, n)
	for i := range xtx {
		xtx[i] = make([]float64, n)
	}
	xty := make([]float64, n)
	for r, row := range rows {
		if len(row) != n {
			return nil, errors.New("volatility: inconsistent training matrix")
		}
		for i := 0; i < n; i++ {
			xty[i] += row[i] * targets[r]
			for j := 0; j < n; j++ {
				xtx[i][j] += row[i] * row[j]
			}
		}
	}
	for i := 1; i < n; i++ {
		xtx[i][i] += ridge
	}
	coef, err := solveLinear(xtx, xty)
	if err != nil {
		return nil, fmt.Errorf("volatility: fit: %w", err)
	}
	return coef, nil
}

// solveLinear solves a x = b with Gaussian elimination and partial pivoting.
func solveLinear(a [][]float64, b []float64) ([]float64, error) {
	n := len(b)
	matrix := make([][]float64, n)
	for i := range matrix {
		matrix[i] = make([]float64, n+1)
		copy(matrix[i], a[i])
		matrix[i][n] = b[i]
	}
	for col := 0; col < n; col++ {
		pivot := col
		for row := col + 1; row < n; row++ {
			if math.Abs(matrix[row][col]) > math.Abs(matrix[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(matrix[pivot][col]) < 1e-12 {
			return nil, errors.New("singular training matrix")
		}
		matrix[col], matrix[pivot] = matrix[pivot], matrix[col]
		scale := matrix[col][col]
		for j := col; j <= n; j++ {
			matrix[col][j] /= scale
		}
		for row := 0; row < n; row++ {
			if row == col {
				continue
			}
			factor := matrix[row][col]
			if factor == 0 {
				continue
			}
			for j := col; j <= n; j++ {
				matrix[row][j] -= factor * matrix[col][j]
			}
		}
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = matrix[i][n]
	}
	return out, nil
}

// dot returns the scalar product of a row and the coefficients.
func dot(row, coef []float64) float64 {
	sum := 0.0
	for i := range coef {
		if i < len(row) {
			sum += row[i] * coef[i]
		}
	}
	return sum
}
