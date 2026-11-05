package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/backtest/rules"
	"github.com/kmlebedev/txmlconnector/terminal/fingerprint"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
	"github.com/kmlebedev/txmlconnector/terminal/research"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
	"github.com/kmlebedev/txmlconnector/terminal/volatility"
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

// toolNameVolForecast is the agent tool name of the volatility oracle forecast.
const toolNameVolForecast = "vol_forecast"

// toolNameVolEvents is the agent tool name of the release calendar / news lookup
// behind the volatility oracle.
const toolNameVolEvents = "vol_events"

// maxVolDrivers bounds the driver list of the compact model payload.
const maxVolDrivers = 6

// maxVolEvents bounds the event markers of the UI artifact.
const maxVolEvents = 50

// defaultVolPeriodSec is the default bar period of vol_forecast: 15-minute bars
// give enough complete trading days for the HAR month window within the feed
// history limit (5000 bars is about 150 trading days).
const defaultVolPeriodSec = 900

// defaultVolHorizon is the default forecast horizon of vol_forecast.
const defaultVolHorizon = 8 * time.Hour

// maxVolBars bounds the history request of vol_forecast.
const maxVolBars = 5000

// toolNameWalkForward is the agent tool name of the walk-forward grid search.
const toolNameWalkForward = "walk_forward"

// toolNameLOBBacktest is the agent tool name of the order-book replay backtest.
const toolNameLOBBacktest = "lob_backtest"

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
	return []ToolInfo{
		backtestRulesTool, batchBacktestTool, detectExecutionAlgosTool, lobBacktestTool,
		walkForwardTool, volForecastTool, volEventsTool,
	}
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
		"asOf":              strProp("Момент, на который известны данные (point-in-time), RFC3339 UTC; обязателен"),
		"from":              strProp("Начало записанной ленты, RFC3339 (UTC), например 2026-11-05T07:00:00Z"),
		"to":                strProp("Конец записанной ленты, RFC3339 (UTC)"),
		"marginCallWindows": strArrayProp("Окна маржин-коллов МСК \"HH:MM-HH:MM\"; по умолчанию 14:00-14:30 и 18:00-18:30"),
	}, "symbol", "asOf", "from", "to"),
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
	case toolNameVolForecast:
		res, ui, err := e.toolVolForecast(ctx, args)
		if err != nil {
			return nil, nil, true, err
		}
		return res, &Artifact{Kind: "volatility", Payload: ui}, true, nil
	case toolNameVolEvents:
		res, ui, err := e.toolVolEvents(ctx, args)
		if err != nil {
			return nil, nil, true, err
		}
		return res, &Artifact{Kind: "vol_events", Payload: ui}, true, nil
	case toolNameLOBBacktest:
		res, ui, err := e.toolLOBBacktest(ctx, args)
		if err != nil {
			return nil, nil, true, err
		}
		return res, &Artifact{Kind: "lob_backtest", Payload: ui}, true, nil
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
	AsOf       time.Time               `json:"asOf"`
	Footprints []fingerprint.Footprint `json:"footprints"`
	Warnings   []string                `json:"warnings,omitempty"`
	ArtifactID string                  `json:"artifactId,omitempty"`
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
	window, err := parseLOBWindow(args)
	if err != nil {
		return empty, emptyArtifact, err
	}
	lob, ok := e.data.(LOBDataSource)
	if !ok {
		return empty, emptyArtifact, errors.New(
			"agent: источник ленты стакана не подключён: инструменту нужен LOBDataSource " +
				"(md.book_events/md.trades); запись рынка подключается отдельной задачей, см. docs/ai-terminal/fingerprint.md")
	}
	events, err := lob.LOBEvents(ctx, symbol, window.From, window.To)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: LOB events for %s: %w", symbol, err)
	}
	// Point-in-time: events recorded after asOf were not known yet.
	events = eventsUpTo(events, window.AsOf)
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
	artifact := executionAlgosArtifact{Symbol: symbol, From: window.From, To: window.To, Footprints: footprints}
	artifactID := toolArtifacts.store("execution_algos", artifact)
	if len(footprints) > maxExecutionFootprints {
		warnings = append(warnings, fmt.Sprintf(
			"в ответ модели попали первые %d отпечатков, полный список — в artifact", maxExecutionFootprints))
		footprints = footprints[:maxExecutionFootprints]
	}
	return executionAlgosResult{
		Symbol: symbol, From: window.From, To: window.To, AsOf: window.AsOf,
		Footprints: footprints, Warnings: warnings, ArtifactID: artifactID,
	}, artifact, nil
}

