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

// TestMarketableLimitFillPrice checks the P2 regression: a crossing limit fills
// at the touch price (ask for a buy, bid for a sell), not at its own limit
// price, so AvgFillPrice/cash/PnL follow the zero-slippage rule of §7.
func TestMarketableLimitFillPrice(t *testing.T) {
	b := newTestBroker(100000, 0)
	// Buy limit 102 crosses ask 101: fill at 101, not 102.
	buy, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 10, Type: Limit, Price: 102}, quote("S", 100, 100, 101))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if buy.AvgFillPrice != 101 {
		t.Fatalf("buy fill = %v, want touch ask 101", buy.AvgFillPrice)
	}
	snap := b.Snapshot()
	wantCash := 100000.0 - 101.0*10 - 101.0*10*5/10000 // 5 bps default commission
	if !floatEq(snap.Cash, wantCash) {
		t.Fatalf("cash = %v, want %v", snap.Cash, wantCash)
	}
	if !floatEq(snap.Positions[0].AvgPrice, 101) {
		t.Fatalf("position avg = %v, want 101", snap.Positions[0].AvgPrice)
	}
	// Sell limit 95 crosses bid 99: fill at 99, not 95, and realize (99-101)*10.
	sell, err := b.Place(OrderRequest{Symbol: "S", Side: Sell, Qty: 10, Type: Limit, Price: 95}, quote("S", 100, 99, 100))
	if err != nil {
		t.Fatalf("sell: %v", err)
	}
	if sell.AvgFillPrice != 99 {
		t.Fatalf("sell fill = %v, want touch bid 99", sell.AvgFillPrice)
	}
	snap = b.Snapshot()
	if !floatEq(snap.RealizedPnL, (99-101)*10) {
		t.Fatalf("realized = %v, want %v", snap.RealizedPnL, (99-101)*10)
	}
	if len(snap.Positions) != 0 {
		t.Fatalf("positions = %+v, want flat", snap.Positions)
	}
	// A resting limit that becomes marketable later also fills at the touch.
	if _, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 1, Type: Limit, Price: 90}, quote("S", 100, 99, 100)); err != nil {
		t.Fatalf("resting place: %v", err)
	}
	if ev := b.OnQuote(quote("S", 90, 89, 89.5)); len(ev) == 0 {
		t.Fatal("no fill event")
	}
	snap = b.Snapshot()
	lastTrade := snap.Trades[len(snap.Trades)-1]
	if lastTrade.Price != 89.5 {
		t.Fatalf("resting fill price = %v, want touch ask 89.5", lastTrade.Price)
	}
}

// TestDuplicateClientOrderIDRejected is the R3 guard rail of the paper broker:
// an order that reuses a client order id already processed by the account is
// rejected instead of being executed a second time.
func TestDuplicateClientOrderIDRejected(t *testing.T) {
	b := newTestBroker(100000, 0)
	q := quote("S", 100, 100, 100)
	req := OrderRequest{Symbol: "S", Side: Buy, Qty: 1, Type: Market, ClientOrderID: "conv-1:call_1"}
	if _, err := b.Place(req, q); err != nil {
		t.Fatalf("first Place: %v", err)
	}
	if _, err := b.Place(req, q); !errors.Is(err, ErrDuplicateClientOrderID) {
		t.Fatalf("duplicate Place err = %v, want ErrDuplicateClientOrderID", err)
	}
	snap := b.Snapshot()
	if len(snap.Orders) != 1 || len(snap.Trades) != 1 || len(snap.Positions) != 1 {
		t.Fatalf("snapshot after duplicate = %+v, want one order/trade/position", snap)
	}
	if snap.Positions[0].Qty != 1 {
		t.Fatalf("position qty = %d, want 1 (no second fill)", snap.Positions[0].Qty)
	}

	// Orders without a client order id are never treated as duplicates.
	for i := 0; i < 2; i++ {
		if _, err := b.Place(OrderRequest{Symbol: "S", Side: Buy, Qty: 1, Type: Market}, q); err != nil {
			t.Fatalf("anonymous Place #%d: %v", i+1, err)
		}
	}
}

// TestAllocateMovesBudgetFromMainAccount checks the budget transfer the agent
// autonomy uses: money leaves the main account and appears on the named
// sub-account, and the transfer can never exceed the free main cash.
func TestAllocateMovesBudgetFromMainAccount(t *testing.T) {
	b := newTestBroker(100_000, 5)
	if err := b.Allocate(AutonomyAccount, 25_000); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	snap := b.Snapshot()
	if !floatEq(snap.Cash, 75_000) {
		t.Fatalf("main cash = %v, want 75000", snap.Cash)
	}
	acc, ok := snap.Accounts[AutonomyAccount]
	if !ok {
		t.Fatalf("no %q account in snapshot accounts %v", AutonomyAccount, snap.Accounts)
	}
	if !floatEq(acc.Cash, 25_000) {
		t.Fatalf("autonomy cash = %v, want 25000", acc.Cash)
	}
	if main, ok := snap.Accounts[MainAccount]; !ok || !floatEq(main.Cash, 75_000) {
		t.Fatalf("main account view = %+v, want cash 75000", main)
	}

	// The transfer cannot exceed the main cash and leaves the account intact.
	if err := b.Allocate(AutonomyAccount, 1_000_000); err == nil {
		t.Fatal("oversized Allocate must fail")
	}
	if snap := b.Snapshot(); !floatEq(snap.Cash, 75_000) {
		t.Fatalf("main cash changed by a failed transfer: %v", snap.Cash)
	}
	// A non-positive budget is rejected.
	for _, amount := range []float64{0, -100} {
		if err := b.Allocate(AutonomyAccount, amount); err == nil {
			t.Fatalf("Allocate(%v) must fail", amount)
		}
	}
}

