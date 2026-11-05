package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// llmRecord is one captured provider request.
type llmRecord struct {
	path string
	auth string
	body llmChatRequest
}

func writeJSONResponse(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// toolCallPayload builds an OpenAI-compatible response with one tool call.
func toolCallPayload(name, arguments string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 0,
		"model":   "test-model",
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []any{map[string]any{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": arguments,
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	}
}

// textPayload builds an OpenAI-compatible text response.
func textPayload(content string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 0,
		"model":   "test-model",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
	}
}

func hasToolMessage(req llmChatRequest) bool {
	for _, msg := range req.Messages {
		if msg.Role == "tool" {
			return true
		}
	}
	return false
}

// newLLMEngine builds an engine pointed at a fake OpenAI-compatible server.
func newLLMEngine(t *testing.T, serverURL string, timeout time.Duration) (*Engine, *fakeBroker) {
	t.Helper()
	data := newFakeDataSource()
	broker := newFakeBroker(1_000_000)
	cfg := Config{
		DefaultSymbol: "TQBR:SBER",
		LLM: LLMConfig{
			BaseURL: serverURL + "/v1",
			APIKey:  "secret-test-key",
			Model:   "test-model",
			Timeout: timeout,
		},
	}
	return newTestEngineWith(t, cfg, data, broker), broker
}

// TestChatLLMToolLoop checks the request shape and the tool loop: tool_call →
// real tool execution → final text.
func TestChatLLMToolLoop(t *testing.T) {
	var mu sync.Mutex
	var records []llmRecord
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llmChatRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		records = append(records, llmRecord{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: req})
		mu.Unlock()
		if hasToolMessage(req) {
			writeJSONResponse(w, textPayload("Готово: бэктест выполнен на реальных данных. Это бумажная аналитика."))
			return
		}
		writeJSONResponse(w, toolCallPayload("run_backtest",
			`{"symbol":"TQBR:SBER","periodSec":300,"strategy":"sma_cross","count":100}`))
	}))
	defer srv.Close()

	e, broker := newLLMEngine(t, srv.URL, 2*time.Second)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "протестируй SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !resp.LLM {
		t.Fatal("LLM response must have LLM=true")
	}
	if !strings.HasPrefix(resp.Answer, "Готово:") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "run_backtest" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Error != "" {
		t.Errorf("tool call error = %q", resp.ToolCalls[0].Error)
	}
	if _, ok := resp.ToolCalls[0].Result.(backtest.Result); !ok {
		t.Errorf("tool result type = %T, want backtest.Result", resp.ToolCalls[0].Result)
	}
	if len(resp.Artifacts) != 1 || resp.Artifacts[0].Kind != "backtest" {
		t.Errorf("artifacts = %+v", resp.Artifacts)
	}
	if len(broker.placedOrders()) != 0 {
		t.Error("no paper orders expected from a backtest run")
	}
	if strings.Contains(resp.Answer, "secret-test-key") {
		t.Fatal("API key leaked into the answer")
	}

	mu.Lock()
	captured := append([]llmRecord(nil), records...)
	mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(captured))
	}
	first := captured[0]
	if first.path != "/v1/chat/completions" {
		t.Errorf("path = %q", first.path)
	}
	if first.auth != "Bearer secret-test-key" {
		t.Errorf("Authorization header = %q", first.auth)
	}
	if first.body.Model != "test-model" {
		t.Errorf("model = %q", first.body.Model)
	}
	if len(first.body.Tools) != len(toolCatalogNames) {
		t.Errorf("tools = %d, want %d", len(first.body.Tools), len(toolCatalogNames))
	}
	if first.body.ToolChoice != "auto" {
		t.Errorf("tool_choice = %q, want auto", first.body.ToolChoice)
	}
	if first.body.Temperature != 0.2 || first.body.Stream {
		t.Errorf("temperature/stream = %v/%v", first.body.Temperature, first.body.Stream)
	}
	if len(first.body.Messages) != 2 || first.body.Messages[0].Role != "system" {
		t.Fatalf("first request messages = %+v", first.body.Messages)
	}
	system := contentString(first.body.Messages[0].Content)
	if !strings.Contains(system, "аналитик") || !strings.Contains(system, "200 слов") ||
		!strings.Contains(system, "бумажная") {
		t.Errorf("system prompt = %q", system)
	}
	second := captured[1]
	if len(second.body.Messages) != 4 {
		t.Fatalf("second request messages = %d, want 4", len(second.body.Messages))
	}
	last := second.body.Messages[len(second.body.Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "call_1" {
		t.Errorf("tool message = %+v", last)
	}
}

// TestChatLLMFallbackHTTP500 checks the LLM failure fallback to the local
// analyst without leaking the key or the provider body.
func TestChatLLMFallbackHTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("<html>provider internal failure</html>"))
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 2*time.Second)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "протестируй SBER 5м"})
	if err != nil {
		t.Fatalf("Chat must fall back, got error: %v", err)
	}
	if resp.LLM {
		t.Fatal("LLM must be false after a provider failure")
	}
	if !strings.Contains(resp.Answer, "LLM недоступен (http_500)") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "локальный аналитик") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if len(resp.ToolCalls) == 0 {
		t.Error("fallback must execute the real tools")
	}
	for _, secret := range []string{"secret-test-key", "provider internal failure"} {
		if strings.Contains(resp.Answer, secret) {
			t.Fatalf("answer leaks %q: %q", secret, resp.Answer)
		}
	}
}