// lobWindow is the validated point-in-time window of an order-book tool: the
// [from, to] recording range and the asOf anchor of the knowledge instant.
type lobWindow struct {
	From time.Time
	To   time.Time
	AsOf time.Time
}

// parseLOBWindow reads the window of an order-book research tool: asOf is
// required (a research request has no implicit "now"), and it must not precede
// the start of the recording range.
func parseLOBWindow(args map[string]any) (lobWindow, error) {
	asOf, err := toolAsOfArg(args)
	if err != nil {
		return lobWindow{}, err
	}
	from, err := toolTimeArg(args, "from")
	if err != nil {
		return lobWindow{}, err
	}
	to, err := toolTimeArg(args, "to")
	if err != nil {
		return lobWindow{}, err
	}
	if !to.After(from) {
		return lobWindow{}, fmt.Errorf("%w: to (%s) must be after from (%s)",
			errBadArgument, to.Format(time.RFC3339), from.Format(time.RFC3339))
	}
	if asOf.Before(from) {
		return lobWindow{}, fmt.Errorf("%w: asOf (%s) is before from (%s): a point-in-time request cannot look at a window that starts after its knowledge instant",
			errBadArgument, asOf.Format(time.RFC3339), from.Format(time.RFC3339))
	}
	return lobWindow{From: from, To: to, AsOf: asOf}, nil
}

