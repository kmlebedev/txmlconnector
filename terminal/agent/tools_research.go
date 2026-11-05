package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
	"github.com/kmlebedev/txmlconnector/terminal/fingerprint"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
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

// toolNameDetectExecutionAlgos is the agent tool name of the order-book
// fingerprint analysis.
const toolNameDetectExecutionAlgos = "detect_execution_algos"

// toolNameWalkForward is the agent tool name of the walk-forward grid search.
const toolNameWalkForward = "walk_forward"

// maxBatchSymbols bounds one batch comparison so a single tool call cannot fan
// out unbounded history requests.
const maxBatchSymbols = 20

// maxExecutionFootprints bounds the footprint count of the compact model
// payload; the full list is in the UI artifact.
const maxExecutionFootprints = 20

// errRulesStrategyNeedsSpec is the run_backtest guard: the "rules" catalog
// strategy cannot be built from numeric params, it needs a rules.Spec.
var errRulesStrategyNeedsSpec = fmt.Errorf(
	"agent: strategy %q needs a declarative Spec: call %s with a JSON rules.Spec instead of run_backtest",
	rules.ID, toolNameBacktestRules)

// researchToolCatalog returns the catalog of research tools added by phases
// E–G, in catalog order.
func researchToolCatalog() []ToolInfo {
	return []ToolInfo{backtestRulesTool, batchBacktestTool, detectExecutionAlgosTool, walkForwardTool}
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

// detectExecutionAlgosTool is the detect_execution_algos tool description.
var detectExecutionAlgosTool = ToolInfo{
	Name:  toolNameDetectExecutionAlgos,
	Title: "Отпечатки исполняющих роботов",
	Description: "Найти на записанной ленте стакана признаки исполняющих алгоритмов: TWAP/VWAP/POV " +
		"(периодичность клипов, темп против профиля объёма, доля рынка), айсберг (пополнение уровня " +
		"после съедания), маржин-колл (серия растущих продаж в окне) и кластеры неизвестных паттернов. " +
		"Атрибуция вероятностная: у каждой находки есть Confidence и Evidence; обвинительных формулировок нет.",
	Input: objSchema(map[string]any{
		"symbol":            strProp("Тикер инструмента, например TQBR:SBER"),
		"from":              strProp("Начало записанной ленты, RFC3339 (UTC), например 2026-11-05T07:00:00Z"),
		"to":                strProp("Конец записанной ленты, RFC3339 (UTC)"),
		"marginCallWindows": strArrayProp("Окна маржин-коллов МСК \"HH:MM-HH:MM\"; по умолчанию 14:00-14:30 и 18:00-18:30"),
	}, "symbol", "from", "to"),
}

// LOBDataSource is the optional extension of DataSource for the order-book
// research tools: the recorded md.book_events/md.trades tape of one instrument.
// cmd/terminal currently provides the bar history only, so until the recorder
// pipeline is wired into the terminal the LOB tools report a clear configuration
// error instead of silently analysing nothing.
type LOBDataSource interface {
	LOBEvents(ctx context.Context, symbol string, from, to time.Time) ([]lobreplay.Event, error)
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
	case toolNameDetectExecutionAlgos:
		res, ui, err := e.toolDetectExecutionAlgos(ctx, args)
		if err != nil {
			return nil, nil, true, err
		}
		return res, &Artifact{Kind: "execution_algos", Payload: ui}, true, nil
	case toolNameWalkForward:
		res, ui, err := e.toolWalkForward(ctx, args)
		if err != nil {
			return nil, nil, true, err
		}
		return res, ui, true, nil
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

// executionAlgosResult is the compact model-facing payload of
// detect_execution_algos: the model gets the footprints with their evidence and
// warnings, the chart/UI payload is the artifact.
type executionAlgosResult struct {
	Symbol     string                  `json:"symbol"`
	From       time.Time               `json:"from"`
	To         time.Time               `json:"to"`
	Footprints []fingerprint.Footprint `json:"footprints"`
	Warnings   []string                `json:"warnings,omitempty"`
}

// executionAlgosArtifact is the UI payload of detect_execution_algos: the full
// footprint list, including the ones trimmed from the model payload.
type executionAlgosArtifact struct {
	Symbol     string                  `json:"symbol"`
	From       time.Time               `json:"from"`
	To         time.Time               `json:"to"`
	Footprints []fingerprint.Footprint `json:"footprints"`
}

// toolDetectExecutionAlgos implements detect_execution_algos: it reads the
// recorded LOB tape through the optional LOBDataSource and runs the fingerprint
// detectors. The attribution is probabilistic by construction; the tool never
// turns a footprint into an accusation.
func (e *Engine) toolDetectExecutionAlgos(ctx context.Context, args map[string]any) (executionAlgosResult, executionAlgosArtifact, error) {
	empty := executionAlgosResult{}
	emptyArtifact := executionAlgosArtifact{}
	if err := contextErr(ctx); err != nil {
		return empty, emptyArtifact, err
	}
	symbol := e.resolveSymbol(argString(args, "symbol"))
	if symbol == "" {
		return empty, emptyArtifact, errors.New("agent: symbol is required")
	}
	from, err := toolTimeArg(args, "from")
	if err != nil {
		return empty, emptyArtifact, err
	}
	to, err := toolTimeArg(args, "to")
	if err != nil {
		return empty, emptyArtifact, err
	}
	if !to.After(from) {
		return empty, emptyArtifact, fmt.Errorf("agent: to (%s) must be after from (%s)", to.Format(time.RFC3339), from.Format(time.RFC3339))
	}
	lob, ok := e.data.(LOBDataSource)
	if !ok {
		return empty, emptyArtifact, errors.New(
			"agent: источник ленты стакана не подключён: инструменту нужен LOBDataSource " +
				"(md.book_events/md.trades); запись рынка подключается отдельной задачей, см. docs/ai-terminal/fingerprint.md")
	}
	events, err := lob.LOBEvents(ctx, symbol, from, to)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: LOB events for %s: %w", symbol, err)
	}
	cfg := fingerprint.DefaultConfig()
	cfg.Symbol = symbol
	if windows := argStringList(args, "marginCallWindows"); windows != nil {
		cfg.MarginCallWindows = windows
	}
	footprints := fingerprint.Detect(ctx, events, cfg)
	warnings := make([]string, 0, 2)
	if len(footprints) == 0 {
		warnings = append(warnings,
			"отпечатков не найдено: лента короткая или паттерны ниже порога уверенности")
	}
	artifact := executionAlgosArtifact{Symbol: symbol, From: from, To: to, Footprints: footprints}
	if len(footprints) > maxExecutionFootprints {
		warnings = append(warnings, fmt.Sprintf(
			"в ответ модели попали первые %d отпечатков, полный список — в artifact", maxExecutionFootprints))
		footprints = footprints[:maxExecutionFootprints]
	}
	return executionAlgosResult{
		Symbol: symbol, From: from, To: to, Footprints: footprints, Warnings: warnings,
	}, artifact, nil
}

// toolTimeArg parses a tool timestamp: RFC3339 text (the documented tool
// argument form), a "YYYY-MM-DD HH:MM:SS"/date text, or Unix seconds as a JSON
// number. It returns a UTC time.
func toolTimeArg(args map[string]any, key string) (time.Time, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return time.Time{}, fmt.Errorf("agent: %s is required: pass an RFC3339 time", key)
	}
	switch value := raw.(type) {
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return time.Time{}, fmt.Errorf("agent: %s is empty: pass an RFC3339 time", key)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.ParseInLocation(layout, text, time.UTC); err == nil {
				return parsed.UTC(), nil
			}
		}
		return time.Time{}, fmt.Errorf("agent: %s %q is not an RFC3339 time", key, text)
	case float64:
		return time.Unix(int64(value), 0).UTC(), nil
	case int64:
		return time.Unix(value, 0).UTC(), nil
	case int:
		return time.Unix(int64(value), 0).UTC(), nil
	case json.Number:
		seconds, err := value.Int64()
		if err != nil {
			return time.Time{}, fmt.Errorf("agent: %s is not a Unix time: %w", key, err)
		}
		return time.Unix(seconds, 0).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("agent: %s has unsupported type %T", key, raw)
	}
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

