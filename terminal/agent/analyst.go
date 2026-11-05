package agent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// analystIntent is the deterministic parse of a free-form user message.
type analystIntent struct {
	Symbol    string
	PeriodSec int
	Count     int
	Capital   float64
	Strategy  string
	Start     bool
	Stop      bool
}

// localAnalyst is the deterministic demo mode used when no LLM is configured
// (and the fallback when the LLM fails). It parses the request, runs the real
// tools (compare_strategies → get_market_hints → run_backtest of the winner,
// plus start_autonomy for "запусти") and renders a Russian answer with the
// concrete metrics and risks.
func (e *Engine) localAnalyst(ctx context.Context, message string) (ChatResponse, error) {
	started := e.now()
	intent, err := e.parseIntent(message)
	if err != nil {
		return ChatResponse{}, err
	}
	resp := ChatResponse{LLM: false, ToolCalls: []ToolCall{}, Artifacts: []Artifact{}}

	if intent.Stop {
		if !e.Status().Autonomy.Running {
			resp.Answer = "Автономия сейчас не запущена — останавливать нечего."
			resp.ElapsedMs = e.now().Sub(started).Milliseconds()
			return resp, nil
		}
		call, artifact := e.executeToolCall(ctx, "stop_autonomy", map[string]any{})
		resp.ToolCalls = append(resp.ToolCalls, call)
		if artifact != nil {
			resp.Artifacts = append(resp.Artifacts, *artifact)
		}
		if call.Error != "" {
			resp.Answer = "Не удалось остановить автоторговлю: " + call.Error
		} else if st, ok := call.Result.(AutonomyStatus); ok {
			resp.Answer = fmt.Sprintf("Автоторговля остановлена. Итог: PnL %s ₽ (%s%%), сделок %d.",
				money(st.PnL), pct(st.PnLPct), st.Trades)
		} else {
			resp.Answer = "Автоторговля остановлена."
		}
		resp.ElapsedMs = e.now().Sub(started).Milliseconds()
		return resp, nil
	}

	// 1) compare_strategies: real data from the feed, all strategies ranked.
	compareCall, compareArtifact := e.executeToolCall(ctx, "compare_strategies", map[string]any{
		"symbol":    intent.Symbol,
		"periodSec": intent.PeriodSec,
		"count":     intent.Count,
	})
	resp.ToolCalls = append(resp.ToolCalls, compareCall)
	if compareArtifact != nil {
		resp.Artifacts = append(resp.Artifacts, *compareArtifact)
	}
	if compareCall.Error != "" {
		resp.Answer = fmt.Sprintf("Не удалось получить реальные данные по %s: %s", intent.Symbol, compareCall.Error)
		resp.ElapsedMs = e.now().Sub(started).Milliseconds()
		return resp, nil
	}
	compare, _ := compareCall.Result.(compareResult)
	if intent.Strategy == "" && compare.Best.Strategy == "" {
		// Every strategy made zero trades: zero Sharpe/drawdown is meaningless,
		// so there is no "best" strategy and no autonomy to start.
		barsCount := 0
		if len(compare.Ranked) > 0 {
			barsCount = compare.Ranked[0].Metrics.Bars
		}
		label := intent.Symbol
		if name := instrumentName(e.data.Instruments(), intent.Symbol); name != "" {
			label = name + " (" + intent.Symbol + ")"
		}
		resp.Answer = fmt.Sprintf("%s, период %s, %d баров: ни одна стратегия не дала сделок — сигналов нет. "+
			"Запускать автоторговлю не рекомендую: без сделок стратегию нельзя оценить. Не является инвестиционной рекомендацией.\n\n"+
			"Рекомендация: не запускать — сигналов нет (ни одна стратегия не торговала на выбранном периоде).",
			label, periodLabel(intent.PeriodSec), barsCount)
		resp.ElapsedMs = e.now().Sub(started).Milliseconds()
		return resp, nil
	}
	chosen := intent.Strategy
	if chosen == "" {
		chosen = compare.Best.Strategy
	}
	params := map[string]float64(nil)
	if chosen == compare.Best.Strategy {
		params = compare.Best.Params
	}

	// 2) get_market_hints: the technical picture of the same instrument.
	hintsCall, hintsArtifact := e.executeToolCall(ctx, "get_market_hints", map[string]any{
		"symbol":    intent.Symbol,
		"periodSec": intent.PeriodSec,
	})
	resp.ToolCalls = append(resp.ToolCalls, hintsCall)
	if hintsArtifact != nil {
		resp.Artifacts = append(resp.Artifacts, *hintsArtifact)
	}
	hints, hintsOK := hintsCall.Result.(MarketHints)
	if !hintsOK {
		hints = MarketHints{Symbol: intent.Symbol, PeriodSec: intent.PeriodSec}
	}

	// 3) run_backtest of the winner (or of the explicitly requested strategy).
	btArgs := map[string]any{
		"symbol":    intent.Symbol,
		"periodSec": intent.PeriodSec,
		"strategy":  chosen,
		"count":     intent.Count,
	}
	if intent.Capital > 0 {
		btArgs["initialCash"] = intent.Capital
	}
	if params != nil {
		btArgs["params"] = params
	}
	btCall, btArtifact := e.executeToolCall(ctx, "run_backtest", btArgs)
	resp.ToolCalls = append(resp.ToolCalls, btCall)
	if btArtifact != nil {
		resp.Artifacts = append(resp.Artifacts, *btArtifact)
	}
	if btCall.Error != "" {
		resp.Answer = fmt.Sprintf("Не удалось прогнать бэктест %s по %s: %s", chosen, intent.Symbol, btCall.Error)
		resp.ElapsedMs = e.now().Sub(started).Milliseconds()
		return resp, nil
	}
	result, ok := btCall.Result.(backtest.Result)
	if !ok {
		resp.Answer = "Бэктест не вернул результат."
		resp.ElapsedMs = e.now().Sub(started).Milliseconds()
		return resp, nil
	}

	// 4) "запусти/стартуй/торгуй" — start the paper autonomy on the tested
	// strategy, but never on a strategy that produced no trades.
	var autonomy *AutonomyStatus
	var autonomyErr string
	autoStarted := false
	if intent.Start && result.Metrics.Trades > 0 {
		autoArgs := map[string]any{
			"symbol":    intent.Symbol,
			"periodSec": intent.PeriodSec,
			"strategy":  chosen,
		}
		if params != nil {
			autoArgs["params"] = params
		}
		if intent.Capital > 0 {
			autoArgs["budget"] = intent.Capital
		}
		autoCall, autoArtifact := e.executeToolCall(ctx, "start_autonomy", autoArgs)
		resp.ToolCalls = append(resp.ToolCalls, autoCall)
		if autoArtifact != nil {
			resp.Artifacts = append(resp.Artifacts, *autoArtifact)
		}
		if autoCall.Error != "" {
			autonomyErr = autoCall.Error
		} else if st, ok := autoCall.Result.(AutonomyStatus); ok {
			autonomy = &st
			autoStarted = true
		}
	}

	launched := intent.Start && (autoStarted || e.Status().Autonomy.Running)
	resp.Answer = e.formatAnalystAnswer(intent, chosen, result, hints, hintsOK, autonomy, autonomyErr, autoStarted, launched)
	resp.ElapsedMs = e.now().Sub(started).Milliseconds()
	return resp, nil
}

