package research

import (
	"math"
)

// eulerMasheroni is the Euler–Mascheroni constant used by the expected maximum
// of independent Sharpe ratios (Bailey & López de Prado).
const eulerMasheroni = 0.5772156649015329

// Moments summarizes one return series.
type Moments struct {
	Count    int
	Mean     float64
	Std      float64
	Sharpe   float64 // per-observation mean/std, not annualized
	Skewness float64
	Kurtosis float64 // raw kurtosis (a normal distribution is 3)
}

// MomentsOf computes the moments of a return series. A series shorter than two
// observations has zero Std, Sharpe, Skewness and Kurtosis.
func MomentsOf(returns []float64) Moments {
	out := Moments{Count: len(returns)}
	if len(returns) == 0 {
		return out
	}
	var sum float64
	for _, value := range returns {
		sum += value
	}
	out.Mean = sum / float64(len(returns))
	if len(returns) < 2 {
		return out
	}
	var m2, m3, m4 float64
	for _, value := range returns {
		diff := value - out.Mean
		m2 += diff * diff
		m3 += diff * diff * diff
		m4 += diff * diff * diff * diff
	}
	n := float64(len(returns))
	out.Std = math.Sqrt(m2 / (n - 1))
	if out.Std > 0 {
		out.Sharpe = out.Mean / out.Std
		// Population (biased) standardized moments are the conventional
		// definition of skewness/kurtosis in the DSR formula.
		variance := m2 / n
		sigma := math.Sqrt(variance)
		if sigma > 0 {
			out.Skewness = (m3 / n) / (sigma * sigma * sigma)
			out.Kurtosis = (m4 / n) / (variance * variance)
		}
	}
	return out
}

// SharpeRatio returns the per-observation Sharpe ratio (mean divided by sample
// standard deviation, no annualization). A degenerate series returns 0.
func SharpeRatio(returns []float64) float64 { return MomentsOf(returns).Sharpe }

// normalCDF is the standard normal cumulative distribution.
func normalCDF(x float64) float64 { return 0.5 * (1 + math.Erf(x/math.Sqrt2)) }

// normalQuantile is the standard normal inverse CDF (probit).
func normalQuantile(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	return math.Sqrt2 * math.Erfinv(2*p-1)
}

// DeflatedSharpe returns the deflated Sharpe ratio of Bailey & López de Prado:
// the probability that the observed Sharpe ratio (bestSharpe, per-observation,
// not annualized) is a real discovery rather than the maximum of trials
// independent trials. observations is the number of return observations;
// skewness and kurtosis describe their distribution (raw kurtosis, normal = 3).
//
// The expected maximum Sharpe under the null is estimated from the variance of
// the Sharpe estimator, 1/(observations-1); more trials raise that expected
// maximum and therefore lower the result.
func DeflatedSharpe(bestSharpe float64, trials, observations int, skewness, kurtosis float64) float64 {
	if trials < 1 || observations < 2 || math.IsNaN(bestSharpe) || math.IsInf(bestSharpe, 0) {
		return 0
	}
	varianceSR := 1 / float64(observations-1)
	stdSR := math.Sqrt(varianceSR)
	expectedMax := 0.0
	if trials > 1 {
		n := float64(trials)
		expectedMax = stdSR * ((1-eulerMasheroni)*normalQuantile(1-1/n) +
			eulerMasheroni*normalQuantile(1-1/(n*math.E)))
	}
	variance := 1 - skewness*bestSharpe + (kurtosis-1)/4*bestSharpe*bestSharpe
	if variance <= 0 {
		variance = math.SmallestNonzeroFloat64
	}
	z := (bestSharpe - expectedMax) * math.Sqrt(float64(observations-1)) / math.Sqrt(variance)
	if math.IsNaN(z) || math.IsInf(z, 0) {
		return 0
	}
	value := normalCDF(z)
	return clampUnit(value)
}

