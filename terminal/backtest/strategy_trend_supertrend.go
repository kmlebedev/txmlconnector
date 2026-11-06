package backtest

// Supertrend: a Wilder ATR trailing band that flips the trend when the close
// crosses it. The strategy opens a long on a down-to-up flip and closes it on an
// up-to-down flip.
//
// The bands are recursive, so the strategy is driven sequentially (the engine
// calls OnBar for 0..n-1). An out-of-order call replays the state from bar 0,
// which keeps the OnBar contract "value at i depends only on bars[0..i]".

var (
	supertrendPeriodParam = paramSpec{
		Key:     "period",
		Title:   "Период ATR, баров",
		Default: 10,
		Min:     2,
		Max:     100,
		Step:    1,
	}
	supertrendMultiplierParam = paramSpec{
		Key:     "multiplier",
		Title:   "Множитель ATR",
		Default: 3,
		Min:     0.5,
		Max:     10,
		Step:    0.1,
	}
)

var supertrendInfo = StrategyInfo{
	ID:   "trend_supertrend",
	Name: "Supertrend",
	Description: "Вход в лонг при развороте ATR-тренда вверх, выход — при развороте вниз " +
		"(полосы hl2±mult·ATR Уайлдера).",
	Params: paramInfos(supertrendPeriodParam, supertrendMultiplierParam),
}

// supertrend holds the recursive band state for the bars currently replayed.
type supertrend struct {
	period     int
	multiplier float64

	barLen   int
	computed int // number of bars with a computed band
	atr      float64
	atrReady bool
	seedSum  float64
	finalUp  []float64
	finalLow []float64
	trend    []int8 // 0 unknown, +1 up, -1 down
}

func newSupertrend(params map[string]float64) Strategy {
	return &supertrend{
		period:     intParam(supertrendPeriodParam.value(params)),
		multiplier: supertrendMultiplierParam.value(params),
	}
}

func (s *supertrend) ID() string { return supertrendInfo.ID }

func (s *supertrend) Params() map[string]float64 {
	return map[string]float64{
		"period":     float64(s.period),
		"multiplier": s.multiplier,
	}
}

// reset starts a new bar series.
func (s *supertrend) reset(bars []Bar) {
	s.barLen = len(bars)
	s.computed = 0
	s.atr = 0
	s.atrReady = false
	s.seedSum = 0
	s.finalUp = make([]float64, len(bars))
	s.finalLow = make([]float64, len(bars))
	s.trend = make([]int8, len(bars))
}

// prepare advances the recursive state through bar i.
func (s *supertrend) prepare(bars []Bar, i int) {
	if s.barLen != len(bars) {
		s.reset(bars)
	}
	for s.computed <= i {
		s.advance(bars, s.computed)
		s.computed++
	}
}

// advance computes the band and the trend for one bar.
func (s *supertrend) advance(bars []Bar, j int) {
	tr := trueRangeAt(bars, j)
	switch {
	case j >= 1 && j <= s.period:
		s.seedSum += tr
		if j == s.period {
			s.atr = s.seedSum / float64(s.period)
			s.atrReady = true
		}
	case j > s.period:
		s.atr = (s.atr*float64(s.period-1) + tr) / float64(s.period)
	}
	if j < s.period || !s.atrReady {
		return
	}
	hl2 := (bars[j].High + bars[j].Low) / 2
	basicUpper := hl2 + s.multiplier*s.atr
	basicLower := hl2 - s.multiplier*s.atr
	if j == s.period {
		s.finalUp[j] = basicUpper
		s.finalLow[j] = basicLower
		if bars[j].Close >= basicLower {
			s.trend[j] = 1
		} else {
			s.trend[j] = -1
		}
		return
	}
	if basicUpper < s.finalUp[j-1] || bars[j-1].Close > s.finalUp[j-1] {
		s.finalUp[j] = basicUpper
	} else {
		s.finalUp[j] = s.finalUp[j-1]
	}
	if basicLower > s.finalLow[j-1] || bars[j-1].Close < s.finalLow[j-1] {
		s.finalLow[j] = basicLower
	} else {
		s.finalLow[j] = s.finalLow[j-1]
	}
	if s.trend[j-1] == 1 {
		if bars[j].Close < s.finalLow[j] {
			s.trend[j] = -1
		} else {
			s.trend[j] = 1
		}
		return
	}
	if bars[j].Close > s.finalUp[j] {
		s.trend[j] = 1
	} else {
		s.trend[j] = -1
	}
}

// OnBar reports Buy on a down-to-up trend flip and Sell on an up-to-down flip.
func (s *supertrend) OnBar(bars []Bar, i int) Action {
	if i < 0 || i >= len(bars) {
		return Hold
	}
	s.prepare(bars, i)
	if i <= s.period {
		return Hold
	}
	previous, current := s.trend[i-1], s.trend[i]
	switch {
	case previous == -1 && current == 1:
		return Buy
	case previous == 1 && current == -1:
		return Sell
	}
	return Hold
}
