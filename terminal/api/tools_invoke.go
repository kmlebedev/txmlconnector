package api

// Shared tool invocation path (plan Task 9): the DSH tool bridge and the MCP
// server call the Go agent tools through one endpoint instead of duplicating
// the dispatch. The mutating tools still go through the human approval gate, so
// the reply may carry a proposal instead of an executed result.

import (
	"context"
	"net/http"
	"strings"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
)

// ToolInvoker is the optional engine capability the invoke endpoint needs. It
// is asserted separately from AgentAPI so the chat interface and its test fakes
// stay unchanged.
type ToolInvoker interface {
	Invoke(ctx context.Context, name string, args map[string]any) (agent.ToolCall, *agent.Artifact)
}

// toolsInvokeRequest is the request body of POST /api/agent/tools/{name}/invoke:
// only the model-facing tool arguments.
type toolsInvokeRequest struct {
	Args map[string]any `json:"args"`
}

// toolsInvokeResponse is the reply: exactly one of result/error is set, plus the
// artifact card and the pending proposal when they apply.
type toolsInvokeResponse struct {
	Result   any             `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Artifact *agent.Artifact `json:"artifact,omitempty"`
	Proposal *agent.Proposal `json:"proposal,omitempty"`
}

// handleInvokeTool serves POST /api/agent/tools/{name}/invoke: the shared
// invocation path for the DSH plugin and MCP clients. An unknown tool name is a
// JSON 404; a tool-level failure is a 200 with the error field, because it is a
// normal tool result for the model, not an API failure.
func (s *Server) handleInvokeTool(w http.ResponseWriter, r *http.Request) {
	if s.deps.Agent == nil {
		writeUnavailable(w, "AI-агент недоступен")
		return
	}
	invoker, ok := s.deps.Agent.(ToolInvoker)
	if !ok {
		writeUnavailable(w, "инструменты AI-агента недоступны")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент")
		return
	}
	if !s.hasAgentTool(name) {
		writeError(w, http.StatusNotFound, codeNotFound, "инструмент не найден: "+name)
		return
	}
	var req toolsInvokeRequest
	if r.ContentLength != 0 {
		if !decodeBody(w, r, &req) {
			return
		}
	}
	ctx, cancel := requestContext(r, agentRequestTimeout)
	defer cancel()
	call, artifact := invoker.Invoke(ctx, name, req.Args)
	resp := toolsInvokeResponse{Artifact: artifact}
	if proposal, ok := agent.ProposalFromResult(call.Result); ok {
		resp.Proposal = &proposal
	} else if call.Error != "" {
		resp.Error = call.Error
	} else {
		resp.Result = call.Result
	}
	writeJSON(w, http.StatusOK, resp)
}

// hasAgentTool reports whether the agent catalog contains the tool name.
func (s *Server) hasAgentTool(name string) bool {
	for _, tool := range s.deps.Agent.Tools() {
		if tool.Name == name {
			return true
		}
	}
	return false
}
