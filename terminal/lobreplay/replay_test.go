package lobreplay

import (
	"context"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native/model"
)

// fixturePath is the synthetic ten-minute SBER recording in the plan Task 11
// schema; see testdata/README.md.
const fixturePath = "testdata/sber-10m.jsonl.gz"

// testStart is 10:00 MSK on 2026-11-05 in UTC.
var testStart = time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)

// at returns the test clock instant ms milliseconds after testStart.
func at(ms int) time.Time { return testStart.Add(time.Duration(ms) * time.Millisecond) }

// mustPrice parses a price literal exactly; a bad literal is a test bug.
func mustPrice(s string) model.Decimal {
	price, err := parsePrice(s)
	if err != nil {
		panic(err)
	}
	return price
}

func snapshot(ts time.Time, side, price string, qty int64) Event {
	return Event{TS: ts, Kind: KindSnapshot, Side: side, Price: mustPrice(price), Qty: qty}
}

func level(ts time.Time, side, price string, qty int64) Event {
	return Event{TS: ts, Kind: KindLevel, Side: side, Price: mustPrice(price), Qty: qty}
}

func gap(ts time.Time) Event { return Event{TS: ts, Kind: KindGap} }

func trade(ts time.Time, side, price string, qty, tradeNo int64) Event {
	ev := Event{TS: ts, Kind: KindTrade, Side: side, Price: mustPrice(price), Qty: qty}
	if tradeNo != 0 {
		ev.Trade = &Trade{TradeNo: tradeNo}
	}
	return ev
}

// sliceSource replays an in-memory event slice; tests do not need a file.
type sliceSource struct {
	events []Event
	next   int
}

func newSliceSource(events ...Event) *sliceSource { return &sliceSource{events: events} }

func (s *sliceSource) Next() (Event, error) {
	if s.next >= len(s.events) {
		return Event{}, io.EOF
	}
	ev := s.events[s.next]
	s.next++
	return ev, nil
}

func (s *sliceSource) Close() error { return nil }

type strategyFunc func(*Book, Event) []Order

func (f strategyFunc) OnEvent(b *Book, ev Event) []Order { return f(b, ev) }

func runEvents(t *testing.T, strategy Strategy, fm FillModel, events ...Event) Result {
	t.Helper()
	res, err := Run(context.Background(), newSliceSource(events...), strategy, fm)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func wantQty(t *testing.T, what string, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s qty = %d, want %d", what, got, want)
	}
}

func wantPrice(t *testing.T, what string, got model.Decimal, want string) {
	t.Helper()
	if priceUnits(got) != priceUnits(mustPrice(want)) {
		t.Fatalf("%s price = %s, want %s", what, got, want)
	}
}

func hasWarning(warnings []string, substr string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, substr) {
			return true
		}
	}
	return false
}

func TestBookBestDepthImbalance(t *testing.T) {
	book := NewBook()
	book.setLevel(SideBid, mustPrice("100.00"), 5)
	book.setLevel(SideBid, mustPrice("99.50"), 7)
	book.setLevel(SideBid, mustPrice("99.00"), 3)
	book.setLevel(SideAsk, mustPrice("101.00"), 4)
	book.setLevel(SideAsk, mustPrice("101.50"), 6)

	bid, ask := book.Best()
	wantPrice(t, "best bid", bid.Price, "100.00")
	wantQty(t, "best bid", bid.Qty, 5)
	wantPrice(t, "best ask", ask.Price, "101.00")
	wantQty(t, "best ask", ask.Qty, 4)

	bids, asks := book.Depth(2)
	if len(bids) != 2 || len(asks) != 2 {
		t.Fatalf("Depth(2) = %d bids, %d asks, want 2/2", len(bids), len(asks))
	}
	wantPrice(t, "bids[0]", bids[0].Price, "100.00")
	wantPrice(t, "bids[1]", bids[1].Price, "99.50")
	wantPrice(t, "asks[0]", asks[0].Price, "101.00")
	wantPrice(t, "asks[1]", asks[1].Price, "101.50")

	// (5 - 4) / (5 + 4) over the best level of each side.
	if got, want := book.Imbalance(1), 1.0/9.0; math.Abs(got-want) > 1e-12 {
		t.Fatalf("Imbalance(1) = %v, want %v", got, want)
	}
	if got := book.Imbalance(0); got != 0 {
		t.Fatalf("Imbalance(0) = %v, want 0", got)
	}
	if got := NewBook().Imbalance(3); got != 0 {
		t.Fatalf("empty book Imbalance = %v, want 0", got)
	}

	// A zero quantity deletes the level.
	book.setLevel(SideBid, mustPrice("100.00"), 0)
	bid, _ = book.Best()
	wantPrice(t, "best bid after delete", bid.Price, "99.50")

	book.Reset()
	bid, ask = book.Best()
	wantQty(t, "best bid after reset", bid.Qty, 0)
	wantQty(t, "best ask after reset", ask.Qty, 0)
}

