package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// errLiveAutonomy is the iteration-1 guard rail: autonomy trades the paper
// replay only, live autonomy is explicitly out of scope.
var errLiveAutonomy = errors.New("live autonomy is not implemented in iteration 1")

// toolCatalogNames is the frozen list of §9.1 tools, in catalog order.
var toolCatalogNames = []string{
	"list_instruments",
	"get_market_hints",
	"get_history",
	"run_backtest",
	"compare_strategies",
	"get_quote",
	"get_portfolio",
	"get_strategies",
	"place_order",
	"cancel_order",
	"start_autonomy",
	"stop_autonomy",
	"get_agent_status",
	"get_backtest_history",
	toolNameBacktestRules,
	toolNameBatchBacktest,
	toolNameDetectExecutionAlgos,
	toolNameGetNews,
}

// buildTools returns the tool catalog with JSON schemas. Research tools
// (phases E–G) are appended from tools_research.go so the shared catalog body
// stays small.
func buildTools() []ToolInfo {
	tools := []ToolInfo{
		{
			Name:        "list_instruments",
			Title:       "Инструменты",
			Description: "Список доступных инструментов терминала с последней ценой и изменением за день.",
			Input:       objSchema(map[string]any{}),
		},
		{
			Name:        "get_market_hints",
			Title:       "Технические подсказки",
			Description: "Техническая картина по инструменту: SMA20/50, RSI14, ATR14, волатильность, тренд, поддержка/сопротивление и breadth рынка.",
			Input: objSchema(map[string]any{
				"symbol":    strProp("Тикер инструмента, например TQBR:SBER или SBER"),
				"periodSec": intProp("Период свечей в секундах: 60, 300, 900, 3600 или 86400"),
			}, "symbol"),
		},
		{
			Name:        "get_history",
			Title:       "История котировок",
			Description: "Реальная история баров инструмента (последние не более 600 баров).",
			Input: objSchema(map[string]any{
				"symbol":    strProp("Тикер инструмента"),
				"periodSec": intProp("Период свечей в секундах"),
				"count":     intProp("Сколько последних баров вернуть, максимум 600"),
			}, "symbol", "periodSec"),
		},
		{
			Name:        "run_backtest",
			Title:       "Бэктест стратегии",
			Description: "Прогнать одну стратегию на реальной истории инструмента; возвращает метрики, equity и сделки.",
			Input: objSchema(map[string]any{
				"symbol":      strProp("Тикер инструмента"),
				"periodSec":   intProp("Период свечей в секундах"),
				"strategy":    strProp("ID стратегии: sma_cross, rsi_reversion, donchian_breakout, momentum"),
				"params":      numMapProp("Параметры стратегии (имена и значения)"),
				"initialCash": numProp("Стартовый капитал, ₽ (по умолчанию 1000000)"),
				"feeBps":      numProp("Комиссия в базисных пунктах (по умолчанию 5)"),
				"slippageBps": numProp("Проскальзывание в базисных пунктах (по умолчанию 1)"),
				"count":       intProp(fmt.Sprintf("Сколько последних баров использовать, максимум %d", maxBacktestBars)),
			}, "symbol", "strategy"),
		},
		{
			Name:        "compare_strategies",
			Title:       "Сравнение стратегий",
			Description: "Прогнать все стратегии на инструменте, отсортировать по доходности и вернуть лучшую.",
			Input: objSchema(map[string]any{
				"symbol":    strProp("Тикер инструмента"),
				"periodSec": intProp("Период свечей в секундах"),
				"count":     intProp(fmt.Sprintf("Сколько последних баров использовать, максимум %d", maxBacktestBars)),
			}, "symbol"),
		},
		{
			Name:        "get_quote",
			Title:       "Котировка и стакан",
			Description: "Текущая котировка инструмента и стакан заявок (лучшие уровни первыми).",
			Input: objSchema(map[string]any{
				"symbol": strProp("Тикер инструмента"),
			}, "symbol"),
		},
		{
			Name:        "get_portfolio",
			Title:       "Портфель",
			Description: "Бумажный портфель: стоимость, кэш, дневное изменение и позиции.",
			Input:       objSchema(map[string]any{}),
		},
		{
			Name:        "get_strategies",
			Title:       "Каталог стратегий",
			Description: "Список стратегий бэктеста с параметрами и описанием.",
			Input:       objSchema(map[string]any{}),
		},
		{
			Name:        "place_order",
			Title:       "Бумажная заявка",
			Description: "Выставить бумажную заявку (только paper; риск-лимиты проверяются). Реальные ордера в итерации 1 недоступны.",
			Input: objSchema(map[string]any{
				"symbol": strProp("Тикер инструмента"),
				"side":   enumProp("Направление", "buy", "sell"),
				"qty":    intProp("Количество бумаг (целое)"),
				"type":   enumProp("Тип заявки: market или limit", "market", "limit"),
				"price":  numProp("Цена для лимитной заявки, ₽"),
			}, "symbol", "side", "qty"),
		},
		{
			Name:        "cancel_order",
			Title:       "Отмена заявки",
			Description: "Отменить бумажную заявку по её ID.",
			Input: objSchema(map[string]any{
				"orderId": strProp("ID заявки"),
			}, "orderId"),
		},
		{
			Name:        "start_autonomy",
			Title:       "Запуск автоторговли",
			Description: "Запустить автономную стратегию на бумажном replay: реальные исторические бары, риск-лимиты, лог. Live запрещён.",
			Input: objSchema(map[string]any{
				"symbol":         strProp("Тикер инструмента"),
				"periodSec":      intProp("Период свечей в секундах"),
				"strategy":       strProp("ID стратегии бэктеста"),
				"params":         numMapProp("Параметры стратегии"),
				"speed":          numProp("Скорость replay, баров в секунду (1..60, по умолчанию 5)"),
				"budget":         numProp("Бюджет стратегии, ₽ (по умолчанию 200000)"),
				"maxPositionPct": numProp("Максимальная доля портфеля в позиции, %"),
				"stopLossPct":    numProp("Стоп-лосс, %"),
				"takeProfitPct":  numProp("Тейк-профит, %"),
				"dailyLossPct":   numProp("Максимальный дневной убыток от начального equity, %"),
				"live":           boolProp("Live-режим запрещён в итерации 1 (должен быть false)"),
			}, "symbol", "strategy"),
		},
		{
			Name:        "stop_autonomy",
			Title:       "Остановка автоторговли",
			Description: "Kill-switch: остановить автономную торговлю и вернуть итоговый статус.",
			Input:       objSchema(map[string]any{}),
		},
		{
			Name:        "get_agent_status",
			Title:       "Статус агента",
			Description: "Статус агента: запущена ли автономия, PnL, лог, доступность LLM.",
			Input:       objSchema(map[string]any{}),
		},
		{
			Name:        "get_backtest_history",
			Title:       "История бэктестов",
			Description: "Последние бэктесты агента с метриками и equity для UI.",
			Input:       objSchema(map[string]any{}),
		},
	}
	tools = append(tools, researchToolCatalog()...)
	// The recorded-news tool (plan Task 12) is appended from tools_news.go so
	// its schema and dispatch stay with the md.news implementation.
	return append(tools, newsToolCatalog()...)
}

