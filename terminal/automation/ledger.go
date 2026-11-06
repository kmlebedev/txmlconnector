// Package automation is the cost bookkeeping of the AI terminal (plan Task 18):
// it records which agent tools ran in which conversation and how many LLM tokens
// each call cost, then surfaces repeated tool sequences whose price in tokens
// makes them candidates for automation — a Go function or an MCP tool instead of
// paying the LLM for the same workflow every time.
package automation

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// maxHotspotSeqLen bounds the enumerated tool sequences: a repetitive agent
// workflow is short (2-5 calls); longer exact repeats are rare and would only
// produce noisy near-duplicates.
const maxHotspotSeqLen = 5

// ToolCall is one executed agent tool call as the ledger sees it. It mirrors the
// JSON shape of agent.ToolCall without importing the agent package: the agent
// depends on this package, not the other way round.
type ToolCall struct {
	Name   string         `json:"name"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// Hotspot is one repeated tool sequence: the same tools in the same order with
// the same argument shape, seen Calls times across the conversations and priced
// Tokens tokens in total. Example is the argument map of the first occurrence of
// the first call (in the deterministic conversation order), so a human can see
// what the sequence was run on.
type Hotspot struct {
	Sequence []string       `json:"sequence"`
	Calls    int            `json:"calls"`
	Tokens   int            `json:"tokens"`
	Example  map[string]any `json:"example,omitempty"`
}

// sequenceKey is one counted occurrence group.
type sequenceKey struct {
	names   []string
	shapes  []string
	calls   int
	tokens  int
	example map[string]any
}

// Ledger collects the tool calls and their LLM token cost per conversation. It is
// safe for concurrent use.
type Ledger struct {
	mu            sync.Mutex
	conversations map[string][]entry
}

// entry is one recorded call of a conversation.
type entry struct {
	name   string
	shape  string
	tokens int
	args   map[string]any
}

// New returns an empty ledger.
func New() *Ledger {
	return &Ledger{conversations: make(map[string][]entry)}
}

// Record appends one executed tool call of the conversation conv. The token
// counters are the usage of the LLM round trip that produced the call (prompt and
// completion); a negative value is treated as 0. A call without a name is
// ignored, and a nil ledger is a no-op so callers never crash on an unconfigured
// ledger.
func (l *Ledger) Record(conv string, call ToolCall, promptTokens, completionTokens int) {
	if l == nil {
		return
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return
	}
	tokens := promptTokens + completionTokens
	if tokens < 0 {
		tokens = 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conversations == nil {
		l.conversations = make(map[string][]entry)
	}
	l.conversations[conv] = append(l.conversations[conv], entry{
		name:   name,
		shape:  argShape(call.Args),
		tokens: tokens,
		args:   copyArgs(call.Args),
	})
}

// Hotspots returns the repeated tool sequences seen at least minCalls times,
// ordered by token cost (descending), then by occurrence count and by the
// sequence itself, so the result is deterministic. Occurrences may overlap inside
// one conversation. A sequence that is a contiguous part of a longer sequence with
// the same number of occurrences is dropped: the longer sequence explains every
// occurrence and is the better automation candidate. minCalls below 2 is raised to
// 2, because a single occurrence is not a repetition.
func (l *Ledger) Hotspots(minCalls int) []Hotspot {
	if l == nil {
		// A nil ledger owns no conversations; return an empty, non-nil slice so
		// the JSON contract is stable.
		return []Hotspot{}
	}
	if minCalls < 2 {
		minCalls = 2
	}
	l.mu.Lock()
	conversations := make([][]entry, 0, len(l.conversations))
	for _, calls := range l.conversations {
		conversations = append(conversations, calls)
	}
	l.mu.Unlock()
	// Sort the conversations by their first recorded tool call: map iteration is
	// random and the hotspot order must be deterministic. Two conversations with
	// identical first call strings may still be ordered arbitrarily; the final
	// sort below does not depend on the insertion order of equal sequences except
	// for the Example of the first occurrence, so the sort key includes the
	// conversation content hash to keep even that stable.
	sort.SliceStable(conversations, func(i, j int) bool {
		return conversationSortKey(conversations[i]) < conversationSortKey(conversations[j])
	})

	counted := make(map[string]*sequenceKey)
	for _, calls := range conversations {
		for length := 2; length <= maxHotspotSeqLen && length <= len(calls); length++ {
			for start := 0; start+length <= len(calls); start++ {
				window := calls[start : start+length]
				key, names, shapes := windowKey(window)
				group := counted[key]
				if group == nil {
					group = &sequenceKey{names: names, shapes: shapes, example: copyArgs(window[0].args)}
					counted[key] = group
				}
				group.calls++
				for _, call := range window {
					group.tokens += call.tokens
				}
			}
		}
	}

	groups := make([]*sequenceKey, 0, len(counted))
	for _, group := range counted {
		if group.calls >= minCalls {
			groups = append(groups, group)
		}
	}
	// Drop the subsequences that a longer counted sequence fully explains.
	kept := make([]*sequenceKey, 0, len(groups))
	for _, candidate := range groups {
		explained := false
		for _, longer := range groups {
			if len(longer.names) <= len(candidate.names) || longer.calls < candidate.calls {
				continue
			}
			if containsSequence(longer.names, candidate.names) && containsSequence(longer.shapes, candidate.shapes) {
				explained = true
				break
			}
		}
		if !explained {
			kept = append(kept, candidate)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].tokens != kept[j].tokens {
			return kept[i].tokens > kept[j].tokens
		}
		if kept[i].calls != kept[j].calls {
			return kept[i].calls > kept[j].calls
		}
		return strings.Join(kept[i].names, ",") < strings.Join(kept[j].names, ",")
	})
	out := make([]Hotspot, len(kept))
	for index, group := range kept {
		out[index] = Hotspot{
			Sequence: append([]string(nil), group.names...),
			Calls:    group.calls,
			Tokens:   group.tokens,
			Example:  group.example,
		}
	}
	return out
}

// windowKey builds the grouping key of one call window: the tool names and the
// argument shapes, both in order.
func windowKey(window []entry) (string, []string, []string) {
	names := make([]string, len(window))
	shapes := make([]string, len(window))
	var builder strings.Builder
	for index, call := range window {
		names[index] = call.name
		shapes[index] = call.shape
		builder.WriteString(call.name)
		builder.WriteByte(0)
		builder.WriteString(call.shape)
		builder.WriteByte(0)
	}
	return builder.String(), names, shapes
}

// containsSequence reports whether sub appears as a contiguous subsequence of
// full.
func containsSequence(full, sub []string) bool {
	if len(sub) == 0 || len(sub) > len(full) {
		return false
	}
	for start := 0; start+len(sub) <= len(full); start++ {
		matched := true
		for offset := range sub {
			if full[start+offset] != sub[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// conversationSortKey returns a deterministic key for one conversation: the tool
// names, their argument shapes, the token cost and the concrete arguments. The
// concrete arguments matter: without them two conversations with different values
// (another ticker) would sort in map-iteration order and the Example of a hotspot
// would change from run to run.
func conversationSortKey(calls []entry) string {
	var builder strings.Builder
	for _, call := range calls {
		builder.WriteString(call.name)
		builder.WriteByte(0)
		builder.WriteString(call.shape)
		builder.WriteByte(0)
		builder.WriteString(strconv.Itoa(call.tokens))
		builder.WriteByte(0)
		if encoded, err := json.Marshal(call.args); err == nil {
			builder.Write(encoded)
		}
		builder.WriteByte(0)
	}
	return builder.String()
}

// argShape canonicalizes the argument structure: every object's key names (in
// sorted order) and every value's type. Calls with the same form of arguments but
// different concrete values — the usual case of a repeated research workflow on
// another ticker — share one shape.
func argShape(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	var builder strings.Builder
	writeShape(&builder, args)
	return builder.String()
}

// writeShape renders the type skeleton of one argument value.
func writeShape(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder.WriteByte('{')
		for _, key := range keys {
			builder.WriteString(strconv.Quote(key))
			builder.WriteByte(':')
			writeShape(builder, typed[key])
			builder.WriteByte(',')
		}
		builder.WriteByte('}')
	case []any:
		builder.WriteByte('[')
		for _, item := range typed {
			writeShape(builder, item)
		}
		builder.WriteByte(']')
	case string:
		builder.WriteByte('s')
	case bool:
		builder.WriteByte('b')
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		builder.WriteByte('n')
	case nil:
		builder.WriteString("null")
	default:
		builder.WriteByte('?')
	}
}

// copyArgs returns a shallow copy of the argument map, so a caller mutating its
// own map cannot change a recorded occurrence.
func copyArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}
