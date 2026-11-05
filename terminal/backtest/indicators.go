package backtest

// sma returns the simple moving average of the closes of bars[i-period+1 .. i].
// Callers must ensure 0 <= i-period+1 and i < len(bars).
func sma(bars []Bar, i, period int) float64 {
	sum := 0.0
	for j := i - period + 1; j <= i; j++ {
		sum += bars[j].Close
	}
	return sum / float64(period)
}

// rsiWilder returns Wilder's RSI over bars[0..i] with the given period. It is a
// pure function of the bars it is allowed to see (0..i), so it cannot introduce
// lookahead. A flat series (no gains and no losses) returns 50.
func rsiWilder(bars []Bar, i, period int) float64 {
	if period < 1 || i < period {
		return 50
	}
	var gain, loss float64
	for j := 1; j <= period; j++ {
		change := bars[j].Close - bars[j-1].Close
		if change > 0 {
			gain += change
		} else {
			loss -= change
		}
	}
	avgGain := gain / float64(period)
	avgLoss := loss / float64(period)
	for j := period + 1; j <= i; j++ {
		change := bars[j].Close - bars[j-1].Close
		var up, down float64
		if change > 0 {
			up = change
		} else {
			down = -change
		}
		avgGain = (avgGain*float64(period-1) + up) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + down) / float64(period)
	}
	switch {
	case avgLoss == 0 && avgGain == 0:
		return 50
	case avgLoss == 0:
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs)
}
