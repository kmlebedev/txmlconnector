package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
	"github.com/kmlebedev/txmlconnector/terminal/research"
)

// This file holds the research/backtest tools of plan phases E–G. They are
// wired into the frozen catalog and dispatch from terminal/agent/tools.go
// through two hooks:
//
//   - researchToolCatalog() is appended to buildTools() and backtest_rules is
//     listed in toolCatalogNames;
//   - invokeResearchTool() handles a research tool before the core dispatch
//     switch and reports handled=false for every other name.
//
// Keeping new research tools here means later tasks (walk_forward,
// batch_backtest, volatility) never edit the shared tools.go dispatch body.

// toolNameBacktestRules is the agent tool name of the declarative rules
// backtest.
const toolNameBacktestRules = "backtest_rules"

// toolNameBatchBacktest is the agent tool name of the multi-symbol comparison.
const toolNameBatchBacktest = "batch_backtest"

// maxBatchSymbols bounds one batch comparison so a single tool call cannot fan
// out unbounded history requests.
const maxBatchSymbols = 20

// errRulesStrategyNeedsSpec is the run_backtest guard: the "rules" catalog
// strategy cannot be built from numeric params, it needs a rules.Spec.
var errRulesStrategyNeedsSpec = fmt.Errorf(
	"agent: strategy %q needs a declarative Spec: call %s with a JSON rules.Spec instead of run_backtest",
	rules.ID, toolNameBacktestRules)

// researchToolCatalog returns the catalog of research tools added by phases
// E–G, in catalog order.
func researchToolCatalog() []ToolInfo {
	return []ToolInfo{backtestRulesTool, batchBacktestTool}
}

// backtestRulesTool is the backtest_rules tool description.
var backtestRulesTool = ToolInfo{
	Name:  toolNameBacktestRules,
	Title: "Бэктест по правилам",
	Description: "Прогнать декларативные правила из JSON (индикаторы и условия входа, выхода и фильтры) " +
		"на реальной истории инструмента; возвращает метрики, equity и сделки.",
	Input: objSchema(map[string]any{
		"symbol":      strProp("Тикер инструмента, например TQBR:SBER или SBER"),
		"periodSec":   intProp("Период свечей в секундах: 60, 300, 900, 3600 или 86400"),
		"count":       intProp(fmt.Sprintf("Сколько последних баров использовать, максимум %d", maxBacktestBars)),
		"rules":       rulesSpecProp(),
		"initialCash": numProp("Стартовый капитал, ₽ (по умолчанию 1000000)"),
		"feeBps":      numProp("Комиссия в базисных пунктах (по умолчанию 5)"),
		"slippageBps": numProp("Проскальзывание в базисных пунктах (по умолчанию 1)"),
	}, "symbol", "rules"),
}

// rulesSpecProp is the JSON Schema of the rules.Spec argument. The Spec struct
// is decoded strictly by parseRulesSpec, so the description lists the exact
// shape instead of relying on the model to guess it.
func rulesSpecProp() map[string]any {
	return map[string]any{
		"type": "object",
		"description": "Декларативные правила (rules.Spec): indicators — карта имя → {kind, period, source, scale}; " +
			"kind: rsi_wilder|sma|ema|atr|donchian_high|donchian_low|roc; source: close|high|low|volume; " +
			"entry, exit, filters — массивы {left, op, right}; op: <|<=|>|>=|cross_above|cross_below; " +
			"left/right — имя индикатора, close или число; side: long; stopLossPct, takeProfitPct — необязательные проценты",
		"additionalProperties": true,
	}
}

// batchBacktestTool is the batch_backtest tool description.
var batchBacktestTool = ToolInfo{
	Name:  toolNameBatchBacktest,
	Title: "Сравнение тикеров",
	Description: "Прогнать одну стратегию или декларативные правила по нескольким тикерам и сравнить доходность " +
		"с buy & hold; предупреждает о малой выборке (small_sample) и нулевых расходах (no_costs).",
	Input: objSchema(map[string]any{
		"symbols":     strArrayProp("Тикеры инструментов, например [\"TQBR:GAZP\", \"TQBR:LKOH\", \"TQBR:SBER\"]"),
		"periodSec":   intProp("Период свечей в секундах: 60, 300, 900, 3600 или 86400"),
		"count":       intProp(fmt.Sprintf("Сколько последних баров на тикер, максимум %d", maxBacktestBars)),
		"strategy":    strProp("ID каталоговой стратегии: sma_cross, rsi_reversion, donchian_breakout или momentum"),
		"params":      numMapProp("Параметры каталоговой стратегии (имена и значения)"),
		"rules":       rulesSpecProp(),
		"feeBps":      numProp("Комиссия в базисных пунктах (0 — явно без комиссии)"),
		"slippageBps": numProp("Проскальзывание в базисных пунктах (0 — явно без проскальзывания)"),
	}, "symbols"),
}

