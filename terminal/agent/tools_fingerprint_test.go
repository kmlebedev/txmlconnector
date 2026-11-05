package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
)

// lobDataSource is the fakeDataSource extension the order-book research tools
// read: a recorded md.book_events/md.trades tape per instrument. The production
// cmd/terminal adapter does not implement LOBDataSource yet, so the tools report
// a configuration error there (see docs/ai-terminal/fingerprint.md).
type lobDataSource struct {
	*fakeDataSource
	events map[string][]lobreplay.Event
	err    error
}

func (f *lobDataSource) LOBEvents(_ context.Context, symbol string, from, to time.Time) ([]lobreplay.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	events, ok := f.events[symbol]
	if !ok {
		return nil, fmt.Errorf("no recorded tape for %s", symbol)
	}
	return events, nil
}

// lobTestTape builds a one-minute recorded tape with an injected TWAP schedule:
// 15 equal clips of 25 units every two seconds inside the first analysis window.
// It is the ground truth of the tool tests.
func lobTestTape(t *testing.T) []lobreplay.Event {
	t.Helper()
	start := time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(7))
	price := func(units int64) model.Decimal { return model.Decimal{Units: units, Scale: 6} }
	events := make([]lobreplay.Event, 0, 128)
	for level := int64(1); level <= 5; level++ {
		step := level * 10000 // 0.01
		events = append(events,
			lobreplay.Event{TS: start, Kind: lobreplay.KindSnapshot, Side: lobreplay.SideBid,
				Price: price(100_000_000 - step), Qty: 500},
			lobreplay.Event{TS: start, Kind: lobreplay.KindSnapshot, Side: lobreplay.SideAsk,
				Price: price(100_000_000 + step), Qty: 500},
		)
	}
	tradeNo := int64(0)
	appendTrade := func(ts time.Time, side string, qty int64) {
		tradeNo++
		events = append(events, lobreplay.Event{
			TS: ts, Kind: lobreplay.KindTrade, Side: side,
			Price: price(100_000_000), Qty: qty,
			Trade: &lobreplay.Trade{TradeNo: tradeNo},
		})
	}
	// Random background trades: one per second, both sides, sizes 5..54.
	for i := 0; i < 40; i++ {
		side := lobreplay.SideBuy
		if rng.Intn(2) == 0 {
			side = lobreplay.SideSell
		}
		appendTrade(start.Add(time.Duration(i)*time.Second+time.Duration(rng.Intn(400))*time.Millisecond),
			side, int64(5+rng.Intn(50)))
	}
	// The injected TWAP schedule: equal clips at a fixed two-second interval.
	for i := 0; i < 15; i++ {
		appendTrade(start.Add(5*time.Second+time.Duration(i)*2*time.Second), lobreplay.SideBuy, 25)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS.Before(events[j].TS) })
	return events
}

// detectExecutionArgs is a valid detect_execution_algos argument set.
func detectExecutionArgs() map[string]any {
	return map[string]any{
		"symbol": "TQBR:SBER",
		"from":   "2026-11-05T07:00:00Z",
		"to":     "2026-11-05T07:10:00Z",
	}
}

// newLOBToolEngineWithConfig builds an engine whose data source also serves the
// recorded LOB tape of TQBR:SBER.
func newLOBToolEngineWithConfig(t *testing.T, cfg Config) *Engine {
	t.Helper()
	data := newFakeDataSource()
	lob := &lobDataSource{
		fakeDataSource: data,
		events:         map[string][]lobreplay.Event{"TQBR:SBER": lobTestTape(t)},
	}
	return newTestEngineWith(t, cfg, lob, newFakeBroker(1_000_000))
}

// newLOBToolEngine builds the LOB tool engine with the usual test default symbol.
func newLOBToolEngine(t *testing.T) *Engine {
	return newLOBToolEngineWithConfig(t, Config{DefaultSymbol: "TQBR:SBER"})
}

