// Package fingerprint detects execution algorithms in a recorded order-book and
// trade tape (terminal/lobreplay.Event): TWAP/VWAP/POV schedules, iceberg refills
// on the visible L2 level, margin-call liquidation bursts and unknown clusters of
// similar windows (DBSCAN).
//
// The tape is anonymous: there is no order number and no participant id, so the
// attribution is probabilistic. Every Footprint carries a Confidence in [0,1]
// and the Evidence map it was derived from. The package never accuses anybody:
// it reports features that resemble a known execution pattern, and a footprint
// below Config.MinConfidence is not reported at all.
package fingerprint

import (
	"math"
	"sort"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native/model"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
)

// priceScale and priceFactor mirror the md schema: prices are int64 Decimal64(6)
// scaled units, so no float64 touches a price on the way to the features.
const (
	priceScale  = 6
	priceFactor = int64(1_000_000)
)

// side indexes of the per-side feature arrays.
const (
	sideBuy  = 0
	sideSell = 1
)

// trade is one aggressive trade of the tape.
type trade struct {
	ts    time.Time
	side  string // lobreplay.SideBuy or lobreplay.SideSell
	qty   int64
	price int64 // Decimal64(6) scaled units
	no    int64
}

// levelChange is one visible L2 level update of the tape.
type levelChange struct {
	ts    time.Time
	side  string // lobreplay.SideBid or lobreplay.SideAsk
	price int64
	qty   int64
}

// tape is the prepared analysis stream: trades and level changes sorted by time.
type tape struct {
	trades []trade
	levels []levelChange
	from   time.Time
	to     time.Time
}

// loadTape converts the replay events into the trade/L2 tape used by the
// detectors. Malformed events are skipped: Detect has no error return and one bad
// row must not silently change the analysis of a whole recording. Duplicated
// trade numbers (ReplacingMergeTree can return the same row twice before a merge)
// are counted once.
func loadTape(events []lobreplay.Event) tape {
	var out tape
	seen := make(map[int64]struct{})
	var first, last time.Time
	touch := func(ts time.Time) {
		if first.IsZero() || ts.Before(first) {
			first = ts
		}
		if last.IsZero() || ts.After(last) {
			last = ts
		}
	}
	for _, ev := range events {
		switch ev.Kind {
		case lobreplay.KindTrade:
			if ev.Qty <= 0 || (ev.Side != lobreplay.SideBuy && ev.Side != lobreplay.SideSell) {
				continue
			}
			price := decimalUnits(ev.Price)
			if price <= 0 {
				continue
			}
			no := int64(0)
			if ev.Trade != nil {
				no = ev.Trade.TradeNo
			}
			if no != 0 {
				if _, ok := seen[no]; ok {
					continue
				}
				seen[no] = struct{}{}
			}
			out.trades = append(out.trades, trade{ts: ev.TS, side: ev.Side, qty: ev.Qty, price: price, no: no})
			touch(ev.TS)
		case lobreplay.KindSnapshot, lobreplay.KindLevel:
			if ev.Side != lobreplay.SideBid && ev.Side != lobreplay.SideAsk {
				continue
			}
			price := decimalUnits(ev.Price)
			if price <= 0 || ev.Qty < 0 {
				continue
			}
			out.levels = append(out.levels, levelChange{ts: ev.TS, side: ev.Side, price: price, qty: ev.Qty})
			touch(ev.TS)
		}
	}
	sort.SliceStable(out.trades, func(i, j int) bool { return out.trades[i].ts.Before(out.trades[j].ts) })
	sort.SliceStable(out.levels, func(i, j int) bool { return out.levels[i].ts.Before(out.levels[j].ts) })
	out.from, out.to = first, last
	return out
}

// decimalUnits converts a model.Decimal price to Decimal64(6) scaled units, the
// same scale the md recording stores.
func decimalUnits(price model.Decimal) int64 {
	units := price.Units
	for scale := int(price.Scale); scale < priceScale; scale++ {
		units *= 10
	}
	for scale := priceScale; scale < int(price.Scale); scale++ {
		units /= 10
	}
	return units
}

// window is one analysis slice of the tape.
type window struct {
	from, to time.Time
	trades   []trade
	levels   []levelChange
}