// clampUnit trims a probability into [0, 1].
func clampUnit(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// blockStat is the pre-computed sum and sum of squares of one trial inside one
// contiguous time block.
type blockStat struct {
	sum   float64
	sumSq float64
	count int
}

// PBO returns the Probability of Backtest Overfitting (Bailey, Borwein, López de
// Prado & Zhu) estimated with combinatorial symmetric cross-validation (CSCV).
//
// trialReturns is a matrix [nTrials][nObservations] of per-period returns; every
// trial must be aligned with the same observations. blocks is the number of
// contiguous time blocks (default and conventional value 16, forced even and
// capped at 20). The function partitions the observations into blocks, and for
// every combination of half of the blocks treats the selected half as
// in-sample and the complementary half as out-of-sample: the in-sample best
// strategy is scored out-of-sample and counted as overfit when its rank is in
// the bottom half among the trials. The result is the share of combinations
// where that happens: 0 = the in-sample winner stays the winner, 1 = always
// overfits. Not enough data returns 0.5 (undecided).
func PBO(trialReturns [][]float64, blocks int) float64 {
	nTrials := len(trialReturns)
	if nTrials < 2 {
		return 0.5
	}
	nObs := len(trialReturns[0])
	for _, series := range trialReturns {
		if len(series) < nObs {
			nObs = len(series)
		}
	}
	if nObs < 4 {
		return 0.5
	}
	if blocks <= 0 {
		blocks = 16
	}
	if blocks%2 != 0 {
		blocks++
	}
	if blocks > 20 {
		blocks = 20
		if blocks%2 != 0 {
			blocks--
		}
	}
	if blocks > nObs {
		blocks = nObs
	}
	if blocks < 4 {
		return 0.5
	}

	// Per trial x block statistics; the block boundaries split the observations
	// into contiguous groups of nearly equal size.
	stats := make([][]blockStat, nTrials)
	for trial := range trialReturns {
		stats[trial] = make([]blockStat, blocks)
	}
	for block := 0; block < blocks; block++ {
		start := block * nObs / blocks
		end := (block + 1) * nObs / blocks
		for trial := 0; trial < nTrials; trial++ {
			series := trialReturns[trial]
			var stat blockStat
			for index := start; index < end; index++ {
				value := series[index]
				stat.sum += value
				stat.sumSq += value * value
				stat.count++
			}
			stats[trial][block] = stat
		}
	}

	half := blocks / 2
	combinations := 0
	overfit := 0
	sharpe := make([]float64, nTrials)
	for mask := 0; mask < 1<<blocks; mask++ {
		if bitsSet(mask) != half {
			continue
		}
		combinations++
		inSampleBest := -1
		inSampleSharpe := math.Inf(-1)
		for trial := 0; trial < nTrials; trial++ {
			isSharpe, ok := combinedSharpe(stats[trial], mask, blocks)
			if !ok {
				inSampleSharpe = math.Inf(1)
				break
			}
			sharpe[trial] = isSharpe
			if isSharpe > inSampleSharpe {
				inSampleSharpe = isSharpe
				inSampleBest = trial
			}
		}
		if inSampleBest < 0 || math.IsInf(inSampleSharpe, 0) {
			continue
		}
		inverse := mask ^ (1<<blocks - 1)
		for trial := 0; trial < nTrials; trial++ {
			oosSharpe, ok := combinedSharpe(stats[trial], inverse, blocks)
			if !ok {
				sharpe[trial] = math.Inf(-1)
				continue
			}
			sharpe[trial] = oosSharpe
		}
		rank := 1
		for trial := 0; trial < nTrials; trial++ {
			if trial != inSampleBest && sharpe[trial] < sharpe[inSampleBest] {
				rank++
			}
		}
		// Relative rank in (0, 1); below the median means the in-sample winner
		// did not make it out of sample.
		relative := float64(rank) / float64(nTrials+1)
		if relative <= 0.5 {
			overfit++
		}
	}
	if combinations == 0 {
		return 0.5
	}
	return float64(overfit) / float64(combinations)
}

// combinedSharpe computes the Sharpe ratio of one trial over the blocks selected
// by mask using the pre-computed block statistics.
func combinedSharpe(stats []blockStat, mask, blocks int) (float64, bool) {
	var sum, sumSq float64
	count := 0
	for block := 0; block < blocks; block++ {
		if mask&(1<<block) == 0 {
			continue
		}
		sum += stats[block].sum
		sumSq += stats[block].sumSq
		count += stats[block].count
	}
	if count < 2 {
		return 0, false
	}
	mean := sum / float64(count)
	variance := sumSq/float64(count) - mean*mean
	if variance <= 0 {
		return 0, true
	}
	std := math.Sqrt(variance)
	if std == 0 {
		return 0, true
	}
	return mean / std, true
}

// bitsSet counts the set bits of a small non-negative integer.
func bitsSet(value int) int {
	count := 0
	for value != 0 {
		value &= value - 1
		count++
	}
	return count
}
