package mcp

// Tests of the MCP streamable-HTTP server (plan Task 10): JSON-RPC 2.0
// initialize/tools/list/tools/call, the protocol revision echo, the tool
// result envelope and the transport error handling.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
)

// jsonSchema builds one raw JSON Schema without Go raw-string literals.
func jsonSchema(text string) json.RawMessage { return json.RawMessage(text) }

// quoteTool is the tool catalog fixture.
func quoteTool() agent.ToolInfo {
	return agent.ToolInfo{
		Name:        "get_quote",
		Title:       "Котировка",
		Description: "Котировка и стакан инструмента",
		Input:       jsonSchema("{\"type\":\"object\",\"properties\":{\"symbol\":{\"type\":\"string\",\"description\":\"Тикер\"}},\"required\":[\"symbol\"],\"additionalProperties\":false}"),
	}
}

// fakeInvoker is the ToolInvoker test double over the real agent types.
type fakeInvoker struct {
	tools []agent.ToolInfo
	calls []string
}

func (f *fakeInvoker) Tools() []agent.ToolInfo { return f.tools }

func (f *fakeInvoker) Invoke(_ context.Context, name string, args map[string]any) (agent.ToolCall, *agent.Artifact) {
	f.calls = append(f.calls, name)
	symbol, _ := args["symbol"].(string)
	if symbol == "TQBR:NOPE" {
		return agent.ToolCall{Name: name, Args: args, Error: "agent: no quote for TQBR:NOPE"}, nil
	}
	return agent.ToolCall{
		Name:       name,
		Args:       args,
		Result:     map[string]any{"symbol": symbol, "last": 293.22},
		DurationMs: 3,
	}, nil
}

// rpcMessage builds one JSON-RPC request or notification.
func rpcMessage(id any, method string, params any) map[string]any {
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		message["id"] = id
	}
	if params != nil {
		message["params"] = params
	}
	return message
}

// postJSON sends one payload to the MCP handler.
func postJSON(t *testing.T, url string, payload any) (int, []byte, http.Header) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	var data bytes.Buffer
	_, _ = data.ReadFrom(resp.Body)
	return resp.StatusCode, data.Bytes(), resp.Header
}

// decodeRPC decodes one JSON-RPC response envelope.
func decodeRPC(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode JSON-RPC reply %s: %v", data, err)
	}
	return envelope
}