// splitWindows slices the tape into Config.Window windows with Config.Step. The
// windows overlap when Step < Window; the event order inside a window is the tape
// order.
func splitWindows(t tape, cfg Config) []window {
	if len(t.trades) == 0 {
		return nil
	}
	windows := make([]window, 0, 8)
	tradeAt, levelAt := 0, 0
	for start := t.from; !start.After(t.to); start = start.Add(cfg.Step) {
		end := start.Add(cfg.Window)
		for tradeAt < len(t.trades) && t.trades[tradeAt].ts.Before(start) {
			tradeAt++
		}
		tradeEnd := tradeAt
		for tradeEnd < len(t.trades) && t.trades[tradeEnd].ts.Before(end) {
			tradeEnd++
		}
		for levelAt < len(t.levels) && t.levels[levelAt].ts.Before(start) {
			levelAt++
		}
		levelEnd := levelAt
		for levelEnd < len(t.levels) && t.levels[levelEnd].ts.Before(end) {
			levelEnd++
		}
		windows = append(windows, window{
			from:   start,
			to:     end,
			trades: t.trades[tradeAt:tradeEnd],
			levels: t.levels[levelAt:levelEnd],
		})
	}
	return windows
}

// sideFeatures are the trade-size and timing features of one aggressor side in
// one window.
type sideFeatures struct {
	trades        int
	volume        int64
	modeQty       int64
	modeCount     int
	modeShare     float64 // modeCount / trades
	entropy       float64 // normalized Shannon entropy of the exact sizes, 0..1
	distinctSizes int

	// The clip series is the sequence of trades whose size equals modeQty: an
	// execution schedule trades equal clips, background noise does not.
	clipIntervals  int
	medianInterval time.Duration
	// intervalRegularity is the share of clip intervals inside ±25% of the
	// median interval: one odd same-size background trade cannot erase an
	// otherwise periodic schedule.
	intervalRegularity float64
	// intervalCV is the coefficient of variation of the inlier intervals.
	intervalCV   float64
	autocorrPeak float64 // peak normalized autocorrelation of the binned clip series
	spectralPeak float64 // peak DFT power share of the binned clip series
}

// aggregateFeatures are the window-wide features used by the VWAP/POV/margin
// detectors and by the unknown-algo clustering.
type aggregateFeatures struct {
	trades   int
	totalVol int64
	buyVol   int64
	sellVol  int64

	// vwapCorr is the Pearson correlation between the per-bucket side volume and
	// the modeled intraday volume profile (U-shape of the Moscow session).
	vwapCorr [2]float64

	// povMean/povCV describe the stability of the side participation share over
	// the sub-buckets of the window.
	povMean [2]float64
	povCV   [2]float64
	povOk   [2]bool

	// sellShare is the sell aggressor share of the window volume.
	sellShare float64
	// sizeGrowth is the relative increase of the mean sell size between the
	// first and the second half of the window: an escalating aggression.
	sizeGrowth float64
	// priceDecline is the relative drop of the volume-weighted price between the
	// first and the second half of the window (positive = the price fell).
	priceDecline float64

	iceberg icebergFeatures
}

// windowFeatures is everything the detectors need for one window.
type windowFeatures struct {
	sides [2]sideFeatures
	agg   aggregateFeatures
}

// computeWindowFeatures extracts the complete feature set of one window.
func computeWindowFeatures(w window, cfg Config) windowFeatures {
	var out windowFeatures
	var buyTrades, sellTrades []trade
	for _, tr := range w.trades {
		out.agg.totalVol += tr.qty
		switch tr.side {
		case lobreplay.SideBuy:
			buyTrades = append(buyTrades, tr)
			out.agg.buyVol += tr.qty
		case lobreplay.SideSell:
			sellTrades = append(sellTrades, tr)
			out.agg.sellVol += tr.qty
		}
	}
	out.agg.trades = len(w.trades)
	out.sides[sideBuy] = computeSideFeatures(buyTrades, w, cfg)
	out.sides[sideSell] = computeSideFeatures(sellTrades, w, cfg)
	if out.agg.totalVol > 0 {
		out.agg.sellShare = float64(out.agg.sellVol) / float64(out.agg.totalVol)
	}
	out.agg.vwapCorr[sideBuy] = vwapCorrelation(buyTrades, w, cfg.VWAPBuckets)
	out.agg.vwapCorr[sideSell] = vwapCorrelation(sellTrades, w, cfg.VWAPBuckets)
	out.agg.povMean[sideBuy], out.agg.povCV[sideBuy], out.agg.povOk[sideBuy] = participationShare(buyTrades, w.trades, w, cfg.VWAPBuckets)
	out.agg.povMean[sideSell], out.agg.povCV[sideSell], out.agg.povOk[sideSell] = participationShare(sellTrades, w.trades, w, cfg.VWAPBuckets)

	out.agg.sizeGrowth = sellSizeGrowth(sellTrades, w)
	out.agg.priceDecline = windowPriceDecline(w)
	out.agg.iceberg = computeIceberg(w.levels, w.trades, cfg)
	return out
}

