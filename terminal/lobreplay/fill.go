package lobreplay

import (
	"fmt"
	"math"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native/model"
)

// Order types of the replay.
const (
	// OrderLimit is a resting order: it joins the book at Price and fills only
	// when trades print at that price. An empty Order.Type means OrderLimit.
	OrderLimit = "limit"
	// OrderMarket is an immediate-or-cancel order: at activation it walks the
	// recorded book level by level and the unexecuted remainder is cancelled.
	OrderMarket = "market"
)

// Order is one instruction a Strategy returns from OnEvent.
type Order struct {
	// Side is SideBuy or SideSell.
	Side string
	// Type is OrderLimit (or the empty default) or OrderMarket.
	Type string
	// Price is the limit price; it is ignored by market orders.
	Price model.Decimal
	// Qty is the order size in instrument units and must be positive.
	Qty int64
}

// FillModel configures execution.
type FillModel struct {
	// LatencyMs is the delay between the event that placed an order and the
	// moment the order becomes visible to the market. Zero means the next
	// activation check of the same event: a market order fills immediately and
	// a limit order joins the queue at the current book.
	LatencyMs int
	// QueuePosition makes limit fills conservative: the order joins the back of
	// the queue at its price and fills only after trades at that price consume
	// the volume that was resting ahead of it (plan Task 14: the queue position
	// is the level volume at placement). The queue is measured when the order
	// becomes visible, i.e. at placement plus LatencyMs; with zero latency that
	// is the placement moment. With QueuePosition=false any trade at the order
	// price may fill the order (an optimistic upper bound).
	QueuePosition bool
	// FeeBps is the commission on the traded notional in basis points. Real
	// runs use the plan default of 5 bps; an explicit zero produces the
	// no_costs warning in Result.Warnings.
	FeeBps float64
}

// The orders of a replay are hypothetical and are simulated independently: a
// trade that prints at the price of several working orders is applied to each of
// them (each order has its own queue position). This is the usual order-level
// replay approximation and must be read as an upper bound of the fillable
// volume, not as a simulation of our own market impact.

// validate rejects impossible execution parameters.
func (m FillModel) validate() error {
	if m.LatencyMs < 0 {
		return fmt.Errorf("lobreplay: LatencyMs must not be negative: %d", m.LatencyMs)
	}
	if m.FeeBps < 0 || m.FeeBps >= 10_000 {
		return fmt.Errorf("lobreplay: FeeBps must be in [0, 10000): %v", m.FeeBps)
	}
	return nil
}

// Fill is one execution of an order. A limit order can fill in several parts.
type Fill struct {
	OrderID int64         `json:"orderId"`
	TS      time.Time     `json:"ts"`
	Side    string        `json:"side"`
	Type    string        `json:"type"`
	Price   model.Decimal `json:"price"`
	Qty     int64         `json:"qty"`
	Fee     model.Decimal `json:"fee"`
}

// RoundTrip is one closed position built from the fills. Side is "long" or
// "short"; PnL is the gross price result and Fee the commission of the closed
// quantity, so the net result is PnL - Fee (PnLPct is already net).
type RoundTrip struct {
	EntryTime  time.Time     `json:"entryTime"`
	ExitTime   time.Time     `json:"exitTime"`
	Side       string        `json:"side"`
	EntryPrice model.Decimal `json:"entryPrice"`
	ExitPrice  model.Decimal `json:"exitPrice"`
	Qty        int64         `json:"qty"`
	PnL        model.Decimal `json:"pnl"`
	Fee        model.Decimal `json:"fee"`
	PnLPct     float64       `json:"pnlPct"`
}

// fillEngine owns the order lifecycle: latency activation, limit fills from the
// trade feed, market order walks and the resulting portfolio.
type fillEngine struct {
	fm FillModel

	nextOrderID int64
	working     []*workingOrder
	fills       []Fill

	orders      int
	filled      int
	cancelled   int
	unfilledQty int64

	portfolio portfolio
}

// workingOrder is one order in flight.
type workingOrder struct {
	id        int64
	order     Order
	activeAt  time.Time
	active    bool
	remaining int64
	// queueAhead is the volume resting at the limit price when the order became
	// visible; trades at that price consume it before the order fills.
	queueAhead int64
}

// newFillEngine builds the execution engine; the caller already validated fm.
func newFillEngine(fm FillModel) *fillEngine {
	return &fillEngine{fm: fm, portfolio: newPortfolio()}
}

// submit validates the orders of one strategy call and queues them; the orders
// become visible at now + LatencyMs.
func (e *fillEngine) submit(orders []Order, now time.Time) error {
	activeAt := now.Add(time.Duration(e.fm.LatencyMs) * time.Millisecond)
	for _, order := range orders {
		normalized, err := normalizeOrder(order)
		if err != nil {
			return err
		}
		e.nextOrderID++
		e.working = append(e.working, &workingOrder{
			id:        e.nextOrderID,
			order:     normalized,
			activeAt:  activeAt,
			remaining: normalized.Qty,
		})
		e.orders++
	}
	return nil
}

