package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// testNow is the frozen engine clock used by the tests.
var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// fakeDataSource is a deterministic DataSource for engine/tool tests.
type fakeDataSource struct {
	instruments []InstrumentRef
	quotes      map[string]Quote
	books       map[string]OrderBook
	histories   map[string]map[int][]backtest.Bar
	histErr     error
}

func newFakeDataSource() *fakeDataSource {
	bars := syntheticBars(300, 100, 0.1)
	byInstrument := func() map[int][]backtest.Bar {
		return map[int][]backtest.Bar{
			60:    syntheticBars(300, 100, 0.1),
			300:   bars,
			900:   syntheticBars(300, 100, 0.1),
			3600:  syntheticBars(300, 100, 0.1),
			86400: syntheticBars(300, 100, 0.1),
		}
	}
	return &fakeDataSource{
		instruments: []InstrumentRef{
			{Symbol: "TQBR:SBER", Name: "Сбербанк", Last: 293.22, ChangePct: 0.36},
			{Symbol: "TQBR:GAZP", Name: "Газпром", Last: 132.5, ChangePct: -1.2},
			{Symbol: "TQBR:YNDX", Name: "Яндекс", Last: 4100, ChangePct: 2.1},
		},
		quotes: map[string]Quote{
			"TQBR:SBER": {Symbol: "TQBR:SBER", Last: 293.22, Bid: 293.2, Ask: 293.26, ChangePct: 0.36, Time: testNow},
			"TQBR:GAZP": {Symbol: "TQBR:GAZP", Last: 132.5, Bid: 132.4, Ask: 132.6, ChangePct: -1.2, Time: testNow},
		},
		books: map[string]OrderBook{
			"TQBR:SBER": {
				Symbol: "TQBR:SBER",
				Last:   293.22,
				Bids:   []Level{{Price: 293.2, Qty: 100}, {Price: 293.1, Qty: 200}},
				Asks:   []Level{{Price: 293.26, Qty: 100}, {Price: 293.3, Qty: 150}},
			},
		},
		histories: map[string]map[int][]backtest.Bar{
			"TQBR:SBER": byInstrument(),
			"TQBR:GAZP": byInstrument(),
		},
	}
}

func (f *fakeDataSource) Instruments() []InstrumentRef {
	return append([]InstrumentRef(nil), f.instruments...)
}

func (f *fakeDataSource) Quote(symbol string) (Quote, bool) {
	q, ok := f.quotes[symbol]
	return q, ok
}

func (f *fakeDataSource) OrderBook(symbol string) (OrderBook, error) {
	if book, ok := f.books[symbol]; ok {
		return book, nil
	}
	return OrderBook{Symbol: symbol, Last: f.lastFor(symbol)}, nil
}

func (f *fakeDataSource) lastFor(symbol string) float64 {
	if q, ok := f.quotes[symbol]; ok {
		return q.Last
	}
	for _, ins := range f.instruments {
		if ins.Symbol == symbol {
			return ins.Last
		}
	}
	return 0
}

func (f *fakeDataSource) History(_ context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	periods, ok := f.histories[symbol]
	if !ok {
		return nil, fmt.Errorf("fake data: no history for %q", symbol)
	}
	bars, ok := periods[periodSec]
	if !ok {
		for _, candidate := range periods {
			bars = candidate
			break
		}
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("fake data: empty history for %q", symbol)
	}
	if count > 0 && len(bars) > count {
		bars = bars[len(bars)-count:]
	}
	return append([]backtest.Bar(nil), bars...), nil
}

// syntheticBars builds n rising OHLCV bars with 1-minute spacing.
func syntheticBars(n int, start, step float64) []backtest.Bar {
	bars := make([]backtest.Bar, n)
	for i := 0; i < n; i++ {
		price := start + step*float64(i)
		bars[i] = backtest.Bar{
			Time:   testNow.Add(time.Duration(i) * time.Minute),
			Open:   price,
			High:   price + step,
			Low:    price - step,
			Close:  price + step/2,
			Volume: int64(1000 + i),
		}
	}
	return bars
}

// oscillatingBars builds a sinusoid OHLCV series (5m spacing) so that real
// strategies actually produce round trips (monotonic data yields no closed
// trades in the frozen backtest semantics).
func oscillatingBars(n int) []backtest.Bar {
	bars := make([]backtest.Bar, n)
	for i := range bars {
		open := 100 + 5*math.Sin(float64(i)*2*math.Pi/40)
		closes := 100 + 5*math.Sin(float64(i+1)*2*math.Pi/40)
		high := math.Max(open, closes) + 0.5
		low := math.Min(open, closes) - 0.5
		bars[i] = backtest.Bar{
			Time:   testNow.Add(time.Duration(i) * 5 * time.Minute),
			Open:   open,
			High:   high,
			Low:    low,
			Close:  closes,
			Volume: int64(1000 + i),
		}
	}
	return bars
}