// computeSideFeatures derives the clip-size and periodicity features of one side.
func computeSideFeatures(trades []trade, w window, cfg Config) sideFeatures {
	var out sideFeatures
	out.trades = len(trades)
	if len(trades) == 0 {
		return out
	}
	counts := make(map[int64]int, len(trades))
	for _, tr := range trades {
		out.volume += tr.qty
		counts[tr.qty]++
	}
	out.distinctSizes = len(counts)
	for qty, count := range counts {
		if count > out.modeCount || (count == out.modeCount && (out.modeQty == 0 || qty < out.modeQty)) {
			out.modeCount = count
			out.modeQty = qty
		}
	}
	out.modeShare = float64(out.modeCount) / float64(len(trades))
	out.entropy = normalizedEntropy(counts, len(trades))

	clips := make([]time.Time, 0, out.modeCount)
	for _, tr := range trades {
		if tr.qty == out.modeQty {
			clips = append(clips, tr.ts)
		}
	}
	out.clipIntervals = len(clips)
	if len(clips) < cfg.MinClips || w.to.Before(w.from) {
		return out
	}
	intervals := make([]float64, 0, len(clips)-1)
	for i := 1; i < len(clips); i++ {
		intervals = append(intervals, float64(clips[i].Sub(clips[i-1]))/float64(time.Millisecond))
	}
	out.medianInterval = percentileDuration(intervals, 0.5)
	medianMillis := float64(out.medianInterval / time.Millisecond)
	inliers := make([]float64, 0, len(intervals))
	for _, interval := range intervals {
		if math.Abs(interval-medianMillis) <= 0.25*medianMillis {
			inliers = append(inliers, interval)
		}
	}
	if len(intervals) > 0 {
		out.intervalRegularity = float64(len(inliers)) / float64(len(intervals))
	}
	out.intervalCV = coefficientOfVariation(inliers)

	series := binSeries(clips, w.from, w.to, cfg.BinSize)
	out.autocorrPeak = autocorrelationPeak(series,
		int(cfg.MinPeriod/cfg.BinSize), int(cfg.MaxPeriod/cfg.BinSize))
	out.spectralPeak = spectralPeakShare(series,
		cfg.MinPeriod.Seconds()/cfg.BinSize.Seconds(),
		cfg.MaxPeriod.Seconds()/cfg.BinSize.Seconds())
	return out
}

// sellSizeGrowth returns the relative increase of the mean sell size between the
// first and the second half of the window (0 when either half is empty).
func sellSizeGrowth(sellTrades []trade, w window) float64 {
	if len(sellTrades) < 4 {
		return 0
	}
	mid := w.from.Add(w.to.Sub(w.from) / 2)
	var firstCount, secondCount int
	var firstSum, secondSum int64
	for _, tr := range sellTrades {
		if tr.ts.Before(mid) {
			firstCount++
			firstSum += tr.qty
		} else {
			secondCount++
			secondSum += tr.qty
		}
	}
	if firstCount == 0 || secondCount == 0 || firstSum == 0 {
		return 0
	}
	firstMean := float64(firstSum) / float64(firstCount)
	secondMean := float64(secondSum) / float64(secondCount)
	return secondMean/firstMean - 1
}

// normalizedEntropy returns the Shannon entropy of the size distribution divided
// by ln(distinct): 0 for one size, 1 for an even spread.
func normalizedEntropy(counts map[int64]int, total int) float64 {
	if len(counts) <= 1 || total == 0 {
		return 0
	}
	entropy := 0.0
	for _, count := range counts {
		p := float64(count) / float64(total)
		entropy -= p * math.Log(p)
	}
	return entropy / math.Log(float64(len(counts)))
}

// coefficientOfVariation returns std/mean of the values (0 when undefined).
func coefficientOfVariation(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	mean := sum / float64(len(values))
	if mean == 0 {
		return 0
	}
	var variance float64
	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}
	variance /= float64(len(values))
	return math.Sqrt(variance) / mean
}

// percentileDuration returns the median of the millisecond values.
func percentileDuration(values []float64, fraction float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(fraction * float64(len(sorted)-1))
	return time.Duration(sorted[index]) * time.Millisecond
}

// binSeries puts the timestamps into BinSize buckets of the window as a presence
// count series.
func binSeries(timestamps []time.Time, from, to time.Time, binSize time.Duration) []float64 {
	if binSize <= 0 || !to.After(from) {
		return nil
	}
	bins := int(to.Sub(from)/binSize) + 1
	series := make([]float64, bins)
	for _, ts := range timestamps {
		index := int(ts.Sub(from) / binSize)
		if index < 0 || index >= bins {
			continue
		}
		series[index]++
	}
	return series
}

