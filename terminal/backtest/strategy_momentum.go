package backtest

var (
	momentumLookbackParam = paramSpec{
		Key:     "lookback",
		Title:   "Окно доходности, баров",
		Default: 20,
		Min:     2,
		Max:     500,
		Step:    1,
	}
	momentumThresholdParam = paramSpec{
		Key:     "thresholdPct",
		Title:   "Порог доходности, %",
		Default: 1.0,
		Min:     -10,
		Max:     50,
		Step:    0.1,
	}
)

var momentumInfo = StrategyInfo{
	ID:          "momentum",
	Name:        "Моментум",
	Description: "Вход в лонг при доходности за lookback баров выше порога, выход — при отрицательной доходности.",
	Params:      paramInfos(momentumLookbackParam, momentumThresholdParam),
}

// momentum trades the lookback rate of change of the close price.
type momentum struct {
	lookback     int
	thresholdPct float64
}

func newMomentum(params map[string]float64) Strategy {
	return &momentum{
		lookback:     intParam(momentumLookbackParam.value(params)),
		thresholdPct: momentumThresholdParam.value(params),
	}
}

func (s *momentum) ID() string { return momentumInfo.ID }

func (s *momentum) Params() map[string]float64 {
	return map[string]float64{
		"lookback":     float64(s.lookback),
		"thresholdPct": s.thresholdPct,
	}
}

// OnBar reports Buy when the lookback return is above thresholdPct percent and
// Sell when it is negative.
func (s *momentum) OnBar(bars []Bar, i int) Action {
	if i < s.lookback || i >= len(bars) {
		return Hold
	}
	base := bars[i-s.lookback].Close
	if base == 0 {
		return Hold
	}
	returnPct := (bars[i].Close/base - 1) * 100
	if returnPct > s.thresholdPct {
		return Buy
	}
	if returnPct < 0 {
		return Sell
	}
	return Hold
}