// TestChatLLMFallbackInvalidJSON checks the invalid-response fallback class.
func TestChatLLMFallbackInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not json"))
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 2*time.Second)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "проанализируй SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.LLM {
		t.Fatal("LLM must be false")
	}
	if !strings.Contains(resp.Answer, "LLM недоступен (invalid_response)") {
		t.Errorf("answer = %q", resp.Answer)
	}
}

// TestChatLLMFallbackTimeout checks the timeout fallback class.
func TestChatLLMFallbackTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 30*time.Millisecond)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "протестируй SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.LLM {
		t.Fatal("LLM must be false after a timeout")
	}
	if !strings.Contains(resp.Answer, "LLM недоступен (timeout)") {
		t.Errorf("answer = %q", resp.Answer)
	}
}

// TestChatLLMMaxIterations stops after 8 tool iterations and asks for the
// final answer without tools.
func TestChatLLMMaxIterations(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llmChatRequest
		_ = json.Unmarshal(body, &req)
		atomic.AddInt32(&calls, 1)
		if len(req.Tools) == 0 {
			writeJSONResponse(w, textPayload("Финальный ответ без инструментов."))
			return
		}
		writeJSONResponse(w, toolCallPayload("list_instruments", `{}`))
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 2*time.Second)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "что на рынке?"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !resp.LLM {
		t.Fatal("LLM must be true")
	}
	if !strings.Contains(resp.Answer, "Финальный ответ") {
		t.Errorf("answer = %q", resp.Answer)
	}
	if len(resp.ToolCalls) != maxToolIterations {
		t.Errorf("tool calls = %d, want %d", len(resp.ToolCalls), maxToolIterations)
	}
	if got := atomic.LoadInt32(&calls); got != int32(maxToolIterations+1) {
		t.Errorf("provider calls = %d, want %d", got, maxToolIterations+1)
	}
}

// TestChatLLMInvalidToolArguments reports a tool error but keeps the loop.
func TestChatLLMInvalidToolArguments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llmChatRequest
		_ = json.Unmarshal(body, &req)
		if hasToolMessage(req) {
			writeJSONResponse(w, textPayload("Ответ после ошибки аргументов."))
			return
		}
		writeJSONResponse(w, toolCallPayload("run_backtest", "{this is not json"))
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 2*time.Second)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "протестируй SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !resp.LLM {
		t.Fatal("LLM must be true")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if !strings.Contains(resp.ToolCalls[0].Error, "invalid tool arguments JSON") {
		t.Errorf("tool error = %q", resp.ToolCalls[0].Error)
	}
	if !strings.Contains(resp.Answer, "Ответ после ошибки") {
		t.Errorf("answer = %q", resp.Answer)
	}
}

