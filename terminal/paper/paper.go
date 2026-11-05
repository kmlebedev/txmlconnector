// Package paper is the in-process paper-trading broker of the AI terminal: it
// fills market and limit orders against the current quote, tracks cash, fees,
// positions and realized PnL, and is safe for concurrent use. It never speaks
// to an exchange.
package paper

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Config configures the paper account.
type Config struct {
	InitialCash float64
	FeeBps      float64 // commission in basis points; 0 means the 5 bps default
}

// Side is the order direction.
type Side string

// Order sides.
const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// OrderType is the execution type of an order.
type OrderType string

// Order types.
const (
	Market OrderType = "market"
	Limit  OrderType = "limit"
)

// Status is the lifecycle state of an order.
type Status string

// Order statuses.
const (
	StatusNew       Status = "new"
	StatusFilled    Status = "filled"
	StatusPartial   Status = "partial"
	StatusCancelled Status = "cancelled"
	StatusRejected  Status = "rejected"
)

// OrderRequest is one order placement request.
type OrderRequest struct {
	Symbol string
	Side   Side
	Qty    int64
	Type   OrderType
	Price  float64
	Source string // "ui" | "agent" | "autonomy"
	// ClientOrderID is the optional idempotency key of the request. A request
	// that reuses an id already processed by this account is rejected with
	// ErrDuplicateClientOrderID instead of being executed twice.
	ClientOrderID string
}

// Order is the state of one paper order.
type Order struct {
	ID           string
	Symbol       string
	Side         Side
	Type         OrderType
	Qty          int64
	Filled       int64
	Price        float64
	AvgFillPrice float64
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Source       string
	Message      string
}

// Trade is one executed paper trade.
type Trade struct {
	ID      string
	OrderID string
	Symbol  string
	Side    Side
	Qty     int64
	Price   float64
	Fee     float64
	Time    time.Time
	Source  string
}

// Position is one long position of the paper account.
type Position struct {
	Symbol      string
	Qty         int64
	AvgPrice    float64
	RealizedPnL float64
}

// QuoteView is the market snapshot used to fill orders.
type QuoteView struct {
	Symbol string
	Last   float64
	Bid    float64
	Ask    float64
	Time   time.Time
}

// Snapshot is a consistent copy of the paper account state.
type Snapshot struct {
	Cash        float64
	RealizedPnL float64
	Fees        float64
	Positions   []Position
	Orders      []Order
	Trades      []Trade
	UpdatedAt   time.Time
}

// Event is one change produced by OnQuote (fill or rejection of a resting
// order). Type is "orders" for an order state change and "trade" for a fill.
type Event struct {
	Type string
	Data any
}

// RejectError explains why an order was rejected before or at placement. The
// message is user-facing Russian text.
type RejectError struct {
	Reason string
}

func (e *RejectError) Error() string { return e.Reason }

// Errors of the order lifecycle, matched with errors.Is.
var (
	// ErrOrderNotFound is returned by Cancel for an unknown order id.
	ErrOrderNotFound = errors.New("заявка не найдена")
	// ErrOrderNotCancellable is returned by Cancel for a terminal order.
	ErrOrderNotCancellable = errors.New("заявку нельзя отменить")
	// ErrDuplicateClientOrderID is returned by Place when a request reuses a
	// client order id that this account already processed.
	ErrDuplicateClientOrderID = errors.New("заявка с таким clientOrderId уже обработана")
)

// Broker is the paper-trading account. All methods are safe for concurrent use.
type Broker struct {
	mu   sync.Mutex
	cfg  Config
	now  func() time.Time
	cash float64
	fees float64
	// realized is the total realized PnL of the account.
	realized float64

	positions map[string]*Position
	posOrder  []string

	orders     map[string]*Order
	orderOrder []string
	trades     []Trade
	// clientOrderIDs deduplicates agent orders by their idempotency key.
	clientOrderIDs map[string]struct{}

	orderSeq int64
	tradeSeq int64
}

