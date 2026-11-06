// Package mcp serves the AI terminal tools over the Model Context Protocol
// (streamable HTTP transport) so external MCP clients — Claude Code, other
// agents — call the same Go tools as the DSH plugin (plan Task 10).
//
// The transport is deliberately small: JSON-RPC 2.0 over HTTP POST with an
// application/json reply (GET returns 405, no server-initiated SSE stream).
// The endpoint is mounted under the API Guard, so every request needs the
// per-launch X-Terminal-Token.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
)

const (
	// defaultProtocolVersion is the MCP revision used when the client does not
	// name one. Clients normally send their own revision in initialize.
	defaultProtocolVersion = "2025-03-26"
	// maxBodyBytes bounds one JSON-RPC request batch.
	maxBodyBytes  = 4 << 20
	serverName    = "ai-terminal"
	serverVersion = "0.1.0"
)

// JSON-RPC error codes (https://www.jsonrpc.org/specification#error_object).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// ToolInvoker is the agent capability the MCP server exposes: the tool catalog
// and the shared invoke path (the same one the DSH bridge uses).
type ToolInvoker interface {
	Tools() []agent.ToolInfo
	Invoke(ctx context.Context, name string, args map[string]any) (agent.ToolCall, *agent.Artifact)
}

// NewServer returns the MCP streamable-HTTP handler. It must be mounted under
// the API Guard (the caller wraps it in cmd/terminal/main.go).
func NewServer(invoker ToolInvoker) http.Handler {
	return &server{invoker: invoker}
}

type server struct {
	invoker ToolInvoker
}

// rpcRequest is one JSON-RPC request or notification. A nil ID means the
// message is a notification and gets no response body.
type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// rpcError is one JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcResponse is one JSON-RPC response envelope.
type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

// initializeParams is the subset of initialize params the server reads.
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

// callParams is the tools/call params.
type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeBody(w, http.StatusMethodNotAllowed, errorResponse(nil, codeInvalidRequest, "MCP endpoint accepts POST only"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeBody(w, http.StatusBadRequest, errorResponse(nil, codeParseError, "request body is too large or unreadable"))
		return
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		writeBody(w, http.StatusBadRequest, errorResponse(nil, codeParseError, "empty request body"))
		return
	}
	if strings.HasPrefix(trimmed, "[") {
		s.serveBatch(w, r, body)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeBody(w, http.StatusBadRequest, errorResponse(nil, codeParseError, "invalid JSON: "+err.Error()))
		return
	}
	if req.ID == nil {
		s.serveNotification(r, req)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := s.dispatch(r.Context(), req)
	writeBody(w, http.StatusOK, resp)
}

func (s *server) serveBatch(w http.ResponseWriter, r *http.Request, body []byte) {
	var raws []json.RawMessage
	if err := json.Unmarshal(body, &raws); err != nil {
		writeBody(w, http.StatusBadRequest, errorResponse(nil, codeParseError, "invalid JSON batch: "+err.Error()))
		return
	}
	responses := make([]rpcResponse, 0, len(raws))
	for _, raw := range raws {
		var req rpcRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			responses = append(responses, errorResponse(nil, codeParseError, "invalid JSON-RPC request"))
			continue
		}
		if req.ID == nil {
			s.serveNotification(r, req)
			continue
		}
		responses = append(responses, s.dispatch(r.Context(), req))
	}
	if len(responses) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeBody(w, http.StatusOK, responses)
}

// serveNotification handles a message without an id: notifications are
// acknowledged with 202 and never produce a JSON-RPC response.
func (s *server) serveNotification(_ *http.Request, _ rpcRequest) {
	// initialized, cancelled and progress notifications carry no server work.
}

// dispatch executes one request and returns its JSON-RPC response.
func (s *server) dispatch(ctx context.Context, req rpcRequest) rpcResponse {
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, codeInvalidRequest, "jsonrpc must be \"2.0\"")
	}
	if strings.TrimSpace(req.Method) == "" {
		return errorResponse(req.ID, codeInvalidRequest, "method is required")
	}
	switch req.Method {
	case "initialize":
		return resultResponse(req.ID, s.initialize(req.Params))
	case "ping":
		return resultResponse(req.ID, map[string]any{})
	case "tools/list":
		return resultResponse(req.ID, map[string]any{"tools": s.toolList()})
	case "tools/call":
		return s.toolCall(ctx, req.ID, req.Params)
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		return rpcResponse{}
	default:
		return errorResponse(req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

// initialize answers the MCP handshake: the client's protocol revision is
// echoed when present, otherwise the server names its own default.
func (s *server) initialize(params json.RawMessage) map[string]any {
	version := defaultProtocolVersion
	if len(params) > 0 {
		var parsed initializeParams
		if err := json.Unmarshal(params, &parsed); err == nil && strings.TrimSpace(parsed.ProtocolVersion) != "" {
			version = parsed.ProtocolVersion
		}
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{"name": serverName, "version": serverVersion},
	}
}

// toolList projects the Go tool catalog to MCP tool declarations.
func (s *server) toolList() []map[string]any {
	tools := s.invoker.Tools()
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		schema := json.RawMessage(tool.Input)
		if len(schema) == 0 {
			schema = json.RawMessage("{\"type\":\"object\"}")
		}
		entry := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": schema,
		}
		if tool.Title != "" {
			entry["title"] = tool.Title
		}
		out = append(out, entry)
	}
	return out
}

// toolCall runs one tool through the shared invoke path and returns its result
// as MCP text content. A tool-level failure is a successful tools/call with
// isError=true; an unknown tool name is a JSON-RPC invalid-params error.
func (s *server) toolCall(ctx context.Context, id *json.RawMessage, params json.RawMessage) rpcResponse {
	var parsed callParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &parsed); err != nil {
			return errorResponse(id, codeInvalidParams, "invalid tools/call params: "+err.Error())
		}
	}
	name := strings.TrimSpace(parsed.Name)
	if name == "" {
		return errorResponse(id, codeInvalidParams, "tools/call requires a tool name")
	}
	if !s.hasTool(name) {
		return errorResponse(id, codeInvalidParams, "unknown tool: "+name)
	}
	call, artifact := s.invoker.Invoke(ctx, name, parsed.Arguments)
	envelope := map[string]any{}
	isError := false
	if proposal, ok := agent.ProposalFromResult(call.Result); ok {
		envelope["proposal"] = proposal
	} else if call.Error != "" {
		envelope["error"] = call.Error
		isError = true
	} else {
		envelope["result"] = call.Result
		if artifact != nil {
			envelope["artifact"] = artifact
		}
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return errorResponse(id, codeInternalError, "cannot encode the tool result")
	}
	return resultResponse(id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(data)}},
		"isError": isError,
	})
}

func (s *server) hasTool(name string) bool {
	for _, tool := range s.invoker.Tools() {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// resultResponse builds a successful JSON-RPC response.
func resultResponse(id *json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// errorResponse builds a JSON-RPC error response.
func errorResponse(id *json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

// writeBody writes one JSON body with the JSON-RPC content type.
func writeBody(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := fmt.Fprintln(w, mustJSON(payload)); err != nil {
		return
	}
}

// mustJSON marshals a response; the structures above always encode.
func mustJSON(payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "{\"jsonrpc\":\"2.0\",\"id\":null,\"error\":{\"code\":-32603,\"message\":\"internal encoding error\"}}"
	}
	return string(raw)
}
