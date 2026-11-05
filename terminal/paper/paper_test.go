package paper

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func newTestBroker(cash, feeBps float64) *Broker {
	b := New(Config{InitialCash: cash, FeeBps: feeBps})
	b.now = fixedClock()
	return b
}

func quote(symbol string, last, bid, ask float64) QuoteView {
	return QuoteView{Symbol: symbol, Last: last, Bid: bid, Ask: ask, Time: time.Now()}
}

// TestMarketBuyFill checks that a market buy fills at the ask, moves cash and
// opens a position with the correct average price.
func TestMarketBuyFill(t *testing.T) {
	b := newTestBroker(100000, 5)
	o, err := b.Place(OrderRequest{Symbol: "TQBR:SBER", Side: Buy, Qty: 10, Type: Market, Source: "ui"}, quote("TQBR:SBER", 293.22, 293.20, 293.26))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if o.Status != StatusFilled || o.Filled != 10 {
		t.Fatalf("order = %+v, want filled qty 10", o)
	}
	if o.AvgFillPrice != 293.26 {
		t.Fatalf("avg fill = %v, want ask 293.26", o.AvgFillPrice)
	}
	fee := 293.26 * 10 * 5 / 10000
	snap := b.Snapshot()
	if want := 100000 - 293.26*10 - fee; !floatEq(snap.Cash, want) {
		t.Fatalf("cash = %v, want %v", snap.Cash, want)
	}
	if !floatEq(snap.Fees, fee) {
		t.Fatalf("fees = %v, want %v", snap.Fees, fee)
	}
	if len(snap.Positions) != 1 || snap.Positions[0].Qty != 10 || !floatEq(snap.Positions[0].AvgPrice, 293.26) {
		t.Fatalf("positions = %+v", snap.Positions)
	}
	if len(snap.Orders) != 1 || len(snap.Trades) != 1 || snap.Trades[0].Price != 293.26 {
		t.Fatalf("orders/trades = %+v / %+v", snap.Orders, snap.Trades)
	}
}

// TestMarketSellUsesBidAndRealizesPnL checks sell pricing and realized PnL on
// the average price.
func TestMarketSellUsesBidAndRealizesPnL(t *testing.T) {
	b := newTestBroker(100000, 0)
	if _, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 10, Type: Market}, quote("S", 100, 100, 101)); err != nil {
		t.Fatalf("buy: %v", err)
	}
	o, err := b.Place(OrderRequest{Symbol: "S", Side: Sell, Qty: 4, Type: Market}, quote("S", 110, 109, 110))
	if err != nil {
		t.Fatalf("sell: %v", err)
	}
	if o.AvgFillPrice != 109 {
		t.Fatalf("sell fill = %v, want bid 109", o.AvgFillPrice)
	}
	snap := b.Snapshot()
	if !floatEq(snap.RealizedPnL, (109-101)*4) {
		t.Fatalf("realized = %v, want %v", snap.RealizedPnL, (109-101)*4)
	}
	if len(snap.Positions) != 1 || snap.Positions[0].Qty != 6 {
		t.Fatalf("positions = %+v", snap.Positions)
	}
}

// TestSellMoreThanPositionRejected checks that short selling is rejected.
func TestSellMoreThanPositionRejected(t *testing.T) {
	b := newTestBroker(100000, 5)
	o, err := b.Place(OrderRequest{Symbol: "S", Side: Sell, Qty: 1, Type: Market}, quote("S", 100, 99, 100))
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *RejectError", err)
	}
	if o.Status != StatusRejected || o.Message == "" {
		t.Fatalf("order = %+v, want rejected with message", o)
	}
	snap := b.Snapshot()
	if snap.Cash != 100000 || len(snap.Positions) != 0 || len(snap.Trades) != 0 {
		t.Fatalf("snapshot after reject = %+v", snap)
	}
	if len(snap.Orders) != 1 {
		t.Fatalf("orders = %+v, want rejected order kept", snap.Orders)
	}
}

// TestInsufficientCashRejected checks the cash guard.
func TestInsufficientCashRejected(t *testing.T) {
	b := newTestBroker(100, 0)
	_, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 10, Type: Market}, quote("S", 100, 100, 100))
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *RejectError", err)
	}
	if snap := b.Snapshot(); snap.Cash != 100 || len(snap.Positions) != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// TestLimitPendingThenFillOnQuote checks a resting limit order filled by a
// later quote touch, and that only touching quotes fill it.
func TestLimitPendingThenFillOnQuote(t *testing.T) {
	b := newTestBroker(100000, 0)
	o, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 5, Type: Limit, Price: 90}, quote("S", 100, 99, 101))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if o.Status != StatusNew || o.Filled != 0 {
		t.Fatalf("order = %+v, want resting new", o)
	}
	if ev := b.OnQuote(quote("S", 100, 95, 96)); len(ev) != 0 {
		t.Fatalf("events on non-touching quote = %+v", ev)
	}
	ev := b.OnQuote(quote("S", 90, 89, 90))
	if len(ev) == 0 {
		t.Fatal("no events on touching quote")
	}
	snap := b.Snapshot()
	if snap.Orders[0].Status != StatusFilled || snap.Orders[0].AvgFillPrice != 90 {
		t.Fatalf("order after fill = %+v", snap.Orders[0])
	}
	if len(snap.Positions) != 1 || snap.Positions[0].Qty != 5 {
		t.Fatalf("positions = %+v", snap.Positions)
	}
}