// walkForwardTool is the walk_forward tool description.
var walkForwardTool = ToolInfo{
	Name:  toolNameWalkForward,
	Title: "Walk-forward проверка",
	Description: "Скользящая walk-forward проверка стратегии: перебор сетки параметров на обучающих окнах, " +
		"проверка лучшей комбинации на следующих окнах; возвращает OOS/IS метрики, deflated Sharpe " +
		"(поправка на число испытаний) и PBO (вероятность переобучения).",
	Input: objSchema(map[string]any{
		"symbol":      strProp("Тикер инструмента, например TQBR:SBER или SBER"),
		"periodSec":   intProp("Период свечей в секундах: 60, 300, 900, 3600 или 86400"),
		"count":       intProp(fmt.Sprintf("Сколько последних баров использовать, максимум %d", maxBacktestBars)),
		"strategy":    strProp("ID каталоговой стратегии: sma_cross, rsi_reversion, donchian_breakout, momentum или trend_*"),
		"grid":        gridProp(),
		"trainBars":   intProp("Баров в обучающем окне (обязательно)"),
		"testBars":    intProp("Баров в тестовом окне (обязательно)"),
		"stepBars":    intProp("Сдвиг окна в барах (0 = testBars)"),
		"feeBps":      numProp("Комиссия в базисных пунктах (по умолчанию 5)"),
		"slippageBps": numProp("Проскальзывание в базисных пунктах (по умолчанию 1)"),
	}, "symbol", "strategy", "trainBars", "testBars"),
}