// Tools returns the tool catalog with JSON schemas.
func (e *Engine) Tools() []ToolInfo {
	out := make([]ToolInfo, len(e.tools))
	copy(out, e.tools)
	return out
}

// mutatingTools is the frozen set of tools with side effects. The local
// fallback must never replay them after an LLM failure (review finding R3).
var mutatingTools = map[string]bool{
	"place_order":    true,
	"cancel_order":   true,
	"start_autonomy": true,
	"stop_autonomy":  true,
}

// isMutatingTool reports whether a tool changes state (orders or autonomy).
func isMutatingTool(name string) bool { return mutatingTools[name] }

// toolInvocation carries the LLM call identity into tool execution so a
// mutating tool can build an idempotent client order id. Direct tool calls
// (the local analyst, tests) leave it empty.
type toolInvocation struct {
	ConversationID string
	ToolCallID     string
}

// clientOrderID is the idempotency key of one agent order:
// conversationID + ":" + toolCallID. Without a conversation id the tool call id
// alone is already unique, so it is used as the key.
func (inv toolInvocation) clientOrderID() string {
	if inv.ToolCallID == "" {
		return ""
	}
	if inv.ConversationID == "" {
		return inv.ToolCallID
	}
	return inv.ConversationID + ":" + inv.ToolCallID
}