// normalizeOrder validates one strategy order and fills its default type.
func normalizeOrder(order Order) (Order, error) {
	if order.Side != SideBuy && order.Side != SideSell {
		return Order{}, fmt.Errorf("order side %q (want %q or %q)", order.Side, SideBuy, SideSell)
	}
	switch order.Type {
	case "", OrderLimit:
		order.Type = OrderLimit
	case OrderMarket:
	default:
		return Order{}, fmt.Errorf("order type %q (want %q or %q)", order.Type, OrderLimit, OrderMarket)
	}
	if order.Qty <= 0 {
		return Order{}, fmt.Errorf("order qty must be positive: %d", order.Qty)
	}
	if order.Type == OrderLimit && priceUnits(order.Price) <= 0 {
		return Order{}, fmt.Errorf("limit order price must be positive: %s", order.Price)
	}
	return order, nil
}

// activate makes the due orders visible and executes market orders against the
// book of the current event. A limit order takes its queue position from the
// volume resting at its price at that moment: the queue it joins.
func (e *fillEngine) activate(book *Book, now time.Time) {
	if len(e.working) == 0 {
		return
	}
	kept := e.working[:0]
	for _, order := range e.working {
		if !order.active {
			if now.Before(order.activeAt) {
				kept = append(kept, order)
				continue
			}
			order.active = true
			if order.order.Type == OrderMarket {
				e.executeMarket(book, order, now)
				continue // market orders are immediate-or-cancel
			}
			order.queueAhead = 0
			if e.fm.QueuePosition {
				order.queueAhead = book.qtyAt(orderBookSide(order.order.Side), priceUnits(order.order.Price))
			}
		}
		kept = append(kept, order)
	}
	e.working = kept
}

// onTrade applies one md.trades row to the visible limit orders: an aggressive
// sell hits the resting bids, an aggressive buy lifts the asks. With
// QueuePosition the trade first consumes the volume ahead of the order; only the
// overflow fills it. A trade with an unknown aggressor only moves the mark.
func (e *fillEngine) onTrade(ev Event) {
	if ev.Side != SideBuy && ev.Side != SideSell {
		return
	}
	units := priceUnits(ev.Price)
	aggressorBuy := ev.Side == SideBuy
	kept := e.working[:0]
	for _, order := range e.working {
		if !order.active || order.remaining <= 0 || order.order.Type != OrderLimit {
			kept = append(kept, order)
			continue
		}
		fillSide := SideBuy
		if aggressorBuy {
			fillSide = SideSell
		}
		if order.order.Side != fillSide || priceUnits(order.order.Price) != units {
			kept = append(kept, order)
			continue
		}
		qty := ev.Qty
		if qty <= 0 {
			kept = append(kept, order)
			continue
		}
		if e.fm.QueuePosition && order.queueAhead > 0 {
			if qty <= order.queueAhead {
				order.queueAhead -= qty
				kept = append(kept, order)
				continue
			}
			qty -= order.queueAhead
			order.queueAhead = 0
		}
		if qty > order.remaining {
			qty = order.remaining
		}
		if qty > 0 {
			price := decimalPrice(units)
			e.recordFill(Fill{
				OrderID: order.id, TS: ev.TS, Side: order.order.Side, Type: OrderLimit,
				Price: price, Qty: qty, Fee: e.fee(price, qty),
			})
			order.remaining -= qty
		}
		if order.remaining == 0 {
			e.filled++
			continue // the order is done: drop it from the working set
		}
		kept = append(kept, order)
	}
	e.working = kept
}

// executeMarket walks the recorded book from the best level out and fills what
// the visible liquidity allows; the immediate-or-cancel remainder is counted in
// Result.UnfilledQty. The recorded book is not modified: replay orders are
// hypothetical and must not perturb the data they are measured against.
func (e *fillEngine) executeMarket(book *Book, order *workingOrder, now time.Time) {
	// A market buy takes the asks, a market sell hits the bids: the opposite
	// side of the book from the side the order would rest on as a limit order.
	bookSide := SideAsk
	if order.order.Side == SideSell {
		bookSide = SideBid
	}
	remaining := order.remaining
	for _, units := range book.sidePrices(bookSide) {
		if remaining == 0 {
			break
		}
		available := book.qtyAt(bookSide, units)
		if available <= 0 {
			continue
		}
		qty := min(available, remaining)
		price := decimalPrice(units)
		e.recordFill(Fill{
			OrderID: order.id, TS: now, Side: order.order.Side, Type: OrderMarket,
			Price: price, Qty: qty, Fee: e.fee(price, qty),
		})
		remaining -= qty
	}
	if remaining == 0 {
		e.filled++
		return
	}
	e.unfilledQty += remaining
}

