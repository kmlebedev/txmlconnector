package fingerprint

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native/model"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
)

// testStart is 10:00 Moscow time on 2026-11-05 in UTC.
var testStart = time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)

// priceOf converts whole currency units into a Decimal64(6) price.
func priceOf(units int64) model.Decimal {
	return model.Decimal{Units: units * priceFactor, Scale: uint8(priceScale)}
}

// syntheticTape builds a deterministic background tape: a ten-level book snapshot
// and about 1.2 random trades per second (both sides, sizes 1..200), with an
// occasional level update. It is the base tape the Synthesize ground truth is
// planted into.
func syntheticTape(seed int64, start time.Time, duration time.Duration) []lobreplay.Event {
	rng := rand.New(rand.NewSource(seed))
	const basePrice = 100 * priceFactor
	events := make([]lobreplay.Event, 0, 1024)
	for level := int64(1); level <= 10; level++ {
		step := level * (priceFactor / 100) // 0.01
		events = append(events,
			lobreplay.Event{TS: start, Kind: lobreplay.KindSnapshot, Side: lobreplay.SideBid,
				Price: model.Decimal{Units: basePrice - step, Scale: uint8(priceScale)},
				Qty:   int64(100 + rng.Intn(900))},
			lobreplay.Event{TS: start, Kind: lobreplay.KindSnapshot, Side: lobreplay.SideAsk,
				Price: model.Decimal{Units: basePrice + step, Scale: uint8(priceScale)},
				Qty:   int64(100 + rng.Intn(900))},
		)
	}
	ts := start
	tradeNo := int64(0)
	end := start.Add(duration)
	for ts.Before(end) {
		ts = ts.Add(time.Duration(200+rng.Intn(700)) * time.Millisecond)
		if ts.After(end) {
			break
		}
		side := lobreplay.SideBuy
		if rng.Intn(2) == 0 {
			side = lobreplay.SideSell
		}
		noise := int64(rng.Intn(81)-40) * (priceFactor / 10000) // ±0.004
		tradeNo++
		events = append(events, lobreplay.Event{
			TS: ts, Kind: lobreplay.KindTrade, Side: side,
			Price: model.Decimal{Units: basePrice + noise, Scale: uint8(priceScale)},
			Qty:   int64(1 + rng.Intn(200)),
			Trade: &lobreplay.Trade{TradeNo: tradeNo},
		})
		if rng.Intn(3) == 0 {
			bookSide := lobreplay.SideBid
			if rng.Intn(2) == 0 {
				bookSide = lobreplay.SideAsk
			}
			step := int64(1+rng.Intn(10)) * (priceFactor / 100)
			units := basePrice - step
			if bookSide == lobreplay.SideAsk {
				units = basePrice + step
			}
			events = append(events, lobreplay.Event{
				TS: ts, Kind: lobreplay.KindLevel, Side: bookSide,
				Price: model.Decimal{Units: units, Scale: uint8(priceScale)},
				Qty:   int64(100 + rng.Intn(900)),
			})
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS.Before(events[j].TS) })
	return events
}