// crashBars rises for the first few bars and then crashes.
func crashBars(n int) []backtest.Bar {
	bars := syntheticBars(n, 100, 0.5)
	for i := 5; i < len(bars); i++ {
		price := 102.5 - 3*float64(i-4)
		bars[i] = backtest.Bar{
			Time:   testNow.Add(time.Duration(i) * time.Minute),
			Open:   price,
			High:   price + 0.5,
			Low:    price - 0.5,
			Close:  price - 0.2,
			Volume: 1000,
		}
	}
	return bars
}

// fakeBroker is a thread-safe paper broker fake.
type fakeBroker struct {
	mu         sync.Mutex
	cash       float64
	totalValue float64
	positions  []PositionView
	orders     []OrderView
	placements []PlaceOrder
	seq        int
	cancelErr  error
	placeErr   error
}

func newFakeBroker(cash float64) *fakeBroker {
	return &fakeBroker{cash: cash, totalValue: cash}
}

func (b *fakeBroker) Place(req PlaceOrder) (OrderView, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.placeErr != nil {
		return OrderView{}, b.placeErr
	}
	b.placements = append(b.placements, req)
	b.seq++
	price := req.Price
	if price <= 0 {
		price = 100
	}
	order := OrderView{
		ID:           fmt.Sprintf("ord_%06d", b.seq),
		Symbol:       req.Symbol,
		Side:         req.Side,
		Type:         req.Type,
		Status:       "filled",
		Qty:          req.Qty,
		Filled:       req.Qty,
		Price:        req.Price,
		AvgFillPrice: price,
		CreatedAt:    testNow,
	}
	b.orders = append(b.orders, order)
	if req.Side == "buy" {
		b.cash -= price * float64(req.Qty)
	} else {
		b.cash += price * float64(req.Qty)
	}
	return order, nil
}

func (b *fakeBroker) Cancel(id string) (OrderView, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancelErr != nil {
		return OrderView{}, b.cancelErr
	}
	for i, order := range b.orders {
		if order.ID == id {
			b.orders[i].Status = "cancelled"
			return b.orders[i], nil
		}
	}
	return OrderView{}, fmt.Errorf("order %q not found", id)
}

func (b *fakeBroker) Orders() []OrderView {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]OrderView(nil), b.orders...)
}

func (b *fakeBroker) Portfolio() PortfolioView {
	b.mu.Lock()
	defer b.mu.Unlock()
	return PortfolioView{
		TotalValue: b.totalValue,
		Cash:       b.cash,
		Positions:  append([]PositionView(nil), b.positions...),
	}
}

func (b *fakeBroker) Cash() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cash
}

func (b *fakeBroker) placedOrders() []PlaceOrder {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]PlaceOrder(nil), b.placements...)
}

// fakeStrategies is the deterministic strategy catalog used by the fake
// backtest seam.
var fakeStrategies = []backtest.StrategyInfo{
	{ID: "sma_cross", Name: "SMA Cross", Description: "пересечение средних",
		Params: []backtest.Param{{Key: "fast", Default: 10}, {Key: "slow", Default: 30}}},
	{ID: "rsi_reversion", Name: "RSI Reversion", Description: "возврат от перепроданности",
		Params: []backtest.Param{{Key: "period", Default: 14}}},
	{ID: "donchian_breakout", Name: "Donchian Breakout", Description: "пробой канала"},
	{ID: "momentum", Name: "Momentum", Description: "импульс доходности"},
}

// fakeBacktestRun returns deterministic, strategy-specific metrics.
func fakeBacktestRun(req backtest.Request) (backtest.Result, error) {
	returns := map[string]float64{
		"sma_cross":         7.5,
		"rsi_reversion":     3.2,
		"donchian_breakout": 1.1,
		"momentum":          -0.8,
	}
	totalReturn, ok := returns[req.Strategy]
	if !ok {
		return backtest.Result{}, fmt.Errorf("fake backtest: unknown strategy %q", req.Strategy)
	}
	return makeFakeResult(req, 3, totalReturn, 1.2, -2.4), nil
}

