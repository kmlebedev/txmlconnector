package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestToolsCatalog checks the frozen §9.1 tool set and that every ToolInfo
// carries a valid JSON Schema object.
func TestToolsCatalog(t *testing.T) {
	e, _, _ := newTestEngine(t)
	tools := e.Tools()
	if len(tools) != len(toolCatalogNames) {
		t.Fatalf("Tools() returned %d tools, want %d", len(tools), len(toolCatalogNames))
	}
	required := map[string][]string{
		"get_market_hints":   {"symbol"},
		"get_history":        {"symbol", "periodSec"},
		"run_backtest":       {"symbol", "strategy"},
		"compare_strategies": {"symbol"},
		"get_quote":          {"symbol"},
		"place_order":        {"symbol", "side", "qty"},
		"cancel_order":       {"orderId"},
		"start_autonomy":     {"symbol", "strategy"},
	}
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		seen[tool.Name] = true
		if tool.Title == "" || tool.Description == "" {
			t.Errorf("tool %q: empty title/description", tool.Name)
		}
		if !json.Valid(tool.Input) {
			t.Fatalf("tool %q: Input is not valid JSON: %s", tool.Name, tool.Input)
		}
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.Input, &schema); err != nil {
			t.Fatalf("tool %q: schema unmarshal: %v", tool.Name, err)
		}
		if schema.Type != "object" {
			t.Errorf("tool %q: schema type = %q, want object", tool.Name, schema.Type)
		}
		if schema.Properties == nil {
			t.Errorf("tool %q: schema has no properties", tool.Name)
		}
		for _, property := range schema.Required {
			if _, ok := schema.Properties[property]; !ok {
				t.Errorf("tool %q: required %q is not declared in properties", tool.Name, property)
			}
		}
		if want, ok := required[tool.Name]; ok {
			for _, property := range want {
				if _, ok := schema.Properties[property]; !ok {
					t.Errorf("tool %q: missing property %q", tool.Name, property)
				}
				found := false
				for _, item := range schema.Required {
					if item == property {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("tool %q: required list is missing %q", tool.Name, property)
				}
			}
		}
	}
	for _, name := range toolCatalogNames {
		if !seen[name] {
			t.Errorf("tool %q is missing from the catalog", name)
		}
	}
}

