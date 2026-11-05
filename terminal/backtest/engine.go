package backtest

import (
	"fmt"
	"math"
	"time"
)

// Engine defaults, guard rails and warning thresholds. The Request DTOs use
// `omitempty`, so a zero value means "use the documented default".
const (
	defaultInitialCash = 1_000_000.0
	defaultFeeBps      = 5.0
	defaultSlippageBps = 1.0
	defaultRiskPct     = 100.0

	// minBars is the smallest series the engine can replay.
	minBars = 2
	// recommendedBars below which the metrics are considered unreliable.
	recommendedBars = 50
	// highDrawdownPct is the drawdown warning threshold.
	highDrawdownPct = -20.0
)

// runConfig is a normalized Request: zero defaults are resolved and the values
// are validated.
type runConfig struct {
	periodSec   int
	initialCash float64
	feeBps      float64
	slippageBps float64
	riskPct     float64
}

func normalizeRequest(req Request) (runConfig, error) {
	if req.PeriodSec < 0 {
		return runConfig{}, fmt.Errorf("backtest: periodSec must not be negative: %d", req.PeriodSec)
	}
	if req.InitialCash < 0 {
		return runConfig{}, fmt.Errorf("backtest: initialCash must not be negative: %v", req.InitialCash)
	}
	if req.FeeBps < 0 || req.FeeBps >= 10_000 {
		return runConfig{}, fmt.Errorf("backtest: feeBps must be in [0, 10000): %v", req.FeeBps)
	}
	if req.SlippageBps < 0 || req.SlippageBps >= 10_000 {
		return runConfig{}, fmt.Errorf("backtest: slippageBps must be in [0, 10000): %v", req.SlippageBps)
	}
	if req.RiskPct < 0 {
		return runConfig{}, fmt.Errorf("backtest: riskPct must not be negative: %v", req.RiskPct)
	}
	cfg := runConfig{
		periodSec:   req.PeriodSec,
		initialCash: req.InitialCash,
		feeBps:      req.FeeBps,
		slippageBps: req.SlippageBps,
		riskPct:     req.RiskPct,
	}
	if cfg.initialCash == 0 {
		cfg.initialCash = defaultInitialCash
	}
	if cfg.feeBps == 0 {
		cfg.feeBps = defaultFeeBps
	}
	if cfg.slippageBps == 0 {
		cfg.slippageBps = defaultSlippageBps
	}
	if cfg.riskPct == 0 {
		cfg.riskPct = defaultRiskPct
	}
	if cfg.riskPct > 100 {
		// Long-only cash account: a position can never exceed the equity.
		cfg.riskPct = 100
	}
	return cfg, nil
}

// validateBars rejects unusable price data instead of silently producing NaN
// metrics.
func validateBars(bars []Bar) error {
	for i, bar := range bars {
		for _, price := range [...]float64{bar.Open, bar.High, bar.Low, bar.Close} {
			if math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
				return fmt.Errorf("backtest: invalid price in bar %d: %v", i, price)
			}
		}
	}
	return nil
}

// dropPartialBars removes the still forming candles of a live feed. Their
// provisional close and volume are not a closed bar, so neither the strategy
// nor the metrics may use them.
func dropPartialBars(bars []Bar) ([]Bar, int) {
	dropped := 0
	for _, bar := range bars {
		if bar.Partial {
			dropped++
		}
	}
	if dropped == 0 {
		return bars, 0
	}
	closed := make([]Bar, 0, len(bars)-dropped)
	for _, bar := range bars {
		if bar.Partial {
			continue
		}
		closed = append(closed, bar)
	}
	return closed, dropped
}

// replayResult is the raw engine output before metrics are derived.
type replayResult struct {
	equity         []float64
	trades         []Trade
	exposureBars   int
	skippedEntries int
	openPosition   int64
}

// replay walks the bars once. A strategy decision taken on the close of bar i is
// filled at the open of bar i+1 (the last bar has no next open, so OnBar is
// never called for it). Slippage always moves the fill against the direction,
// commission is charged on the turnover, and the position size is a whole number
// of units limited by RiskPct of the current equity and by available cash.
func replay(bars []Bar, strategy Strategy, cfg runConfig) replayResult {
	n := len(bars)
	feeRate := cfg.feeBps / 10_000
	slipRate := cfg.slippageBps / 10_000

	out := replayResult{
		equity: make([]float64, n),
		trades: make([]Trade, 0, 8),
	}
	cash := cfg.initialCash
	out.equity[0] = cash

	var qty int64
	var entryPrice, entryCost float64
	var entryTime time.Time
	var entryBar int

	for i := 0; i < n-1; i++ {
		next := bars[i+1]
		action := strategy.OnBar(bars, i)
		switch {
		case action == Buy && qty == 0:
			budget := cash * cfg.riskPct / 100
			fillPrice := next.Open * (1 + slipRate)
			byBudget := int64(math.Floor(budget / fillPrice))
			byCash := int64(math.Floor(cash / (fillPrice * (1 + feeRate))))
			size := byBudget
			if byCash < size {
				size = byCash
			}
			if size < 1 {
				// Not enough money for a single unit: skip the signal, it is
				// not an error.
				out.skippedEntries++
				break
			}
			entryPrice = fillPrice
			entryTime = next.Time
			entryBar = i + 1
			entryCost = float64(size) * entryPrice * (1 + feeRate)
			cash -= entryCost
			qty = size
		case action == Sell && qty > 0:
			fillPrice := next.Open * (1 - slipRate)
			proceeds := float64(qty) * fillPrice * (1 - feeRate)
			cash += proceeds
			pnl := proceeds - entryCost
			out.trades = append(out.trades, Trade{
				EntryTime:  entryTime,
				ExitTime:   next.Time,
				Side:       "long",
				EntryPrice: entryPrice,
				ExitPrice:  fillPrice,
				Qty:        qty,
				PnL:        pnl,
				PnLPct:     pnl / entryCost * 100,
				BarsHeld:   (i + 1) - entryBar,
				Reason:     "signal",
			})
			qty = 0
			entryPrice = 0
			entryCost = 0
		}
		out.equity[i+1] = cash + float64(qty)*next.Close
		if qty > 0 {
			out.exposureBars++
		}
	}
	out.openPosition = qty
	return out
}

