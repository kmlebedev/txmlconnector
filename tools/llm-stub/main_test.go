package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postJSON(t *testing.T, url, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode response: %v (%s)", err, raw)
	}
	return payload
}

func messageOf(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	choices, ok := payload["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("no choices in %+v", payload)
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("bad choice %+v", choices[0])
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		t.Fatalf("bad message in %+v", choice)
	}
	return message
}

// TestStubToolCallThenFinal checks the deterministic two-step conversation.
func TestStubToolCallThenFinal(t *testing.T) {
	srv := httptest.NewServer(newHandler("TQBR:SBER"))
	defer srv.Close()

	first := postJSON(t, srv.URL,
		`{"model":"llm-stub","messages":[{"role":"system","content":"..."},{"role":"user","content":"протестируй SBER"}],"tools":[{"type":"function","function":{"name":"run_backtest"}}]}`)
	message := messageOf(t, first)
	calls, ok := message["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls = %+v", message["tool_calls"])
	}
	call, _ := calls[0].(map[string]any)
	fn, _ := call["function"].(map[string]any)
	if fn["name"] != "run_backtest" {
		t.Fatalf("tool name = %v", fn["name"])
	}
	arguments, _ := fn["arguments"].(string)
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		t.Fatalf("arguments are not JSON: %v", err)
	}
	if args["symbol"] != "TQBR:SBER" || args["strategy"] != "sma_cross" {
		t.Errorf("tool arguments = %v", args)
	}
	if args["periodSec"] != float64(300) {
		t.Errorf("periodSec = %v", args["periodSec"])
	}

	second := postJSON(t, srv.URL,
		`{"model":"llm-stub","messages":[{"role":"user","content":"протестируй SBER"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"run_backtest","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"{\"ok\":true}"}],"tools":[{"type":"function","function":{"name":"run_backtest"}}]}`)
	secondMessage := messageOf(t, second)
	content, _ := secondMessage["content"].(string)
	if content == "" {
		t.Fatal("final iteration must return a text answer")
	}
	if strings.Contains(strings.ToLower(content), "tool_calls") {
		t.Errorf("final content = %q", content)
	}
}

// TestStubWithoutTools returns text when the request carries no tools.
func TestStubWithoutTools(t *testing.T) {
	srv := httptest.NewServer(newHandler("TQBR:SBER"))
	defer srv.Close()
	payload := postJSON(t, srv.URL, `{"model":"llm-stub","messages":[{"role":"user","content":"привет"}]}`)
	message := messageOf(t, payload)
	if message["content"] == nil || message["content"] == "" {
		t.Fatalf("content = %+v", message["content"])
	}
}

// TestStubHealthAndErrors checks /health and the error paths.
func TestStubHealthAndErrors(t *testing.T) {
	srv := httptest.NewServer(newHandler(""))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health status = %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("GET chat/completions: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET chat/completions status = %d, want 405", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader("{bad json"))
	if err != nil {
		t.Fatalf("POST bad json: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path status = %d, want 404", resp.StatusCode)
	}
}

// TestExtractSymbol prefers the first symbol found in previous tool results.
func TestExtractSymbol(t *testing.T) {
	messages := []chatMessage{{
		Role:    "tool",
		Content: json.RawMessage(`{"instruments":[{"symbol":"TQBR:GAZP","last":1},{"symbol":"TQBR:SBER"}]}`),
	}}
	if got := extractSymbol(messages, "TQBR:SBER"); got != "TQBR:GAZP" {
		t.Errorf("extractSymbol = %q, want TQBR:GAZP", got)
	}
	if got := extractSymbol(nil, "TQBR:SBER"); got != "TQBR:SBER" {
		t.Errorf("extractSymbol fallback = %q", got)
	}
}
