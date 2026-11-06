package agent

// Conversation memory tests (plan Task 6 / R4): a bounded per-conversation
// history that survives between Chat calls, keeps the system prompt and never
// splits an assistant(tool_calls)/tool pair; the model sees compacted tool
// results only.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// conversationTestTurn builds one complete dialog turn: the user question, one
// assistant tool call, its compacted result and the final answer.
func conversationTestTurn(index int) []llmMessage {
	callID := fmt.Sprintf("call_%d", index)
	symbol := "TQBR:SBER"
	if index%2 == 1 {
		symbol = "TQBR:GAZP"
	}
	return []llmMessage{
		{Role: "user", Content: fmt.Sprintf("вопрос %d: протестируй %s", index, symbol)},
		{Role: "assistant", ToolCalls: []llmToolCall{{
			ID:   callID,
			Type: "function",
			Function: llmFunctionCall{
				Name:      "run_backtest",
				Arguments: `{"symbol":"` + symbol + `"}`,
			},
		}}},
		{Role: "tool", ToolCallID: callID, Content: `{"symbol":"` + symbol + `","metrics":{"totalReturnPct":7.5,"trades":3}}`},
		{Role: "assistant", Content: fmt.Sprintf("ответ %d", index)},
	}
}

// messagesSize returns the serialized size of a message slice, the metric the
// byte limit is expressed in.
func messagesSize(t *testing.T, messages []llmMessage) int {
	t.Helper()
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return len(raw)
}

// assertToolPairsIntact checks that every tool message follows the assistant
// message that requested it: a trimmed history must not start with an orphan
// tool result or split a tool call from its result.
func assertToolPairsIntact(t *testing.T, messages []llmMessage) {
	t.Helper()
	pending := map[string]bool{}
	for i, msg := range messages {
		switch msg.Role {
		case "assistant":
			pending = map[string]bool{}
			for _, call := range msg.ToolCalls {
				pending[call.ID] = true
			}
		case "tool":
			if !pending[msg.ToolCallID] {
				t.Fatalf("message %d: tool result %q has no matching assistant tool call", i, msg.ToolCallID)
			}
			delete(pending, msg.ToolCallID)
		}
	}
}

// TestConversationTrimsByBytes checks the byte bound: the oldest turns are
// evicted, the system prompt stays and the assistant/tool pairs remain whole.
func TestConversationTrimsByBytes(t *testing.T) {
	const maxBytes = 2000
	store := newMemoryConversations(100, maxBytes)
	system := llmMessage{Role: "system", Content: systemPrompt}
	store.Append("c", system)
	for i := 0; i < 8; i++ {
		store.Append("c", conversationTestTurn(i)...)
	}

	history := store.Load("c")
	if len(history) == 0 {
		t.Fatal("conversation history is empty")
	}
	if history[0].Role != "system" || contentString(history[0].Content) != systemPrompt {
		t.Fatalf("system prompt was evicted: first message = %+v", history[0])
	}
	if size := messagesSize(t, history); size > maxBytes {
		t.Fatalf("history size = %d, want <= %d", size, maxBytes)
	}
	if len(history) > 1+4*4 {
		t.Fatalf("history kept %d messages, want the oldest turns evicted", len(history))
	}
	if len(history) < 5 {
		t.Fatalf("history kept %d messages, want at least the newest turn", len(history))
	}
	assertToolPairsIntact(t, history)
	for _, msg := range history {
		if msg.Role == "user" && strings.Contains(contentString(msg.Content), "вопрос 0:") {
			t.Fatal("oldest turn was not evicted")
		}
	}
	last := history[len(history)-1]
	if last.Role != "assistant" || contentString(last.Content) != "ответ 7" {
		t.Fatalf("last message = %+v, want the newest answer", last)
	}
}

// TestConversationTrimsByTurns checks the turn bound: only the newest maxTurns
// turns are kept, the system prompt always survives.
func TestConversationTrimsByTurns(t *testing.T) {
	store := newMemoryConversations(2, 0)
	system := llmMessage{Role: "system", Content: systemPrompt}
	store.Append("c", system)
	for i := 0; i < 5; i++ {
		store.Append("c", conversationTestTurn(i)...)
	}

	history := store.Load("c")
	users := 0
	for _, msg := range history {
		if msg.Role == "user" {
			users++
		}
	}
	if users != 2 {
		t.Fatalf("kept user turns = %d, want 2", users)
	}
	if history[0].Role != "system" || contentString(history[0].Content) != systemPrompt {
		t.Fatalf("system prompt was evicted: first message = %+v", history[0])
	}
	if !strings.Contains(contentString(history[1].Content), "вопрос 3:") {
		t.Fatalf("oldest kept user message = %q, want вопрос 3", contentString(history[1].Content))
	}
	assertToolPairsIntact(t, history)
}