// eventsUpTo returns the recorded events observed at or before asOf. The replay
// order is preserved, so the point-in-time tape is a prefix of the recording.
func eventsUpTo(events []lobreplay.Event, asOf time.Time) []lobreplay.Event {
	out := make([]lobreplay.Event, 0, len(events))
	for _, ev := range events {
		if ev.TS.After(asOf) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// barsUpTo returns the closed bars with the open time at or before asOf.
func barsUpTo(bars []backtest.Bar, asOf time.Time) []backtest.Bar {
	out := make([]backtest.Bar, 0, len(bars))
	for _, bar := range bars {
		if bar.Time.After(asOf) {
			continue
		}
		out = append(out, bar)
	}
	return out
}

// sliceEventSource replays an in-memory LOB tape through lobreplay.Run.
type sliceEventSource struct {
	events []lobreplay.Event
	next   int
}

// Next returns the next recorded event or io.EOF at the end of the tape.
func (s *sliceEventSource) Next() (lobreplay.Event, error) {
	if s.next >= len(s.events) {
		return lobreplay.Event{}, io.EOF
	}
	ev := s.events[s.next]
	s.next++
	return ev, nil
}

// Close releases the in-memory source (a no-op).
func (s *sliceEventSource) Close() error { return nil }

// lobImbalanceStrategy is the deterministic "imbalance" strategy of
// lob_backtest: when the book imbalance over the best Window levels exceeds
// Threshold it sends a market order of Qty units in that direction and aims to
// hold at most MaxPosition units. The Strategy interface has no fill callback, so
// the position is tracked on submitted orders; the reported metrics always come
// from the real fills of lobreplay.
type lobImbalanceStrategy struct {
	window      int
	threshold   float64
	qty         int64
	maxPosition int64
	position    int64
}

// OnEvent implements lobreplay.Strategy.
func (s *lobImbalanceStrategy) OnEvent(book *lobreplay.Book, ev lobreplay.Event) []lobreplay.Order {
	bid, ask := book.Best()
	if bid.Qty == 0 || ask.Qty == 0 {
		return nil
	}
	imbalance := book.Imbalance(s.window)
	switch {
	case imbalance >= s.threshold && s.position < s.maxPosition:
		qty := min(s.qty, s.maxPosition-s.position)
		s.position += qty
		return []lobreplay.Order{{Side: lobreplay.SideBuy, Type: lobreplay.OrderMarket, Qty: qty}}
	case imbalance <= -s.threshold && s.position > -s.maxPosition:
		qty := min(s.qty, s.position+s.maxPosition)
		s.position -= qty
		return []lobreplay.Order{{Side: lobreplay.SideSell, Type: lobreplay.OrderMarket, Qty: qty}}
	}
	return nil
}

// newLOBImbalanceStrategy builds the imbalance strategy from the tool params.
func newLOBImbalanceStrategy(params map[string]float64) (*lobImbalanceStrategy, error) {
	window := int(paramFloat(params, "window", 5))
	threshold := paramFloat(params, "threshold", 0.35)
	qty := int64(paramFloat(params, "qty", 10))
	maxPosition := int64(paramFloat(params, "maxPosition", 100))
	if window < 1 || window > 50 {
		return nil, fmt.Errorf("%w: params.window=%d must be in 1..50", errBadArgument, window)
	}
	if threshold <= 0 || threshold > 1 {
		return nil, fmt.Errorf("%w: params.threshold=%v must be in (0, 1]", errBadArgument, threshold)
	}
	if qty <= 0 || maxPosition <= 0 {
		return nil, fmt.Errorf("%w: params.qty and params.maxPosition must be positive", errBadArgument)
	}
	return &lobImbalanceStrategy{window: window, threshold: threshold, qty: qty, maxPosition: maxPosition}, nil
}

// paramFloat returns one strategy parameter or the default.
func paramFloat(params map[string]float64, key string, def float64) float64 {
	if value, ok := params[key]; ok {
		return value
	}
	return def
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
		"asOf":        strProp("Момент, на который известны данные (point-in-time), RFC3339 UTC; бары позже asOf не используются"),
		"periodSec":   intProp("Период свечей в секундах: 60, 300, 900, 3600 или 86400"),
		"count":       intProp(fmt.Sprintf("Сколько последних баров использовать, максимум %d", maxBacktestBars)),
		"strategy":    strProp("ID каталоговой стратегии: sma_cross, rsi_reversion, donchian_breakout, momentum или trend_*"),
		"grid":        gridProp(),
		"trainBars":   intProp("Баров в обучающем окне (обязательно)"),
		"testBars":    intProp("Баров в тестовом окне (обязательно)"),
		"stepBars":    intProp("Сдвиг окна в барах (0 = testBars)"),
		"feeBps":      numProp("Комиссия в базисных пунктах (по умолчанию 5)"),
		"slippageBps": numProp("Проскальзывание в базисных пунктах (по умолчанию 1)"),
	}, "symbol", "asOf", "strategy", "trainBars", "testBars"),
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
	AsOf           time.Time        `json:"asOf"`
	Trials         int              `json:"trials"`
	Folds          int              `json:"folds"`
	OOS            backtest.Metrics `json:"oos"`
	IS             backtest.Metrics `json:"is"`
	DeflatedSharpe float64          `json:"deflatedSharpe"`
	PBO            float64          `json:"pbo"`
	Warnings       []string         `json:"warnings,omitempty"`
	ArtifactID     string           `json:"artifactId,omitempty"`
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
	asOf, err := toolAsOfArg(args)
	if err != nil {
		return empty, nil, err
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
	// Point-in-time: bars closing after asOf were not known yet.
	bars = barsUpTo(bars, asOf)
	if len(bars) < 2 {
		return empty, nil, fmt.Errorf(
			"agent: not enough history for %s at asOf %s (%d bars up to asOf)",
			symbol, asOf.Format(time.RFC3339), len(bars))
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
		AsOf:           asOf,
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
	view.ArtifactID = toolArtifacts.store("walk_forward", result)
	return view, &Artifact{Kind: "walk_forward", Payload: result}, nil
}

// volForecastTool is the vol_forecast tool description.
var volForecastTool = ToolInfo{
	Name:  toolNameVolForecast,
	Title: "Оракул волатильности",
	Description: "Спрогнозировать волатильность инструмента на горизонт: HAR-RV по внутридневным барам " +
		"плюс окна релизов макростатистики и затухающая интенсивность новостей. Возвращает ожидаемое " +
		"отклонение сигма в процентах, множитель к базовой HAR-волатильности, режим (calm|normal|event|stress) " +
		"и вклады драйверов. Обучение — только по данным до asOf; качество (QLIKE) считается на последних 30 днях.",
	Input: objSchema(map[string]any{
		"symbol":    strProp("Тикер инструмента, например TQBR:SBER или SBER"),
		"periodSec": intProp("Период внутридневных баров в секундах: 900 по умолчанию, 300/900/3600"),
		"count":     intProp(fmt.Sprintf("Сколько последних баров использовать, максимум %d", maxVolBars)),
		"horizon":   strProp("Горизонт прогноза: длительность Go (8h, 90m) или число часов; по умолчанию 8h"),
		"asOf":      strProp("Момент прогноза (point-in-time), RFC3339 UTC; обязателен. Обучение видит только данные до asOf"),
	}, "symbol", "asOf"),
}

// volEventsTool is the vol_events tool description.
var volEventsTool = ToolInfo{
	Name:  toolNameVolEvents,
	Title: "Календарь релизов и новости",
	Description: "Релизы макростатистики (заседания ЦБ, FOMC, CPI, NFP и т.д.) и аннотированные новости " +
		"в окне [from, to]: категории, страна, точность времени (Exact) и тональность новостей. " +
		"Источник — винтажный слой ClickHouse (vintage.Store); без него инструмент сообщает, что источник не подключён.",
	Input: objSchema(map[string]any{
		"asOf":       strProp("Момент, на который известны данные (point-in-time), RFC3339 UTC; обязателен и не раньше from"),
		"from":       strProp("Начало окна, RFC3339 UTC"),
		"to":         strProp("Конец окна, RFC3339 UTC"),
		"categories": strArrayProp("Необязательные категории релизов: cbr_rate, ru_cpi, ru_gdp, us_cpi, us_nfp, fomc"),
	}, "asOf", "from", "to"),
}

// VolatilityDataSource is the optional extension of DataSource for the
// volatility oracle: the point-in-time releases and the annotated news of the
// vintage layer (terminal/vintage, Task 13). cmd/terminal provides the bar
// history only, so until the ClickHouse vintage store is wired into the data
// adapter the volatility tools report a clear configuration error instead of
// silently fitting on bars alone.
type VolatilityDataSource interface {
	Releases(ctx context.Context, from, to time.Time, categories ...string) ([]vintage.Release, error)
	News(ctx context.Context, from, to time.Time, sources ...string) ([]vintage.NewsItem, error)
}

// volEvent is one release marker of the volatility artifacts.
type volEvent struct {
	At       time.Time `json:"at"`
	Category string    `json:"category"`
	Title    string    `json:"title"`
	Country  string    `json:"country"`
	Exact    bool      `json:"exact"`
}

// volForecastResult is the compact model-facing payload of vol_forecast.
type volForecastResult struct {
	Symbol     string              `json:"symbol"`
	At         time.Time           `json:"at"`
	HorizonSec int64               `json:"horizonSec"`
	SigmaPct   float64             `json:"sigmaPct"`
	Multiplier float64             `json:"multiplier"`
	Regime     string              `json:"regime"`
	Drivers    []volatility.Driver `json:"drivers"`
	QLIKE      float64             `json:"qlike,omitempty"`
	QLIKEDays  int                 `json:"qlikeDays,omitempty"`
	Warnings   []string            `json:"warnings,omitempty"`
	ArtifactID string              `json:"artifactId,omitempty"`
}

// volForecastArtifact is the UI payload of vol_forecast: the full driver list,
// the release markers and the horizon geometry for the chart overlay.
type volForecastArtifact struct {
	Symbol     string              `json:"symbol"`
	At         time.Time           `json:"at"`
	HorizonSec int64               `json:"horizonSec"`
	SigmaPct   float64             `json:"sigmaPct"`
	Multiplier float64             `json:"multiplier"`
	Regime     string              `json:"regime"`
	Drivers    []volatility.Driver `json:"drivers"`
	Events     []volEvent          `json:"events,omitempty"`
	From       time.Time           `json:"from"`
	To         time.Time           `json:"to"`
}

// volEventsResult is the compact model-facing payload of vol_events.
type volEventsResult struct {
	AsOf       time.Time  `json:"asOf"`
	From       time.Time  `json:"from"`
	To         time.Time  `json:"to"`
	Events     []volEvent `json:"events"`
	News       int        `json:"news"`
	Positive   int        `json:"newsPositive"`
	Negative   int        `json:"newsNegative"`
	Warnings   []string   `json:"warnings,omitempty"`
	ArtifactID string     `json:"artifactId,omitempty"`
}

// volEventsArtifact is the UI payload of vol_events: the release markers.
type volEventsArtifact struct {
	AsOf   time.Time  `json:"asOf"`
	From   time.Time  `json:"from"`
	To     time.Time  `json:"to"`
	Events []volEvent `json:"events"`
}

// toolVolForecast implements vol_forecast: it fits the volatility oracle on the
// instrument's intraday bars plus the point-in-time releases and news of the
// vintage layer, then forecasts the requested horizon. Only data before asOf is
// used for training.
func (e *Engine) toolVolForecast(ctx context.Context, args map[string]any) (volForecastResult, volForecastArtifact, error) {
	empty := volForecastResult{}
	emptyArtifact := volForecastArtifact{}
	if err := contextErr(ctx); err != nil {
		return empty, emptyArtifact, err
	}
	symbol := e.resolveSymbol(argString(args, "symbol"))
	if symbol == "" {
		return empty, emptyArtifact, errors.New("agent: symbol is required")
	}
	periodSec, err := argIntStrict(args, "periodSec", 0, minToolPeriodSec, maxToolPeriodSec)
	if err != nil {
		return empty, emptyArtifact, err
	}
	period := defaultVolPeriodSec
	if periodSec > 0 {
		period = e.normalizePeriod(int(periodSec))
	}
	countArg, err := argIntStrict(args, "count", 5000, minToolCount, maxVolBars)
	if err != nil {
		return empty, emptyArtifact, err
	}
	count := int(countArg)
	horizon, err := toolHorizonArg(args, "horizon", defaultVolHorizon)
	if err != nil {
		return empty, emptyArtifact, err
	}
	asOf, err := toolAsOfArg(args)
	if err != nil {
		return empty, emptyArtifact, err
	}
	vds, ok := e.data.(VolatilityDataSource)
	if !ok {
		return empty, emptyArtifact, errors.New(
			"agent: винтажный источник (календарь релизов и новости) не подключён: инструменту vol_forecast нужен " +
				"VolatilityDataSource (vintage.Store из ClickHouse); подключение — отдельной задачей, см. docs/ai-terminal/research.md")
	}
	bars, err := e.data.History(ctx, symbol, period, count)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: history for %s: %w", symbol, err)
	}
	// Point-in-time: the oracle trains on the closed bars up to asOf only.
	bars = barsUpTo(bars, asOf)
	if len(bars) == 0 {
		return empty, emptyArtifact, fmt.Errorf("agent: no history for %s up to asOf %s", symbol, asOf.Format(time.RFC3339))
	}
	historyFrom := bars[0].Time.UTC()
	releases, err := vds.Releases(ctx, historyFrom, asOf)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: releases for %s: %w", symbol, err)
	}
	news, err := vds.News(ctx, asOf.AddDate(0, 0, -14), asOf)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: news: %w", err)
	}
	model, err := volatility.Fit(ctx, bars, releases, news, asOf)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf(
			"agent: oracle fit for %s (period %ds, %d bars): %w; для HAR нужна история не менее ~62 торговых дней "+
				"внутридневных баров — увеличьте periodSec (900) и count", symbol, period, len(bars), err)
	}
	model.Symbol = symbol
	upcoming, err := vds.Releases(ctx, asOf, asOf.Add(horizon+48*time.Hour))
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: upcoming releases: %w", err)
	}
	recentNews, err := vds.News(ctx, asOf.AddDate(0, 0, -2), asOf)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: recent news: %w", err)
	}
	forecast := model.Forecast(asOf, horizon, upcoming, recentNews)
	qlike, qlikeDays := model.QLIKE(bars, releases, news, asOf.AddDate(0, 0, -30), asOf)

	warnings := make([]string, 0, 2)
	if forecast.Multiplier >= 2 {
		warnings = append(warnings, fmt.Sprintf(
			"ожидается повышенная волатильность: множитель %.2f, режим %s", forecast.Multiplier, forecast.Regime))
	}
	if qlikeDays == 0 {
		warnings = append(warnings, "QLIKE не посчитан: в последних 30 днях нет полных торговых дней")
	}
	events := volEventsOf(upcoming)
	if len(events) > maxVolEvents {
		warnings = append(warnings, fmt.Sprintf(
			"маркеров в artifact: первые %d из %d релизов горизонта", maxVolEvents, len(events)))
		events = events[:maxVolEvents]
	}
	drivers := forecast.Drivers
	if len(drivers) > maxVolDrivers {
		drivers = drivers[:maxVolDrivers]
	}
	result := volForecastResult{
		Symbol:     symbol,
		At:         forecast.At,
		HorizonSec: int64(forecast.Horizon / time.Second),
		SigmaPct:   round2(forecast.SigmaPct),
		Multiplier: round2(forecast.Multiplier),
		Regime:     forecast.Regime,
		Drivers:    drivers,
		QLIKE:      round4(qlike),
		QLIKEDays:  qlikeDays,
		Warnings:   warnings,
	}
	artifact := volForecastArtifact{
		Symbol:     symbol,
		At:         forecast.At,
		HorizonSec: int64(forecast.Horizon / time.Second),
		SigmaPct:   forecast.SigmaPct,
		Multiplier: forecast.Multiplier,
		Regime:     forecast.Regime,
		Drivers:    forecast.Drivers,
		Events:     events,
		From:       historyFrom,
		To:         asOf,
	}
	result.ArtifactID = toolArtifacts.store("vol_forecast", artifact)
	return result, artifact, nil
}

