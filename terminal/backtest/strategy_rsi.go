package backtest

var (
	rsiPeriodParam = paramSpec{
		Key:     "period",
		Title:   "Период RSI, баров",
		Default: 14,
		Min:     2,
		Max:     200,
		Step:    1,
	}
	rsiOversoldParam = paramSpec{
		Key:     "oversold",
		Title:   "Порог перепроданности RSI",
		Default: 30,
		Min:     1,
		Max:     99,
		Step:    1,
	}
	rsiOverboughtParam = paramSpec{
		Key:     "overbought",
		Title:   "Порог перекупленности RSI",
		Default: 55,
		Min:     1,
		Max:     99,
		Step:    1,
	}
)

var rsiReversionInfo = StrategyInfo{
	ID:          "rsi_reversion",
	Name:        "RSI-разворот",
	Description: "Вход в лонг при RSI ниже порога перепроданности, выход — при RSI выше порога перекупленности.",
	Params:      paramInfos(rsiPeriodParam, rsiOversoldParam, rsiOverboughtParam),
}

// rsiReversion is a mean-reversion strategy on Wilder's RSI.
type rsiReversion struct {
	period     int
	oversold   float64
	overbought float64
}

func newRSIReversion(params map[string]float64) Strategy {
	period := intParam(rsiPeriodParam.value(params))
	oversold := rsiOversoldParam.value(params)
	overbought := rsiOverboughtParam.value(params)
	if oversold >= overbought {
		oversold = overbought - 1
	}
	if oversold < rsiOversoldParam.Min {
		oversold = rsiOversoldParam.Min
		if overbought <= oversold {
			overbought = oversold + 1
		}
	}
	return &rsiReversion{period: period, oversold: oversold, overbought: overbought}
}

func (s *rsiReversion) ID() string { return rsiReversionInfo.ID }

func (s *rsiReversion) Params() map[string]float64 {
	return map[string]float64{
		"period":     float64(s.period),
		"oversold":   s.oversold,
		"overbought": s.overbought,
	}
}

// OnBar reports Buy while RSI is oversold and Sell while it is overbought; the
// engine ignores repeated entries/exits of the same direction.
func (s *rsiReversion) OnBar(bars []Bar, i int) Action {
	if i < s.period || i >= len(bars) {
		return Hold
	}
	rsi := rsiWilder(bars, i, s.period)
	if rsi <= s.oversold {
		return Buy
	}
	if rsi >= s.overbought {
		return Sell
	}
	return Hold
}