// autocorrelationPeak returns the largest normalized autocorrelation of the series
// for the lags in [minLag, maxLag].
func autocorrelationPeak(series []float64, minLag, maxLag int) float64 {
	if len(series) < 3 {
		return 0
	}
	var energy float64
	for _, value := range series {
		energy += value * value
	}
	if energy == 0 {
		return 0
	}
	if minLag < 1 {
		minLag = 1
	}
	if maxLag >= len(series) {
		maxLag = len(series) - 1
	}
	peak := 0.0
	for lag := minLag; lag <= maxLag; lag++ {
		var sum float64
		for i := 0; i+lag < len(series); i++ {
			sum += series[i] * series[i+lag]
		}
		if ratio := sum / energy; ratio > peak {
			peak = ratio
		}
	}
	return peak
}

// spectralPeakShare returns the largest single-frequency DFT power share of the
// series for periods in [minPeriod, maxPeriod] bins, normalized so that a flat
// spectrum stays near 0 and a perfectly periodic clip series stands out.
func spectralPeakShare(series []float64, minPeriod, maxPeriod float64) float64 {
	if len(series) < 4 || minPeriod < 1 {
		return 0
	}
	if maxPeriod > float64(len(series)) {
		maxPeriod = float64(len(series))
	}
	var total float64
	for _, value := range series {
		total += value * value
	}
	if total == 0 {
		return 0
	}
	peak := 0.0
	for period := math.Max(1, math.Floor(minPeriod)); period <= maxPeriod; period++ {
		omega := 2 * math.Pi / period
		var real, imag float64
		for index, value := range series {
			if value == 0 {
				continue
			}
			angle := omega * float64(index)
			real += value * math.Cos(angle)
			imag -= value * math.Sin(angle)
		}
		if power := real*real + imag*imag; power > peak {
			peak = power
		}
	}
	// Parseval: the sum of all DFT powers is len(series) * sum(x²).
	return peak / (float64(len(series)) * total)
}

// intradayVolumeWeight models the relative intraday volume at t: the U-shaped
// profile of the MOEX main session (10:00–18:40 Moscow time), high at the open and
// at the close and lower in the middle of the day. The exact shape is an
// inference (I) documented in docs/ai-terminal/research.md; only the relative
// correlation enters the VWAP score.
func intradayVolumeWeight(t time.Time) float64 {
	local := t.In(moscow)
	minutes := float64(local.Hour()*60 + local.Minute())
	const sessionOpen = 10 * 60
	const sessionClose = 18*60 + 40
	progress := (minutes - sessionOpen) / float64(sessionClose-sessionOpen)
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	centered := 2*progress - 1 // -1 at the open, +1 at the close
	return 1 + 2*centered*centered
}

// vwapCorrelation correlates the per-bucket side volume with the modeled intraday
// volume profile: a VWAP schedule follows the profile, a random tape does not.
func vwapCorrelation(trades []trade, w window, buckets int) float64 {
	if buckets < 3 || !w.to.After(w.from) {
		return 0
	}
	observed := make([]float64, buckets)
	expected := make([]float64, buckets)
	size := w.to.Sub(w.from) / time.Duration(buckets)
	if size <= 0 {
		return 0
	}
	used := 0
	for _, tr := range trades {
		index := int(tr.ts.Sub(w.from) / size)
		if index < 0 || index >= buckets {
			continue
		}
		observed[index] += float64(tr.qty)
		used++
	}
	if used < 3 {
		return 0
	}
	for index := range expected {
		mid := w.from.Add(time.Duration(index)*size + size/2)
		expected[index] = intradayVolumeWeight(mid)
	}
	return pearson(observed, expected)
}

// participationShare returns the mean and the coefficient of variation of the
// side volume share over the sub-buckets of the window: a POV schedule keeps the
// share stable.
func participationShare(sideTrades, allTrades []trade, w window, buckets int) (mean, cv float64, ok bool) {
	if buckets < 3 || !w.to.After(w.from) {
		return 0, 0, false
	}
	size := w.to.Sub(w.from) / time.Duration(buckets)
	if size <= 0 {
		return 0, 0, false
	}
	sideVolume := make([]float64, buckets)
	totalVolume := make([]float64, buckets)
	for _, tr := range sideTrades {
		index := int(tr.ts.Sub(w.from) / size)
		if index < 0 || index >= buckets {
			continue
		}
		sideVolume[index] += float64(tr.qty)
	}
	for _, tr := range allTrades {
		index := int(tr.ts.Sub(w.from) / size)
		if index < 0 || index >= buckets {
			continue
		}
		totalVolume[index] += float64(tr.qty)
	}
	shares := make([]float64, 0, buckets)
	for index, volume := range totalVolume {
		if volume <= 0 {
			continue
		}
		shares = append(shares, sideVolume[index]/volume)
	}
	if len(shares) < 3 {
		return 0, 0, false
	}
	return meanOf(shares), coefficientOfVariation(shares), true
}