// TestMarketableLimitFillsImmediately checks price-crossing limit orders.
func TestMarketableLimitFillsImmediately(t *testing.T) {
	b := newTestBroker(100000, 0)
	o, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 5, Type: Limit, Price: 102}, quote("S", 100, 99, 101))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if o.Status != StatusFilled {
		t.Fatalf("order = %+v, want immediately filled", o)
	}
	// Sell limit below the bid must also be marketable.
	if _, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 5, Type: Market}, quote("S", 100, 99, 101)); err != nil {
		t.Fatalf("buy: %v", err)
	}
	o, err = b.Place(OrderRequest{Symbol: "S", Side: Sell, Qty: 3, Type: Limit, Price: 95}, quote("S", 100, 99, 101))
	if err != nil {
		t.Fatalf("sell limit: %v", err)
	}
	if o.Status != StatusFilled {
		t.Fatalf("sell order = %+v, want filled", o)
	}
}

// TestCancel checks cancellation of a resting order and rejection of cancelling
// a filled one.
func TestCancel(t *testing.T) {
	b := newTestBroker(100000, 0)
	o, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 1, Type: Limit, Price: 50}, quote("S", 100, 99, 101))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	got, err := b.Cancel(o.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got.Status != StatusCancelled {
		t.Fatalf("status = %v, want cancelled", got.Status)
	}
	if _, err := b.Cancel(o.ID); !errors.Is(err, ErrOrderNotCancellable) {
		t.Fatalf("second cancel err = %v, want ErrOrderNotCancellable", err)
	}
	if _, err := b.Cancel("ord_999999"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("missing cancel err = %v, want ErrOrderNotFound", err)
	}
}

// TestRejectedOrderKeepsCash guards against half-applied fills.
func TestRejectedOrderKeepsCash(t *testing.T) {
	// FeeBps 0 means the 5 bps default: 10 shares at 100 plus the 0.5 fee is
	// exactly all cash, so a second buy must be rejected and leave the account
	// untouched.
	b := newTestBroker(1000.5, 0)
	if _, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 10, Type: Market}, quote("S", 100, 100, 100)); err != nil {
		t.Fatalf("first buy: %v", err)
	}
	if _, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 1, Type: Market}, quote("S", 100, 100, 100)); err == nil {
		t.Fatal("second buy: want error")
	}
	snap := b.Snapshot()
	if snap.Cash != 0 || len(snap.Positions) != 1 || snap.Positions[0].Qty != 10 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// TestSnapshotCopies checks that the caller cannot mutate broker state through
// the returned snapshot.
func TestSnapshotCopies(t *testing.T) {
	b := newTestBroker(100000, 0)
	if _, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 2, Type: Market}, quote("S", 100, 100, 100)); err != nil {
		t.Fatalf("Place: %v", err)
	}
	snap := b.Snapshot()
	snap.Positions[0].Qty = 999
	snap.Orders[0].Status = StatusCancelled
	snap.Trades[0].Price = 1
	again := b.Snapshot()
	if again.Positions[0].Qty != 2 || again.Orders[0].Status != StatusFilled || again.Trades[0].Price != 100 {
		t.Fatalf("snapshot leaked internal state: %+v", again)
	}
}

// TestRepeatedQueryDoesNotChangeCash ensures a quote on an instrument without
// pending orders is a no-op.
func TestRepeatedQueryDoesNotChangeCash(t *testing.T) {
	b := newTestBroker(100000, 0)
	for i := 0; i < 10; i++ {
		if ev := b.OnQuote(quote("S", 100, 99, 101)); len(ev) != 0 {
			t.Fatalf("events = %+v", ev)
		}
	}
	if snap := b.Snapshot(); snap.Cash != 100000 {
		t.Fatalf("cash = %v", snap.Cash)
	}
}

// TestConcurrentAccess exercises the broker from several goroutines; run with
// -race to catch unsynchronized access.
func TestConcurrentAccess(t *testing.T) {
	b := newTestBroker(10_000_000, 5)
	q := quote("S", 100, 99, 101)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				switch i % 4 {
				case 0:
					_, _ = b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 1, Type: Market}, q)
				case 1:
					_, _ = b.Place(OrderRequest{Symbol: "S", Side: Sell, Qty: 1, Type: Market}, q)
				case 2:
					_, _ = b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 1, Type: Limit, Price: 50}, q)
				case 3:
					_ = b.Snapshot()
				}
				_ = b.OnQuote(q)
			}
		}(i)
	}
	wg.Wait()
	if snap := b.Snapshot(); snap.Cash < 0 {
		t.Fatalf("negative cash: %v", snap.Cash)
	}
}

func floatEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
