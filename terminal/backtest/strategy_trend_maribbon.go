package backtest

// MA ribbon: a fan of evenly spaced simple moving averages between the fast and
// the slow period. A fully ordered ribbon (fast above slow) opens a long; the
// opposite order closes it.

var (
	maribbonFastParam = paramSpec{
		Key:     "fast",
		Title:   "Быстрый период ленты, баров",
		Default: 5,
		Min:     2,
		Max:     50,
		Step:    1,
	}
	maribbonSlowParam = paramSpec{
		Key:     "slow",
		Title:   "Медленный период ленты, баров",
		Default: 40,
		Min:     3,
		Max:     400,
		Step:    1,
	}
)

// maribbonLevels is the number of moving averages in the ribbon.
const maribbonLevels = 5

var maribbonInfo = StrategyInfo{
	ID:   "trend_maribbon",
	Name: "Лента скользящих",
	Description: "Вход в лонг, когда лента из пяти MA выстроена по возрастанию (fast→slow), " +
		"выход — при обратном порядке.",
	Params: paramInfos(maribbonFastParam, maribbonSlowParam),
}

// maribbon trades the alignment of a fan of moving averages.
type maribbon struct {
	fast int
	slow int
}

func newMARibbon(params map[string]float64) Strategy {
	fast := intParam(maribbonFastParam.value(params))
	slow := intParam(maribbonSlowParam.value(params))
	if fast >= slow {
		fast = slow - 1
	}
	if fast < int(maribbonFastParam.Min) {
		fast = int(maribbonFastParam.Min)
		if slow <= fast {
			slow = fast + 1
		}
	}
	return &maribbon{fast: fast, slow: slow}
}

func (s *maribbon) ID() string { return maribbonInfo.ID }

func (s *maribbon) Params() map[string]float64 {
	return map[string]float64{"fast": float64(s.fast), "slow": float64(s.slow)}
}

// periods returns the evenly spaced ribbon periods, fast first.
func (s *maribbon) periods() [maribbonLevels]int {
	var periods [maribbonLevels]int
	step := float64(s.slow-s.fast) / float64(maribbonLevels-1)
	for level := 0; level < maribbonLevels; level++ {
		period := s.fast + int(float64(level)*step+0.5)
		if level > 0 && period <= periods[level-1] {
			period = periods[level-1] + 1
		}
		periods[level] = period
	}
	return periods
}

// OnBar opens a long on a fully bullish ribbon and closes it on a fully
// bearish one.
func (s *maribbon) OnBar(bars []Bar, i int) Action {
	periods := s.periods()
	if i < periods[maribbonLevels-1] || i >= len(bars) {
		return Hold
	}
	values := make([]float64, maribbonLevels)
	for level, period := range periods {
		values[level] = sma(bars, i, period)
	}
	bullish, bearish := true, true
	for level := 1; level < maribbonLevels; level++ {
		if !(values[level-1] > values[level]) {
			bullish = false
		}
		if !(values[level-1] < values[level]) {
			bearish = false
		}
	}
	if bullish {
		return Buy
	}
	if bearish {
		return Sell
	}
	return Hold
}