// TestConversationLRUEvictsOldest checks the 64-dialog LRU: the least recently
// used conversation is evicted first, and a Load touch keeps a dialog alive.
func TestConversationLRUEvictsOldest(t *testing.T) {
	store := newMemoryConversations(4, 1<<20)
	hello := llmMessage{Role: "user", Content: "привет"}
	for i := 0; i < conversationLRUSize; i++ {
		store.Append(fmt.Sprintf("conv-%d", i), hello)
	}
	// Two more dialogs evict the two least recently used ones.
	store.Append("conv-extra-1", hello)
	store.Append("conv-extra-2", hello)
	if got := store.Load("conv-0"); len(got) != 0 {
		t.Fatalf("oldest conversation was not evicted: %+v", got)
	}
	if got := store.Load("conv-1"); len(got) != 0 {
		t.Fatalf("second oldest conversation was not evicted: %+v", got)
	}
	if got := store.Load("conv-2"); len(got) != 1 {
		t.Fatalf("recent conversation was evicted: %+v", got)
	}
	// A Load touch makes conv-2 recent: the next insert evicts conv-3 instead.
	_ = store.Load("conv-2")
	store.Append("conv-extra-3", hello)
	if got := store.Load("conv-2"); len(got) != 1 {
		t.Fatalf("touched conversation was evicted: %+v", got)
	}
	if got := store.Load("conv-3"); len(got) != 0 {
		t.Fatalf("least recently used conversation was not evicted: %+v", got)
	}
}

// TestConversationReset drops one dialog without touching the others.
func TestConversationReset(t *testing.T) {
	store := newMemoryConversations(4, 4096)
	store.Append("a", llmMessage{Role: "user", Content: "раз"})
	store.Append("b", llmMessage{Role: "user", Content: "два"})
	store.Reset("a")
	if got := store.Load("a"); len(got) != 0 {
		t.Fatalf("reset conversation still has %d messages", len(got))
	}
	if got := store.Load("b"); len(got) != 1 {
		t.Fatalf("other conversation = %+v, want intact", got)
	}
}

// TestConversationLoadReturnsCopy checks that the caller cannot mutate the
// stored history through the slice returned by Load.
func TestConversationLoadReturnsCopy(t *testing.T) {
	store := newMemoryConversations(4, 4096)
	store.Append("c", llmMessage{Role: "user", Content: "привет"})
	got := store.Load("c")
	got[0].Role = "system"
	got[0].Content = "подмена"
	if fresh := store.Load("c"); fresh[0].Role != "user" || contentString(fresh[0].Content) != "привет" {
		t.Fatalf("Load returned a view into the store: %+v", fresh[0])
	}
}

