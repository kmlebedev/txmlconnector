package backtest

import "math"

// Keltner channel: a moving-average middle line with an ATR-scaled band. The
// close above the upper band opens a long, the close below the middle line
// closes it.

var (
	keltnerPeriodParam = paramSpec{
		Key:     "period",
		Title:   "Период средней и ATR, баров",
		Default: 20,
		Min:     2,
		Max:     200,
		Step:    1,
	}
	keltnerMultiplierParam = paramSpec{
		Key:     "multiplier",
		Title:   "Множитель ATR",
		Default: 2,
		Min:     0.5,
		Max:     5,
		Step:    0.1,
	}
)

var keltnerInfo = StrategyInfo{
	ID:   "trend_keltner",
	Name: "Канал Кельтнера",
	Description: "Вход в лонг при закрытии выше верхней границы EMA±mult·ATR, " +
		"выход — при закрытии ниже средней линии канала.",
	Params: paramInfos(keltnerPeriodParam, keltnerMultiplierParam),
}

// keltner trades breakouts of an ATR-scaled channel around an EMA.
type keltner struct {
	period     int
	multiplier float64
}

func newKeltner(params map[string]float64) Strategy {
	return &keltner{
		period:     intParam(keltnerPeriodParam.value(params)),
		multiplier: keltnerMultiplierParam.value(params),
	}
}

func (s *keltner) ID() string { return keltnerInfo.ID }

func (s *keltner) Params() map[string]float64 {
	return map[string]float64{
		"period":     float64(s.period),
		"multiplier": s.multiplier,
	}
}

// OnBar opens a long above the upper channel and exits below the middle line.
func (s *keltner) OnBar(bars []Bar, i int) Action {
	if i < s.period-1 || i >= len(bars) {
		return Hold
	}
	middle := emaAt(bars, i, s.period)
	band := s.multiplier * simpleATR(bars, i, s.period)
	closePrice := bars[i].Close
	if closePrice > middle+band {
		return Buy
	}
	if closePrice < middle {
		return Sell
	}
	return Hold
}

// emaAt returns the exponential moving average seeded with the simple average
// of the first period closes. The value at i depends only on bars[0..i].
func emaAt(bars []Bar, i, period int) float64 {
	if period <= 0 || i < period-1 || i >= len(bars) {
		return bars[maxInt(i, 0)].Close
	}
	alpha := 2 / float64(period+1)
	var value float64
	for j := 0; j < period; j++ {
		value += bars[j].Close
	}
	value /= float64(period)
	for j := period; j <= i; j++ {
		value += alpha * (bars[j].Close - value)
	}
	return value
}

// trueRangeAt returns the Wilder true range of bar i.
func trueRangeAt(bars []Bar, i int) float64 {
	tr := bars[i].High - bars[i].Low
	if i == 0 {
		return tr
	}
	prevClose := bars[i-1].Close
	if gap := math.Abs(bars[i].High - prevClose); gap > tr {
		tr = gap
	}
	if gap := math.Abs(bars[i].Low - prevClose); gap > tr {
		tr = gap
	}
	return tr
}

// simpleATR returns the mean true range over the last period bars ending at i.
// The value at i depends only on bars[0..i].
func simpleATR(bars []Bar, i, period int) float64 {
	if period <= 0 || i < 0 || i >= len(bars) {
		return 0
	}
	start := i - period + 1
	if start < 0 {
		start = 0
	}
	sum := 0.0
	for j := start; j <= i; j++ {
		sum += trueRangeAt(bars, j)
	}
	return sum / float64(i-start+1)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