// New builds a paper broker with the given configuration.
func New(cfg Config) *Broker {
	if cfg.FeeBps <= 0 {
		cfg.FeeBps = 5
	}
	return &Broker{
		cfg:            cfg,
		now:            time.Now,
		cash:           cfg.InitialCash,
		positions:      map[string]*Position{},
		orders:         map[string]*Order{},
		clientOrderIDs: map[string]struct{}{},
	}
}

// Place validates and executes a new order. A marketable order fills at once; a
// non-marketable limit order rests until OnQuote touches it. A rejected order is
// stored in the order history with StatusRejected and returned together with a
// *RejectError.
func (b *Broker) Place(req OrderRequest, q QuoteView) (Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if req.ClientOrderID != "" {
		if _, seen := b.clientOrderIDs[req.ClientOrderID]; seen {
			return Order{}, fmt.Errorf("%w: %s", ErrDuplicateClientOrderID, req.ClientOrderID)
		}
		b.clientOrderIDs[req.ClientOrderID] = struct{}{}
	}
	now := b.now()
	o := &Order{
		ID:        fmt.Sprintf("ord_%06d", b.nextOrderIDLocked()),
		Symbol:    req.Symbol,
		Side:      req.Side,
		Type:      req.Type,
		Qty:       req.Qty,
		Price:     req.Price,
		Status:    StatusNew,
		CreatedAt: now,
		UpdatedAt: now,
		Source:    req.Source,
	}
	b.orders[o.ID] = o
	b.orderOrder = append(b.orderOrder, o.ID)
	if reason := validateRequest(req); reason != "" {
		return b.rejectLocked(o, reason), &RejectError{Reason: reason}
	}
	if req.Type == Limit {
		if fillPrice, ok := marketablePrice(req, q); ok {
			if reason := b.executeLocked(o, fillPrice); reason != "" {
				return b.rejectLocked(o, reason), &RejectError{Reason: reason}
			}
			return *o, nil
		}
		// Rests until a quote touches the limit price.
		o.Message = "заявка выставлена"
		return *o, nil
	}
	if reason := b.executeLocked(o, marketPrice(req.Side, q)); reason != "" {
		return b.rejectLocked(o, reason), &RejectError{Reason: reason}
	}
	return *o, nil
}

// Cancel cancels a resting order.
func (b *Broker) Cancel(id string) (Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.orders[id]
	if o == nil {
		return Order{}, fmt.Errorf("%w: %s", ErrOrderNotFound, id)
	}
	if o.Status != StatusNew {
		return *o, fmt.Errorf("%w: %s (%s)", ErrOrderNotCancellable, id, o.Status)
	}
	o.Status = StatusCancelled
	o.UpdatedAt = b.now()
	o.Message = "заявка отменена"
	return *o, nil
}

// OnQuote fills resting limit orders touched by q and returns the resulting
// events (an order being rejected on fill counts as an "orders" event too).
func (b *Broker) OnQuote(q QuoteView) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var events []Event
	for _, id := range b.orderOrder {
		o := b.orders[id]
		if o == nil || o.Status != StatusNew || o.Type != Limit || o.Symbol != q.Symbol {
			continue
		}
		fillPrice, ok := marketablePrice(OrderRequest{Type: Limit, Price: o.Price, Side: o.Side}, q)
		if !ok {
			continue
		}
		if reason := b.executeLocked(o, fillPrice); reason != "" {
			b.rejectLocked(o, reason)
			events = append(events, Event{Type: "orders", Data: *o})
			continue
		}
		events = append(events, Event{Type: "trade", Data: b.trades[len(b.trades)-1]})
	}
	return events
}

// Snapshot returns a deep copy of the account state.
func (b *Broker) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := Snapshot{
		Cash:        b.cash,
		RealizedPnL: b.realized,
		Fees:        b.fees,
		Orders:      make([]Order, 0, len(b.orderOrder)),
		Trades:      make([]Trade, len(b.trades)),
		UpdatedAt:   b.now(),
	}
	for _, name := range b.posOrder {
		if pos := b.positions[name]; pos != nil {
			snap.Positions = append(snap.Positions, *pos)
		}
	}
	for _, id := range b.orderOrder {
		if o := b.orders[id]; o != nil {
			snap.Orders = append(snap.Orders, *o)
		}
	}
	copy(snap.Trades, b.trades)
	return snap
}