// TestReplayResetsBookAfterGap is Review Focus 3: the replay must not join the
// book before and after a recording break, and the orders resting across the
// break cannot be trusted either.
func TestReplayResetsBookAfterGap(t *testing.T) {
	type view struct {
		kind string
		bids []Level
		asks []Level
	}
	var views []view
	placed := false
	strategy := strategyFunc(func(b *Book, ev Event) []Order {
		bids, asks := b.Depth(10)
		views = append(views, view{kind: ev.Kind, bids: bids, asks: asks})
		if !placed && ev.Kind == KindSnapshot && ev.Side == SideBid {
			placed = true
			return []Order{{Side: SideBuy, Type: OrderLimit, Price: mustPrice("100.00"), Qty: 5}}
		}
		return nil
	})
	events := []Event{
		snapshot(at(0), SideBid, "100.00", 5),
		snapshot(at(0), SideAsk, "101.00", 5),
		level(at(10), SideBid, "99.50", 7),
		gap(at(20)),
		snapshot(at(30), SideBid, "200.00", 3),
		snapshot(at(30), SideAsk, "201.00", 3),
	}
	res := runEvents(t, strategy, FillModel{QueuePosition: true}, events...)

	if len(views) != len(events) {
		t.Fatalf("strategy saw %d events, want %d", len(views), len(events))
	}
	gapView := views[3]
	if gapView.kind != KindGap || len(gapView.bids) != 0 || len(gapView.asks) != 0 {
		t.Fatalf("gap view = %+v, want an empty book", gapView)
	}
	last := views[len(views)-1]
	if len(last.bids) != 1 || len(last.asks) != 1 {
		t.Fatalf("book after the second snapshot = %+v, want one level per side", last)
	}
	wantPrice(t, "last bid", last.bids[0].Price, "200.00")
	wantQty(t, "last bid", last.bids[0].Qty, 3)
	wantPrice(t, "last ask", last.asks[0].Price, "201.00")
	wantQty(t, "last ask", last.asks[0].Qty, 3)
	for _, old := range []string{"100.00", "101.00", "99.50"} {
		for _, lvl := range append(append([]Level(nil), last.bids...), last.asks...) {
			if priceUnits(lvl.Price) == priceUnits(mustPrice(old)) {
				t.Fatalf("level %s survived the gap", old)
			}
		}
	}

	if res.Cancelled != 1 {
		t.Fatalf("cancelled orders = %d, want 1 (the order resting before the gap)", res.Cancelled)
	}
	if len(res.Fills) != 0 {
		t.Fatalf("fills = %+v, want none", res.Fills)
	}
	if !hasWarning(res.Warnings, "разрыв") {
		t.Fatalf("warnings = %v, want a recording-gap warning", res.Warnings)
	}
}