// parseIntent extracts the symbol/period/count/capital/strategy/action from a
// Russian or English message.
func (e *Engine) parseIntent(message string) (analystIntent, error) {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" {
		return analystIntent{}, fmt.Errorf("agent: empty message")
	}
	symbol := e.defaultSymbol()
	if symbol == "" {
		return analystIntent{}, fmt.Errorf("agent: no instruments available for analysis")
	}
	intent := analystIntent{
		Symbol:    e.matchInstrument(text, symbol),
		PeriodSec: e.cfg.DefaultPeriodSec,
		Count:     maxHistoryBars,
	}
	if period, ok := parsePeriodSec(text); ok {
		intent.PeriodSec = period
	}
	intent.Count = parseCountRange(text, intent.PeriodSec)
	if capital, ok := parseCapital(text); ok {
		intent.Capital = capital
	}
	intent.Strategy = parseStrategy(text)

	// Autonomy is started/stopped only on an explicit imperative. Negations,
	// modal/permission constructions, hypotheticals and any question (even
	// without a "?") must never start or stop a real paper runner.
	stopIntent := parseStopIntent(text)
	startIntent := hasAny(text,
		"запусти", "запускай", "стартуй", "стартани", "стартуем",
		"включи автоторг", "включай автоторг", "включи торговлю",
		"торгуй", "начни торговать", "начинай торговать",
		"start trading", "start autonomy", "start auto", "run autonomy",
	)
	if hasNoStartMarker(text) {
		startIntent = false
	}
	intent.Stop = stopIntent
	intent.Start = !stopIntent && startIntent
	return intent, nil
}

