package agent

import "context"

// Invoke runs one tool by name for callers outside the LLM loop: the DSH tool
// bridge (plan Task 9) and the MCP server share this path instead of
// duplicating the dispatch. It is the public wrapper over executeToolCall, so
// mutating tools keep the human approval gate and the normal tool events
// (§9.5), and a tool failure is reported in ToolCall.Error instead of ending
// the turn.
func (e *Engine) Invoke(ctx context.Context, name string, args map[string]any) (ToolCall, *Artifact) {
	if args == nil {
		args = map[string]any{}
	}
	return e.executeToolCall(ctx, name, args)
}

// ProposalFromResult extracts the pending proposal carried by an invoke result.
// A gated mutating tool returns the proposal as its canonical tool result, so
// callers that must show it (the DSH card, the MCP envelope) need this helper
// instead of guessing the JSON shape.
func ProposalFromResult(result any) (Proposal, bool) {
	res, ok := result.(proposalResult)
	if !ok {
		return Proposal{}, false
	}
	return res.Proposal, true
}