func TestLimitFillRespectsQueue(t *testing.T) {
	newStrategy := func() Strategy {
		placed := false
		return strategyFunc(func(b *Book, ev Event) []Order {
			if placed || ev.Kind != KindSnapshot || ev.Side != SideAsk {
				return nil
			}
			placed = true
			return []Order{{Side: SideBuy, Type: OrderLimit, Price: mustPrice("100.00"), Qty: 10}}
		})
	}
	events := []Event{
		snapshot(at(0), SideBid, "100.00", 100),
		snapshot(at(0), SideAsk, "101.00", 100),
		trade(at(1000), SideSell, "100.00", 60, 1),
		trade(at(2000), SideSell, "100.00", 50, 2),
	}

	// Queue-aware: the 60-lot sell only eats part of the 100 lots that were
	// resting ahead of the order; the order fills once the next trade consumes
	// the rest of the queue.
	queued := runEvents(t, newStrategy(), FillModel{QueuePosition: true}, events...)
	if len(queued.Fills) != 1 {
		t.Fatalf("queued fills = %+v, want exactly one", queued.Fills)
	}
	fill := queued.Fills[0]
	if !fill.TS.Equal(at(2000)) {
		t.Fatalf("queued fill time = %v, want %v (after the queue was eaten)", fill.TS, at(2000))
	}
	wantPrice(t, "queued fill", fill.Price, "100.00")
	wantQty(t, "queued fill", fill.Qty, 10)
	if fill.Side != SideBuy || fill.Type != OrderLimit {
		t.Fatalf("queued fill = %+v", fill)
	}
	if queued.Filled != 1 {
		t.Fatalf("filled orders = %d, want 1", queued.Filled)
	}

	// Optimistic model: QueuePosition=false ignores the volume ahead, so the
	// first 60-lot trade fills the 10-lot order immediately.
	optimistic := runEvents(t, newStrategy(), FillModel{}, events...)
	if len(optimistic.Fills) != 1 {
		t.Fatalf("optimistic fills = %+v, want exactly one", optimistic.Fills)
	}
	if !optimistic.Fills[0].TS.Equal(at(1000)) {
		t.Fatalf("optimistic fill time = %v, want %v", optimistic.Fills[0].TS, at(1000))
	}
	wantQty(t, "optimistic fill", optimistic.Fills[0].Qty, 10)
}

func TestMarketOrderWalksBook(t *testing.T) {
	baseEvents := func() []Event {
		return []Event{
			snapshot(at(0), SideBid, "100.00", 50),
			snapshot(at(0), SideAsk, "101.00", 5),
			snapshot(at(0), SideAsk, "101.50", 5),
			snapshot(at(0), SideAsk, "102.00", 10),
			level(at(10), SideBid, "100.00", 60), // trigger: the whole book is visible
		}
	}
	marketBuy := func(qty int64) Strategy {
		placed := false
		return strategyFunc(func(b *Book, ev Event) []Order {
			if placed || ev.Kind != KindLevel {
				return nil
			}
			placed = true
			return []Order{{Side: SideBuy, Type: OrderMarket, Qty: qty}}
		})
	}

	t.Run("walks the levels", func(t *testing.T) {
		res := runEvents(t, marketBuy(12), FillModel{FeeBps: 5}, baseEvents()...)
		if len(res.Fills) != 3 {
			t.Fatalf("fills = %+v, want 3 levels", res.Fills)
		}
		wantPrice(t, "fill[0]", res.Fills[0].Price, "101.00")
		wantQty(t, "fill[0]", res.Fills[0].Qty, 5)
		wantPrice(t, "fill[1]", res.Fills[1].Price, "101.50")
		wantQty(t, "fill[1]", res.Fills[1].Qty, 5)
		wantPrice(t, "fill[2]", res.Fills[2].Price, "102.00")
		wantQty(t, "fill[2]", res.Fills[2].Qty, 2)
		if res.UnfilledQty != 0 {
			t.Fatalf("unfilled qty = %d, want 0", res.UnfilledQty)
		}
		if res.Filled != 1 {
			t.Fatalf("filled orders = %d, want 1", res.Filled)
		}
		// 5 bps of the 5 * 101.00 turnover = 0.2525.
		wantPrice(t, "fill[0] fee", res.Fills[0].Fee, "0.252500")
	})

	t.Run("cancels the illiquid remainder", func(t *testing.T) {
		res := runEvents(t, marketBuy(30), FillModel{}, baseEvents()...)
		if len(res.Fills) != 3 {
			t.Fatalf("fills = %+v, want 3", res.Fills)
		}
		var filled int64
		for _, fill := range res.Fills {
			filled += fill.Qty
		}
		wantQty(t, "walked qty", filled, 20)
		if res.UnfilledQty != 10 {
			t.Fatalf("unfilled qty = %d, want the 10-lot IOC remainder", res.UnfilledQty)
		}
		if res.Filled != 0 {
			t.Fatalf("filled orders = %d, want 0 for the partial IOC", res.Filled)
		}
		if !hasWarning(res.Warnings, "ликвидности") {
			t.Fatalf("warnings = %v, want an illiquid-remainder warning", res.Warnings)
		}
	})
}