// toolVolEvents implements vol_events: the release calendar and the news of the
// window [from, to] from the vintage layer.
func (e *Engine) toolVolEvents(ctx context.Context, args map[string]any) (volEventsResult, volEventsArtifact, error) {
	empty := volEventsResult{}
	emptyArtifact := volEventsArtifact{}
	if err := contextErr(ctx); err != nil {
		return empty, emptyArtifact, err
	}
	asOf, err := toolAsOfArg(args)
	if err != nil {
		return empty, emptyArtifact, err
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
		return empty, emptyArtifact, fmt.Errorf("%w: to (%s) must be after from (%s)",
			errBadArgument, to.Format(time.RFC3339), from.Format(time.RFC3339))
	}
	// The calendar is published in advance, so a point-in-time request may look
	// at a window that starts before asOf but must not start after it.
	if from.After(asOf) {
		return empty, emptyArtifact, fmt.Errorf(
			"%w: from (%s) is after asOf (%s): the request window starts after its knowledge instant",
			errBadArgument, from.Format(time.RFC3339), asOf.Format(time.RFC3339))
	}
	vds, ok := e.data.(VolatilityDataSource)
	if !ok {
		return empty, emptyArtifact, errors.New(
			"agent: винтажный источник не подключён: инструменту vol_events нужен VolatilityDataSource " +
				"(vintage.Store из ClickHouse); подключение — отдельной задачей, см. docs/ai-terminal/research.md")
	}
	categories := argStringList(args, "categories")
	releases, err := vds.Releases(ctx, from, to, categories...)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: releases: %w", err)
	}
	news, err := vds.News(ctx, from, to)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: news: %w", err)
	}
	events := volEventsOf(releases)
	warnings := make([]string, 0, 2)
	if len(events) > maxVolEvents {
		warnings = append(warnings, fmt.Sprintf("в artifact: первые %d из %d релизов", maxVolEvents, len(events)))
		events = events[:maxVolEvents]
	}
	if len(events) == 0 {
		warnings = append(warnings, "в окне нет релизов календаря")
	}
	positive, negative := 0, 0
	for _, item := range news {
		switch {
		case item.Sentiment > 0.05:
			positive++
		case item.Sentiment < -0.05:
			negative++
		}
	}
	artifact := volEventsArtifact{AsOf: asOf, From: from, To: to, Events: events}
	result := volEventsResult{
		AsOf:       asOf,
		From:       from,
		To:         to,
		Events:     events,
		News:       len(news),
		Positive:   positive,
		Negative:   negative,
		Warnings:   warnings,
		ArtifactID: toolArtifacts.store("vol_events", artifact),
	}
	return result, artifact, nil
}