// TestPlaceAtFillsIntoNamedAccount checks that a replay fill is executed at the
// explicit price/time on the named account, leaves the main account untouched
// and shows up in the global order/trade history.
func TestPlaceAtFillsIntoNamedAccount(t *testing.T) {
	b := newTestBroker(100_000, 5)
	if err := b.Allocate(AutonomyAccount, 10_000); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	at := time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC) // 10:00 МСК
	order, err := b.PlaceAt(
		OrderRequest{Symbol: "TQBR:SBER", Side: Buy, Qty: 10, Type: Market, Source: "autonomy"},
		QuoteView{Symbol: "TQBR:SBER", Last: 100, Time: at}, at, AutonomyAccount)
	if err != nil {
		t.Fatalf("PlaceAt: %v", err)
	}
	if order.Status != StatusFilled || order.Filled != 10 || order.AvgFillPrice != 100 {
		t.Fatalf("order = %+v, want a fill of 10 at 100", order)
	}
	if !order.CreatedAt.Equal(at) || !order.UpdatedAt.Equal(at) {
		t.Fatalf("order times = %v/%v, want the replay instant %v", order.CreatedAt, order.UpdatedAt, at)
	}
	fee := 100.0 * 10 * 5 / 10000
	snap := b.Snapshot()
	// The allocation already moved 10000 to the autonomy account; the fill
	// itself must not touch the main account.
	if !floatEq(snap.Cash, 90_000) {
		t.Fatalf("main cash = %v, want 90000 untouched by the autonomy fill", snap.Cash)
	}
	if len(snap.Orders) != 1 || len(snap.Trades) != 1 {
		t.Fatalf("global orders/trades = %d/%d, want 1/1", len(snap.Orders), len(snap.Trades))
	}
	acc := snap.Accounts[AutonomyAccount]
	if !floatEq(acc.Cash, 10_000-1000-fee) {
		t.Fatalf("autonomy cash = %v, want %v", acc.Cash, 10_000-1000-fee)
	}
	if len(acc.Positions) != 1 || acc.Positions[0].Qty != 10 || !floatEq(acc.Positions[0].AvgPrice, 100) {
		t.Fatalf("autonomy positions = %+v", acc.Positions)
	}
	if len(acc.Trades) != 1 || acc.Trades[0].Account != AutonomyAccount || !acc.Trades[0].Time.Equal(at) {
		t.Fatalf("autonomy trades = %+v, want one trade at %v on %q", acc.Trades, at, AutonomyAccount)
	}
	if main := snap.Accounts[MainAccount]; len(main.Positions) != 0 || len(main.Trades) != 0 {
		t.Fatalf("main account received the autonomy fill: %+v", main)
	}

	// A sell on the same account realizes PnL there and keeps the histories apart.
	sellAt := at.Add(5 * time.Minute)
	if _, err := b.PlaceAt(
		OrderRequest{Symbol: "TQBR:SBER", Side: Sell, Qty: 10, Type: Market, Source: "autonomy"},
		QuoteView{Symbol: "TQBR:SBER", Last: 110, Time: sellAt}, sellAt, AutonomyAccount); err != nil {
		t.Fatalf("sell PlaceAt: %v", err)
	}
	snap = b.Snapshot()
	acc = snap.Accounts[AutonomyAccount]
	if !floatEq(acc.RealizedPnL, (110-100)*10) {
		t.Fatalf("autonomy realized = %v, want %v", acc.RealizedPnL, (110-100)*10)
	}
	if len(acc.Positions) != 0 || len(acc.Trades) != 2 {
		t.Fatalf("autonomy after sell = %+v", acc)
	}
	if !floatEq(snap.RealizedPnL, 0) {
		t.Fatalf("main realized = %v, want 0 (main account traded nothing)", snap.RealizedPnL)
	}
}

// TestPlaceAtDefaultsToMainAndRejectsDuplicates covers the account default and
// the idempotency key across Place/PlaceAt.
func TestPlaceAtDefaultsToMainAndRejectsDuplicates(t *testing.T) {
	b := newTestBroker(100_000, 0)
	at := time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)
	req := OrderRequest{Symbol: "S", Side: Buy, Qty: 1, Type: Market, ClientOrderID: "conv-1:call_1"}
	if _, err := b.PlaceAt(req, QuoteView{Symbol: "S", Last: 100}, at, ""); err != nil {
		t.Fatalf("PlaceAt on the default account: %v", err)
	}
	snap := b.Snapshot()
	if len(snap.Accounts[MainAccount].Orders) != 1 || !floatEq(snap.Cash, 100_000-100*(1+5.0/10000)) {
		t.Fatalf("default account did not receive the fill: %+v", snap.Accounts[MainAccount])
	}
	if _, err := b.PlaceAt(req, QuoteView{Symbol: "S", Last: 100}, at, ""); !errors.Is(err, ErrDuplicateClientOrderID) {
		t.Fatalf("duplicate PlaceAt err = %v, want ErrDuplicateClientOrderID", err)
	}
}