// strArrayProp declares a JSON array of strings argument.
func strArrayProp(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       map[string]any{"type": "string"},
	}
}

// batchBacktestResult is the compact tool payload the model receives: rows,
// warnings and the best ticker. The chart series and trades live in the
// artifact, which is only rendered by the UI, so the LLM context stays small.
type batchBacktestResult struct {
	Rows     []research.BatchRow `json:"rows"`
	Best     string              `json:"best,omitempty"`
	Warnings []string            `json:"warnings,omitempty"`
}

// batchBacktestArtifact is the UI payload of batch_backtest (plan Task 24:
// Result.Trades + Series map[string][]float64 for the chart panel).
type batchBacktestArtifact struct {
	Rows     []research.BatchRow `json:"rows"`
	Charts   []batchChart        `json:"charts"`
	Best     string              `json:"best,omitempty"`
	Warnings []string            `json:"warnings,omitempty"`
}

// batchChart is the chart data of one batch symbol: the trades, the indicator
// series (rules strategies only, aligned with the candles and NaN on warm-up)
// and the number of bars the chart should show.
type batchChart struct {
	Symbol string               `json:"symbol"`
	Bars   int                  `json:"bars"`
	Trades []backtest.Trade     `json:"trades"`
	Series map[string][]float64 `json:"series,omitempty"`
}

// invokeResearchTool dispatches one research tool call. handled is false when
// name is not a research tool: the caller must then fall through to the core
// dispatch switch. Panics are recovered by the core invokeTool wrapper.
func (e *Engine) invokeResearchTool(ctx context.Context, name string, args map[string]any) (result any, artifact *Artifact, handled bool, err error) {
	switch name {
	case toolNameBacktestRules:
		res, err := e.toolBacktestRules(ctx, args)
		if err != nil {
			return nil, nil, true, err
		}
		return res, &Artifact{Kind: "backtest", Payload: res}, true, nil
	case toolNameBatchBacktest:
		res, ui, err := e.toolBatchBacktest(ctx, args)
		if err != nil {
			return nil, nil, true, err
		}
		return res, &Artifact{Kind: "batch_backtest", Payload: ui}, true, nil
	}
	return nil, nil, false, nil
}

// argStringList reads a string-array tool argument. A single comma/semicolon/
// whitespace separated string is accepted too, because models sometimes flatten
// arrays into one string.
func argStringList(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if item == nil {
				continue
			}
			if text, ok := item.(string); ok {
				out = append(out, text)
				continue
			}
			out = append(out, fmt.Sprint(item))
		}
		return out
	case string:
		return strings.FieldsFunc(value, func(r rune) bool {
			switch r {
			case ',', ';', ' ', '\n', '\t':
				return true
			}
			return false
		})
	}
	return nil
}