// lobBacktestTool is the lob_backtest tool description.
var lobBacktestTool = ToolInfo{
	Name:  toolNameLOBBacktest,
	Title: "Бэктест по ленте стакана",
	Description: "Прогнать стратегию по записанной ленте стакана (md.book_events/md.trades): queue-aware " +
		"исполнение лимитных заявок, задержка, комиссия; возвращает метрики в формате бэктеста, число " +
		"исполнений и artifact с полной кривой эквити. Стратегия imbalance: рыночная заявка по дисбалансу стакана.",
	Input: objSchema(map[string]any{
		"symbol":     strProp("Тикер инструмента, например TQBR:SBER"),
		"asOf":       strProp("Момент, на который известны данные (point-in-time), RFC3339 UTC; обязателен"),
		"from":       strProp("Начало записанной ленты, RFC3339 UTC"),
		"to":         strProp("Конец записанной ленты, RFC3339 UTC"),
		"strategy":   strProp("Стратегия: imbalance (по дисбалансу стакана)"),
		"params":     numMapProp("Параметры: window (5), threshold (0.35), qty (10), maxPosition (100)"),
		"latencyMs":  intProp("Задержка заявки в миллисекундах (0)"),
		"queueAhead": boolProp("true — лимитная заявка встаёт в очередь уровня (консервативно); по умолчанию true"),
		"feeBps":     numProp("Комиссия в базисных пунктах (по умолчанию 5)"),
	}, "symbol", "asOf", "from", "to", "strategy"),
}