// TestChatLLMEmptyTextFallsBack covers a reachable provider that returns no
// text at all: the classified fallback still answers.
func TestChatLLMEmptyTextFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(w, textPayload(""))
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 2*time.Second)
	resp, err := e.Chat(context.Background(), ChatRequest{Message: "проанализируй SBER"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.LLM {
		t.Fatal("LLM must be false")
	}
	if !strings.Contains(resp.Answer, "empty_response") {
		t.Errorf("answer = %q", resp.Answer)
	}
}

// TestChatContextCancelledAborts makes sure a canceled request is not turned
// into a slow local-analyst fallback.
func TestChatContextCancelledAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Chat(ctx, ChatRequest{Message: "проанализируй SBER"}); err == nil {
		t.Fatal("expected the context error")
	}
}

// TestChatNoFallbackAfterMutatingTool is the R3 regression test: when the LLM
// fails after a mutating tool already ran, the local fallback must not replay
// the action. The response is marked partial and keeps the executed tool calls.
func TestChatNoFallbackAfterMutatingTool(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		body, _ := io.ReadAll(r.Body)
		var req llmChatRequest
		_ = json.Unmarshal(body, &req)
		if hasToolMessage(req) {
			// The follow-up model round trip fails after the order ran.
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSONResponse(w, toolCallPayload("place_order",
			`{"symbol":"TQBR:SBER","side":"buy","qty":10,"type":"market"}`))
	}))
	defer srv.Close()

	e, broker := newLLMEngine(t, srv.URL, 2*time.Second)
	// R5 gates mutating tools in the default confirm mode; this test exercises
	// the "action already executed, then the LLM died" path, so the envelope
	// auto-approves the small order.
	e.cfg.Approval = ApprovalPolicy{
		Mode:     ApprovalEnvelope,
		Envelope: RiskLimits{MaxPositionPct: 100, MaxOrderPct: 100},
	}
	resp, err := e.Chat(context.Background(), ChatRequest{
		Message: "купи 10 SBER по рынку", ConversationID: "conv-1",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	placed := broker.placedOrders()
	if len(placed) != 1 {
		t.Fatalf("broker placements = %d, want exactly one order (no fallback replay)", len(placed))
	}
	if placed[0].ClientOrderID != "conv-1:call_1" {
		t.Errorf("ClientOrderID = %q, want the conversation and tool call ids joined", placed[0].ClientOrderID)
	}
	if !resp.Partial {
		t.Error("Partial = false, want true after a mutating tool ran")
	}
	if !resp.LLM {
		t.Error("LLM = false, want the partial response to stay LLM=true")
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "place_order" {
		t.Fatalf("tool calls = %+v, want the executed place_order", resp.ToolCalls)
	}
	if !strings.HasPrefix(resp.Answer, "LLM недоступен после выполнения действий") {
		t.Errorf("answer = %q, want the partial-action prefix", resp.Answer)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("provider calls = %d, want 2 (tool round trip, then failure)", got)
	}
}

// TestChatFallsBackWhenOnlyReadToolsRan checks that a read-only tool round trip
// still falls back to the deterministic local analyst after an LLM failure.
func TestChatFallsBackWhenOnlyReadToolsRan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llmChatRequest
		_ = json.Unmarshal(body, &req)
		if hasToolMessage(req) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSONResponse(w, toolCallPayload("get_quote", `{"symbol":"TQBR:SBER"}`))
	}))
	defer srv.Close()

	e, broker := newLLMEngine(t, srv.URL, 2*time.Second)
	resp, err := e.Chat(context.Background(), ChatRequest{
		Message: "проанализируй SBER 5м", ConversationID: "conv-2",
	})
	if err != nil {
		t.Fatalf("Chat must fall back, got error: %v", err)
	}
	if resp.LLM {
		t.Error("LLM = true, want the local analyst fallback")
	}
	if resp.Partial {
		t.Error("Partial = true, want a complete fallback answer")
	}
	if !strings.Contains(resp.Answer, "LLM недоступен (http_503)") ||
		!strings.Contains(resp.Answer, "локальный аналитик") {
		t.Errorf("answer = %q, want the local analyst fallback text", resp.Answer)
	}
	if len(resp.ToolCalls) == 0 {
		t.Error("fallback must execute the real read-only tools")
	}
	if len(broker.placedOrders()) != 0 {
		t.Errorf("broker placements = %d, want 0", len(broker.placedOrders()))
	}
}