// makeFakeResult builds a deterministic backtest.Result for the seams. A
// trades=0 result exercises the "strategy produced no signals" paths.
func makeFakeResult(req backtest.Request, trades int, totalReturn, sharpe, maxDrawdown float64) backtest.Result {
	initial := req.InitialCash
	if initial <= 0 {
		initial = defaultBacktestCash
	}
	params := map[string]float64{"fast": 10, "slow": 30}
	for key, value := range req.Params {
		params[key] = value
	}
	var from, to time.Time
	if len(req.Bars) > 0 {
		from = req.Bars[0].Time
		to = req.Bars[len(req.Bars)-1].Time
	}
	res := backtest.Result{
		Symbol:    req.Symbol,
		Strategy:  req.Strategy,
		PeriodSec: req.PeriodSec,
		Params:    params,
		Metrics: backtest.Metrics{
			InitialCash:        initial,
			FinalEquity:        initial * (1 + totalReturn/100),
			TotalReturnPct:     totalReturn,
			BenchmarkReturnPct: 1.5,
			MaxDrawdownPct:     maxDrawdown,
			Sharpe:             sharpe,
			WinRatePct:         60,
			ProfitFactor:       1.5,
			AvgTradePct:        1.1,
			ExposurePct:        45,
			Trades:             trades,
			Bars:               len(req.Bars),
			From:               from,
			To:                 to,
		},
		Equity: []backtest.EquityPoint{
			{Time: from, Equity: initial, Benchmark: initial},
			{Time: to, Equity: initial * (1 + totalReturn/100), Benchmark: initial * 1.015},
		},
	}
	if trades > 0 {
		res.Trades = []backtest.Trade{{
			EntryTime: from, ExitTime: to, Side: "long",
			EntryPrice: 100, ExitPrice: 101, Qty: 10,
			PnL: 10, PnLPct: 1, BarsHeld: 2, Reason: "signal",
		}}
	}
	return res
}

// scriptedStrategy returns predetermined actions per bar index.
type scriptedStrategy struct {
	id      string
	params  map[string]float64
	actions map[int]backtest.Action
}

func (s *scriptedStrategy) ID() string                 { return s.id }
func (s *scriptedStrategy) Params() map[string]float64 { return s.params }
func (s *scriptedStrategy) OnBar(_ []backtest.Bar, i int) backtest.Action {
	return s.actions[i]
}

// strategyScript builds a NewStrategy seam returning one scripted strategy.
func strategyScript(actions map[int]backtest.Action) func(string, map[string]float64) (backtest.Strategy, error) {
	return func(id string, params map[string]float64) (backtest.Strategy, error) {
		return &scriptedStrategy{id: id, params: params, actions: actions}, nil
	}
}

// newTestEngine builds an Engine wired to the deterministic fakes.
func newTestEngine(t *testing.T) (*Engine, *fakeDataSource, *fakeBroker) {
	t.Helper()
	data := newFakeDataSource()
	broker := newFakeBroker(1_000_000)
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, data, broker)
	return e, data, broker
}

// newTestEngineWith builds an Engine with the given config and fakes.
func newTestEngineWith(t *testing.T, cfg Config, data DataSource, broker Broker) *Engine {
	t.Helper()
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return testNow }
	}
	e, err := New(cfg, data, broker)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.backtestRun = fakeBacktestRun
	e.strategyInfos = func() []backtest.StrategyInfo { return fakeStrategies }
	e.newStrategy = strategyScript(nil)
	e.stepDelay = func(float64) time.Duration { return 0 }
	return e
}

// waitAutonomy waits (wall clock) until the replay goroutine finishes.
func waitAutonomy(t *testing.T, e *Engine) AutonomyStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := e.Status().Autonomy
		if !st.Running {
			return st
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("autonomy did not finish within the timeout")
	return AutonomyStatus{}
}

// toolNames returns the names from a ChatResponse tool call list.
func toolNames(calls []ToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.Name)
	}
	return out
}

// findToolCall returns the first tool call with the given name.
func findToolCall(calls []ToolCall, name string) (ToolCall, bool) {
	for _, call := range calls {
		if call.Name == name {
			return call, true
		}
	}
	return ToolCall{}, false
}

// artifactKinds returns the artifact kinds of a response.
func artifactKinds(artifacts []Artifact) []string {
	out := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, artifact.Kind)
	}
	return out
}

// errDataSource fails every history request.
type errDataSource struct{ err error }

func (e *errDataSource) Instruments() []InstrumentRef { return nil }
func (e *errDataSource) Quote(string) (Quote, bool)   { return Quote{}, false }
func (e *errDataSource) OrderBook(string) (OrderBook, error) {
	return OrderBook{}, e.err
}
func (e *errDataSource) History(context.Context, string, int, int) ([]backtest.Bar, error) {
	return nil, e.err
}

var _ DataSource = (*fakeDataSource)(nil)
var _ DataSource = (*errDataSource)(nil)
var _ Broker = (*fakeBroker)(nil)
var _ = errors.New
