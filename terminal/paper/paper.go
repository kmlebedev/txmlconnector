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

// Paper accounts. The main account is funded by Config.InitialCash and serves
// the terminal UI and the agent order tool; the autonomy account receives a
// budget from the main account via Allocate and is used by the autonomy replay.
const (
	// MainAccount is the default account of the terminal.
	MainAccount = "main"
	// AutonomyAccount is the sub-account the agent autonomy replay trades.
	AutonomyAccount = "autonomy"
)

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
	Account      string
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
	Account string
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

// AccountView is the consistent copy of one paper sub-account state.
type AccountView struct {
	Cash        float64
	RealizedPnL float64
	Fees        float64
	Positions   []Position
	Orders      []Order
	Trades      []Trade
	UpdatedAt   time.Time
}

// Snapshot is a consistent copy of the paper broker state. The top-level fields
// describe the main account (the terminal portfolio); Accounts is the
// per-account view, including the autonomy sub-account when it exists.
type Snapshot struct {
	Cash        float64
	RealizedPnL float64
	Fees        float64
	Positions   []Position
	Orders      []Order
	Trades      []Trade
	UpdatedAt   time.Time
	Accounts    map[string]AccountView
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

// accountState is the mutable state of one paper account. The caller holds
// Broker.mu.
type accountState struct {
	cash      float64
	realized  float64
	fees      float64
	positions map[string]*Position
	posOrder  []string
}

// Broker is the paper-trading account. All methods are safe for concurrent use.
type Broker struct {
	mu           sync.Mutex
	cfg          Config
	now          func() time.Time
	accounts     map[string]*accountState
	accountOrder []string

	orders     map[string]*Order
	orderOrder []string
	trades     []Trade
	// clientOrderIDs deduplicates agent orders by their idempotency key.
	clientOrderIDs map[string]struct{}

	orderSeq int64
	tradeSeq int64
}

// New builds a paper broker with the given configuration; the main account is
// funded with InitialCash.
func New(cfg Config) *Broker {
	if cfg.FeeBps <= 0 {
		cfg.FeeBps = 5
	}
	b := &Broker{
		cfg:            cfg,
		now:            time.Now,
		accounts:       map[string]*accountState{},
		orders:         map[string]*Order{},
		clientOrderIDs: map[string]struct{}{},
	}
	b.accountLocked(MainAccount).cash = cfg.InitialCash
	return b
}

// accountLocked returns the named account, creating an empty one on demand.
// The caller holds b.mu.
func (b *Broker) accountLocked(name string) *accountState {
	if name == "" {
		name = MainAccount
	}
	if acc := b.accounts[name]; acc != nil {
		return acc
	}
	acc := &accountState{positions: map[string]*Position{}}
	b.accounts[name] = acc
	b.accountOrder = append(b.accountOrder, name)
	return acc
}

// Allocate transfers amount from the main account to the named sub-account. The
// agent autonomy uses it to get its budget out of the main cash.
func (b *Broker) Allocate(account string, amount float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if account == "" || account == MainAccount {
		return errors.New("нельзя выделить бюджет основному счёту")
	}
	if amount <= 0 {
		return fmt.Errorf("сумма выделения должна быть больше нуля, получено %.2f ₽", amount)
	}
	main := b.accountLocked(MainAccount)
	if main.cash < amount {
		return fmt.Errorf("недостаточно свободного кэша: нужно %.2f ₽, доступно %.2f ₽", amount, main.cash)
	}
	main.cash -= amount
	b.accountLocked(account).cash += amount
	return nil
}

// Place validates and executes a new order on the main account. A marketable
// order fills at once; a non-marketable limit order rests until OnQuote touches
// it. A rejected order is stored in the order history with StatusRejected and
// returned together with a *RejectError.
func (b *Broker) Place(req OrderRequest, q QuoteView) (Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.placeLocked(MainAccount, req, q, b.now())
}

// PlaceAt validates and executes an order at an explicit replay quote and
// instant on the named account ("" means the main account). The autonomy
// replay uses it to fill at the open of the next historical bar instead of the
// live quote path.
func (b *Broker) PlaceAt(req OrderRequest, fill QuoteView, at time.Time, account string) (Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.placeLocked(account, req, fill, at)
}

// placeLocked is the common placement path. The caller holds b.mu.
func (b *Broker) placeLocked(account string, req OrderRequest, q QuoteView, at time.Time) (Order, error) {
	if account == "" {
		account = MainAccount
	}
	if req.ClientOrderID != "" {
		if _, seen := b.clientOrderIDs[req.ClientOrderID]; seen {
			return Order{}, fmt.Errorf("%w: %s", ErrDuplicateClientOrderID, req.ClientOrderID)
		}
		b.clientOrderIDs[req.ClientOrderID] = struct{}{}
	}
	acc := b.accountLocked(account)
	o := &Order{
		ID:        fmt.Sprintf("ord_%06d", b.nextOrderIDLocked()),
		Symbol:    req.Symbol,
		Side:      req.Side,
		Type:      req.Type,
		Qty:       req.Qty,
		Price:     req.Price,
		Status:    StatusNew,
		CreatedAt: at,
		UpdatedAt: at,
		Source:    req.Source,
		Account:   account,
	}
	b.orders[o.ID] = o
	b.orderOrder = append(b.orderOrder, o.ID)
	if reason := validateRequest(req); reason != "" {
		return b.rejectLocked(o, reason, at), &RejectError{Reason: reason}
	}
	if req.Type == Limit {
		if fillPrice, ok := marketablePrice(req, q); ok {
			if reason := b.executeLocked(acc, o, fillPrice, at); reason != "" {
				return b.rejectLocked(o, reason, at), &RejectError{Reason: reason}
			}
			return *o, nil
		}
		// Rests until a quote touches the limit price.
		o.Message = "заявка выставлена"
		return *o, nil
	}
	if reason := b.executeLocked(acc, o, marketPrice(req.Side, q), at); reason != "" {
		return b.rejectLocked(o, reason, at), &RejectError{Reason: reason}
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
	at := b.now()
	var events []Event
	for _, id := range b.orderOrder {
		o := b.orders[id]
		if o == nil || o.Status != StatusNew || o.Type != Limit || o.Symbol != q.Symbol {
			continue
		}
		acc := b.accounts[o.Account]
		if acc == nil {
			continue
		}
		fillPrice, ok := marketablePrice(OrderRequest{Type: Limit, Price: o.Price, Side: o.Side}, q)
		if !ok {
			continue
		}
		if reason := b.executeLocked(acc, o, fillPrice, at); reason != "" {
			b.rejectLocked(o, reason, at)
			events = append(events, Event{Type: "orders", Data: *o})
			continue
		}
		events = append(events, Event{Type: "trade", Data: b.trades[len(b.trades)-1]})
	}
	return events
}

// Snapshot returns a deep copy of the broker state: the top-level fields are the
// main account, Accounts holds every account (main included).
func (b *Broker) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	main := b.accountLocked(MainAccount)
	snap := Snapshot{
		Cash:        main.cash,
		RealizedPnL: main.realized,
		Fees:        main.fees,
		Positions:   b.positionsLocked(main),
		Orders:      make([]Order, 0, len(b.orderOrder)),
		Trades:      make([]Trade, len(b.trades)),
		UpdatedAt:   now,
		Accounts:    make(map[string]AccountView, len(b.accountOrder)),
	}
	for _, id := range b.orderOrder {
		if o := b.orders[id]; o != nil {
			snap.Orders = append(snap.Orders, *o)
		}
	}
	copy(snap.Trades, b.trades)
	for _, name := range b.accountOrder {
		acc := b.accounts[name]
		if acc == nil {
			continue
		}
		view := AccountView{
			Cash:        acc.cash,
			RealizedPnL: acc.realized,
			Fees:        acc.fees,
			Positions:   b.positionsLocked(acc),
			UpdatedAt:   now,
		}
		for _, o := range snap.Orders {
			if o.Account == name {
				view.Orders = append(view.Orders, o)
			}
		}
		for _, trade := range snap.Trades {
			if trade.Account == name {
				view.Trades = append(view.Trades, trade)
			}
		}
		snap.Accounts[name] = view
	}
	return snap
}

// positionsLocked copies the open positions of one account. The caller holds
// b.mu.
func (b *Broker) positionsLocked(acc *accountState) []Position {
	if len(acc.posOrder) == 0 {
		return nil
	}
	out := make([]Position, 0, len(acc.posOrder))
	for _, name := range acc.posOrder {
		if pos := acc.positions[name]; pos != nil {
			out = append(out, *pos)
		}
	}
	return out
}

// executeLocked applies a fill of o at price/at to the given account and returns
// a rejection reason, or an empty string on success. The caller holds b.mu.
func (b *Broker) executeLocked(acc *accountState, o *Order, price float64, at time.Time) string {
	if price <= 0 {
		return "нет рыночной цены"
	}
	turnover := price * float64(o.Qty)
	fee := turnover * b.cfg.FeeBps / 10000
	switch o.Side {
	case Buy:
		if acc.cash < turnover+fee {
			return "недостаточно средств"
		}
		acc.cash -= turnover + fee
		pos := acc.positions[o.Symbol]
		if pos == nil {
			pos = &Position{Symbol: o.Symbol}
			acc.positions[o.Symbol] = pos
			acc.posOrder = append(acc.posOrder, o.Symbol)
		}
		total := pos.AvgPrice*float64(pos.Qty) + turnover
		pos.Qty += o.Qty
		pos.AvgPrice = total / float64(pos.Qty)
	case Sell:
		pos := acc.positions[o.Symbol]
		if pos == nil || pos.Qty < o.Qty {
			return "продажа больше позиции (шорт не поддерживается)"
		}
		acc.cash += turnover - fee
		pnl := (price - pos.AvgPrice) * float64(o.Qty)
		pos.Qty -= o.Qty
		pos.RealizedPnL += pnl
		acc.realized += pnl
		if pos.Qty == 0 {
			delete(acc.positions, o.Symbol)
		}
	default:
		return "неизвестная сторона заявки"
	}
	acc.fees += fee
	o.Status = StatusFilled
	o.Filled = o.Qty
	o.AvgFillPrice = price
	o.UpdatedAt = at
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
		Time:    at,
		Source:  o.Source,
		Account: o.Account,
	})
	return ""
}

// rejectLocked marks o rejected with the user-facing reason.
func (b *Broker) rejectLocked(o *Order, reason string, at time.Time) Order {
	o.Status = StatusRejected
	o.Message = reason
	o.UpdatedAt = at
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