// lobBacktestResult is the compact model-facing payload of lob_backtest: the
// metrics in the backtest.Metrics format, the execution counters and the
// artifactId of the full replay result (equity curve, fills, round trips).
type lobBacktestResult struct {
	Symbol      string           `json:"symbol"`
	Strategy    string           `json:"strategy"`
	AsOf        time.Time        `json:"asOf"`
	From        time.Time        `json:"from"`
	To          time.Time        `json:"to"`
	Metrics     backtest.Metrics `json:"metrics"`
	Events      int              `json:"events"`
	Orders      int              `json:"orders"`
	Fills       int              `json:"fills"`
	RoundTrips  int              `json:"roundTrips"`
	UnfilledQty int64            `json:"unfilledQty"`
	Warnings    []string         `json:"warnings,omitempty"`
	ArtifactID  string           `json:"artifactId,omitempty"`
}

// lobBacktestArtifact is the UI payload of lob_backtest: the full replay result
// (equity curve, fills, closed round trips) behind the compact model payload.
type lobBacktestArtifact struct {
	Symbol   string           `json:"symbol"`
	Strategy string           `json:"strategy"`
	AsOf     time.Time        `json:"asOf"`
	From     time.Time        `json:"from"`
	To       time.Time        `json:"to"`
	Result   lobreplay.Result `json:"result"`
}