// hasNoStartMarker reports a negation, modal/permission construction,
// hypothetical clause or question form that vetoes starting the paper runner.
func hasNoStartMarker(text string) bool {
	if strings.Contains(text, "?") {
		return true
	}
	return hasAny(text,
		// Negations / prohibitions.
		"не запускай", "не запускать", "не запусти", "не включай", "не включать",
		"не стартуй", "не стартани", "не начинай", "не начни", "не торгуй",
		"нельзя", "запрещено", "запрещен", "не надо", "не стоит",
		// Modal / permission / obligation.
		"можно", "разрешено", "стоит ли", "запускать ли", "ли запускать",
		"надо ли", "нужно ли", "должен ли", "должна ли", "следует ли",
		"хотел бы", "хотелось бы", "планирую", "собираюсь",
		// Hypothetical / conditional.
		"что будет", "что если", "если запустить", "если запустим",
		"если включить", "если старт", "в случае если", "при условии",
		"представь", "допустим", "как будто",
		// Interrogatives without "?".
		"как запустить", "как включить", "как стартовать", "почему", "зачем", "сколько",
		// English modal / hypothetical.
		"should i", "should we", "can i", "could i", "would i", "may i",
		"is it possible", "what if", "if i start", "allowed to",
	)
}

// parseStopIntent accepts only an explicit stop imperative ("останови",
// "стоп", "stop", "выключи автоторговлю"); questions and modality are not
// actions.
func parseStopIntent(text string) bool {
	stopIntent := hasAny(text, "останови", "остановите", "выключи", "выключай", "halt", "stop_autonomy")
	if hasAny(text, "стоп") && !hasAny(text, "стоп-лосс", "стоп лосс", "стоплосс") {
		stopIntent = true
	}
	if hasAny(text, "stop") && !hasAny(text, "stop loss", "stop-loss") {
		stopIntent = true
	}
	if strings.Contains(text, "?") || hasAny(text,
		"не останавливай", "не останавливать", "не надо останавливать", "не стоит останавливать",
		"не выключай", "не отключай", "нельзя остановить",
		"можно остановить", "можно ли остановить", "стоит ли остановить",
		"надо ли остановить", "нужно ли остановить", "должен ли остановить",
		"что будет если остановить", "если остановить", "как остановить",
		"should i stop", "should we stop", "can i stop",
	) {
		stopIntent = false
	}
	return stopIntent
}

// matchInstrument finds the instrument mentioned in the message (ticker,
// "BOARD:SECCODE", full or partial Russian/English name) and falls back to the
// configured default.
func (e *Engine) matchInstrument(text, fallback string) string {
	bestScore := 0
	best := ""
	words := messageWords(text)
	for _, ins := range e.data.Instruments() {
		code := strings.ToLower(secCode(ins.Symbol))
		if code != "" && wordIn(text, code) {
			if score := 100 + len(code); score > bestScore {
				bestScore, best = score, ins.Symbol
			}
		}
		name := strings.ToLower(ins.Name)
		if name == "" {
			continue
		}
		if strings.Contains(text, name) {
			if score := 80 + len(name); score > bestScore {
				bestScore, best = score, ins.Symbol
			}
			continue
		}
		for _, word := range words {
			if len([]rune(word)) < 3 {
				continue
			}
			if strings.Contains(name, word) {
				if score := 20 + len(word); score > bestScore {
					bestScore, best = score, ins.Symbol
				}
				break
			}
		}
	}
	if best != "" {
		return best
	}
	return fallback
}

// messageWords splits a lowercase message into word-ish tokens (Cyrillic and
// Latin letters only).
func messageWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return false
		case r >= 'а' && r <= 'я', r == 'ё':
			return false
		default:
			return true
		}
	})
}