// executeToolCall runs one tool, emits SSE events and measures its duration.
func (e *Engine) executeToolCall(ctx context.Context, name string, args map[string]any) (ToolCall, *Artifact) {
	return e.executeToolCallFor(ctx, toolInvocation{}, name, args)
}

// executeToolCallFor runs one tool on behalf of one LLM call: mutating tools
// receive the call identity needed for their ClientOrderID and pass through the
// human-in-the-loop approval gate (§9.5).
func (e *Engine) executeToolCallFor(ctx context.Context, inv toolInvocation, name string, args map[string]any) (ToolCall, *Artifact) {
	return e.executeToolCallWith(ctx, inv, name, args, true)
}

// executeToolCallDirect runs a tool that a human already approved: it bypasses
// the approval gate but keeps the normal tool events.
func (e *Engine) executeToolCallDirect(ctx context.Context, inv toolInvocation, name string, args map[string]any) (ToolCall, *Artifact) {
	return e.executeToolCallWith(ctx, inv, name, args, false)
}

// executeToolCallWith is the common tool wrapper; gate enables the approval
// gate of the mutating tools.
func (e *Engine) executeToolCallWith(ctx context.Context, inv toolInvocation, name string, args map[string]any, gate bool) (ToolCall, *Artifact) {
	if args == nil {
		args = map[string]any{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	e.publish(Event{Type: "tool_call", Data: map[string]any{"name": name, "args": args}})
	call := ToolCall{Name: name, Args: args}
	result, artifact, err := e.invokeToolWith(ctx, inv, name, args, gate)
	call.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		call.Error = err.Error()
	} else {
		call.Result = result
	}
	e.publish(Event{Type: "tool_result", Data: call})
	if artifact != nil {
		switch artifact.Kind {
		case "backtest":
			e.publish(Event{Type: "backtest", Data: artifact.Payload})
		case "autonomy":
			e.publish(Event{Type: "autonomy", Data: artifact.Payload})
		}
	}
	return call, artifact
}

// invokeTool is the in-process tool dispatch with the approval gate enabled.
// Panics inside a data source or a tool implementation are recovered and
// reported as tool errors.
func (e *Engine) invokeTool(ctx context.Context, inv toolInvocation, name string, args map[string]any) (result any, artifact *Artifact, err error) {
	return e.invokeToolWith(ctx, inv, name, args, true)
}

// invokeToolWith is the in-process tool dispatch; gate enables the approval
// gate of the mutating tools (the approve path calls it with gate=false).
func (e *Engine) invokeToolWith(ctx context.Context, inv toolInvocation, name string, args map[string]any, gate bool) (result any, artifact *Artifact, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, artifact, err = nil, nil, fmt.Errorf("agent: tool %s panicked: %v", name, r)
		}
	}()
	if err := contextErr(ctx); err != nil {
		return nil, nil, err
	}
	// Research tools (declarative rules, walk-forward, batch backtests, …) are
	// dispatched from tools_research.go; handled=false falls through.
	if result, artifact, handled, err := e.invokeResearchTool(ctx, name, args); handled {
		return result, artifact, err
	}
	// The recorded-news tool (plan Task 12) is dispatched from tools_news.go
	// against the md.news store of the command wiring.
	if result, artifact, handled, err := e.invokeNewsTool(ctx, e.news, name, args); handled {
		return result, artifact, err
	}
	switch name {
	case "list_instruments":
		return e.data.Instruments(), nil, nil
	case "get_market_hints":
		periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
		if err != nil {
			return nil, nil, err
		}
		h, err := e.Hints(ctx, argString(args, "symbol"), int(periodSec))
		if err != nil {
			return nil, nil, err
		}
		return h, &Artifact{Kind: "hints", Payload: h}, nil
	case "get_history":
		symbol := e.resolveSymbol(argString(args, "symbol"))
		if symbol == "" {
			return nil, nil, errors.New("agent: symbol is required")
		}
		periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
		if err != nil {
			return nil, nil, err
		}
		countArg, err := argIntStrict(args, "count", maxHistoryBars, minToolCount, maxBacktestBars)
		if err != nil {
			return nil, nil, err
		}
		period := e.normalizePeriod(int(periodSec))
		count := clampCount(int(countArg), maxHistoryBars)
		bars, err := e.data.History(ctx, symbol, period, count)
		if err != nil {
			return nil, nil, fmt.Errorf("agent: history for %s: %w", symbol, err)
		}
		if len(bars) > maxHistoryBars {
			bars = bars[len(bars)-maxHistoryBars:]
		}
		return historyResult{Symbol: symbol, PeriodSec: period, Source: "feed", Bars: bars}, nil, nil
	case "run_backtest":
		res, err := e.toolRunBacktest(ctx, args)
		if err != nil {
			return nil, nil, err
		}
		return res, &Artifact{Kind: "backtest", Payload: res}, nil
	case "compare_strategies":
		res, best, err := e.toolCompareStrategies(ctx, args)
		if err != nil {
			return nil, nil, err
		}
		if best == nil {
			// No strategy traded on this history: there is no backtest card.
			return res, nil, nil
		}
		return res, &Artifact{Kind: "backtest", Payload: *best}, nil
	case "get_quote":
		res, err := e.toolQuote(ctx, args)
		if err != nil {
			return nil, nil, err
		}
		return res, nil, nil
	case "get_portfolio":
		if e.broker == nil {
			return nil, nil, errors.New("agent: paper broker is not configured")
		}
		return e.broker.Portfolio(), nil, nil
	case "get_strategies":
		return e.strategyCatalog(), nil, nil
	case "place_order":
		qty, err := argIntStrict(args, "qty", 0, minToolQty, maxToolQty)
		if err != nil {
			return nil, nil, err
		}
		// The order is validated (structure and the hard risk limits) before the
		// approval gate: a malformed or risk-blocked order is an error, never a
		// proposal a human could not legally approve (R5).
		req, err := e.checkOrderRisk(
			argString(args, "symbol"),
			argString(args, "side"),
			qty,
			argString(args, "type"),
			argFloat(args, "price", 0),
		)
		if err != nil {
			return nil, nil, err
		}
		if gate {
			if result, gated := e.gatePlaceOrder(args, req); gated {
				return result, nil, nil
			}
		}
		req.ClientOrderID = inv.clientOrderID()
		order, err := e.broker.Place(req)
		if err != nil {
			return nil, nil, fmt.Errorf("agent: place order: %w", err)
		}
		return order, nil, nil
	case "cancel_order":
		if e.broker == nil {
			return nil, nil, errors.New("agent: paper broker is not configured")
		}
		id := argString(args, "orderId")
		if id == "" {
			return nil, nil, errors.New("agent: orderId is required")
		}
		if gate {
			if !e.orderExists(id) {
				return nil, nil, fmt.Errorf("agent: cancel order %s: %w", id, errOrderNotFound)
			}
			if result, gated := e.gateMutatingTool("cancel_order", args); gated {
				return result, nil, nil
			}
		}
		order, err := e.broker.Cancel(id)
		if err != nil {
			return nil, nil, fmt.Errorf("agent: cancel order %s: %w", id, err)
		}
		return map[string]any{"order": order}, nil, nil
	case "start_autonomy":
		cfg, err := e.autonomyConfigFromArgs(args)
		if err != nil {
			return nil, nil, err
		}
		if cfg.Live {
			return nil, nil, errLiveAutonomy
		}
		if gate {
			// Never propose starting a runner that is already active, and never
			// propose a configuration the engine would reject at start time.
			if e.Status().Autonomy.Running {
				return nil, nil, ErrAlreadyRunning
			}
			if _, _, _, _, err := e.prepareAutonomy(ctx, cfg); err != nil {
				return nil, nil, err
			}
			if result, gated := e.gateMutatingTool("start_autonomy", args); gated {
				return result, nil, nil
			}
		}
		if err := e.StartAutonomy(ctx, cfg); err != nil {
			return nil, nil, err
		}
		st := e.autonomyStatus()
		return st, &Artifact{Kind: "autonomy", Payload: st}, nil
	case "stop_autonomy":
		// Documented exception (§9.5): the kill-switch is never delayed by a
		// human confirmation, otherwise a bad autonomy run could not be stopped.
		if err := e.StopAutonomy(); err != nil {
			return nil, nil, err
		}
		st := e.autonomyStatus()
		return st, &Artifact{Kind: "autonomy", Payload: st}, nil
	case "get_agent_status":
		return e.Status(), nil, nil
	case "get_backtest_history":
		return e.Backtests(), nil, nil
	default:
		return nil, nil, fmt.Errorf("agent: unknown tool %q", name)
	}
}