func TestLatencyDelaysVisibility(t *testing.T) {
	t.Run("limit order joins the queue after the latency", func(t *testing.T) {
		placed := false
		strategy := strategyFunc(func(b *Book, ev Event) []Order {
			if placed || ev.Kind != KindSnapshot || ev.Side != SideAsk {
				return nil
			}
			placed = true
			return []Order{{Side: SideBuy, Type: OrderLimit, Price: mustPrice("100.00"), Qty: 10}}
		})
		events := []Event{
			snapshot(at(0), SideBid, "100.00", 100),
			snapshot(at(0), SideAsk, "101.00", 100),
			trade(at(10), SideSell, "100.00", 200, 1),
			trade(at(60), SideSell, "100.00", 200, 2),
		}
		res := runEvents(t, strategy, FillModel{LatencyMs: 50, QueuePosition: true}, events...)
		if len(res.Fills) != 1 {
			t.Fatalf("fills = %+v, want one fill after the latency", res.Fills)
		}
		if !res.Fills[0].TS.Equal(at(60)) {
			t.Fatalf("fill time = %v, want %v: the trade inside the latency window must not fill", res.Fills[0].TS, at(60))
		}
	})

	t.Run("market order executes against the book at activation", func(t *testing.T) {
		placed := false
		strategy := strategyFunc(func(b *Book, ev Event) []Order {
			if placed || ev.Kind != KindSnapshot || ev.Side != SideAsk {
				return nil
			}
			placed = true
			return []Order{{Side: SideBuy, Type: OrderMarket, Qty: 5}}
		})
		events := []Event{
			snapshot(at(0), SideBid, "100.00", 50),
			snapshot(at(0), SideAsk, "101.00", 5),
			level(at(25), SideAsk, "101.00", 0),
			level(at(25), SideAsk, "105.00", 5),
			level(at(60), SideBid, "100.00", 60),
		}
		res := runEvents(t, strategy, FillModel{LatencyMs: 50}, events...)
		if len(res.Fills) != 1 {
			t.Fatalf("fills = %+v, want one fill at activation", res.Fills)
		}
		if !res.Fills[0].TS.Equal(at(60)) {
			t.Fatalf("fill time = %v, want %v", res.Fills[0].TS, at(60))
		}
		wantPrice(t, "delayed market fill", res.Fills[0].Price, "105.00")
		wantQty(t, "delayed market fill", res.Fills[0].Qty, 5)
	})
}

// marketMaker is the deterministic strategy of the fixture replays: every tenth
// event it quotes a one-lot limit order at the current best bid or ask. It has no
// clock, no randomness and no state beyond its event counter and the side toggle.
type marketMaker struct {
	events int
	buy    bool
}