// cancelAll drops the resting orders: a recording gap invalidates the queue
// state, so an order is never assumed to survive the break.
func (e *fillEngine) cancelAll() {
	for _, order := range e.working {
		if order.remaining > 0 {
			e.cancelled++
		}
	}
	e.working = nil
}

// recordFill books one execution and updates the portfolio.
func (e *fillEngine) recordFill(fill Fill) {
	e.fills = append(e.fills, fill)
	e.portfolio.apply(fill)
}

// fee returns the commission of a fill in scaled price units. FeeBps is a
// float64 by the frozen FillModel interface, but only the rate goes through a
// float, never a price: the notional is exact int64 scaled units.
func (e *fillEngine) fee(price model.Decimal, qty int64) model.Decimal {
	if e.fm.FeeBps == 0 || qty <= 0 {
		return model.Decimal{}
	}
	notional := priceUnits(price) * qty
	units := int64(math.Round(float64(notional) * e.fm.FeeBps / 10_000))
	return decimalPrice(units)
}

// orderBookSide is the book side a limit order rests on.
func orderBookSide(side string) string {
	if side == SideBuy {
		return SideBid
	}
	return SideAsk
}

// initialCash is the notional the equity curve starts from: the documented
// default of terminal/backtest (1 000 000 currency units), so a replay result and
// a bar backtest result are comparable.
const initialCash = 1_000_000.0

// portfolio tracks the signed position, cash and closed round trips of the
// replay. Prices and PnL are int64 scaled price units at Decimal64(6).
type portfolio struct {
	position   int64 // signed: > 0 long, < 0 short
	avgEntry   int64 // weighted average entry price of the open position
	entryTime  time.Time
	entryFee   int64 // fees paid on the open position, allocated on close
	cash       int64 // cash in scaled price units
	roundTrips []RoundTrip
}

// newPortfolio starts the replay with the default initial cash.
func newPortfolio() portfolio {
	return portfolio{cash: int64(initialCash) * priceFactor}
}

// apply books one fill: cash, the open position and, when the fill closes (part
// of) the position, one closed round trip. The average-cost model is the one
// terminal/backtest uses.
func (p *portfolio) apply(fill Fill) {
	units := priceUnits(fill.Price)
	fee := priceUnits(fill.Fee)
	dir := int64(1)
	if fill.Side == SideSell {
		dir = -1
	}
	p.cash -= dir * units * fill.Qty
	p.cash -= fee

	remaining := fill.Qty
	feeLeft := fee
	for remaining > 0 {
		openQty := absInt64(p.position)
		switch {
		case openQty == 0:
			p.open(dir, units, remaining, feeLeft, fill.TS)
			return
		case (p.position > 0) == (dir > 0):
			newQty := openQty + remaining
			p.avgEntry = (p.avgEntry*openQty + units*remaining) / newQty
			p.entryFee += feeLeft
			p.position += dir * remaining
			return
		default:
			closeQty := min(openQty, remaining)
			partFee := feeLeft * closeQty / remaining
			feeLeft -= partFee
			p.close(closeQty, units, partFee, fill.TS)
			remaining -= closeQty
		}
	}
}

// open starts a new position with one fill part.
func (p *portfolio) open(dir, units, qty, fee int64, at time.Time) {
	p.position = dir * qty
	p.avgEntry = units
	p.entryTime = at
	p.entryFee = fee
}

// close closes qty units of the open position and records the round trip. fee is
// the exit commission of this part; the matching share of the entry fees is
// allocated to the round trip as well.
func (p *portfolio) close(qty, units, fee int64, at time.Time) {
	openQty := absInt64(p.position)
	long := p.position > 0
	entryFeeShare := int64(0)
	if openQty > 0 {
		entryFeeShare = p.entryFee * qty / openQty
	}
	p.entryFee -= entryFeeShare
	gross := (units - p.avgEntry) * qty
	if !long {
		gross = -gross
	}
	pnlPct := 0.0
	entryNotional := p.avgEntry * qty
	if entryNotional != 0 {
		pnlPct = float64(gross-entryFeeShare-fee) / float64(entryNotional) * 100
	}
	side := "long"
	if !long {
		side = "short"
	}
	p.roundTrips = append(p.roundTrips, RoundTrip{
		EntryTime:  p.entryTime,
		ExitTime:   at,
		Side:       side,
		EntryPrice: decimalPrice(p.avgEntry),
		ExitPrice:  decimalPrice(units),
		Qty:        qty,
		PnL:        decimalPrice(gross),
		Fee:        decimalPrice(entryFeeShare + fee),
		PnLPct:     pnlPct,
	})
	if long {
		p.position -= qty
	} else {
		p.position += qty
	}
	if p.position == 0 {
		p.avgEntry = 0
		p.entryFee = 0
		p.entryTime = time.Time{}
	}
}

// equityUnits returns cash plus the open position marked at markUnits.
func (p *portfolio) equityUnits(markUnits int64) int64 {
	return p.cash + p.position*markUnits
}

// absInt64 returns the absolute value of v (v is never math.MinInt64 here).
func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