// autonomyStatus is a race-free snapshot of the current autonomy status.
func (e *Engine) autonomyStatus() AutonomyStatus {
	e.mu.Lock()
	run := e.autonomy
	e.mu.Unlock()
	if run == nil {
		return AutonomyStatus{Log: []LogEntry{}}
	}
	return run.snapshot()
}

// historyResult is the get_history tool payload.
type historyResult struct {
	Symbol    string         `json:"symbol"`
	PeriodSec int            `json:"periodSec"`
	Source    string         `json:"source"`
	Bars      []backtest.Bar `json:"bars"`
}

// bookView is the order-book part of the get_quote payload.
type bookView struct {
	Bids []Level `json:"bids"`
	Asks []Level `json:"asks"`
}

// quoteResult is the get_quote tool payload.
type quoteResult struct {
	Symbol    string    `json:"symbol"`
	Last      float64   `json:"last"`
	Bid       float64   `json:"bid"`
	Ask       float64   `json:"ask"`
	ChangePct float64   `json:"changePct"`
	Time      time.Time `json:"time"`
	Book      bookView  `json:"book"`
}

// rankedStrategy is one compare_strategies row. Warning is set for a strategy
// that produced no trades on the selected history (it is never "the best").
type rankedStrategy struct {
	Strategy string           `json:"strategy"`
	Metrics  backtest.Metrics `json:"metrics"`
	Warning  string           `json:"warning,omitempty"`
}

