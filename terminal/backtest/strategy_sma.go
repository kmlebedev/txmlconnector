package backtest

var (
	smaFastParam = paramSpec{
		Key:     "fast",
		Title:   "Быстрый период, баров",
		Default: 10,
		Min:     2,
		Max:     200,
		Step:    1,
	}
	smaSlowParam = paramSpec{
		Key:     "slow",
		Title:   "Медленный период, баров",
		Default: 30,
		Min:     3,
		Max:     500,
		Step:    1,
	}
)

var smaCrossInfo = StrategyInfo{
	ID:          "sma_cross",
	Name:        "Пересечение SMA",
	Description: "Вход в лонг при пересечении быстрой SMA выше медленной, выход — при обратном пересечении.",
	Params:      paramInfos(smaFastParam, smaSlowParam),
}

// smaCross trades golden/death crosses of two simple moving averages.
type smaCross struct {
	fast int
	slow int
}

func newSMACross(params map[string]float64) Strategy {
	fast := intParam(smaFastParam.value(params))
	slow := intParam(smaSlowParam.value(params))
	if fast >= slow {
		fast = slow - 1
	}
	if fast < 2 {
		fast = 2
	}
	return &smaCross{fast: fast, slow: slow}
}

func (s *smaCross) ID() string { return smaCrossInfo.ID }

func (s *smaCross) Params() map[string]float64 {
	return map[string]float64{"fast": float64(s.fast), "slow": float64(s.slow)}
}

// OnBar reports Buy on a golden cross and Sell on a death cross. It needs two
// full slow windows, so the first possible decision is at i == slow.
func (s *smaCross) OnBar(bars []Bar, i int) Action {
	if i < s.slow || i >= len(bars) {
		return Hold
	}
	fastPrev := sma(bars, i-1, s.fast)
	fastNow := sma(bars, i, s.fast)
	slowPrev := sma(bars, i-1, s.slow)
	slowNow := sma(bars, i, s.slow)
	if fastPrev <= slowPrev && fastNow > slowNow {
		return Buy
	}
	if fastPrev >= slowPrev && fastNow < slowNow {
		return Sell
	}
	return Hold
}