func newMarketMaker() *marketMaker { return &marketMaker{buy: true} }

func (m *marketMaker) OnEvent(b *Book, ev Event) []Order {
	m.events++
	if m.events%10 != 0 {
		return nil
	}
	bid, ask := b.Best()
	order := Order{Side: SideBuy, Type: OrderLimit, Price: bid.Price, Qty: 1}
	if !m.buy {
		order = Order{Side: SideSell, Type: OrderLimit, Price: ask.Price, Qty: 1}
	}
	if priceUnits(order.Price) <= 0 {
		return nil
	}
	m.buy = !m.buy
	return []Order{order}
}

func runFixture(t *testing.T, strategy Strategy, fm FillModel) Result {
	t.Helper()
	src, err := OpenJSONL(fixturePath)
	if err != nil {
		t.Fatalf("OpenJSONL(%s): %v", fixturePath, err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	res, err := Run(context.Background(), src, strategy, fm)
	if err != nil {
		t.Fatalf("Run fixture: %v", err)
	}
	return res
}

func TestReplayDeterministic(t *testing.T) {
	fm := FillModel{FeeBps: 5}
	first := runFixture(t, newMarketMaker(), fm)
	second := runFixture(t, newMarketMaker(), fm)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two runs differ: events %d/%d, fills %d/%d, round trips %d/%d",
			first.Events, second.Events, len(first.Fills), len(second.Fills),
			len(first.RoundTrips), len(second.RoundTrips))
	}
	if first.Events < 1000 {
		t.Fatalf("fixture replayed only %d events, want >= 1000", first.Events)
	}
	if len(first.Fills) == 0 {
		t.Fatalf("fixture run produced no fills")
	}
	if first.Metrics.Bars != first.Events {
		t.Fatalf("metrics bars = %d, want %d events", first.Metrics.Bars, first.Events)
	}
	if len(first.Equity) != first.Events {
		t.Fatalf("equity points = %d, want %d", len(first.Equity), first.Events)
	}
	if first.Metrics.Trades != len(first.RoundTrips) {
		t.Fatalf("metrics trades = %d, want %d round trips", first.Metrics.Trades, len(first.RoundTrips))
	}
	for name, value := range map[string]float64{
		"finalEquity":    first.Metrics.FinalEquity,
		"totalReturnPct": first.Metrics.TotalReturnPct,
		"maxDrawdownPct": first.Metrics.MaxDrawdownPct,
		"winRatePct":     first.Metrics.WinRatePct,
		"profitFactor":   first.Metrics.ProfitFactor,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("%s = %v, want a finite metric", name, value)
		}
	}
	// The fixture contains one reconnect: the replay reports the recording break.
	if !hasWarning(first.Warnings, "разрыв") {
		t.Fatalf("warnings = %v, want a recording-gap warning", first.Warnings)
	}

	queuedFM := FillModel{FeeBps: 5, QueuePosition: true}
	queuedFirst := runFixture(t, newMarketMaker(), queuedFM)
	queuedSecond := runFixture(t, newMarketMaker(), queuedFM)
	if !reflect.DeepEqual(queuedFirst, queuedSecond) {
		t.Fatalf("two queue-aware runs differ: events %d/%d, fills %d/%d",
			queuedFirst.Events, queuedSecond.Events, len(queuedFirst.Fills), len(queuedSecond.Fills))
	}
}