// meanOf returns the arithmetic mean.
func meanOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

// pearson returns the Pearson correlation of two equal-length series (0 when a
// series is constant).
func pearson(a, b []float64) float64 {
	if len(a) != len(b) || len(a) < 3 {
		return 0
	}
	meanA, meanB := meanOf(a), meanOf(b)
	var covariance, varianceA, varianceB float64
	for index := range a {
		da := a[index] - meanA
		db := b[index] - meanB
		covariance += da * db
		varianceA += da * da
		varianceB += db * db
	}
	if varianceA == 0 || varianceB == 0 {
		return 0
	}
	return covariance / math.Sqrt(varianceA*varianceB)
}

// windowPriceDecline returns the relative drop of the volume-weighted trade price
// between the first and the second half of the window (positive = fell).
func windowPriceDecline(w window) float64 {
	if len(w.trades) < 4 {
		return 0
	}
	mid := w.from.Add(w.to.Sub(w.from) / 2)
	var firstVolume, secondVolume float64
	var firstNotional, secondNotional float64
	for _, tr := range w.trades {
		notional := float64(tr.price) * float64(tr.qty)
		if tr.ts.Before(mid) {
			firstVolume += float64(tr.qty)
			firstNotional += notional
		} else {
			secondVolume += float64(tr.qty)
			secondNotional += notional
		}
	}
	if firstVolume == 0 || secondVolume == 0 {
		return 0
	}
	firstVWAP := firstNotional / firstVolume
	secondVWAP := secondNotional / secondVolume
	if firstVWAP == 0 {
		return 0
	}
	return (firstVWAP - secondVWAP) / firstVWAP
}

// icebergFeatures are the L2 refill statistics of the window.
type icebergFeatures struct {
	cycles    int    // refill cycles of the best price
	refills   int    // total refill cycles over all prices
	prices    int    // prices that showed at least one refill cycle
	side      string // book side of the best price (bid/ask)
	maxPrice  int64  // price of the best cycle count
	cycleSize int64  // displayed size that was refilled
}

// computeIceberg detects the visible iceberg signature: a visible level is eaten
// by trades and then restored to (almost) the same size shortly afterwards. A
// cancellation-restore without a trade in between is not counted.
func computeIceberg(levels []levelChange, trades []trade, cfg Config) icebergFeatures {
	var out icebergFeatures
	if len(levels) == 0 || len(trades) == 0 {
		return out
	}
	byPrice := make(map[int64][]levelChange)
	for _, change := range levels {
		byPrice[change.price] = append(byPrice[change.price], change)
	}
	tradesByPrice := make(map[int64][]time.Time)
	for _, tr := range trades {
		tradesByPrice[tr.price] = append(tradesByPrice[tr.price], tr.ts)
	}
	for price, changes := range byPrice {
		var prevQty int64 = -1
		var decreaseQty int64
		var decreaseAt time.Time
		var pending bool
		cycles := 0
		for _, change := range changes {
			switch {
			case prevQty >= 0 && change.qty < prevQty:
				decreaseQty = prevQty
				decreaseAt = change.ts
				pending = true
				prevQty = change.qty
			case prevQty >= 0 && pending && change.qty > prevQty:
				if float64(change.qty) >= 0.9*float64(decreaseQty) &&
					change.ts.Sub(decreaseAt) <= cfg.RefillWindow &&
					hasTradeIn(tradesByPrice[price], decreaseAt.Add(-cfg.RefillWindow), change.ts) {
					cycles++
					pending = false
				}
				prevQty = change.qty
			default:
				prevQty = change.qty
			}
		}
		if cycles == 0 {
			continue
		}
		out.refills += cycles
		out.prices++
		if cycles > out.cycles {
			out.cycles = cycles
			out.maxPrice = price
			out.cycleSize = decreaseQty
			out.side = changes[0].side
		}
	}
	return out
}

// hasTradeIn reports whether a trade timestamp falls into [from, to].
func hasTradeIn(timestamps []time.Time, from, to time.Time) bool {
	for _, ts := range timestamps {
		if !ts.Before(from) && !ts.After(to) {
			return true
		}
	}
	return false
}
