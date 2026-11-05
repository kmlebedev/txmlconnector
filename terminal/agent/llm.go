package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// systemPrompt is the frozen Russian role prompt of the LLM analyst (§9.2).
const systemPrompt = `Ты — торговый аналитик AI-терминала MOEX/Finam.
Отвечай по-русски, кратко (не более 200 слов).
Используй ТОЛЬКО реальные данные, полученные через инструменты (tools): котировки, историю, бэктест, подсказки, портфель.
Не выдумывай цифры, цены, доходность и сделки. Если данных не хватает — вызови подходящий инструмент.
Обязательно предупреждай о рисках: прошлая доходность не гарантирует будущую, возможны просадки и комиссии, торговля в терминале только бумажная.
Если пользователь просит запустить автоторговлю: сначала выполни run_backtest (или compare_strategies), затем вызови start_autonomy с разумным бюджетом; live-режим запрещён.
Проценты и деньги оформляй по-русски (запятая, знак ₽). Никогда не обещай прибыль.`

// llmMessage is one OpenAI-compatible chat message.
type llmMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"`
	ToolCalls  []llmToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// llmToolCall is one tool call returned by the model.
type llmToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function llmFunctionCall `json:"function"`
}

// llmFunctionCall is the name plus JSON-stringified arguments of one call.
type llmFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// llmTool is one OpenAI function tool definition.
type llmTool struct {
	Type     string          `json:"type"`
	Function llmToolFunction `json:"function"`
}

// llmToolFunction is the OpenAI function payload.
type llmToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// llmChatRequest is the request body of POST {BaseURL}/chat/completions.
type llmChatRequest struct {
	Model       string       `json:"model"`
	Messages    []llmMessage `json:"messages"`
	Tools       []llmTool    `json:"tools,omitempty"`
	ToolChoice  string       `json:"tool_choice,omitempty"`
	Temperature float64      `json:"temperature"`
	Stream      bool         `json:"stream"`
}

// llmChatResponse is the (subset of the) response body we parse.
type llmChatResponse struct {
	Choices []struct {
		Message      llmMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// llmError is a classified provider failure. Its text never contains the API
// key or the raw provider body.
type llmError struct {
	Class string
	Err   error
}

func (e *llmError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("llm: %s: %v", e.Class, e.Err)
	}
	return "llm: " + e.Class
}

func (e *llmError) Unwrap() error { return e.Err }

// llmErrorClass maps an LLM failure to a stable, secret-free class name.
func llmErrorClass(err error) string {
	var le *llmError
	if errors.As(err, &le) {
		return le.Class
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "network"
}

// hasMutatingToolCall reports whether any executed tool call has side effects.
func hasMutatingToolCall(calls []ToolCall) bool {
	for _, call := range calls {
		if isMutatingTool(call.Name) {
			return true
		}
	}
	return false
}

// Chat answers a user message. With a configured LLM it runs the tool-calling
// loop; otherwise (and on a read-only LLM failure) the deterministic local
// analyst answers with LLM:false. A failure after a mutating tool call returns a
// partial answer instead of replaying the side effect (R3).
func (e *Engine) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return ChatResponse{}, errors.New("agent: empty message")
	}
	if err := contextErr(ctx); err != nil {
		return ChatResponse{}, err
	}

	if !e.cfg.LLM.Enabled {
		resp, err := e.localAnalyst(ctx, message)
		if err != nil {
			return ChatResponse{}, err
		}
		resp.LLM = false
		return resp, nil
	}

	resp, err := e.chatLLM(ctx, message, req.ConversationID)
	if err == nil {
		return resp, nil
	}
	if ctxErr := contextErr(ctx); ctxErr != nil {
		return ChatResponse{}, ctxErr
	}
	// Never replay side effects through the local fallback: if the LLM failed
	// after an order or autonomy action already ran, return what happened as a
	// partial answer instead of executing the action a second time (R3).
	if hasMutatingToolCall(resp.ToolCalls) {
		resp.Partial = true
		resp.LLM = true
		resp.Answer = fmt.Sprintf(
			"LLM недоступен после выполнения действий (%s). Выполненные действия не повторяются — проверьте их результат в терминале.",
			llmErrorClass(err))
		return resp, nil
	}
	local, localErr := e.localAnalyst(ctx, message)
	if localErr != nil {
		return ChatResponse{}, fmt.Errorf("agent: LLM failed (%s) and local analyst failed: %w",
			llmErrorClass(err), localErr)
	}
	local.LLM = false
	local.Answer = fmt.Sprintf("LLM недоступен (%s). Отвечает локальный аналитик.\n\n%s",
		llmErrorClass(err), local.Answer)
	return local, nil
}