// parseStrategy maps strategy keywords to the frozen backtest strategy IDs.
func parseStrategy(text string) string {
	switch {
	case hasAny(text, "sma", "скользящ", "пересечени", "перекрест", "крест", "cross", "средн", "moving"):
		return "sma_cross"
	case hasAny(text, "rsi", "перепродан", "перекуплен", "реверси", "reversion", "возврат к средн"):
		return "rsi_reversion"
	case hasAny(text, "пробой", "донч", "donchian", "breakout", "канал"):
		return "donchian_breakout"
	case hasAny(text, "моментум", "momentum", "импульс"):
		return "momentum"
	}
	return ""
}

var numberRe = regexp.MustCompile(`\d[\d \x{00a0}]*(?:[.,]\d+)?`)

// parseCapital extracts a capital/budget amount from a message: either after a
// keyword ("капитал 100000", "бюджет 1 млн") or as a number with a currency or
// a multiplier suffix ("100000 ₽", "100k", "2 млн").
func parseCapital(text string) (float64, bool) {
	keywords := []string{"капитал", "депозит", "бюджет", "capital", "deposit", "budget", "на сумму", "сумма"}
	for _, keyword := range keywords {
		idx := strings.Index(text, keyword)
		if idx < 0 {
			continue
		}
		rest := text[idx+len(keyword):]
		if value, ok := numberWithSuffix(rest); ok && value >= 1000 {
			return value, true
		}
		// Plain number after the keyword: "капитал 100000".
		if loc := numberRe.FindStringIndex(rest); loc != nil {
			if value, ok := parseDecimal(rest[loc[0]:loc[1]]); ok && value >= 1000 {
				return value, true
			}
		}
	}
	return numberWithSuffix(text)
}

// numberWithSuffix scans for the first number followed by a currency sign or a
// multiplier suffix and returns the scaled value.
func numberWithSuffix(text string) (float64, bool) {
	for _, loc := range numberRe.FindAllStringIndex(text, -1) {
		value, ok := parseDecimal(text[loc[0]:loc[1]])
		if !ok || value <= 0 {
			continue
		}
		rest := strings.TrimLeft(text[loc[1]:], " \t\u00a0")
		rest = rest[:min(len(rest), 16)]
		multiplier := 1.0
		switch {
		case strings.HasPrefix(rest, "млн"), strings.HasPrefix(rest, "million"):
			multiplier = 1_000_000
		case strings.HasPrefix(rest, "тыс"), strings.HasPrefix(rest, "тысяч"):
			multiplier = 1_000
		case hasCurrencyPrefix(rest):
			multiplier = 1
		case strings.HasPrefix(rest, "к"), strings.HasPrefix(rest, "k"):
			multiplier = 1_000
		default:
			continue
		}
		scaled := value * multiplier
		if scaled < 1000 || scaled > 1e12 {
			continue
		}
		return scaled, true
	}
	return 0, false
}

func hasCurrencyPrefix(rest string) bool {
	for _, prefix := range []string{"₽", "руб", "rub", "rur", "р."} {
		if strings.HasPrefix(rest, prefix) {
			return true
		}
	}
	return false
}

// parseDecimal parses a Russian-formatted number: spaces as thousands
// separators and a comma as the decimal separator.
func parseDecimal(raw string) (float64, bool) {
	s := strings.ReplaceAll(raw, "\u00a0", "")
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, false
	}
	if strings.Contains(s, ",") {
		if strings.Contains(s, ".") {
			s = strings.ReplaceAll(s, ",", "")
		} else {
			s = strings.ReplaceAll(s, ",", ".")
		}
	}
	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// formatAnalystAnswer renders the Russian local-analyst answer with concrete