// TestDetectExecutionAlgosTool runs the tool through the core dispatch and checks
// the footprint, the artifact and the LLM payload budget.
func TestDetectExecutionAlgosTool(t *testing.T) {
	e := newLOBToolEngine(t)
	result, artifact, err := e.invokeTool(context.Background(), toolInvocation{},
		toolNameDetectExecutionAlgos, detectExecutionArgs())
	if err != nil {
		t.Fatalf("detect_execution_algos: %v", err)
	}
	res, ok := result.(executionAlgosResult)
	if !ok {
		t.Fatalf("result type = %T, want executionAlgosResult", result)
	}
	if res.Symbol != "TQBR:SBER" || res.From.IsZero() || res.To.IsZero() {
		t.Fatalf("result header = %+v", res)
	}
	if len(res.Footprints) == 0 {
		t.Fatalf("no footprints on the synthetic TWAP tape: warnings %v", res.Warnings)
	}
	if res.Footprints[0].Kind != "twap" || res.Footprints[0].Side != "buy" {
		t.Fatalf("first footprint = %+v, want twap/buy", res.Footprints[0])
	}
	if res.Footprints[0].Confidence < 0.6 {
		t.Fatalf("confidence = %.3f, want a reportable footprint", res.Footprints[0].Confidence)
	}
	if artifact == nil || artifact.Kind != "execution_algos" {
		t.Fatalf("artifact = %+v, want kind execution_algos", artifact)
	}
	ui, ok := artifact.Payload.(executionAlgosArtifact)
	if !ok {
		t.Fatalf("artifact payload type = %T, want executionAlgosArtifact", artifact.Payload)
	}
	if len(ui.Footprints) != len(res.Footprints) {
		t.Fatalf("artifact footprints = %d, result footprints = %d", len(ui.Footprints), len(res.Footprints))
	}
	// The model payload must stay small (plan Task 18 budget, checked early).
	payload, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(payload) > 2048 {
		t.Fatalf("model payload = %d bytes, want <= 2048", len(payload))
	}
}

// TestDetectExecutionAlgosToolWithoutLOBSource proves the tool degrades to a
// clear configuration error when the terminal has no recorded order book.
func TestDetectExecutionAlgosToolWithoutLOBSource(t *testing.T) {
	e, _, _ := newTestEngine(t)
	_, _, err := e.invokeTool(context.Background(), toolInvocation{},
		toolNameDetectExecutionAlgos, detectExecutionArgs())
	if err == nil || !strings.Contains(err.Error(), "LOBDataSource") {
		t.Fatalf("error = %v, want a LOBDataSource configuration error", err)
	}
}

// TestDetectExecutionAlgosToolErrors pins the model-facing argument errors.
func TestDetectExecutionAlgosToolErrors(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		mutate func(map[string]any)
		want   string
	}{
		// Without a default symbol an omitted symbol is an error, not a silent
		// fallback to some instrument.
		{"missing symbol", Config{}, func(args map[string]any) { delete(args, "symbol") }, "symbol"},
		{"bad from", Config{DefaultSymbol: "TQBR:SBER"}, func(args map[string]any) { args["from"] = "yesterday" }, "RFC3339"},
		{"to before from", Config{DefaultSymbol: "TQBR:SBER"}, func(args map[string]any) { args["to"] = "2026-11-05T06:00:00Z" }, "must be after"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newLOBToolEngineWithConfig(t, tc.cfg)
			args := detectExecutionArgs()
			tc.mutate(args)
			_, _, handled, err := e.invokeResearchTool(context.Background(), toolNameDetectExecutionAlgos, args)
			if !handled {
				t.Fatal("detect_execution_algos was not handled by the research dispatch")
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestDetectExecutionAlgosToolIsWired checks the frozen catalog wiring: the tool
// is in Engine.Tools() and in toolCatalogNames.
func TestDetectExecutionAlgosToolIsWired(t *testing.T) {
	e, _, _ := newTestEngine(t)
	catalog := make(map[string]bool, len(e.Tools()))
	for _, tool := range e.Tools() {
		catalog[tool.Name] = true
	}
	if !catalog[toolNameDetectExecutionAlgos] {
		t.Fatalf("%q is missing from Engine.Tools()", toolNameDetectExecutionAlgos)
	}
	named := make(map[string]bool, len(toolCatalogNames))
	for _, name := range toolCatalogNames {
		named[name] = true
	}
	if !named[toolNameDetectExecutionAlgos] {
		t.Fatalf("%q is missing from toolCatalogNames", toolNameDetectExecutionAlgos)
	}
}
