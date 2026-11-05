package agent

// Conversation memory (plan Task 6 / R4): the agent remembers the previous
// turns of a dialog, but the context stays bounded — a fixed number of dialogs
// (LRU), a bounded number of turns and a bounded byte budget per dialog.
// Trimmed history always keeps the system prompt and never splits an
// assistant(tool_calls)/tool pair.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ConversationStore keeps the message history of the dialogs of one Engine.
// Load returns a copy, so the caller may keep or mutate the slice freely.
type ConversationStore interface {
	Load(id string) []llmMessage
	Append(id string, msgs ...llmMessage)
	Reset(id string)
}

const (
	// conversationLRUSize bounds the number of dialogs kept in memory.
	conversationLRUSize = 64
	// defaultConversationTurns is the default bound of one dialog in user
	// turns (Engine default, §9.2).
	defaultConversationTurns = 12
	// defaultConversationBytes is the default bound of one dialog history.
	defaultConversationBytes = 64 << 10
	// maxConversationIDLen caps a client-supplied dialog id; a longer one is
	// treated as missing instead of becoming an unbounded map key.
	maxConversationIDLen = 128
)

// memoryConversations is an in-memory LRU ConversationStore.
type memoryConversations struct {
	mu       sync.Mutex
	maxTurns int
	maxBytes int
	entries  map[string]*conversationEntry
	// order holds dialog ids from least to most recently used.
	order []string
}

// conversationEntry is one dialog history.
type conversationEntry struct {
	messages []llmMessage
	bytes    int
}

// newMemoryConversations builds the in-memory store. maxTurns bounds the number
// of user turns of one dialog and maxBytes its serialized size; a non-positive
// limit means "unbounded".
func newMemoryConversations(maxTurns int, maxBytes int) ConversationStore {
	return &memoryConversations{
		maxTurns: maxTurns,
		maxBytes: maxBytes,
		entries:  make(map[string]*conversationEntry),
	}
}

// Load returns a copy of the dialog history; an unknown id yields nil.
func (c *memoryConversations) Load(id string) []llmMessage {
	if id == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[id]
	if entry == nil {
		return nil
	}
	c.touchLocked(id)
	return append([]llmMessage(nil), entry.messages...)
}

// Append adds messages to the dialog and trims it to the configured bounds.
func (c *memoryConversations) Append(id string, msgs ...llmMessage) {
	if id == "" || len(msgs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[id]
	if entry == nil {
		c.evictLocked()
		entry = &conversationEntry{}
		c.entries[id] = entry
	}
	c.touchLocked(id)
	entry.messages = append(entry.messages, msgs...)
	trimConversation(entry, c.maxTurns, c.maxBytes)
}

// Reset drops one dialog history.
func (c *memoryConversations) Reset(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[id]; !ok {
		return
	}
	delete(c.entries, id)
	c.forgetLocked(id)
}

// touchLocked marks the dialog as the most recently used one.
func (c *memoryConversations) touchLocked(id string) {
	c.forgetLocked(id)
	c.order = append(c.order, id)
}

// forgetLocked removes the dialog from the LRU order.
func (c *memoryConversations) forgetLocked(id string) {
	for i, existing := range c.order {
		if existing == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// evictLocked drops the least recently used dialog when the store is full.
func (c *memoryConversations) evictLocked() {
	if len(c.entries) < conversationLRUSize || len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.entries, oldest)
}

// trimConversation enforces the turn and byte bounds. The system prompt (the
// first message, when present) is never evicted; whole user turns are dropped
// from the oldest side, so an assistant(tool_calls)/tool pair is never split.
func trimConversation(entry *conversationEntry, maxTurns, maxBytes int) {
	entry.bytes = messagesBytes(entry.messages)
	system := 0
	if len(entry.messages) > 0 && entry.messages[0].Role == "system" {
		system = 1
	}
	groups := turnGroups(entry.messages[system:])
	trimmed := false
	// Always keep at least the newest turn: a single turn larger than the
	// budget is kept as is, because dropping it would lose the very question
	// the model has to answer.
	for len(groups) > 1 {
		overTurns := maxTurns > 0 && len(groups) > maxTurns
		overBytes := maxBytes > 0 && entry.bytes > maxBytes
		if !overTurns && !overBytes {
			break
		}
		groups = groups[1:]
		trimmed = true
	}
	if !trimmed {
		return
	}
	messages := make([]llmMessage, 0, len(entry.messages))
	messages = append(messages, entry.messages[:system]...)
	for _, group := range groups {
		messages = append(messages, group...)
	}
	entry.messages = messages
	entry.bytes = messagesBytes(messages)
}

// turnGroups splits messages into user turns: a new group starts at every user
// message (and at the first message). Assistant and tool messages stay with the
// question they belong to.
func turnGroups(messages []llmMessage) [][]llmMessage {
	groups := make([][]llmMessage, 0, 4)
	for _, msg := range messages {
		if msg.Role == "user" || len(groups) == 0 {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], msg)
	}
	return groups
}

// messagesBytes estimates the serialized size of a message list.
func messagesBytes(messages []llmMessage) int {
	total := 0
	for _, msg := range messages {
		raw, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		total += len(raw)
	}
	return total
}

// newConversationID generates a dialog id for a request without one.
func newConversationID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("conv-%d", time.Now().UnixNano())
}