// toolBatchBacktest implements batch_backtest: one strategy or rules.Spec on
// several tickers, compared with buy & hold. The compact payload goes to the
// model and the artifact with chart data goes to the UI card.
func (e *Engine) toolBatchBacktest(ctx context.Context, args map[string]any) (batchBacktestResult, batchBacktestArtifact, error) {
	empty := batchBacktestResult{}
	if err := contextErr(ctx); err != nil {
		return empty, batchBacktestArtifact{}, err
	}
	rawSymbols := argStringList(args, "symbols")
	if len(rawSymbols) == 0 {
		return empty, batchBacktestArtifact{}, errors.New("agent: symbols are required: pass a list of tickers")
	}
	if len(rawSymbols) > maxBatchSymbols {
		return empty, batchBacktestArtifact{}, fmt.Errorf(
			"agent: too many symbols: %d > %d", len(rawSymbols), maxBatchSymbols)
	}
	symbols := make([]string, 0, len(rawSymbols))
	seen := make(map[string]bool, len(rawSymbols))
	for _, item := range rawSymbols {
		symbol := e.resolveSymbol(item)
		if symbol == "" {
			return empty, batchBacktestArtifact{}, fmt.Errorf("agent: symbol %q is empty", item)
		}
		if seen[symbol] {
			continue
		}
		seen[symbol] = true
		symbols = append(symbols, symbol)
	}
	periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
	if err != nil {
		return empty, batchBacktestArtifact{}, err
	}
	countArg, err := argIntStrict(args, "count", maxBacktestBars, minToolCount, maxBacktestBars)
	if err != nil {
		return empty, batchBacktestArtifact{}, err
	}
	period := e.normalizePeriod(int(periodSec))
	count := clampCount(int(countArg), maxBacktestBars)

	req := research.BatchRequest{
		Symbols:     symbols,
		PeriodSec:   period,
		Count:       count,
		Strategy:    argString(args, "strategy"),
		Params:      argFloatMap(args, "params"),
		FeeBps:      argFloat(args, "feeBps", e.feeBps()),
		SlippageBps: argFloat(args, "slippageBps", 1),
	}
	if rawRules, ok := args["rules"]; ok && rawRules != nil {
		spec, err := parseRulesSpec(rawRules)
		if err != nil {
			return empty, batchBacktestArtifact{}, err
		}
		req.Rules = &spec
	}
	if req.Strategy == "" && req.Rules == nil {
		return empty, batchBacktestArtifact{}, errors.New("agent: strategy or rules are required")
	}
	hist := func(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error) {
		return e.data.History(ctx, symbol, periodSec, count)
	}
	details, err := research.BatchDetailed(ctx, hist, req)
	if err != nil {
		return empty, batchBacktestArtifact{}, fmt.Errorf("agent: batch backtest: %w", err)
	}

	out := batchBacktestResult{Rows: make([]research.BatchRow, 0, len(details))}
	ui := batchBacktestArtifact{
		Rows:   make([]research.BatchRow, 0, len(details)),
		Charts: make([]batchChart, 0, len(details)),
	}
	best, bestReturn := "", 0.0
	for _, detail := range details {
		row := detail.Row
		out.Rows = append(out.Rows, row)
		ui.Rows = append(ui.Rows, row)
		if row.BacktestID == "" {
			continue
		}
		ui.Charts = append(ui.Charts, batchChart{
			Symbol: row.Symbol,
			Bars:   len(detail.Bars),
			Trades: detail.Result.Trades,
			Series: detail.Series,
		})
		e.recordBacktest(detail.Result)
		if row.Trades > 0 && (best == "" || row.ReturnPct > bestReturn) {
			best, bestReturn = row.Symbol, row.ReturnPct
		}
	}
	out.Best, ui.Best = best, best
	return out, ui, nil
}

// toolBacktestRules implements backtest_rules: the JSON Spec is compiled into a
// lookahead-free strategy and replayed through backtest.RunStrategy, because
// the numeric-param Run path cannot carry a Spec. The result is recorded in the
// agent backtest history exactly like run_backtest results.
func (e *Engine) toolBacktestRules(ctx context.Context, args map[string]any) (backtest.Result, error) {
	if err := contextErr(ctx); err != nil {
		return backtest.Result{}, err
	}
	symbol := e.resolveSymbol(argString(args, "symbol"))
	if symbol == "" {
		return backtest.Result{}, errors.New("agent: symbol is required")
	}
	spec, err := parseRulesSpec(args["rules"])
	if err != nil {
		return backtest.Result{}, err
	}
	strategy, err := rules.Compile(spec)
	if err != nil {
		return backtest.Result{}, fmt.Errorf("agent: %w", err)
	}
	periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
	if err != nil {
		return backtest.Result{}, err
	}
	countArg, err := argIntStrict(args, "count", maxBacktestBars, minToolCount, maxBacktestBars)
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
		Strategy:    strategy.ID(),
		InitialCash: argFloat(args, "initialCash", defaultBacktestCash),
		FeeBps:      argFloat(args, "feeBps", e.feeBps()),
		SlippageBps: argFloat(args, "slippageBps", 1),
	}
	result, err := backtest.RunStrategy(req, strategy)
	if err != nil {
		return backtest.Result{}, fmt.Errorf("agent: backtest rules on %s: %w", symbol, err)
	}
	e.recordBacktest(result)
	return result, nil
}

// parseRulesSpec decodes the "rules" tool argument. The argument is normally a
// JSON object; a JSON-encoded string with the same object is accepted too,
// because models sometimes double-encode tool arguments. Unknown JSON fields
// are rejected so a typo comes back to the model as a tool error instead of a
// silently ignored rule.
func parseRulesSpec(raw any) (rules.Spec, error) {
	var data []byte
	switch value := raw.(type) {
	case nil:
		return rules.Spec{}, errors.New("agent: rules are required: pass a rules.Spec JSON object")
	case string:
		data = []byte(strings.TrimSpace(value))
	case json.RawMessage:
		data = value
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return rules.Spec{}, fmt.Errorf("agent: rules: %w", err)
		}
		data = encoded
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return rules.Spec{}, errors.New("agent: rules are required: pass a rules.Spec JSON object")
	}
	var spec rules.Spec
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return rules.Spec{}, fmt.Errorf("agent: invalid rules JSON: %w", err)
	}
	return spec, nil
}