func TestJSONLSourceReadsFixture(t *testing.T) {
	src, err := OpenJSONL(fixturePath)
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	kinds := make(map[string]int)
	var first, prev time.Time
	trades := 0
	for {
		ev, err := src.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if first.IsZero() {
			first = ev.TS
		}
		if ev.TS.Before(prev) {
			t.Fatalf("timestamps go backwards: %v after %v", ev.TS, prev)
		}
		prev = ev.TS
		kinds[ev.Kind]++
		if ev.Kind == KindTrade {
			trades++
			if ev.Trade == nil {
				t.Fatalf("trade event without the Trade payload: %+v", ev)
			}
			if ev.Side != SideBuy && ev.Side != SideSell {
				t.Fatalf("trade side = %q, want buy or sell", ev.Side)
			}
		}
	}
	if !first.Equal(testStart) {
		t.Fatalf("first event time = %v, want %v", first, testStart)
	}
	if kinds[KindSnapshot] == 0 || kinds[KindLevel] == 0 || kinds[KindTrade] == 0 {
		t.Fatalf("fixture kinds = %v, want snapshot, level and trade rows", kinds)
	}
	if kinds[KindGap] != 1 {
		t.Fatalf("gap rows = %d, want exactly one reconnect marker", kinds[KindGap])
	}
	if trades < 100 {
		t.Fatalf("trades = %d, want >= 100", trades)
	}
}

func TestJSONLSourceParsesPlainLines(t *testing.T) {
	const bookLine = `{"table":"book_events","ts":"2026-11-05 07:00:00.000","recv_ts":"2026-11-05 07:00:00.000000","session_id":"00000000-0000-4000-8000-000000000014","seq":1,"board":"TQBR","sec_code":"SBER","kind":"level","side":"bid","price":"291.460000","qty":100}`
	const tradeLine = `{"table":"trades","ts":"2026-11-05 07:00:00.001","recv_ts":"2026-11-05 07:00:00.001000","board":"TQBR","sec_code":"SBER","trade_no":7,"price":"291.480000","qty":5,"side":"buy","open_interest":0}`
	src := NewJSONLSource(strings.NewReader(bookLine + "\n" + tradeLine + "\n"))
	defer func() {
		if err := src.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	ev, err := src.Next()
	if err != nil {
		t.Fatalf("Next book: %v", err)
	}
	if ev.Kind != KindLevel || ev.Side != SideBid || ev.Qty != 100 {
		t.Fatalf("book event = %+v", ev)
	}
	wantPrice(t, "book event", ev.Price, "291.460000")
	if !ev.TS.Equal(testStart) {
		t.Fatalf("book event time = %v, want %v", ev.TS, testStart)
	}

	ev, err = src.Next()
	if err != nil {
		t.Fatalf("Next trade: %v", err)
	}
	if ev.Kind != KindTrade || ev.Side != SideBuy || ev.Qty != 5 {
		t.Fatalf("trade event = %+v", ev)
	}
	if ev.Trade == nil || ev.Trade.TradeNo != 7 {
		t.Fatalf("trade payload = %+v, want trade_no 7", ev.Trade)
	}
	if _, err := src.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after the end = %v, want io.EOF", err)
	}
}

func TestRunRejectsInvalidSetup(t *testing.T) {
	okStrategy := strategyFunc(func(*Book, Event) []Order { return nil })
	cases := []struct {
		name     string
		src      Source
		strategy Strategy
		fm       FillModel
	}{
		{"nil source", nil, okStrategy, FillModel{}},
		{"nil strategy", newSliceSource(), nil, FillModel{}},
		{"negative latency", newSliceSource(), okStrategy, FillModel{LatencyMs: -1}},
		{"fee out of range", newSliceSource(), okStrategy, FillModel{FeeBps: 10_000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Run(context.Background(), tc.src, tc.strategy, tc.fm); err == nil {
				t.Fatalf("Run: want an error")
			}
		})
	}
}

func TestRunRejectsInvalidOrder(t *testing.T) {
	strategy := strategyFunc(func(*Book, Event) []Order {
		return []Order{{Side: "hold", Type: OrderLimit, Qty: 1}}
	})
	events := []Event{snapshot(at(0), SideBid, "100.00", 1), snapshot(at(0), SideAsk, "101.00", 1)}
	if _, err := Run(context.Background(), newSliceSource(events...), strategy, FillModel{}); err == nil {
		t.Fatalf("Run: want an error for an unknown order side")
	}
}