// bestStrategy is the winner of compare_strategies.
type bestStrategy struct {
	Strategy string             `json:"strategy"`
	Params   map[string]float64 `json:"params"`
	Result   backtest.Result    `json:"result"`
}

// compareResult is the compare_strategies tool payload. Best.Strategy is empty
// when no strategy produced trades ("no signals"): callers must not treat a
// zero-trade run as the winner (its zero Sharpe/drawdown is meaningless).
type compareResult struct {
	Ranked   []rankedStrategy `json:"ranked"`
	Best     bestStrategy     `json:"best"`
	Warnings []string         `json:"warnings,omitempty"`
}

// toolRunBacktest implements run_backtest.
func (e *Engine) toolRunBacktest(ctx context.Context, args map[string]any) (backtest.Result, error) {
	symbol := e.resolveSymbol(argString(args, "symbol"))
	if symbol == "" {
		return backtest.Result{}, errors.New("agent: symbol is required")
	}
	strategy := argString(args, "strategy")
	if strategy == "" {
		return backtest.Result{}, errors.New("agent: strategy is required")
	}
	if strategy == backtest.StrategyRules {
		// The rule strategy is defined by a rules.Spec, not by numeric params.
		return backtest.Result{}, errRulesStrategyNeedsSpec
	}
	periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
	if err != nil {
		return backtest.Result{}, err
	}
	countArg, err := argIntStrict(args, "count", maxHistoryBars, minToolCount, maxBacktestBars)
	if err != nil {
		return backtest.Result{}, err
	}
	period := e.normalizePeriod(int(periodSec))
	count := clampCount(int(countArg), maxBacktestBars)
	bars, err := e.data.History(ctx, symbol, period, count)
	if err != nil {
		return backtest.Result{}, fmt.Errorf("agent: history for %s: %w", symbol, err)
	}
	if len(bars) < 2 {
		return backtest.Result{}, fmt.Errorf("agent: not enough history for %s (%d bars)", symbol, len(bars))
	}
	req := backtest.Request{
		Symbol:      symbol,
		PeriodSec:   period,
		Bars:        bars,
		Strategy:    strategy,
		Params:      argFloatMap(args, "params"),
		InitialCash: argFloat(args, "initialCash", defaultBacktestCash),
		FeeBps:      argFloat(args, "feeBps", e.feeBps()),
		SlippageBps: argFloat(args, "slippageBps", 1),
	}
	res, err := e.runBacktest(req)
	if err != nil {
		return backtest.Result{}, fmt.Errorf("agent: backtest %s on %s: %w", strategy, symbol, err)
	}
	e.recordBacktest(res)
	return res, nil
}