// executeLocked applies a fill of o at price and returns a rejection reason, or
// an empty string on success. The caller holds b.mu.
func (b *Broker) executeLocked(o *Order, price float64) string {
	if price <= 0 {
		return "нет рыночной цены"
	}
	turnover := price * float64(o.Qty)
	fee := turnover * b.cfg.FeeBps / 10000
	switch o.Side {
	case Buy:
		if b.cash < turnover+fee {
			return "недостаточно средств"
		}
		b.cash -= turnover + fee
		pos := b.positions[o.Symbol]
		if pos == nil {
			pos = &Position{Symbol: o.Symbol}
			b.positions[o.Symbol] = pos
			b.posOrder = append(b.posOrder, o.Symbol)
		}
		total := pos.AvgPrice*float64(pos.Qty) + turnover
		pos.Qty += o.Qty
		pos.AvgPrice = total / float64(pos.Qty)
	case Sell:
		pos := b.positions[o.Symbol]
		if pos == nil || pos.Qty < o.Qty {
			return "продажа больше позиции (шорт не поддерживается)"
		}
		b.cash += turnover - fee
		pnl := (price - pos.AvgPrice) * float64(o.Qty)
		pos.Qty -= o.Qty
		pos.RealizedPnL += pnl
		b.realized += pnl
		if pos.Qty == 0 {
			delete(b.positions, o.Symbol)
		}
	default:
		return "неизвестная сторона заявки"
	}
	b.fees += fee
	now := b.now()
	o.Status = StatusFilled
	o.Filled = o.Qty
	o.AvgFillPrice = price
	o.UpdatedAt = now
	o.Message = ""
	b.tradeSeq++
	b.trades = append(b.trades, Trade{
		ID:      fmt.Sprintf("trd_%06d", b.tradeSeq),
		OrderID: o.ID,
		Symbol:  o.Symbol,
		Side:    o.Side,
		Qty:     o.Qty,
		Price:   price,
		Fee:     fee,
		Time:    now,
		Source:  o.Source,
	})
	return ""
}

// rejectLocked marks o rejected with the user-facing reason.
func (b *Broker) rejectLocked(o *Order, reason string) Order {
	o.Status = StatusRejected
	o.Message = reason
	o.UpdatedAt = b.now()
	return *o
}

func (b *Broker) nextOrderIDLocked() int64 {
	b.orderSeq++
	return b.orderSeq
}

// validateRequest returns a user-facing rejection reason or "" when the request
// is executable.
func validateRequest(req OrderRequest) string {
	switch {
	case req.Symbol == "":
		return "не указан инструмент"
	case req.Qty <= 0:
		return "количество должно быть больше нуля"
	case req.Side != Buy && req.Side != Sell:
		return "неизвестная сторона заявки"
	case req.Type != Market && req.Type != Limit:
		return "неизвестный тип заявки"
	case req.Type == Limit && req.Price <= 0:
		return "для лимитной заявки нужна цена"
	}
	return ""
}

// marketPrice is the fill price of a market order: the ask for a buy, the bid
// for a sell, and the last price when the needed side is empty.
func marketPrice(side Side, q QuoteView) float64 {
	var price float64
	if side == Buy {
		price = q.Ask
	} else {
		price = q.Bid
	}
	if price <= 0 {
		price = q.Last
	}
	return price
}

// marketablePrice reports whether a limit order crosses the current quote and
// the price it fills at: the touch price (ask for a buy, bid for a sell), which
// is always on the right side of the limit because of the cross check. Filling
// at the limit price instead would add imaginary slippage, contradicting the
// zero-slippage rule of ARCHITECTURE §7.
func marketablePrice(req OrderRequest, q QuoteView) (float64, bool) {
	if req.Price <= 0 {
		return 0, false
	}
	switch req.Side {
	case Buy:
		if q.Ask > 0 && q.Ask <= req.Price {
			return q.Ask, true
		}
	case Sell:
		if q.Bid > 0 && q.Bid >= req.Price {
			return q.Bid, true
		}
	}
	return 0, false
}
