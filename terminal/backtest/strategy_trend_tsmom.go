package backtest

// TSMOM — time-series momentum: the close-to-close return over a lookback
// window is the signal. Positive momentum above the threshold opens a long,
// negative momentum closes it.

var (
	tsmomLookbackParam = paramSpec{
		Key:     "lookback",
		Title:   "Окно моментума, баров",
		Default: 20,
		Min:     2,
		Max:     500,
		Step:    1,
	}
	tsmomThresholdParam = paramSpec{
		Key:     "thresholdPct",
		Title:   "Порог доходности за окно, %",
		Default: 0,
		Min:     -10,
		Max:     50,
		Step:    0.1,
	}
)

var tsmomInfo = StrategyInfo{
	ID:          "trend_tsmom",
	Name:        "TSMOM",
	Description: "Вход в лонг при доходности за lookback баров выше порога, выход — при отрицательной доходности.",
	Params:      paramInfos(tsmomLookbackParam, tsmomThresholdParam),
}

// tsmom trades the sign of the lookback return.
type tsmom struct {
	lookback     int
	thresholdPct float64
}

func newTSMOM(params map[string]float64) Strategy {
	return &tsmom{
		lookback:     intParam(tsmomLookbackParam.value(params)),
		thresholdPct: tsmomThresholdParam.value(params),
	}
}

func (s *tsmom) ID() string { return tsmomInfo.ID }

func (s *tsmom) Params() map[string]float64 {
	return map[string]float64{
		"lookback":     float64(s.lookback),
		"thresholdPct": s.thresholdPct,
	}
}

// OnBar reports Buy while the lookback return is above the threshold and Sell
// while it is negative.
func (s *tsmom) OnBar(bars []Bar, i int) Action {
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