// toolLOBBacktest implements lob_backtest: the recorded order-book tape is
// replayed point-in-time (events after asOf are dropped) through a deterministic
// strategy with the queue-aware fill model of terminal/lobreplay.
func (e *Engine) toolLOBBacktest(ctx context.Context, args map[string]any) (lobBacktestResult, lobBacktestArtifact, error) {
	empty := lobBacktestResult{}
	emptyArtifact := lobBacktestArtifact{}
	if err := contextErr(ctx); err != nil {
		return empty, emptyArtifact, err
	}
	symbol := e.resolveSymbol(argString(args, "symbol"))
	if symbol == "" {
		return empty, emptyArtifact, errors.New("agent: symbol is required")
	}
	window, err := parseLOBWindow(args)
	if err != nil {
		return empty, emptyArtifact, err
	}
	strategyName := argString(args, "strategy")
	if strategyName == "" {
		return empty, emptyArtifact, fmt.Errorf("%w: strategy is required (supported: imbalance)", errBadArgument)
	}
	if strategyName != "imbalance" {
		return empty, emptyArtifact, fmt.Errorf(
			"%w: strategy %q is not supported (supported: imbalance)", errBadArgument, strategyName)
	}
	strategy, err := newLOBImbalanceStrategy(argFloatMap(args, "params"))
	if err != nil {
		return empty, emptyArtifact, err
	}
	latencyMs, err := argIntStrict(args, "latencyMs", 0, 0, 60_000)
	if err != nil {
		return empty, emptyArtifact, err
	}
	queueAhead := true
	if raw, ok := args["queueAhead"]; ok && raw != nil {
		queueAhead = argBool(args, "queueAhead")
	}
	lob, ok := e.data.(LOBDataSource)
	if !ok {
		return empty, emptyArtifact, errors.New(
			"agent: источник ленты стакана не подключён: инструменту нужен LOBDataSource " +
				"(md.book_events/md.trades); запись рынка подключается отдельной задачей, см. docs/ai-terminal/fingerprint.md")
	}
	events, err := lob.LOBEvents(ctx, symbol, window.From, window.To)
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: LOB events for %s: %w", symbol, err)
	}
	// Point-in-time: a replay must not see the recorded events after asOf.
	events = eventsUpTo(events, window.AsOf)
	if len(events) == 0 {
		return empty, emptyArtifact, fmt.Errorf(
			"agent: no recorded LOB events for %s in [%s, %s] up to asOf %s",
			symbol, window.From.Format(time.RFC3339), window.To.Format(time.RFC3339), window.AsOf.Format(time.RFC3339))
	}
	result, err := lobreplay.Run(ctx, &sliceEventSource{events: events}, strategy, lobreplay.FillModel{
		LatencyMs:     int(latencyMs),
		QueuePosition: queueAhead,
		FeeBps:        argFloat(args, "feeBps", e.feeBps()),
	})
	if err != nil {
		return empty, emptyArtifact, fmt.Errorf("agent: lob backtest %s on %s: %w", strategyName, symbol, err)
	}
	artifact := lobBacktestArtifact{
		Symbol: symbol, Strategy: strategyName, AsOf: window.AsOf,
		From: window.From, To: window.To, Result: result,
	}
	view := lobBacktestResult{
		Symbol:      symbol,
		Strategy:    strategyName,
		AsOf:        window.AsOf,
		From:        window.From,
		To:          window.To,
		Metrics:     result.Metrics,
		Events:      result.Events,
		Orders:      result.Orders,
		Fills:       len(result.Fills),
		RoundTrips:  len(result.RoundTrips),
		UnfilledQty: result.UnfilledQty,
		Warnings:    result.Warnings,
		ArtifactID:  toolArtifacts.store("lob_backtest", artifact),
	}
	return view, artifact, nil
}