// metrics, the technical picture and the risks.
func (e *Engine) formatAnalystAnswer(intent analystIntent, strategy string, result backtest.Result,
	hints MarketHints, hintsOK bool, autonomy *AutonomyStatus, autonomyErr string, autoStarted, launched bool) string {
	var b strings.Builder
	m := result.Metrics

	name := instrumentName(e.data.Instruments(), intent.Symbol)
	if name != "" {
		fmt.Fprintf(&b, "%s (%s)", name, intent.Symbol)
	} else {
		b.WriteString(intent.Symbol)
	}
	fmt.Fprintf(&b, ", период %s, %s баров.\n", periodLabel(intent.PeriodSec), fmtInt(m.Bars))
	if intent.Strategy != "" {
		fmt.Fprintf(&b, "Стратегия %s (по запросу) — доходность %s%%, бенчмарк %s%%, макс. просадка %s%%, сделок %d",
			strategy, pct(m.TotalReturnPct), pct(m.BenchmarkReturnPct), pct(m.MaxDrawdownPct), m.Trades)
	} else {
		fmt.Fprintf(&b, "Лучшая стратегия: %s — доходность %s%%, бенчмарк %s%%, макс. просадка %s%%, сделок %d",
			strategy, pct(m.TotalReturnPct), pct(m.BenchmarkReturnPct), pct(m.MaxDrawdownPct), m.Trades)
	}
	if m.Trades > 0 {
		fmt.Fprintf(&b, ", win rate %s%%, Sharpe %s", pct(m.WinRatePct), pct(m.Sharpe))
	}
	b.WriteString(".\n")
	if hintsOK {
		fmt.Fprintf(&b, "Техническая картина: тренд %s, RSI14 %s, ATR14 %s, волатильность %s%% годовых, поддержка %s, сопротивление %s",
			trendRu(hints.Trend), pct(hints.RSI14), pct(hints.ATR14), pct(hints.VolatilityPct),
			pct(hints.Support), pct(hints.Resistance))
		if hints.Breadth != nil {
			fmt.Fprintf(&b, ", breadth %d↑/%d↓/%d→",
				hints.Breadth["advancers"], hints.Breadth["decliners"], hints.Breadth["unchanged"])
		}
		b.WriteString(".\n")
	}
	fmt.Fprintf(&b, "Риски: просадка до %s%%, комиссия %s б.п., данные реальные, но не гарантируют полноту; "+
		"прошлая доходность не гарантирует будущую. Не является инвестиционной рекомендацией.\n",
		pct(m.MaxDrawdownPct), pct(e.feeBps()))
	if m.Trades <= 0 {
		b.WriteString("Стратегия не совершила сделок на этих данных — её результат не показателен.\n")
	}

	switch {
	case autoStarted && autonomy != nil:
		fmt.Fprintf(&b, "\nАвтономия запущена на бумажном счёте: %s, стратегия %s, бюджет %s ₽, скорость %.0f бар/с.",
			intent.Symbol, strategy, money(autonomy.Config.Budget), autonomy.Config.Speed)
		b.WriteString(" Остановить: запрос «останови автоторговлю» или POST /api/agent/autonomy {\"action\":\"stop\"}.")
	case intent.Start && m.Trades <= 0:
		b.WriteString("\nАвтономию не запускаю: на этих данных стратегия не дала сделок — запуск не рекомендую.")
	case strings.Contains(autonomyErr, "already running"):
		b.WriteString("\nАвтономия уже запущена — повторный запуск не требуется.")
	case autonomyErr != "":
		fmt.Fprintf(&b, "\nАвтономию запустить не удалось: %s", autonomyErr)
	case intent.Start:
		b.WriteString("\nАвтономию запустить не удалось.")
	default:
		b.WriteString("\nЧтобы запустить автоторговлю на бумажном счёте, напишите «запусти».")
	}
	b.WriteString("\n\n")
	if launched {
		// The user's explicit command was actually honoured: the closing line
		// must agree with the action instead of telling them not to launch.
		b.WriteString(startedRecommendationLine(m))
	} else {
		b.WriteString(recommendationLine(m))
	}
	return b.String()
}

