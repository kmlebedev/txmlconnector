package fingerprint

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
)

// Synthesize injects a known execution algorithm into base and returns the new
// tape ordered by timestamp. It is the ground-truth generator of the detector
// tests (and, later, of labeled training data): the same function can plant a
// TWAP/VWAP/POV schedule, an iceberg refill pattern or a margin-call liquidation
// burst into any real or synthetic tape.
//
// The parameters are numeric because the plan fixes the signature
// params map[string]float64. Missing parameters fall back to the defaults:
//
//	startMs    offset of the injection from the first event (10% of the tape)
//	clips      number of clips/sells of the injected algorithm (20; margin 12)
//	qty        clip size, units (TWAP/VWAP 25; POV 100)
//	intervalMs interval between clips (2000; margin 4000)
//	jitterMs   uniform jitter added to an interval (0)
//	side       0 = buy/bid (default), 1 = sell/ask
//	price      trade price, whole currency units (default: the last base trade)
//	cycles     iceberg refill cycles (6)
//	levelQty   displayed iceberg level size (400)
//	 eatQty    quantity eaten per cycle (levelQty/2)
//	qtyStep    quantity increment of one margin-liquidation sell (200)
//	stepPct    price step of one margin-liquidation sell, per cent (0.03)
//	ratio      POV target participation share (0.15)
//
// The injected trades get trade numbers after the last base trade number, so the
// tape stays deduplicable. An unknown kind returns a copy of base unchanged.
func Synthesize(kind string, base []lobreplay.Event, params map[string]float64, seed int64) []lobreplay.Event {
	out := append([]lobreplay.Event(nil), base...)
	if len(base) == 0 {
		return out
	}
	rng := rand.New(rand.NewSource(seed))
	start := firstEventTime(base)
	end := lastEventTime(base)
	startMs := paramF(params, "startMs", 0.1*float64(end.Sub(start))/float64(time.Millisecond))
	injectStart := start.Add(time.Duration(math.Round(startMs)) * time.Millisecond)
	if injectStart.Before(start) {
		injectStart = start
	}
	price := lastTradePrice(base, injectStart)
	if raw := paramF(params, "price", 0); raw > 0 {
		price = int64(math.Round(raw * float64(priceFactor)))
	}
	if price <= 0 {
		price = 100 * priceFactor
	}
	nextTradeNo := maxTradeNo(base) + 1
	timePrice := func(units int64) model.Decimal {
		return model.Decimal{Units: units, Scale: uint8(priceScale)}
	}
	appendTrade := func(ts time.Time, side string, qty, units int64) {
		if qty <= 0 {
			return
		}
		out = append(out, lobreplay.Event{
			TS: ts, Kind: lobreplay.KindTrade, Side: side, Price: timePrice(units), Qty: qty,
			Trade: &lobreplay.Trade{TradeNo: nextTradeNo},
		})
		nextTradeNo++
	}
	appendLevel := func(ts time.Time, side string, units, qty int64) {
		out = append(out, lobreplay.Event{
			TS: ts, Kind: lobreplay.KindLevel, Side: side, Price: timePrice(units), Qty: qty,
		})
	}
	durationOfMs := func(key string, def float64) time.Duration {
		return time.Duration(math.Round(paramF(params, key, def))) * time.Millisecond
	}
	jitterOf := func() time.Duration {
		jitter := durationOfMs("jitterMs", 0)
		if jitter <= 0 {
			return 0
		}
		return time.Duration(rng.Int63n(int64(jitter) + 1))
	}
	aggressor := lobreplay.SideBuy
	if int(paramF(params, "side", 0))%2 != 0 {
		aggressor = lobreplay.SideSell
	}

	switch kind {
	case KindTWAP:
		clips := int(paramF(params, "clips", 20))
		interval := durationOfMs("intervalMs", 2000)
		qty := int64(paramF(params, "qty", 25))
		for i := 0; i < clips; i++ {
			appendTrade(injectStart.Add(time.Duration(i)*interval+jitterOf()), aggressor, qty, price)
		}
	case KindVWAP:
		clips := int(paramF(params, "clips", 40))
		interval := durationOfMs("intervalMs", 2000)
		qty := paramF(params, "qty", 100)
		for i := 0; i < clips; i++ {
			ts := injectStart.Add(time.Duration(i)*interval + jitterOf())
			appendTrade(ts, aggressor, int64(math.Round(qty*intradayVolumeWeight(ts))), price)
		}
	case KindPOV:
		clips := int(paramF(params, "clips", 40))
		interval := durationOfMs("intervalMs", 3000)
		ratio := paramF(params, "ratio", 0.15)
		if ratio <= 0 || ratio >= 1 {
			ratio = 0.15
		}
		for i := 0; i < clips; i++ {
			from := injectStart.Add(time.Duration(i) * interval)
			background := float64(tradeVolumeIn(base, from, from.Add(interval)))
			qty := int64(math.Round(background * ratio / (1 - ratio)))
			appendTrade(from, aggressor, maxInt64(qty, 1), price)
		}
	case KindIceberg:
		if int(paramF(params, "side", 0))%2 != 0 {
			appendIceberg(out, injectStart, lobreplay.SideAsk, lobreplay.SideBuy, params, rng, appendTrade, appendLevel, price)
			break
		}
		appendIceberg(out, injectStart, lobreplay.SideBid, lobreplay.SideSell, params, rng, appendTrade, appendLevel, price)
	case KindMarginLiquidation:
		sells := int(paramF(params, "clips", 12))
		interval := durationOfMs("intervalMs", 4000)
		qtyStep := int64(paramF(params, "qtyStep", 200))
		stepPct := paramF(params, "stepPct", 0.03)
		for i := 0; i < sells; i++ {
			ts := injectStart.Add(time.Duration(i)*interval + jitterOf())
			units := price - int64(math.Round(float64(price)*stepPct/100*float64(i)))
			appendTrade(ts, lobreplay.SideSell, qtyStep*int64(i+1), units)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out
}

// appendIceberg appends the visible-refill pattern: the displayed size is eaten
// by a trade and restored shortly after. tradeSide hits the bookSide level:
// a bid iceberg is eaten by sell aggressors.
func appendIceberg(
	out []lobreplay.Event,
	start time.Time,
	bookSide, tradeSide string,
	params map[string]float64,
	rng *rand.Rand,
	appendTrade func(time.Time, string, int64, int64),
	appendLevel func(time.Time, string, int64, int64),
	price int64,
) {
	cycles := int(paramF(params, "cycles", 6))
	levelQty := int64(paramF(params, "levelQty", 400))
	eatQty := int64(paramF(params, "eatQty", float64(levelQty)/2))
	if levelQty < 2 || eatQty < 1 || eatQty >= levelQty {
		levelQty, eatQty = 400, 200
	}
	appendLevel(start, bookSide, price, levelQty)
	for i := 0; i < cycles; i++ {
		// Irregular cycle timing: an iceberg is an execution pattern, not a
		// perfectly periodic schedule, and the detector must not confuse it
		// with a TWAP.
		base := start.Add(150*time.Millisecond + time.Duration(i)*1500*time.Millisecond +
			time.Duration(rng.Int63n(400))*time.Millisecond)
		appendTrade(base, tradeSide, eatQty, price)
		appendLevel(base.Add(60*time.Millisecond), bookSide, price, levelQty-eatQty)
		appendLevel(base.Add(300*time.Millisecond), bookSide, price, levelQty)
	}
}

// firstEventTime returns the earliest timestamp of the events.
func firstEventTime(events []lobreplay.Event) time.Time {
	var first time.Time
	for _, ev := range events {
		if ev.TS.IsZero() {
			continue
		}
		if first.IsZero() || ev.TS.Before(first) {
			first = ev.TS
		}
	}
	return first
}

// lastEventTime returns the latest timestamp of the events.
func lastEventTime(events []lobreplay.Event) time.Time {
	var last time.Time
	for _, ev := range events {
		if ev.TS.IsZero() {
			continue
		}
		if last.IsZero() || ev.TS.After(last) {
			last = ev.TS
		}
	}
	return last
}

// maxTradeNo returns the largest trade number of the tape (0 when there is none).
func maxTradeNo(events []lobreplay.Event) int64 {
	var max int64
	for _, ev := range events {
		if ev.Kind == lobreplay.KindTrade && ev.Trade != nil && ev.Trade.TradeNo > max {
			max = ev.Trade.TradeNo
		}
	}
	return max
}

// lastTradePrice returns the price of the last trade before the instant, or 0.
func lastTradePrice(events []lobreplay.Event, before time.Time) int64 {
	var price int64
	for _, ev := range events {
		if ev.Kind != lobreplay.KindTrade || ev.Qty <= 0 {
			continue
		}
		if ev.TS.After(before) {
			continue
		}
		if units := decimalUnits(ev.Price); units > 0 {
			price = units
		}
	}
	return price
}

// tradeVolumeIn returns the traded volume in [from, to).
func tradeVolumeIn(events []lobreplay.Event, from, to time.Time) int64 {
	var volume int64
	for _, ev := range events {
		if ev.Kind != lobreplay.KindTrade || ev.Qty <= 0 {
			continue
		}
		if ev.TS.Before(from) || !ev.TS.Before(to) {
			continue
		}
		volume += ev.Qty
	}
	return volume
}

// paramF returns a numeric parameter or the default.
func paramF(params map[string]float64, key string, def float64) float64 {
	if value, ok := params[key]; ok {
		return value
	}
	return def
}

// maxInt64 returns the larger of two int64 values.
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
