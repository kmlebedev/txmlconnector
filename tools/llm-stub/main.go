// Command llm-stub is a tiny fake OpenAI-compatible chat-completions server.
// It lets the AI terminal be demoed end-to-end without an LLM key: the first
// model round trip returns a deterministic run_backtest tool call and the
// second one (after the tool result message) returns a final Russian answer.
//
// Usage:
//
//	go run ./tools/llm-stub -addr 127.0.0.1:8099
//	TERMINAL_LLM_BASE_URL=http://127.0.0.1:8099/v1 TERMINAL_LLM_API_KEY=stub \
//	TERMINAL_LLM_MODEL=llm-stub go run ./cmd/terminal -mode offline
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strings"
	"time"
)

// defaultSymbol is used when the request carries no instrument information.
const defaultSymbol = "TQBR:SBER"

// finalAnswer is returned on the second round trip, after the tool result.
const finalAnswer = "Готово: стратегия проверена на реальной истории терминала. " +
	"Смотрите метрики и кривую equity в карточке бэктеста. Прошлая доходность не гарантирует будущую, " +
	"торговля в терминале бумажная — это не инвестиционная рекомендация."

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []tool        `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
}

type tool struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responseToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type responseMessage struct {
	Role      string             `json:"role"`
	Content   *string            `json:"content"`
	ToolCalls []responseToolCall `json:"tool_calls,omitempty"`
}

type responseChoice struct {
	Index        int             `json:"index"`
	Message      responseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type chatResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []responseChoice `json:"choices"`
}

// newHandler builds the stub HTTP handler. serverSymbol is the fallback
// instrument used in the deterministic run_backtest tool call.
func newHandler(serverSymbol string) http.Handler {
	if strings.TrimSpace(serverSymbol) == "" {
		serverSymbol = defaultSymbol
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "server": "llm-stub", "model": "llm-stub"})
	})
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
			return
		}
		writeJSON(w, http.StatusOK, stubResponse(req, serverSymbol))
	}
	mux.HandleFunc("/v1/chat/completions", handler)
	mux.HandleFunc("/chat/completions", handler)
	return mux
}

// stubResponse decides between a tool call and the final answer: once the
// conversation already contains a tool result (or the request carries no
// tools), the deterministic final text is returned.
func stubResponse(req chatRequest, serverSymbol string) chatResponse {
	hasToolResult := false
	for _, msg := range req.Messages {
		if msg.Role == "tool" {
			hasToolResult = true
			break
		}
	}
	if hasToolResult || len(req.Tools) == 0 {
		answer := finalAnswer
		return chatResponse{
			ID:      "chatcmpl-llm-stub-final",
			Object:  "chat.completion",
			Created: 0,
			Model:   "llm-stub",
			Choices: []responseChoice{{
				Index:        0,
				Message:      responseMessage{Role: "assistant", Content: &answer},
				FinishReason: "stop",
			}},
		}
	}

	symbol := extractSymbol(req.Messages, serverSymbol)
	args, _ := json.Marshal(map[string]any{
		"symbol":    symbol,
		"periodSec": 300,
		"strategy":  "sma_cross",
	})
	return chatResponse{
		ID:      "chatcmpl-llm-stub-tool",
		Object:  "chat.completion",
		Created: 0,
		Model:   "llm-stub",
		Choices: []responseChoice{{
			Index: 0,
			Message: responseMessage{
				Role:    "assistant",
				Content: nil,
				ToolCalls: []responseToolCall{{
					ID:       "call_llm_stub_1",
					Type:     "function",
					Function: functionCall{Name: "run_backtest", Arguments: string(args)},
				}},
			},
			FinishReason: "tool_calls",
		}},
	}
}

// extractSymbol looks for the first "symbol" field in previous tool results
// (for example a list_instruments payload) and falls back to the flag default.
func extractSymbol(messages []chatMessage, fallback string) string {
	for _, msg := range messages {
		if msg.Role != "tool" || len(msg.Content) == 0 {
			continue
		}
		var payload any
		if err := json.Unmarshal(msg.Content, &payload); err != nil {
			continue
		}
		if symbol := firstSymbol(payload); symbol != "" {
			return symbol
		}
	}
	return fallback
}

// firstSymbol walks a decoded JSON payload and returns the first "symbol".
func firstSymbol(value any) string {
	switch v := value.(type) {
	case map[string]any:
		if symbol, ok := v["symbol"].(string); ok && symbol != "" {
			return symbol
		}
		for _, child := range v {
			if symbol := firstSymbol(child); symbol != "" {
				return symbol
			}
		}
	case []any:
		for _, child := range v {
			if symbol := firstSymbol(child); symbol != "" {
				return symbol
			}
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func main() {
	addr := flag.String("addr", ":8099", "HTTP listen address")
	symbol := flag.String("symbol", defaultSymbol, "fallback instrument for the stub run_backtest tool call")
	flag.Parse()

	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(*symbol),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("llm-stub listening on %s (OpenAI-compatible, model llm-stub)", *addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