// TestNewDefaults verifies the frozen contract defaults.
func TestNewDefaults(t *testing.T) {
	data := newFakeDataSource()
	e, err := New(Config{}, data, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e.cfg.DefaultPeriodSec != 300 {
		t.Errorf("DefaultPeriodSec = %d, want 300", e.cfg.DefaultPeriodSec)
	}
	if e.cfg.LLM.Model != "deepseek-chat" {
		t.Errorf("LLM.Model = %q, want deepseek-chat", e.cfg.LLM.Model)
	}
	if e.cfg.LLM.Timeout != 90*time.Second {
		t.Errorf("LLM.Timeout = %v, want 90s", e.cfg.LLM.Timeout)
	}
	if e.cfg.LLM.Enabled {
		t.Error("LLM must be disabled without base URL and API key")
	}
	if e.cfg.FeeBps != 5 {
		t.Errorf("FeeBps = %v, want 5", e.cfg.FeeBps)
	}
	if e.cfg.BacktestHistory != 20 {
		t.Errorf("BacktestHistory = %d, want 20", e.cfg.BacktestHistory)
	}
	want := RiskLimits{MaxPositionPct: 25, MaxOrderPct: 20, StopLossPct: 2, TakeProfitPct: 5, DailyLossPct: 3, MaxTradesPerHour: 30}
	if e.cfg.Risk != want {
		t.Errorf("Risk = %+v, want %+v", e.cfg.Risk, want)
	}
}

func TestNewRequiresDataSource(t *testing.T) {
	if _, err := New(Config{}, nil, nil); err == nil {
		t.Fatal("New with nil data source must fail")
	}
}

// TestLLMEnabledIsComputed checks Enabled = BaseURL != "" && APIKey != "".
func TestLLMEnabledIsComputed(t *testing.T) {
	data := newFakeDataSource()
	e, err := New(Config{LLM: LLMConfig{BaseURL: "http://127.0.0.1:1/v1"}}, data, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e.cfg.LLM.Enabled {
		t.Error("LLM must stay disabled with a base URL but no key")
	}
	if status := e.Status().LLM; status.Enabled || !strings.Contains(status.Note, "локальный аналитик") {
		t.Errorf("LLMStatus = %+v, want disabled local analyst", status)
	}
	e, err = New(Config{LLM: LLMConfig{BaseURL: "http://127.0.0.1:1/v1/", APIKey: "k"}}, data, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !e.cfg.LLM.Enabled {
		t.Error("LLM must be enabled with both base URL and API key")
	}
	if e.cfg.LLM.BaseURL != "http://127.0.0.1:1/v1" {
		t.Errorf("BaseURL = %q, want trimmed base URL", e.cfg.LLM.BaseURL)
	}
	if status := e.Status().LLM; !status.Enabled || status.Model != "deepseek-chat" {
		t.Errorf("LLMStatus = %+v, want enabled deepseek-chat", status)
	}
}

// TestResolveSymbol expands bare tickers and keeps default fallbacks.
func TestResolveSymbol(t *testing.T) {
	e, _, _ := newTestEngine(t)
	cases := map[string]string{
		"":          "TQBR:SBER",
		"SBER":      "TQBR:SBER",
		"sber":      "TQBR:SBER",
		"TQBR:GAZP": "TQBR:GAZP",
		"GAZP":      "TQBR:GAZP",
	}
	for input, want := range cases {
		if got := e.resolveSymbol(input); got != want {
			t.Errorf("resolveSymbol(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestNormalizePeriod accepts only the frozen candle periods.
func TestNormalizePeriod(t *testing.T) {
	e, _, _ := newTestEngine(t)
	for _, period := range []int{60, 300, 900, 3600, 86400} {
		if got := e.normalizePeriod(period); got != period {
			t.Errorf("normalizePeriod(%d) = %d", period, got)
		}
	}
	for _, period := range []int{0, -1, 42, 7200} {
		if got := e.normalizePeriod(period); got != 300 {
			t.Errorf("normalizePeriod(%d) = %d, want default 300", period, got)
		}
	}
}

// TestSubscribeDeliversAndCancels checks the SSE sink lifecycle.
func TestSubscribeDeliversAndCancels(t *testing.T) {
	e, _, _ := newTestEngine(t)
	var mu sync.Mutex
	var events []Event
	cancel := e.Subscribe(func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	if _, _ = e.executeToolCall(context.Background(), "list_instruments", nil); len(events) == 0 {
		t.Fatal("expected tool_call/tool_result events")
	}
	cancel()
	cancel() // idempotent
	mu.Lock()
	before := len(events)
	mu.Unlock()
	_, _ = e.executeToolCall(context.Background(), "get_strategies", nil)
	mu.Lock()
	after := len(events)
	mu.Unlock()
	if after != before {
		t.Fatalf("events after cancel: got %d new events", after-before)
	}
	// A nil sink must not panic.
	e.Subscribe(nil)
}

// TestBacktestsHistoryLimit keeps only cfg.BacktestHistory results.
func TestBacktestsHistoryLimit(t *testing.T) {
	data := newFakeDataSource()
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER", BacktestHistory: 2}, data, newFakeBroker(1_000_000))
	for i := 0; i < 3; i++ {
		call, _ := e.executeToolCall(context.Background(), "run_backtest", map[string]any{
			"symbol": "SBER", "strategy": "sma_cross", "count": 100,
		})
		if call.Error != "" {
			t.Fatalf("run_backtest error: %s", call.Error)
		}
	}
	if got := len(e.Backtests()); got != 2 {
		t.Fatalf("Backtests() = %d results, want 2", got)
	}
}

// TestSubscribePanicIsRecovered makes sure a bad sink cannot kill the engine.
func TestSubscribePanicIsRecovered(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.Subscribe(func(Event) { panic("sink failure") })
	if _, artifact := e.executeToolCall(context.Background(), "list_instruments", nil); artifact != nil {
		t.Fatal("list_instruments must not produce an artifact")
	}
}