// TestMCPInitializeAndList checks the handshake, the initialized notification
// and the tool catalog projection.
func TestMCPInitializeAndList(t *testing.T) {
	inv := &fakeInvoker{tools: []agent.ToolInfo{quoteTool()}}
	srv := httptest.NewServer(NewServer(inv))
	defer srv.Close()

	status, data, headers := postJSON(t, srv.URL, rpcMessage(1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
	}))
	if status != http.StatusOK {
		t.Fatalf("initialize status = %d, body %s", status, data)
	}
	if contentType := headers.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("initialize content type = %q", contentType)
	}
	envelope := decodeRPC(t, data)
	if envelope["jsonrpc"] != "2.0" || envelope["id"] != float64(1) {
		t.Fatalf("initialize envelope = %+v", envelope)
	}
	result, _ := envelope["result"].(map[string]any)
	if result == nil {
		t.Fatalf("initialize result = %+v", envelope)
	}
	if result["protocolVersion"] != "2025-03-26" {
		t.Fatalf("protocolVersion = %v, want the client revision", result["protocolVersion"])
	}
	capabilities, _ := result["capabilities"].(map[string]any)
	toolsCap, _ := capabilities["tools"].(map[string]any)
	if toolsCap == nil || toolsCap["listChanged"] != false {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "ai-terminal" {
		t.Fatalf("serverInfo = %+v", serverInfo)
	}

	status, data, _ = postJSON(t, srv.URL, rpcMessage(nil, "notifications/initialized", nil))
	if status != http.StatusAccepted {
		t.Fatalf("initialized notification status = %d, body %s", status, data)
	}
	if len(bytes.TrimSpace(data)) != 0 {
		t.Fatalf("initialized notification body = %s, want empty", data)
	}

	status, data, _ = postJSON(t, srv.URL, rpcMessage(2, "tools/list", map[string]any{}))
	if status != http.StatusOK {
		t.Fatalf("tools/list status = %d, body %s", status, data)
	}
	envelope = decodeRPC(t, data)
	result, _ = envelope["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %+v, want the single fixture tool", tools)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "get_quote" || tool["description"] == "" {
		t.Fatalf("tool = %+v", tool)
	}
	schema, _ := tool["inputSchema"].(map[string]any)
	if schema["type"] != "object" {
		t.Fatalf("inputSchema = %+v", schema)
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["symbol"]; !ok {
		t.Fatalf("inputSchema properties = %+v", properties)
	}
}

// TestMCPToolCallRoundTrip checks tools/call result content, the tool error
// envelope and the JSON-RPC error cases.
func TestMCPToolCallRoundTrip(t *testing.T) {
	inv := &fakeInvoker{tools: []agent.ToolInfo{quoteTool()}}
	srv := httptest.NewServer(NewServer(inv))
	defer srv.Close()

	status, data, _ := postJSON(t, srv.URL, rpcMessage(1, "tools/call", map[string]any{
		"name":      "get_quote",
		"arguments": map[string]any{"symbol": "TQBR:SBER"},
	}))
	if status != http.StatusOK {
		t.Fatalf("tools/call status = %d, body %s", status, data)
	}
	envelope := decodeRPC(t, data)
	result, _ := envelope["result"].(map[string]any)
	if result == nil {
		t.Fatalf("tools/call result = %+v", envelope)
	}
	if result["isError"] != false {
		t.Fatalf("isError = %v, want false", result["isError"])
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %+v", content)
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "text" {
		t.Fatalf("content block = %+v", block)
	}
	text, _ := block["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool result text %q is not JSON: %v", text, err)
	}
	if payload["result"] == nil {
		t.Fatalf("tool result envelope = %+v", payload)
	}
	quote, _ := payload["result"].(map[string]any)
	if quote["symbol"] != "TQBR:SBER" {
		t.Fatalf("quote = %+v", quote)
	}
	if len(inv.calls) != 1 || inv.calls[0] != "get_quote" {
		t.Fatalf("invoker calls = %+v", inv.calls)
	}

	// A tool-level failure is a successful call with isError=true.
	status, data, _ = postJSON(t, srv.URL, rpcMessage(2, "tools/call", map[string]any{
		"name":      "get_quote",
		"arguments": map[string]any{"symbol": "TQBR:NOPE"},
	}))
	if status != http.StatusOK {
		t.Fatalf("tool error status = %d, body %s", status, data)
	}
	envelope = decodeRPC(t, data)
	result, _ = envelope["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("tool error isError = %v, want true", result["isError"])
	}

	// Unknown tool name is invalid params.
	status, data, _ = postJSON(t, srv.URL, rpcMessage(3, "tools/call", map[string]any{"name": "no_such_tool"}))
	if status != http.StatusOK {
		t.Fatalf("unknown tool status = %d, body %s", status, data)
	}
	envelope = decodeRPC(t, data)
	rpcErr, _ := envelope["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"] != float64(codeInvalidParams) {
		t.Fatalf("unknown tool error = %+v", envelope)
	}

	// Unknown method is method not found.
	status, data, _ = postJSON(t, srv.URL, rpcMessage(4, "tools/nope", map[string]any{}))
	if status != http.StatusOK {
		t.Fatalf("unknown method status = %d, body %s", status, data)
	}
	envelope = decodeRPC(t, data)
	rpcErr, _ = envelope["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"] != float64(codeMethodNotFound) {
		t.Fatalf("unknown method error = %+v", envelope)
	}

	// GET is not the transport: 405 + Allow.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("GET Allow = %q", allow)
	}
}

// TestMCPInvalidJSON checks the parse-error reply.
func TestMCPInvalidJSON(t *testing.T) {
	inv := &fakeInvoker{tools: []agent.ToolInfo{quoteTool()}}
	srv := httptest.NewServer(NewServer(inv))
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("POST invalid JSON: %v", err)
	}
	defer resp.Body.Close()
	var data bytes.Buffer
	_, _ = data.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s", resp.StatusCode, data.String())
	}
	envelope := decodeRPC(t, data.Bytes())
	rpcErr, _ := envelope["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"] != float64(codeParseError) {
		t.Fatalf("parse error = %+v", envelope)
	}
}

// TestMCPBatch checks that a JSON-RPC batch returns one response per request.
func TestMCPBatch(t *testing.T) {
	inv := &fakeInvoker{tools: []agent.ToolInfo{quoteTool()}}
	srv := httptest.NewServer(NewServer(inv))
	defer srv.Close()

	batch := []map[string]any{
		rpcMessage(1, "initialize", map[string]any{"protocolVersion": "2025-03-26"}),
		rpcMessage(2, "tools/list", map[string]any{}),
	}
	status, data, _ := postJSON(t, srv.URL, batch)
	if status != http.StatusOK {
		t.Fatalf("batch status = %d, body %s", status, data)
	}
	var responses []map[string]any
	if err := json.Unmarshal(data, &responses); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("batch responses = %d, want 2", len(responses))
	}
}