// startedRecommendationLine closes the answer when the paper autonomy was
// actually started by the user's explicit command.
func startedRecommendationLine(m backtest.Metrics) string {
	const tail = "Счёт бумажный, прошлая доходность не гарантирует будущую; это не инвестиционная рекомендация."
	switch {
	case m.Trades <= 0:
		return "Запущено по вашей команде на бумажном счёте. Учитывайте: на истории стратегия не дала сделок — " +
			"следите за риск-лимитами и при необходимости остановите: «останови автоторговлю». " + tail
	case m.TotalReturnPct <= 0:
		return fmt.Sprintf("Запущено по вашей команде на бумажном счёте. Учитывайте: на истории стратегия убыточна "+
			"(доходность %s%%, buy&hold %s%%) — следите за риск-лимитами и при необходимости остановите: «останови автоторговлю». ",
			pct(m.TotalReturnPct), pct(m.BenchmarkReturnPct)) + tail
	case m.TotalReturnPct <= m.BenchmarkReturnPct:
		return fmt.Sprintf("Запущено по вашей команде на бумажном счёте. Учитывайте: на истории стратегия не обгоняет buy&hold "+
			"(доходность %s%% против %s%%) — следите за риск-лимитами и при необходимости остановите: «останови автоторговлю». ",
			pct(m.TotalReturnPct), pct(m.BenchmarkReturnPct)) + tail
	default:
		return fmt.Sprintf("Запущено по вашей команде на бумажном счёте. На истории стратегия обгоняет buy&hold "+
			"(доходность %s%% против %s%%, макс. просадка %s%%) — следите за риск-лимитами и при необходимости остановите: «останови автоторговлю». ",
			pct(m.TotalReturnPct), pct(m.BenchmarkReturnPct), pct(m.MaxDrawdownPct)) + tail
	}
}

// recommendationLine renders the explicit "стоит ли запускать" conclusion that
// closes every local-analyst answer. It only describes the numbers of the
// chosen backtest; the autonomy guards live in localAnalyst.
func recommendationLine(m backtest.Metrics) string {
	switch {
	case m.Trades <= 0:
		return "Рекомендация: не запускать — на выбранном периоде стратегия не дала сделок."
	case m.TotalReturnPct <= 0:
		return fmt.Sprintf(
			"Рекомендация: не запускать — на выбранном периоде стратегия убыточна (доходность %s%%, бенчмарк buy&hold %s%%).",
			pct(m.TotalReturnPct), pct(m.BenchmarkReturnPct))
	case m.TotalReturnPct <= m.BenchmarkReturnPct:
		return fmt.Sprintf(
			"Рекомендация: не запускать — стратегия не обгоняет buy&hold (доходность %s%% против %s%% у buy&hold).",
			pct(m.TotalReturnPct), pct(m.BenchmarkReturnPct))
	default:
		return fmt.Sprintf(
			"Рекомендация: можно рассмотреть на бумажном счёте с риск-лимитами (доходность %s%% против %s%% у buy&hold, макс. просадка %s%%). "+
				"Счёт бумажный, прошлая доходность не гарантирует будущую; это не инвестиционная рекомендация.",
			pct(m.TotalReturnPct), pct(m.BenchmarkReturnPct), pct(m.MaxDrawdownPct))
	}
}

// instrumentName returns the human-readable name of a symbol or "".
func instrumentName(instruments []InstrumentRef, symbol string) string {
	for _, ins := range instruments {
		if strings.EqualFold(ins.Symbol, symbol) {
			return ins.Name
		}
	}
	return ""
}

// trendRu renders the frozen trend enum for the Russian answer.
func trendRu(trend string) string {
	switch trend {
	case "up":
		return "вверх"
	case "down":
		return "вниз"
	default:
		return "боковой"
	}
}

// pct formats a percentage with a Russian decimal comma.
func pct(v float64) string {
	return strings.ReplaceAll(fmt.Sprintf("%.2f", v), ".", ",")
}

// money formats a ruble amount with thousands separators and a decimal comma.
func money(v float64) string {
	raw := fmt.Sprintf("%.2f", v)
	neg := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(raw, "-")
	intPart := raw
	frac := ""
	if i := strings.Index(raw, "."); i >= 0 {
		intPart, frac = raw[:i], raw[i+1:]
	}
	var grouped strings.Builder
	for i, digit := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			grouped.WriteByte(' ')
		}
		grouped.WriteRune(digit)
	}
	out := grouped.String()
	if frac != "" {
		out += "," + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// fmtInt formats an integer count with thousands separators.
func fmtInt(v int) string {
	intPart := strconv.Itoa(v)
	neg := strings.HasPrefix(intPart, "-")
	intPart = strings.TrimPrefix(intPart, "-")
	var grouped strings.Builder
	for i, digit := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			grouped.WriteByte(' ')
		}
		grouped.WriteRune(digit)
	}
	out := grouped.String()
	if neg {
		out = "-" + out
	}
	return out
}