// toolCompareStrategies implements compare_strategies: every catalog strategy
// is replayed on the same bars. Only strategies that actually traded are
// eligible for "best" (ranked by total return, then Sharpe as tie-break);
// zero-trade strategies are pushed to the end with a warning because their
// zero Sharpe/drawdown must never win. When nothing traded the returned best is
// nil and the payload says there are no signals.
func (e *Engine) toolCompareStrategies(ctx context.Context, args map[string]any) (compareResult, *backtest.Result, error) {
	symbol := e.resolveSymbol(argString(args, "symbol"))
	if symbol == "" {
		return compareResult{}, nil, errors.New("agent: symbol is required")
	}
	periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
	if err != nil {
		return compareResult{}, nil, err
	}
	countArg, err := argIntStrict(args, "count", maxHistoryBars, minToolCount, maxBacktestBars)
	if err != nil {
		return compareResult{}, nil, err
	}
	period := e.normalizePeriod(int(periodSec))
	count := clampCount(int(countArg), maxBacktestBars)
	bars, err := e.data.History(ctx, symbol, period, count)
	if err != nil {
		return compareResult{}, nil, fmt.Errorf("agent: history for %s: %w", symbol, err)
	}
	if len(bars) < 2 {
		return compareResult{}, nil, fmt.Errorf("agent: not enough history for %s (%d bars)", symbol, len(bars))
	}

	results := make([]backtest.Result, 0, len(e.strategyCatalog()))
	warnings := make([]string, 0, 1)
	var failures []string
	for _, info := range e.strategyCatalog() {
		req := backtest.Request{
			Symbol:      symbol,
			PeriodSec:   period,
			Bars:        bars,
			Strategy:    info.ID,
			InitialCash: defaultBacktestCash,
			FeeBps:      e.feeBps(),
		}
		res, err := e.runBacktest(req)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", info.ID, err))
			continue
		}
		results = append(results, res)
	}
	if len(results) == 0 {
		return compareResult{}, nil, fmt.Errorf("agent: all strategies failed: %s", strings.Join(failures, "; "))
	}
	if len(failures) > 0 {
		warnings = append(warnings, "не удалось прогнать: "+strings.Join(failures, "; "))
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i].Metrics, results[j].Metrics
		tradedA, tradedB := a.Trades > 0, b.Trades > 0
		if tradedA != tradedB {
			// Traded strategies always rank above zero-trade strategies.
			return tradedA
		}
		if a.TotalReturnPct != b.TotalReturnPct {
			return a.TotalReturnPct > b.TotalReturnPct
		}
		if a.Sharpe != b.Sharpe {
			return a.Sharpe > b.Sharpe
		}
		if a.MaxDrawdownPct != b.MaxDrawdownPct {
			return a.MaxDrawdownPct > b.MaxDrawdownPct
		}
		return results[i].Strategy < results[j].Strategy
	})

	out := compareResult{Ranked: make([]rankedStrategy, 0, len(results))}
	for _, res := range results {
		row := rankedStrategy{Strategy: res.Strategy, Metrics: res.Metrics}
		if res.Metrics.Trades <= 0 {
			row.Warning = "нет сделок на выбранном периоде"
		}
		out.Ranked = append(out.Ranked, row)
	}
	if results[0].Metrics.Trades <= 0 {
		out.Warnings = append(out.Warnings,
			"нет стратегий со сделками на выбранном периоде: сигналов нет, лучшая стратегия не выбирается")
		return out, nil, nil
	}
	bestResult := results[0]
	out.Best = bestStrategy{
		Strategy: bestResult.Strategy,
		Params:   bestResult.Params,
		Result:   bestResult,
	}
	e.recordBacktest(bestResult)
	return out, &bestResult, nil
}

