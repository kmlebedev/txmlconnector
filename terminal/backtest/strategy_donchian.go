package backtest

var donchianPeriodParam = paramSpec{
	Key:     "period",
	Title:   "Период канала, баров",
	Default: 20,
	Min:     2,
	Max:     500,
	Step:    1,
}

var donchianBreakoutInfo = StrategyInfo{
	ID:          "donchian_breakout",
	Name:        "Пробой канала Дончиана",
	Description: "Вход в лонг при закрытии выше максимума за period предыдущих баров, выход — при закрытии ниже минимума.",
	Params:      paramInfos(donchianPeriodParam),
}

// donchianBreakout trades breakouts of the highest high / lowest low of the
// preceding period bars.
type donchianBreakout struct {
	period int
}

func newDonchianBreakout(params map[string]float64) Strategy {
	return &donchianBreakout{period: intParam(donchianPeriodParam.value(params))}
}

func (s *donchianBreakout) ID() string { return donchianBreakoutInfo.ID }

func (s *donchianBreakout) Params() map[string]float64 {
	return map[string]float64{"period": float64(s.period)}
}

// OnBar compares the close of bar i with the channel built from bars
// [i-period, i-1] only (the current bar is never part of the channel).
func (s *donchianBreakout) OnBar(bars []Bar, i int) Action {
	if i < s.period || i >= len(bars) {
		return Hold
	}
	highest := bars[i-s.period].High
	lowest := bars[i-s.period].Low
	for j := i - s.period + 1; j < i; j++ {
		if bars[j].High > highest {
			highest = bars[j].High
		}
		if bars[j].Low < lowest {
			lowest = bars[j].Low
		}
	}
	closePrice := bars[i].Close
	if closePrice > highest {
		return Buy
	}
	if closePrice < lowest {
		return Sell
	}
	return Hold
}