// chatLLM runs the OpenAI-compatible tool loop: up to maxToolIterations model
// round trips with tools, then one final answer without tools. On failure it
// returns the calls executed so far together with the error, so Chat can decide
// whether a local fallback is safe (R3).
func (e *Engine) chatLLM(ctx context.Context, message, conversationID string) (ChatResponse, error) {
	started := e.now()
	messages := []llmMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: message},
	}
	tools := e.llmTools()
	resp := ChatResponse{LLM: true, ToolCalls: []ToolCall{}, Artifacts: []Artifact{}}
	answer := ""
	// fail returns the partial response (executed calls, artifacts) with the
	// error that interrupted the loop.
	fail := func(err error) (ChatResponse, error) {
		resp.ElapsedMs = e.now().Sub(started).Milliseconds()
		return resp, err
	}

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		if err := contextErr(ctx); err != nil {
			return fail(err)
		}
		msg, err := e.llmComplete(ctx, messages, tools)
		if err != nil {
			return fail(err)
		}
		if len(msg.ToolCalls) == 0 {
			answer = strings.TrimSpace(contentString(msg.Content))
			if answer != "" {
				break
			}
			return fail(&llmError{Class: "empty_response"})
		}
		messages = append(messages, llmMessage{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: msg.ToolCalls,
		})
		for _, tc := range msg.ToolCalls {
			name := tc.Function.Name
			args, parseErr := parseToolArguments(tc.Function.Arguments)
			var (
				call     ToolCall
				artifact *Artifact
			)
			if parseErr != nil {
				call = ToolCall{
					Name:       name,
					Args:       map[string]any{},
					Error:      "invalid tool arguments JSON: " + parseErr.Error(),
					DurationMs: 0,
				}
				e.publish(Event{Type: "tool_result", Data: call})
			} else {
				call, artifact = e.executeToolCallFor(ctx, toolInvocation{
					ConversationID: conversationID,
					ToolCallID:     tc.ID,
				}, name, args)
			}
			resp.ToolCalls = append(resp.ToolCalls, call)
			if artifact != nil {
				resp.Artifacts = append(resp.Artifacts, *artifact)
			}
			messages = append(messages, llmMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    toolMessageContent(call),
			})
		}
	}

	if answer == "" {
		msg, err := e.llmComplete(ctx, messages, nil)
		if err != nil {
			return fail(err)
		}
		answer = strings.TrimSpace(contentString(msg.Content))
	}
	if answer == "" {
		answer = e.summarizeArtifacts(resp.Artifacts)
	}
	resp.Answer = answer
	resp.ElapsedMs = e.now().Sub(started).Milliseconds()
	e.publish(Event{Type: "message", Data: map[string]any{"answer": answer}})
	return resp, nil
}

// llmComplete performs one POST {BaseURL}/chat/completions request.
func (e *Engine) llmComplete(ctx context.Context, messages []llmMessage, tools []llmTool) (llmMessage, error) {
	body := llmChatRequest{
		Model:       e.cfg.LLM.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: 0.2,
		Stream:      false,
	}
	if len(tools) > 0 {
		body.ToolChoice = "auto"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return llmMessage{}, &llmError{Class: "invalid_request", Err: err}
	}

	timeout := e.cfg.LLM.Timeout
	if timeout <= 0 {
		timeout = defaultLLMTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, e.cfg.LLM.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return llmMessage{}, &llmError{Class: "invalid_request", Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.cfg.LLM.APIKey)

	client := e.llmHTTPClient
	if client == nil {
		client = &http.Client{}
	}
	httpResp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || reqCtx.Err() != nil {
			return llmMessage{}, &llmError{Class: "timeout"}
		}
		return llmMessage{}, &llmError{Class: "network"}
	}
	defer httpResp.Body.Close()

	data, readErr := io.ReadAll(io.LimitReader(httpResp.Body, 8<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return llmMessage{}, &llmError{Class: fmt.Sprintf("http_%d", httpResp.StatusCode)}
	}
	if readErr != nil {
		return llmMessage{}, &llmError{Class: "network"}
	}
	var parsed llmChatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return llmMessage{}, &llmError{Class: "invalid_response", Err: err}
	}
	if parsed.Error != nil {
		// The provider message may echo request details; keep only the class.
		return llmMessage{}, &llmError{Class: "provider_error"}
	}
	if len(parsed.Choices) == 0 {
		return llmMessage{}, &llmError{Class: "invalid_response", Err: errors.New("no choices")}
	}
	msg := parsed.Choices[0].Message
	msg.Role = "assistant"
	return msg, nil
}