// toolQuote implements get_quote.
func (e *Engine) toolQuote(ctx context.Context, args map[string]any) (quoteResult, error) {
	symbol := e.resolveSymbol(argString(args, "symbol"))
	if symbol == "" {
		return quoteResult{}, errors.New("agent: symbol is required")
	}
	q, ok := e.data.Quote(symbol)
	book, bookErr := e.data.OrderBook(symbol)
	if !ok && bookErr != nil && book.Last == 0 {
		return quoteResult{}, fmt.Errorf("agent: no quote for %s", symbol)
	}
	if !ok {
		q.Last = book.Last
	}
	out := quoteResult{
		Symbol:    symbol,
		Last:      q.Last,
		Bid:       q.Bid,
		Ask:       q.Ask,
		ChangePct: q.ChangePct,
		Time:      q.Time,
		Book:      bookView{Bids: book.Bids, Asks: book.Asks},
	}
	if out.Last == 0 && len(book.Bids) > 0 {
		out.Last = book.Bids[0].Price
	}
	return out, nil
}

// errOrderNotFound is returned by the cancel_order validation when the paper
// broker has no such order (the gate must not propose cancelling a ghost).
var errOrderNotFound = errors.New("agent: order not found")

// orderExists reports whether the paper broker knows one order id.
func (e *Engine) orderExists(id string) bool {
	if e.broker == nil {
		return false
	}
	for _, order := range e.broker.Orders() {
		if order.ID == id {
			return true
		}
	}
	return false
}

// autonomyConfigFromArgs maps raw tool arguments to AutonomyConfig. The
// integer periodSec is validated strictly; an error is returned to the model
// as a tool error.
func (e *Engine) autonomyConfigFromArgs(args map[string]any) (AutonomyConfig, error) {
	periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
	if err != nil {
		return AutonomyConfig{}, err
	}
	return AutonomyConfig{
		Symbol:         e.resolveSymbol(argString(args, "symbol")),
		PeriodSec:      int(periodSec),
		Strategy:       argString(args, "strategy"),
		Params:         argFloatMap(args, "params"),
		Speed:          argFloat(args, "speed", 0),
		Budget:         argFloat(args, "budget", 0),
		MaxPositionPct: argFloat(args, "maxPositionPct", 0),
		StopLossPct:    argFloat(args, "stopLossPct", 0),
		TakeProfitPct:  argFloat(args, "takeProfitPct", 0),
		DailyLossPct:   argFloat(args, "dailyLossPct", 0),
		Live:           argBool(args, "live"),
	}, nil
}

// --- JSON schema helpers ---

func objSchema(props map[string]any, required ...string) json.RawMessage {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	m["additionalProperties"] = false
	raw, err := json.Marshal(m)
	if err != nil {
		// Static schemas only; cannot happen at runtime.
		panic("agent: invalid static tool schema: " + err.Error())
	}
	return raw
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func numProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}

func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

func numMapProp(desc string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          desc,
		"additionalProperties": map[string]any{"type": "number"},
	}
}

// --- tool argument helpers ---

// argString returns a trimmed string argument.
func argString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

// argFloat returns a numeric argument or def when it is missing/unparsable.
func argFloat(args map[string]any, key string, def float64) float64 {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", "."), 64); err == nil {
			return f
		}
	}
	return def
}

// argBool returns a boolean argument.
func argBool(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	b, _ := v.(bool)
	return b
}

// argFloatMap returns a numeric map argument (strategy params).
func argFloatMap(args map[string]any, key string) map[string]float64 {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	switch raw := v.(type) {
	case map[string]any:
		return toFloatMap(raw)
	case map[string]float64:
		out := make(map[string]float64, len(raw))
		for k, val := range raw {
			out[k] = val
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

func toFloatMap(raw map[string]any) map[string]float64 {
	out := make(map[string]float64, len(raw))
	for k, val := range raw {
		f, ok := numericValue(val)
		if !ok {
			continue
		}
		out[k] = f
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func numericValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", "."), 64)
		return f, err == nil
	}
	return 0, false
}