// Run replays bars through the named catalog strategy and returns metrics, the
// equity curve and the closed trades. Signals are decided on the close of bar i
// and filled at the open of bar i+1; there is no lookahead, no network and no
// clock access. An open position at the last bar is marked to market, not
// force-closed.
func Run(req Request) (Result, error) {
	strategy, err := NewStrategy(req.Strategy, req.Params)
	if err != nil {
		return Result{}, err
	}
	return runWithStrategy(req, strategy)
}

// RunStrategy replays bars through a caller-built strategy. It is the seam for
// strategies whose definition is not the numeric Params map of the catalog —
// declarative rules are compiled from a JSON Spec by rules.Compile and then
// replayed here. The partial-bar rule of Run applies as well.
func RunStrategy(req Request, strategy Strategy) (Result, error) {
	if strategy == nil {
		return Result{}, fmt.Errorf("backtest: nil strategy")
	}
	return runWithStrategy(req, strategy)
}

// runWithStrategy is the shared engine path of Run and RunStrategy.
func runWithStrategy(req Request, strategy Strategy) (Result, error) {
	cfg, err := normalizeRequest(req)
	if err != nil {
		return Result{}, err
	}

	// Signals are computed on closed bars only: drop the still forming candles
	// of a live feed instead of trading their provisional close.
	bars, droppedPartial := dropPartialBars(req.Bars)
	req.Bars = bars

	n := len(req.Bars)
	result := Result{
		Symbol:    req.Symbol,
		Strategy:  strategy.ID(),
		PeriodSec: cfg.periodSec,
		Params:    strategy.Params(),
		Equity:    []EquityPoint{},
		Trades:    []Trade{},
		Warnings:  []string{},
	}
	result.Metrics.InitialCash = cfg.initialCash
	result.Metrics.FinalEquity = cfg.initialCash
	result.Metrics.Bars = n
	if n > 0 {
		result.Metrics.From = req.Bars[0].Time
		result.Metrics.To = req.Bars[n-1].Time
	}
	if cfg.periodSec == 0 {
		result.Warnings = append(result.Warnings, "periodSec не задан: коэффициент Шарпа не рассчитывается")
	}
	if droppedPartial > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("незакрытые бары отброшены: %d", droppedPartial))
	}
	if n < minBars {
		result.Warnings = append(result.Warnings, fmt.Sprintf("недостаточно баров: %d (нужно не менее %d)", n, minBars))
		return result, nil
	}
	if err := validateBars(req.Bars); err != nil {
		return Result{}, err
	}

	replayed := replay(req.Bars, strategy, cfg)
	times := make([]time.Time, n)
	drawdowns := drawdownSeries(replayed.equity)
	benchmarkBase := cfg.initialCash / req.Bars[0].Close

	result.Equity = make([]EquityPoint, n)
	for j := 0; j < n; j++ {
		times[j] = req.Bars[j].Time
		result.Equity[j] = EquityPoint{
			Time:        req.Bars[j].Time,
			Equity:      replayed.equity[j],
			Benchmark:   benchmarkBase * req.Bars[j].Close,
			DrawdownPct: drawdowns[j],
		}
	}
	result.Trades = replayed.trades
	result.Metrics = computeMetrics(metricsInput{
		initialCash:  cfg.initialCash,
		equity:       replayed.equity,
		times:        times,
		exposureBars: replayed.exposureBars,
		trades:       replayed.trades,
		periodSec:    cfg.periodSec,
	})
	result.Metrics.BenchmarkReturnPct = (req.Bars[n-1].Close/req.Bars[0].Close - 1) * 100

	if n < recommendedBars {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("мало баров: %d < %d — метрики могут быть ненадёжны", n, recommendedBars))
	}
	if len(replayed.trades) == 0 {
		result.Warnings = append(result.Warnings, "сделок не было: стратегия не дала сигналов на этих барах")
	}
	if result.Metrics.MaxDrawdownPct <= highDrawdownPct {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("высокая максимальная просадка: %.1f%%", result.Metrics.MaxDrawdownPct))
	}
	if replayed.skippedEntries > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("пропущено входов из-за нехватки средств: %d", replayed.skippedEntries))
	}
	if replayed.openPosition > 0 {
		result.Warnings = append(result.Warnings,
			"позиция открыта на последнем баре и оценена по последнему close")
	}
	return result, nil
}
