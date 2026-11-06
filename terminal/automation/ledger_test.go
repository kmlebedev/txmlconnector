package automation

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// researchWorkflowCall returns one call of the canonical research workflow with
// the given argument form.
func researchWorkflowCall(name, symbol string) ToolCall {
	switch name {
	case "get_history":
		return ToolCall{Name: name, Args: map[string]any{
			"symbol": symbolType(symbol), "periodSec": float64(300), "count": float64(300),
		}}
	case "run_backtest":
		return ToolCall{Name: name, Args: map[string]any{
			"symbol": symbolType(symbol), "strategy": "sma_cross",
			"params": map[string]any{"fast": float64(2), "slow": float64(3)},
		}}
	case "compare_strategies":
		return ToolCall{Name: name, Args: map[string]any{
			"symbol": symbolType(symbol), "periodSec": float64(300),
		}}
	}
	return ToolCall{Name: name}
}

// symbolType returns the symbol as the string argument value.
func symbolType(symbol string) any { return symbol }

// containsString reports whether values contains want.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// recordResearchDialog records the three-call research workflow of one
// conversation: get_history -> run_backtest -> compare_strategies, the example of
// plan Task 18.
func recordResearchDialog(ledger *Ledger, conv, symbol string) int {
	tokens := 0
	for _, name := range []string{"get_history", "run_backtest", "compare_strategies"} {
		ledger.Record(conv, researchWorkflowCall(name, symbol), 100, 20)
		tokens += 120
	}
	return tokens
}

// TestLedgerFindsRepeatedSequence is the plan Task 18 test: the same research
// workflow in 5 dialogs (different tickers, same argument shape) becomes one
// hotspot with Calls=5 and the summed tokens.
func TestLedgerFindsRepeatedSequence(t *testing.T) {
	ledger := New()
	symbols := []string{"TQBR:SBER", "TQBR:GAZP", "TQBR:LKOH", "TQBR:YNDX", "TQBR:GMKN"}
	wantTokens := 0
	for index, symbol := range symbols {
		wantTokens += recordResearchDialog(ledger, fmt.Sprintf("conv-%d", index), symbol)
	}
	hotspots := ledger.Hotspots(5)
	if len(hotspots) != 1 {
		t.Fatalf("hotspots = %+v, want exactly one repeated sequence", hotspots)
	}
	hotspot := hotspots[0]
	wantSequence := []string{"get_history", "run_backtest", "compare_strategies"}
	if !reflect.DeepEqual(hotspot.Sequence, wantSequence) {
		t.Fatalf("sequence = %v, want %v", hotspot.Sequence, wantSequence)
	}
	if hotspot.Calls != 5 {
		t.Fatalf("calls = %d, want 5", hotspot.Calls)
	}
	if hotspot.Tokens != wantTokens {
		t.Fatalf("tokens = %d, want %d", hotspot.Tokens, wantTokens)
	}
	// The example is one concrete occurrence (the first in the deterministic
	// conversation order), not necessarily the insertion order.
	symbol, ok := hotspot.Example["symbol"].(string)
	if !ok || !containsString(symbols, symbol) {
		t.Fatalf("example = %v, want the arguments of one recorded dialog", hotspot.Example)
	}
	if _, ok := hotspot.Example["periodSec"]; !ok {
		t.Fatalf("example = %v, want the full argument map", hotspot.Example)
	}
}

// TestLedgerGroupsByArgumentShape proves the grouping key is the argument form,
// not the concrete argument values: the same tool names with different argument
// keys are different hotspots.
func TestLedgerGroupsByArgumentShape(t *testing.T) {
	ledger := New()
	for index := 0; index < 3; index++ {
		conv := fmt.Sprintf("range-%d", index)
		ledger.Record(conv, ToolCall{Name: "get_history", Args: map[string]any{
			"symbol": "TQBR:SBER", "periodSec": float64(300), "count": float64(300),
		}}, 10, 5)
		ledger.Record(conv, ToolCall{Name: "run_backtest", Args: map[string]any{
			"symbol": "TQBR:SBER", "strategy": "sma_cross",
		}}, 10, 5)
	}
	for index := 0; index < 3; index++ {
		conv := fmt.Sprintf("range-window-%d", index)
		ledger.Record(conv, ToolCall{Name: "get_history", Args: map[string]any{
			"symbol": "TQBR:SBER", "from": "2026-11-01", "to": "2026-11-05",
		}}, 10, 5)
		ledger.Record(conv, ToolCall{Name: "run_backtest", Args: map[string]any{
			"symbol": "TQBR:SBER", "strategy": "sma_cross",
		}}, 10, 5)
	}
	hotspots := ledger.Hotspots(3)
	if len(hotspots) != 2 {
		t.Fatalf("hotspots = %+v, want two sequences (one per argument shape)", hotspots)
	}
	for _, hotspot := range hotspots {
		if hotspot.Calls != 3 {
			t.Fatalf("hotspot %v calls = %d, want 3", hotspot.Sequence, hotspot.Calls)
		}
	}
}