// llmTools converts the frozen ToolInfo catalog to OpenAI tool definitions.
func (e *Engine) llmTools() []llmTool {
	catalog := e.Tools()
	out := make([]llmTool, 0, len(catalog))
	for _, tool := range catalog {
		out = append(out, llmTool{
			Type: "function",
			Function: llmToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Input,
			},
		})
	}
	return out
}

// parseToolArguments parses the JSON-stringified function arguments.
func parseToolArguments(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return map[string]any{}, nil
	}
	var args map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

// contentString normalizes a message content value to a string.
func contentString(content any) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

// toolMessageContent renders one tool result for the model. Large payloads
// (equity curves, bar arrays) are compacted so the context stays small.
func toolMessageContent(call ToolCall) string {
	if call.Error != "" {
		raw, _ := json.Marshal(map[string]any{"error": call.Error})
		return string(raw)
	}
	raw, err := json.Marshal(toolResultForLLM(call.Name, call.Result))
	if err != nil {
		return `{"ok":true}`
	}
	return string(raw)
}

// toolResultForLLM compacts tool results sent back to the model.
func toolResultForLLM(name string, result any) any {
	switch name {
	case "run_backtest", toolNameBacktestRules:
		if res, ok := result.(backtest.Result); ok {
			return compactBacktest(res)
		}
	case "compare_strategies":
		if cmp, ok := result.(compareResult); ok && len(cmp.Ranked) > 0 {
			ranked := make([]map[string]any, 0, len(cmp.Ranked))
			for _, row := range cmp.Ranked {
				ranked = append(ranked, map[string]any{
					"strategy":       row.Strategy,
					"totalReturnPct": row.Metrics.TotalReturnPct,
					"maxDrawdownPct": row.Metrics.MaxDrawdownPct,
					"trades":         row.Metrics.Trades,
					"winRatePct":     row.Metrics.WinRatePct,
					"sharpe":         row.Metrics.Sharpe,
				})
			}
			return map[string]any{"ranked": ranked, "best": compactBacktest(cmp.Best.Result)}
		}
	case "get_backtest_history":
		if results, ok := result.([]backtest.Result); ok {
			out := make([]map[string]any, 0, len(results))
			for _, res := range results {
				out = append(out, compactBacktest(res))
			}
			return out
		}
	case "get_history":
		if hist, ok := result.(historyResult); ok {
			summary := map[string]any{
				"symbol":    hist.Symbol,
				"periodSec": hist.PeriodSec,
				"source":    hist.Source,
				"bars":      len(hist.Bars),
			}
			if len(hist.Bars) > 0 {
				summary["firstBar"] = hist.Bars[0]
				summary["lastBar"] = hist.Bars[len(hist.Bars)-1]
			}
			return summary
		}
	}
	return result
}

// compactBacktest keeps the metrics and trades of a result, dropping the
// equity curve and bar-level data.
func compactBacktest(res backtest.Result) map[string]any {
	out := map[string]any{
		"symbol":    res.Symbol,
		"strategy":  res.Strategy,
		"periodSec": res.PeriodSec,
		"params":    res.Params,
		"metrics":   res.Metrics,
	}
	if len(res.Warnings) > 0 {
		out["warnings"] = res.Warnings
	}
	if len(res.Trades) > 0 {
		trades := res.Trades
		if len(trades) > 10 {
			trades = trades[len(trades)-10:]
		}
		out["trades"] = trades
	}
	return out
}

// summarizeArtifacts builds a deterministic Russian summary from the collected
// artifacts when the model returns no final text.
func (e *Engine) summarizeArtifacts(artifacts []Artifact) string {
	for i := len(artifacts) - 1; i >= 0; i-- {
		switch payload := artifacts[i].Payload.(type) {
		case backtest.Result:
			m := payload.Metrics
			return fmt.Sprintf("Бэктест %s на %s: доходность %s%%, макс. просадка %s%%, сделок %d. "+
				"Это бумажная аналитика на реальных данных терминала, не инвестиционная рекомендация.",
				payload.Strategy, payload.Symbol, pct(m.TotalReturnPct), pct(m.MaxDrawdownPct), m.Trades)
		case AutonomyStatus:
			return fmt.Sprintf("Автономия на бумажном счёте: PnL %s ₽, сделок %d. "+
				"Остановить можно кнопкой «Стоп» или запросом action=stop.", money(payload.PnL), payload.Trades)
		case MarketHints:
			return fmt.Sprintf("Технические подсказки %s: тренд %s, RSI14 %s, волатильность %s%% годовых. "+
				"Это не инвестиционная рекомендация.", payload.Symbol, trendRu(payload.Trend), pct(payload.RSI14), pct(payload.VolatilityPct))
		}
	}
	return "Готово. Данные получены из инструментов терминала; это не инвестиционная рекомендация."
}