// shuffleTape destroys any schedule in the tape: the trades are shuffled and get
// random timestamps over the same span, while the book events keep their times.
// Clip sizes survive, the periodicity does not: exactly the false-positive check
// of plan Task 17.
func shuffleTape(events []lobreplay.Event, seed int64) []lobreplay.Event {
	rng := rand.New(rand.NewSource(seed))
	var trades, others []lobreplay.Event
	var first, last time.Time
	for _, ev := range events {
		if ev.Kind == lobreplay.KindTrade {
			trades = append(trades, ev)
			continue
		}
		others = append(others, ev)
		if first.IsZero() || ev.TS.Before(first) {
			first = ev.TS
		}
		if last.IsZero() || ev.TS.After(last) {
			last = ev.TS
		}
	}
	rng.Shuffle(len(trades), func(i, j int) { trades[i], trades[j] = trades[j], trades[i] })
	span := last.Sub(first)
	out := make([]lobreplay.Event, 0, len(events))
	out = append(out, others...)
	for _, ev := range trades {
		ev.TS = first.Add(time.Duration(rng.Int63n(int64(span) + 1)))
		out = append(out, ev)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out
}

// findFootprint reports whether fps contains a kind footprint overlapping
// [from, to].
func findFootprint(fps []Footprint, kind string, from, to time.Time) (Footprint, bool) {
	for _, fp := range fps {
		if fp.Kind != kind {
			continue
		}
		if fp.From.Before(to) && fp.To.After(from) {
			return fp, true
		}
	}
	return Footprint{}, false
}

// TestDetectsSyntheticTWAP is the plan Task 17 recall test: an injected TWAP
// schedule (20 equal clips every two seconds) must be found in at least 80% of
// the 20 synthetic backgrounds.
func TestDetectsSyntheticTWAP(t *testing.T) {
	const seeds = 20
	cfg := DefaultConfig()
	params := map[string]float64{"startMs": 125000, "clips": 20, "qty": 25, "intervalMs": 2000}
	from := testStart.Add(125 * time.Second)
	to := from.Add(19 * 2 * time.Second)
	recall := 0
	for seed := int64(1); seed <= seeds; seed++ {
		base := syntheticTape(seed, testStart, 10*time.Minute)
		tape := Synthesize(KindTWAP, base, params, seed)
		fps := Detect(context.Background(), tape, cfg)
		fp, ok := findFootprint(fps, KindTWAP, from, to)
		if !ok {
			t.Logf("seed %d: TWAP not found; footprints %v", seed, footprintSummary(fps))
			continue
		}
		if fp.Side != lobreplay.SideBuy {
			t.Fatalf("seed %d: TWAP side = %q, want buy", seed, fp.Side)
		}
		recall++
	}
	if want := int(0.8 * seeds); recall < want {
		t.Fatalf("TWAP recall = %d/%d, want >= %d", recall, seeds, want)
	}
}

// TestDetectsSyntheticIceberg plants visible-refill cycles at one bid level and
// expects the iceberg footprint of the buy side.
func TestDetectsSyntheticIceberg(t *testing.T) {
	base := syntheticTape(42, testStart, 10*time.Minute)
	params := map[string]float64{"startMs": 120000, "cycles": 6, "levelQty": 500, "eatQty": 250}
	tape := Synthesize(KindIceberg, base, params, 7)
	fps := Detect(context.Background(), tape, DefaultConfig())
	from := testStart.Add(120 * time.Second)
	to := from.Add(10 * time.Second)
	fp, ok := findFootprint(fps, KindIceberg, from, to)
	if !ok {
		t.Fatalf("iceberg not found; footprints %v", footprintSummary(fps))
	}
	if fp.Side != lobreplay.SideBuy {
		t.Fatalf("iceberg side = %q, want buy (a bid level is refilled)", fp.Side)
	}
	if cycles := fp.Evidence["refill_cycles"]; cycles < 4 {
		t.Fatalf("refill cycles = %v, want >= 4", cycles)
	}
}

// TestDetectsSyntheticMarginLiquidation plants an escalating sell series inside
// the default 14:00-14:30 MSK margin-call window and expects the sell-side
// margin_liquidation footprint.
func TestDetectsSyntheticMarginLiquidation(t *testing.T) {
	baseStart := time.Date(2026, 11, 5, 10, 50, 0, 0, time.UTC) // 13:50 MSK
	base := syntheticTape(99, baseStart, 20*time.Minute)
	params := map[string]float64{
		"startMs": 11*60000 + 5000, "clips": 12, "intervalMs": 4000, "qtyStep": 200, "stepPct": 0.03,
	}
	tape := Synthesize(KindMarginLiquidation, base, params, 3)
	fps := Detect(context.Background(), tape, DefaultConfig())
	from := baseStart.Add(11*time.Minute + 5*time.Second)
	to := from.Add(11 * 4 * time.Second)
	fp, ok := findFootprint(fps, KindMarginLiquidation, from, to)
	if !ok {
		t.Fatalf("margin liquidation not found; footprints %v", footprintSummary(fps))
	}
	if fp.Side != lobreplay.SideSell {
		t.Fatalf("margin liquidation side = %q, want sell", fp.Side)
	}
	if fp.Confidence < 0.6 {
		t.Fatalf("confidence = %.3f, want a reportable footprint", fp.Confidence)
	}
}

// TestNoFalsePositiveOnShuffledTape is the plan false-positive guard: shuffling
// the traded tape keeps the clip sizes but destroys the execution schedule, so no
// footprint may reach Confidence 0.3 even with a lowered reporting threshold.
func TestNoFalsePositiveOnShuffledTape(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinConfidence = 0.3
	params := map[string]float64{"startMs": 125000, "clips": 20, "qty": 25, "intervalMs": 2000}
	for seed := int64(1); seed <= 5; seed++ {
		base := syntheticTape(seed, testStart, 10*time.Minute)
		tape := shuffleTape(Synthesize(KindTWAP, base, params, seed), seed*7)
		for _, fp := range Detect(context.Background(), tape, cfg) {
			if fp.Confidence >= 0.3 {
				t.Fatalf("seed %d: false positive %s/%s confidence %.3f evidence %v",
					seed, fp.Kind, fp.Side, fp.Confidence, fp.Evidence)
			}
		}
	}
}

// TestSynthesizeContracts checks the ground-truth generator invariants the
// detector tests rely on: the tape stays ordered, the base is preserved, and the
// injected clips have distinct trade numbers.
func TestSynthesizeContracts(t *testing.T) {
	base := syntheticTape(11, testStart, 2*time.Minute)
	baseTrades := 0
	for _, ev := range base {
		if ev.Kind == lobreplay.KindTrade {
			baseTrades++
		}
	}
	params := map[string]float64{"startMs": 30000, "clips": 8, "qty": 10, "intervalMs": 1000}
	tape := Synthesize(KindTWAP, base, params, 5)
	if len(tape) != len(base)+8 {
		t.Fatalf("tape length = %d, want %d", len(tape), len(base)+8)
	}
	for i := 1; i < len(tape); i++ {
		if tape[i].TS.Before(tape[i-1].TS) {
			t.Fatalf("tape is not ordered at %d: %v after %v", i, tape[i].TS, tape[i-1].TS)
		}
	}
	maxBaseTradeNo := maxTradeNo(base)
	trades := make(map[int64]bool)
	injected := 0
	for _, ev := range tape {
		if ev.Kind != lobreplay.KindTrade {
			continue
		}
		if ev.Trade == nil {
			t.Fatalf("trade without payload: %+v", ev)
		}
		if trades[ev.Trade.TradeNo] {
			t.Fatalf("duplicate trade number %d", ev.Trade.TradeNo)
		}
		trades[ev.Trade.TradeNo] = true
		if ev.Trade.TradeNo > maxBaseTradeNo {
			injected++
			if ev.Qty != 10 || ev.Side != lobreplay.SideBuy {
				t.Fatalf("injected clip = %+v, want qty 10 buy", ev)
			}
		}
	}
	if injected != 8 {
		t.Fatalf("injected clips = %d, want 8 (base trades = %d)", injected, baseTrades)
	}
	if unknown := Synthesize("mystery", base, nil, 1); len(unknown) != len(base) {
		t.Fatalf("unknown kind changed the tape: %d events, want %d", len(unknown), len(base))
	}
}

// TestMarginCallWindowParsing pins the documented default windows and the
// "HH:MM-HH:MM" parser (malformed specs are ignored, not fatal).
func TestMarginCallWindowParsing(t *testing.T) {
	if len(DefaultMarginCallWindows) == 0 {
		t.Fatal("no default margin-call windows documented")
	}
	ranges := parseMarginCallWindows(DefaultMarginCallWindows)
	if len(ranges) != len(DefaultMarginCallWindows) {
		t.Fatalf("parsed %d of %d default windows", len(ranges), len(DefaultMarginCallWindows))
	}
	if ranges[0].start != 14*60 || ranges[0].end != 14*60+30 {
		t.Fatalf("first default window = %+v, want 14:00-14:30", ranges[0])
	}
	if got := parseMarginCallWindows([]string{"bad", "25:99-01:00", "18:00-18:30"}); len(got) != 1 {
		t.Fatalf("malformed windows parsed as %+v, want the valid one only", got)
	}
	// 13:59:30-14:00:30 MSK overlaps 14:00-14:30 MSK.
	inside := window{from: time.Date(2026, 11, 5, 13, 59, 30, 0, moscow), to: time.Date(2026, 11, 5, 14, 0, 30, 0, moscow)}
	if !overlapsMarginCall(inside, ranges) {
		t.Fatal("a window inside 14:00-14:30 MSK was not matched")
	}
	outside := window{from: time.Date(2026, 11, 5, 12, 0, 0, 0, moscow), to: time.Date(2026, 11, 5, 12, 1, 0, 0, moscow)}
	if overlapsMarginCall(outside, ranges) {
		t.Fatal("a 12:00-12:01 MSK window matched a margin-call window")
	}
}

// TestDetectEmptyAndCancelled covers the degenerate inputs: no events/no trades
// return no footprints and a cancelled context returns without panicking.
func TestDetectEmptyAndCancelled(t *testing.T) {
	if fps := Detect(context.Background(), nil, DefaultConfig()); len(fps) != 0 {
		t.Fatalf("nil events returned %+v", fps)
	}
	base := syntheticTape(3, testStart, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if fps := Detect(ctx, base, DefaultConfig()); len(fps) != 0 {
		t.Fatalf("cancelled Detect returned %+v", fps)
	}
}

// TestDetectDeterministic guards the no-clock/no-map-order rule: two runs over
// the same tape return the same footprints.
func TestDetectDeterministic(t *testing.T) {
	base := syntheticTape(21, testStart, 10*time.Minute)
	params := map[string]float64{"startMs": 125000, "clips": 20, "qty": 25, "intervalMs": 2000}
	tape := Synthesize(KindTWAP, base, params, 21)
	first := Detect(context.Background(), tape, DefaultConfig())
	second := Detect(context.Background(), tape, DefaultConfig())
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two runs differ: first %v, second %v", footprintSummary(first), footprintSummary(second))
	}
}

// footprintSummary renders the footprints compactly for a failure message.
func footprintSummary(fps []Footprint) []string {
	out := make([]string, 0, len(fps))
	for _, fp := range fps {
		out = append(out, fmt.Sprintf("%s/%s@%s conf=%.3f evidence=%v",
			fp.Kind, fp.Side, fp.From.In(moscow).Format("15:04:05"), fp.Confidence, fp.Evidence))
	}
	return out
}