// gridProp is the JSON Schema of the parameter grid argument.
func gridProp() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Сетка параметров: имя → список значений (например fast → [2, 3, 5], slow → [8, 13, 21])",
		"additionalProperties": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "number"},
		},
	}
}

// walkForwardView is the compact model-facing payload of walk_forward: the
// aggregate metrics and statistics only; the full per-fold result goes to the
// UI artifact.
type walkForwardView struct {
	Symbol         string           `json:"symbol"`
	Strategy       string           `json:"strategy"`
	PeriodSec      int              `json:"periodSec"`
	Trials         int              `json:"trials"`
	Folds          int              `json:"folds"`
	OOS            backtest.Metrics `json:"oos"`
	IS             backtest.Metrics `json:"is"`
	DeflatedSharpe float64          `json:"deflatedSharpe"`
	PBO            float64          `json:"pbo"`
	Warnings       []string         `json:"warnings,omitempty"`
}

// argGrid decodes the grid tool argument: parameter name -> values. Both the
// JSON shape map[string][]float64 and the heterogeneous map[string]any are
// accepted.
func argGrid(args map[string]any, key string) map[string][]float64 {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	out := make(map[string][]float64)
	switch value := raw.(type) {
	case map[string][]float64:
		for name, values := range value {
			out[name] = append([]float64(nil), values...)
		}
	case map[string]any:
		for name, valuesRaw := range value {
			values, ok := numericSlice(valuesRaw)
			if !ok {
				continue
			}
			out[name] = values
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// numericSlice converts one grid value list to float64. Values that are not
// numbers are dropped by the caller.
func numericSlice(raw any) ([]float64, bool) {
	switch value := raw.(type) {
	case []float64:
		return append([]float64(nil), value...), true
	case []any:
		out := make([]float64, 0, len(value))
		for _, item := range value {
			number, ok := numericValue(item)
			if !ok {
				return nil, false
			}
			out = append(out, number)
		}
		return out, len(out) > 0
	}
	return nil, false
}

// compareWalkForwardWindows derives the compare_strategies walk-forward windows
// from the history length: half of the bars is the train window, a quarter the
// test window and the test length is the step (two folds are typical).
func compareWalkForwardWindows(count int) (train, test, step int, ok bool) {
	if count < 12 {
		return 0, 0, 0, false
	}
	train = count / 2
	test = count / 4
	if train < 6 {
		train = 6
	}
	if test < 2 {
		test = 2
	}
	if train+test > count {
		return 0, 0, 0, false
	}
	return train, test, test, true
}

// walkForwardRunner routes walk-forward windows through the engine backtest
// seam, so a fake engine and the tests behave exactly like run_backtest.
func (e *Engine) walkForwardRunner() research.Runner {
	return func(req backtest.Request) (backtest.Result, error) {
		return e.runBacktest(req)
	}
}

// toolWalkForward implements walk_forward: a rolling grid search whose ranking
// evidence is out of sample. The model gets the aggregates (OOS/IS metrics,
// deflated Sharpe, PBO); the UI artifact carries the full per-fold result.
func (e *Engine) toolWalkForward(ctx context.Context, args map[string]any) (walkForwardView, *Artifact, error) {
	empty := walkForwardView{}
	if err := contextErr(ctx); err != nil {
		return empty, nil, err
	}
	symbol := e.resolveSymbol(argString(args, "symbol"))
	if symbol == "" {
		return empty, nil, errors.New("agent: symbol is required")
	}
	strategy := argString(args, "strategy")
	if strategy == "" {
		return empty, nil, errors.New("agent: strategy is required")
	}
	periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
	if err != nil {
		return empty, nil, err
	}
	countArg, err := argIntStrict(args, "count", maxBacktestBars, minToolCount, maxBacktestBars)
	if err != nil {
		return empty, nil, err
	}
	trainBars, err := argIntStrict(args, "trainBars", 0, 2, maxBacktestBars)
	if err != nil {
		return empty, nil, err
	}
	if trainBars == 0 {
		return empty, nil, errors.New("agent: trainBars is required")
	}
	testBars, err := argIntStrict(args, "testBars", 0, 2, maxBacktestBars)
	if err != nil {
		return empty, nil, err
	}
	if testBars == 0 {
		return empty, nil, errors.New("agent: testBars is required")
	}
	stepBars, err := argIntStrict(args, "stepBars", 0, 0, maxBacktestBars)
	if err != nil {
		return empty, nil, err
	}
	period := e.normalizePeriod(int(periodSec))
	count := clampCount(int(countArg), maxBacktestBars)
	bars, err := e.data.History(ctx, symbol, period, count)
	if err != nil {
		return empty, nil, fmt.Errorf("agent: history for %s: %w", symbol, err)
	}
	if len(bars) < 2 {
		return empty, nil, fmt.Errorf("agent: not enough history for %s (%d bars)", symbol, len(bars))
	}
	result, err := research.WalkForward(ctx, research.WalkForwardRequest{
		Bars:        bars,
		Strategy:    strategy,
		Grid:        argGrid(args, "grid"),
		TrainBars:   int(trainBars),
		TestBars:    int(testBars),
		StepBars:    int(stepBars),
		FeeBps:      argFloat(args, "feeBps", e.feeBps()),
		SlippageBps: argFloat(args, "slippageBps", 1),
		Run:         e.walkForwardRunner(),
	})
	if err != nil {
		return empty, nil, fmt.Errorf("agent: walk-forward %s on %s: %w", strategy, symbol, err)
	}
	view := walkForwardView{
		Symbol:         symbol,
		Strategy:       strategy,
		PeriodSec:      period,
		Trials:         result.Trials,
		Folds:          len(result.Folds),
		OOS:            result.OOS,
		IS:             result.IS,
		DeflatedSharpe: result.DeflatedSharpe,
		PBO:            result.PBO,
	}
	if warning := backtest.SampleWarning(result.OOS.Trades, result.OOS.From, result.OOS.To, period); warning != "" {
		view.Warnings = append(view.Warnings, warning)
	}
	if result.IS.Trades == 0 && result.OOS.Trades == 0 {
		view.Warnings = append(view.Warnings, "стратегия не дала сделок ни в одном окне")
	}
	return view, &Artifact{Kind: "walk_forward", Payload: result}, nil
}