// TestLedgerMinCallsFilters checks the hotness threshold.
func TestLedgerMinCallsFilters(t *testing.T) {
	ledger := New()
	for index := 0; index < 4; index++ {
		recordResearchDialog(ledger, fmt.Sprintf("conv-%d", index), "TQBR:SBER")
	}
	if hotspots := ledger.Hotspots(5); len(hotspots) != 0 {
		t.Fatalf("hotspots at minCalls=5 = %+v, want none", hotspots)
	}
	if hotspots := ledger.Hotspots(4); len(hotspots) != 1 || hotspots[0].Calls != 4 {
		t.Fatalf("hotspots at minCalls=4 = %+v, want the sequence with Calls=4", hotspots)
	}
}

// TestHotspotsDeterministic pins the deterministic order and payload: repeated
// reads and ledgers filled in a different order return the same result.
func TestHotspotsDeterministic(t *testing.T) {
	first := New()
	second := New()
	symbols := []string{"TQBR:SBER", "TQBR:GAZP", "TQBR:LKOH"}
	for index, symbol := range symbols {
		recordResearchDialog(first, fmt.Sprintf("conv-%d", index), symbol)
	}
	for index := len(symbols) - 1; index >= 0; index-- {
		recordResearchDialog(second, fmt.Sprintf("conv-%d", index), symbols[index])
	}
	firstHotspots := first.Hotspots(3)
	secondHotspots := second.Hotspots(3)
	if !reflect.DeepEqual(firstHotspots, secondHotspots) {
		t.Fatalf("hotspots depend on the recording order: %+v vs %+v", firstHotspots, secondHotspots)
	}
	if !reflect.DeepEqual(firstHotspots, first.Hotspots(3)) {
		t.Fatal("two Hotspots calls on the same ledger differ")
	}
}

// TestLedgerConcurrent records from several goroutines: the ledger must be race
// free and lose no call.
func TestLedgerConcurrent(t *testing.T) {
	ledger := New()
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for dialog := 0; dialog < 5; dialog++ {
				conv := fmt.Sprintf("conv-%d-%d", worker, dialog)
				for _, name := range []string{"get_history", "run_backtest", "compare_strategies"} {
					ledger.Record(conv, researchWorkflowCall(name, "TQBR:SBER"), 10, 5)
				}
			}
		}(worker)
	}
	wait.Wait()
	hotspots := ledger.Hotspots(40)
	if len(hotspots) != 1 {
		t.Fatalf("hotspots = %+v, want one sequence seen 40 times", hotspots)
	}
	if hotspots[0].Calls != 40 {
		t.Fatalf("calls = %d, want 40", hotspots[0].Calls)
	}
}

// TestNilLedgerSafe covers the unconfigured-ledger path.
func TestNilLedgerSafe(t *testing.T) {
	var ledger *Ledger
	ledger.Record("conv", ToolCall{Name: "get_history"}, 10, 5)
	if hotspots := ledger.Hotspots(2); hotspots == nil || len(hotspots) != 0 {
		t.Fatalf("nil ledger hotspots = %#v, want an empty non-nil slice", hotspots)
	}
}

// TestLedgerIgnoresNamelessCalls guards the recorder against broken tool data.
func TestLedgerIgnoresNamelessCalls(t *testing.T) {
	ledger := New()
	ledger.Record("conv", ToolCall{Name: "   "}, 10, 5)
	ledger.Record("conv", ToolCall{Name: "get_history"}, -10, -5) // negative usage clamps to 0
	hotspots := ledger.Hotspots(2)
	if len(hotspots) != 0 {
		t.Fatalf("hotspots = %+v, want none", hotspots)
	}
}