// TestChatRemembersPreviousToolResults is the R4 regression test: the second
// question in the same conversation sends the previous tool results (compacted,
// not the raw equity curves) to the LLM; another conversation stays isolated.
func TestChatRemembersPreviousToolResults(t *testing.T) {
	var mu sync.Mutex
	var requests []llmChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llmChatRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" {
			writeJSONResponse(w, textPayload("Готово: бэктест выполнен на реальных данных."))
			return
		}
		symbol := "TQBR:SBER"
		if strings.Contains(contentString(last.Content), "GAZP") {
			symbol = "TQBR:GAZP"
		}
		writeJSONResponse(w, toolCallPayload("run_backtest",
			fmt.Sprintf(`{"symbol":%q,"periodSec":300,"strategy":"sma_cross","count":100}`, symbol)))
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 2*time.Second)
	// The first request carries no dialog id: the server generates one and
	// returns it, and the follow-up continues the same conversation.
	first, err := e.Chat(context.Background(), ChatRequest{Message: "протестируй SBER"})
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if first.ConversationID == "" {
		t.Fatal("a request without conversationId must get a generated one back")
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "run_backtest" {
		t.Fatalf("first tool calls = %+v", first.ToolCalls)
	}

	second, err := e.Chat(context.Background(), ChatRequest{
		Message: "то же для GAZP", ConversationID: first.ConversationID,
	})
	if err != nil {
		t.Fatalf("second Chat: %v", err)
	}
	if len(second.ToolCalls) != 1 || second.ToolCalls[0].Name != "run_backtest" {
		t.Fatalf("second tool calls = %+v", second.ToolCalls)
	}

	// A third, independent dialog must not inherit the previous history.
	if _, err := e.Chat(context.Background(), ChatRequest{
		Message: "что на рынке?", ConversationID: "conv-other",
	}); err != nil {
		t.Fatalf("third Chat: %v", err)
	}

	mu.Lock()
	captured := append([]llmChatRequest(nil), requests...)
	mu.Unlock()
	if len(captured) != 6 {
		t.Fatalf("provider requests = %d, want 6 (2 per turn)", len(captured))
	}
	messages := captured[2].Messages
	if len(messages) < 6 {
		t.Fatalf("second-turn request has %d messages, want the previous turn in the history: %+v", len(messages), messages)
	}
	if messages[0].Role != "system" {
		t.Fatalf("first message = %+v, want the system prompt", messages[0])
	}
	if messages[1].Role != "user" || !strings.Contains(contentString(messages[1].Content), "протестируй SBER") {
		t.Fatalf("previous user message = %+v", messages[1])
	}
	if messages[2].Role != "assistant" || len(messages[2].ToolCalls) != 1 {
		t.Fatalf("previous assistant tool call = %+v", messages[2])
	}
	var previousTool string
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_1" {
			previousTool = contentString(msg.Content)
		}
	}
	if previousTool == "" {
		t.Fatal("previous run_backtest result is missing from the LLM history")
	}
	for _, want := range []string{`"symbol":"TQBR:SBER"`, `"metrics"`} {
		if !strings.Contains(previousTool, want) {
			t.Errorf("previous tool result %q does not contain %q", previousTool, want)
		}
	}
	if strings.Contains(previousTool, `"equity"`) {
		t.Errorf("tool history is not compacted: %q", previousTool)
	}
	assertToolPairsIntact(t, messages)
	last := messages[len(messages)-1]
	if last.Role != "user" || !strings.Contains(contentString(last.Content), "GAZP") {
		t.Fatalf("last message = %+v, want the new GAZP question", last)
	}

	// The isolated dialog starts from the system prompt and its own question.
	isolation := captured[4].Messages
	if len(isolation) != 2 || isolation[0].Role != "system" || isolation[1].Role != "user" {
		t.Fatalf("isolated conversation messages = %+v, want system + user only", isolation)
	}
}

// TestChatGeneratesConversationID checks the dialog id contract: a missing id is
// generated and echoed, an explicit id is preserved, and an absurdly long one is
// treated as missing instead of becoming an unbounded memory key.
func TestChatGeneratesConversationID(t *testing.T) {
	e, _, _ := newTestEngine(t) // deterministic local analyst: no provider needed
	ctx := context.Background()

	first, err := e.Chat(ctx, ChatRequest{Message: "проанализируй SBER"})
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if first.ConversationID == "" {
		t.Fatal("generated conversationId is empty")
	}
	second, err := e.Chat(ctx, ChatRequest{Message: "проанализируй GAZP"})
	if err != nil {
		t.Fatalf("second Chat: %v", err)
	}
	if second.ConversationID == "" || second.ConversationID == first.ConversationID {
		t.Fatalf("each id-less request must get a fresh id: %q then %q",
			first.ConversationID, second.ConversationID)
	}
	explicit, err := e.Chat(ctx, ChatRequest{Message: "проанализируй SBER", ConversationID: "my-dialog"})
	if err != nil {
		t.Fatalf("explicit Chat: %v", err)
	}
	if explicit.ConversationID != "my-dialog" {
		t.Fatalf("explicit conversationId = %q, want my-dialog", explicit.ConversationID)
	}
	tooLong := strings.Repeat("x", maxConversationIDLen+1)
	generated, err := e.Chat(ctx, ChatRequest{Message: "проанализируй SBER", ConversationID: tooLong})
	if err != nil {
		t.Fatalf("long-id Chat: %v", err)
	}
	if generated.ConversationID == "" || generated.ConversationID == tooLong {
		t.Fatalf("oversized conversationId = %q, want a generated id", generated.ConversationID)
	}
}