// volEventsOf maps the vintage releases onto the artifact markers.
func volEventsOf(releases []vintage.Release) []volEvent {
	out := make([]volEvent, 0, len(releases))
	for _, release := range releases {
		out = append(out, volEvent{
			At:       release.At.UTC(),
			Category: release.Category,
			Title:    release.Title,
			Country:  release.Country,
			Exact:    release.Exact,
		})
	}
	return out
}

// toolHorizonArg parses the forecast horizon tool argument: a Go duration
// ("8h", "90m") or a number of hours.
func toolHorizonArg(args map[string]any, key string, def time.Duration) (time.Duration, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return def, nil
	}
	var horizon time.Duration
	switch value := raw.(type) {
	case string:
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("%w: %s=%q: want a Go duration like 8h or a number of hours", errBadArgument, key, value)
		}
		horizon = parsed
	case float64:
		horizon = time.Duration(value * float64(time.Hour))
	case int:
		horizon = time.Duration(value) * time.Hour
	case int64:
		horizon = time.Duration(value) * time.Hour
	case json.Number:
		hours, err := value.Float64()
		if err != nil {
			return 0, fmt.Errorf("%w: %s: %v", errBadArgument, key, err)
		}
		horizon = time.Duration(hours * float64(time.Hour))
	default:
		return 0, fmt.Errorf("%w: %s has unsupported type %T", errBadArgument, key, raw)
	}
	if horizon < time.Minute || horizon > 30*24*time.Hour {
		return 0, fmt.Errorf("%w: %s=%v is outside 1m..720h", errBadArgument, key, horizon)
	}
	return horizon, nil
}

// round2 rounds a value to two decimals for the compact payload.
func round2(value float64) float64 { return math.Round(value*100) / 100 }

// round4 rounds a metric to four decimals for the compact payload.
func round4(value float64) float64 { return math.Round(value*10000) / 10000 }
