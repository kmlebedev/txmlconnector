package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native/model"
	"github.com/kmlebedev/txmlconnector/terminal/automation"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/fingerprint"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

// countToolMessages returns the number of tool-result messages of a provider
// request: it is the iteration counter of the fake provider below.
func countToolMessages(req llmChatRequest) int {
	count := 0
	for _, message := range req.Messages {
		if message.Role == "tool" {
			count++
		}
	}
	return count
}

// TestLLMUsageRecordedInAutomationLedger checks the plan Task 18 token
// bookkeeping: the usage of an OpenAI-compatible response is parsed and split
// across the tool calls the response carried, and two dialogs running the same
// two-step workflow become an AutomationHotspot with the summed token price.
func TestLLMUsageRecordedInAutomationLedger(t *testing.T) {
	saved := automationLedger
	defer func() { automationLedger = saved }()
	automationLedger = automation.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llmChatRequest
		_ = json.Unmarshal(body, &req)
		usage := map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}
		var payload map[string]any
		switch countToolMessages(req) {
		case 0:
			payload = toolCallPayload("get_quote", `{"symbol":"TQBR:SBER"}`)
		case 1:
			payload = toolCallPayload("get_history", `{"symbol":"TQBR:SBER","periodSec":300,"count":100}`)
		default:
			payload = textPayload("Готово: котировка и история получены.")
		}
		payload["usage"] = usage
		writeJSONResponse(w, payload)
	}))
	defer srv.Close()

	e, _ := newLLMEngine(t, srv.URL, 2*time.Second)
	for _, conversationID := range []string{"usage-conv-1", "usage-conv-2"} {
		resp, err := e.Chat(context.Background(), ChatRequest{
			Message: "покажи котировку и историю SBER", ConversationID: conversationID,
		})
		if err != nil {
			t.Fatalf("Chat(%s): %v", conversationID, err)
		}
		if len(resp.ToolCalls) != 2 {
			t.Fatalf("Chat(%s) tool calls = %+v, want 2", conversationID, resp.ToolCalls)
		}
	}

	hotspots := e.AutomationHotspots(2)
	if len(hotspots) != 1 {
		t.Fatalf("hotspots = %+v, want the repeated two-tool workflow", hotspots)
	}
	hotspot := hotspots[0]
	wantSequence := []string{"get_quote", "get_history"}
	if !reflect.DeepEqual(hotspot.Sequence, wantSequence) {
		t.Fatalf("sequence = %v, want %v", hotspot.Sequence, wantSequence)
	}
	if hotspot.Calls != 2 {
		t.Fatalf("calls = %d, want 2 (one per dialog)", hotspot.Calls)
	}
	// Each round trip carries one tool call and 120 usage tokens (100 prompt +
	// 20 completion); two tool round trips per dialog, two dialogs -> 480 tokens,
	// no token counted twice.
	if hotspot.Tokens != 480 {
		t.Fatalf("tokens = %d, want 480", hotspot.Tokens)
	}
}

// TestResearchToolsRequireAsOf is the plan Task 18 point-in-time guard: every
// research tool must refuse a call without asOf and tell the model what to pass.
// The point-in-time argument is not optional — a research request has no implicit
// "now", otherwise the answer silently depends on the wall clock.
func TestResearchToolsRequireAsOf(t *testing.T) {
	e, _, _ := newTestEngine(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{toolNameWalkForward, map[string]any{
			"symbol": "TQBR:SBER", "strategy": "sma_cross", "trainBars": 10, "testBars": 5,
		}},
		{toolNameVolForecast, map[string]any{"symbol": "TQBR:SBER"}},
		{toolNameVolEvents, map[string]any{
			"from": "2026-11-01T00:00:00Z", "to": "2026-11-05T00:00:00Z",
		}},
		{toolNameDetectExecutionAlgos, map[string]any{
			"symbol": "TQBR:SBER", "from": "2026-11-05T07:00:00Z", "to": "2026-11-05T07:10:00Z",
		}},
		{"lob_backtest", map[string]any{
			"symbol": "TQBR:SBER", "from": "2026-11-05T07:00:00Z", "to": "2026-11-05T07:10:00Z",
			"strategy": "imbalance",
		}},
		{toolNameGetNews, map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := e.invokeTool(context.Background(), toolInvocation{}, tc.name, tc.args)
			if err == nil {
				t.Fatalf("%s accepted a call without asOf", tc.name)
			}
			if !strings.Contains(err.Error(), "asOf") {
				t.Fatalf("%s error = %v, want a hint about the required asOf", tc.name, err)
			}
		})
	}
}

// TestToolResultCompactUnder2KB is the plan Task 18 context budget guard: the
// payload sent back to the model stays under 2 KB for every research result, no
// matter how large the full artifact is.
func TestToolResultCompactUnder2KB(t *testing.T) {
	news := make([]NewsItem, 0, 200)
	longBody := strings.Repeat("Подробный текст новости про рынок и ставку. ", 40)
	longTitle := strings.Repeat("Заголовок новости ", 10)
	for index := 0; index < 200; index++ {
		news = append(news, NewsItem{
			ID: int64(index + 1), FirstSeen: testNow.Add(-time.Duration(index) * time.Minute),
			Published: testNow.Add(-time.Duration(index+1) * time.Minute),
			Source:    "SRC", Title: longTitle, Body: longBody,
		})
	}
	equity := make([]backtest.EquityPoint, 0, 1000)
	for index := 0; index < 1000; index++ {
		equity = append(equity, backtest.EquityPoint{
			Time: testNow.Add(time.Duration(index) * time.Minute), Equity: 1_000_000 + float64(index),
		})
	}
	trades := make([]backtest.Trade, 0, 200)
	for index := 0; index < 200; index++ {
		trades = append(trades, backtest.Trade{
			EntryTime: testNow.Add(time.Duration(index) * time.Minute), Side: "long", Qty: 10, PnL: 5, PnLPct: 0.1,
		})
	}
	footprints := make([]fingerprint.Footprint, 0, 20)
	for index := 0; index < 20; index++ {
		footprints = append(footprints, fingerprint.Footprint{
			Kind: fingerprint.KindTWAP, Symbol: "TQBR:SBER", Side: "buy",
			From: testNow.Add(time.Duration(index) * time.Minute), To: testNow.Add(time.Duration(index+1) * time.Minute),
			Confidence: 0.9,
			Evidence: map[string]float64{
				"trades": 120, "volume": 5000, "mode_qty": 25, "mode_share": 0.33,
				"clip_count": 20, "interval_regularity": 0.95, "interval_cv": 0.02,
				"median_interval_ms": 2000, "autocorr_peak": 0.6, "spectral_peak": 0.08,
			},
		})
	}
	cases := []struct {
		name   string
		result any
	}{
		{toolNameGetNews, newsResult{AsOf: testNow, Since: testNow.Add(-24 * time.Hour), News: news, ArtifactID: "news-1"}},
		{"run_backtest", backtest.Result{
			Symbol: "TQBR:SBER", Strategy: "sma_cross", PeriodSec: 300,
			Metrics: backtest.Metrics{Trades: len(trades), Bars: len(equity), InitialCash: 1_000_000},
			Equity:  equity, Trades: trades,
		}},
		{toolNameDetectExecutionAlgos, executionAlgosResult{
			Symbol: "TQBR:SBER", From: testNow, To: testNow.Add(time.Hour),
			Footprints: footprints, Warnings: []string{"отпечатков не найдено"},
		}},
		{toolNameWalkForward, walkForwardView{
			Symbol: "TQBR:SBER", Strategy: "sma_cross", PeriodSec: 300, Trials: 12, Folds: 5,
			OOS:            backtest.Metrics{Trades: 40, Bars: 500, InitialCash: 1_000_000},
			IS:             backtest.Metrics{Trades: 120, Bars: 1500, InitialCash: 1_000_000},
			DeflatedSharpe: 0.8, PBO: 0.25, Warnings: []string{"small_sample: мало сделок"},
		}},
		{toolNameVolEvents, volEventsResult{
			AsOf: testNow, From: testNow.Add(-24 * time.Hour), To: testNow.Add(24 * time.Hour),
			Events: func() []volEvent {
				out := make([]volEvent, 0, maxVolEvents)
				for index := 0; index < maxVolEvents; index++ {
					out = append(out, volEvent{
						At: testNow.Add(time.Duration(index) * time.Hour), Category: "us_cpi",
						Title: strings.Repeat("Заседание и публикация CPI ", 8), Country: "US", Exact: index%2 == 0,
					})
				}
				return out
			}(),
			News: 120, Positive: 30, Negative: 20,
			Warnings: []string{"в artifact: первые 50 из 120 релизов"}, ArtifactID: "vol_events-1",
		}},
		{toolNameLOBBacktest, lobBacktestResult{
			Symbol: "TQBR:SBER", Strategy: "imbalance", AsOf: testNow,
			From: testNow.Add(-time.Hour), To: testNow,
			Metrics: backtest.Metrics{
				InitialCash: 1_000_000, FinalEquity: 1_000_120, TotalReturnPct: 0.012,
				MaxDrawdownPct: -0.4, WinRatePct: 55.5, ProfitFactor: 1.7, AvgTradePct: 0.03,
				ExposurePct: 42.5, Trades: 50, Bars: 3600,
			},
			Events: 5000, Orders: 200, Fills: 180, RoundTrips: 50, UnfilledQty: 10,
			Warnings:   []string{"no_costs: FeeBps=0 — комиссия не взимается", "позиция открыта на конце потока: 10 ед."},
			ArtifactID: "lob_backtest-1",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := toolMessageContent(ToolCall{Name: tc.name, Result: tc.result})
			if len(content) > 2048 {
				t.Fatalf("model payload = %d bytes, want <= 2048", len(content))
			}
		})
	}
}

// lobBacktestTape builds a recorded tape with a strong bid imbalance: five bid
// levels of 1000 units against five ask levels of 100 units. The imbalance
// strategy of lob_backtest therefore sends market buys that walk the ask book.
func lobBacktestTape(t *testing.T) []lobreplay.Event {
	t.Helper()
	price := func(units int64) model.Decimal { return model.Decimal{Units: units, Scale: 6} }
	events := make([]lobreplay.Event, 0, 80)
	for level := int64(1); level <= 5; level++ {
		step := level * 10000 // 0.01
		events = append(events,
			lobreplay.Event{TS: testNow, Kind: lobreplay.KindSnapshot, Side: lobreplay.SideBid,
				Price: price(100_000_000 - step), Qty: 1000},
			lobreplay.Event{TS: testNow, Kind: lobreplay.KindSnapshot, Side: lobreplay.SideAsk,
				Price: price(100_000_000 + step), Qty: 100},
		)
	}
	for index := 0; index < 40; index++ {
		events = append(events, lobreplay.Event{
			TS: testNow.Add(time.Duration(index) * time.Second), Kind: lobreplay.KindTrade,
			Side: lobreplay.SideBuy, Price: price(100_000_000), Qty: 5,
			Trade: &lobreplay.Trade{TradeNo: int64(index + 1)},
		})
	}
	return events
}

// TestLobBacktestTool runs the order-book replay tool on a recorded tape through
// the core dispatch: the imbalance strategy must trade, the metrics must be a
// backtest.Metrics, and the full replay result must be reachable by artifactId.
func TestLOBBacktestTool(t *testing.T) {
	data := newFakeDataSource()
	lob := &lobDataSource{
		fakeDataSource: data,
		events:         map[string][]lobreplay.Event{"TQBR:SBER": lobBacktestTape(t)},
	}
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, lob, newFakeBroker(1_000_000))
	args := map[string]any{
		"symbol":   "TQBR:SBER",
		"asOf":     testNow.Add(5 * time.Minute).Format(time.RFC3339),
		"from":     testNow.Format(time.RFC3339),
		"to":       testNow.Add(10 * time.Minute).Format(time.RFC3339),
		"strategy": "imbalance",
	}
	result, artifact, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameLOBBacktest, args)
	if err != nil {
		t.Fatalf("lob_backtest: %v", err)
	}
	view, ok := result.(lobBacktestResult)
	if !ok {
		t.Fatalf("result type = %T, want lobBacktestResult", result)
	}
	if view.Symbol != "TQBR:SBER" || view.Strategy != "imbalance" || view.AsOf.IsZero() {
		t.Fatalf("result header = %+v", view)
	}
	if view.Fills == 0 || view.Orders == 0 {
		t.Fatalf("no fills on the imbalanced tape: orders %d, fills %d, warnings %v",
			view.Orders, view.Fills, view.Warnings)
	}
	if view.ArtifactID == "" {
		t.Fatal("lob_backtest returned no artifactId")
	}
	if artifact == nil || artifact.Kind != "lob_backtest" {
		t.Fatalf("artifact = %+v, want kind lob_backtest", artifact)
	}
	if stored, ok := toolArtifacts.get(view.ArtifactID); !ok {
		t.Fatalf("artifact %q is not stored", view.ArtifactID)
	} else if full, ok := stored.(lobBacktestArtifact); !ok || len(full.Result.Fills) != view.Fills {
		t.Fatalf("stored artifact = %#v, want the full playback result", stored)
	}
	content := toolMessageContent(ToolCall{Name: toolNameLOBBacktest, Result: view})
	if len(content) > 2048 {
		t.Fatalf("model payload = %d bytes, want <= 2048", len(content))
	}
	// Unknown strategies and a missing LOB source are model-facing errors.
	badStrategy := map[string]any{}
	for key, value := range args {
		badStrategy[key] = value
	}
	badStrategy["strategy"] = "mystery"
	if _, _, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameLOBBacktest, badStrategy); err == nil ||
		!strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unknown strategy error = %v", err)
	}
	plain, _, _ := newTestEngine(t)
	if _, _, err := plain.invokeTool(context.Background(), toolInvocation{}, toolNameLOBBacktest, args); err == nil ||
		!strings.Contains(err.Error(), "LOBDataSource") {
		t.Fatalf("missing LOB source error = %v", err)
	}
}

// TestWalkForwardAsOfHidesFutureBars checks the point-in-time rule end to end:
// the walk-forward result never spans a bar that closes after asOf.
func TestWalkForwardAsOfHidesFutureBars(t *testing.T) {
	e, data, _ := newTestEngine(t)
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{300: syntheticBars(300, 100, 0.1)}
	asOf := testNow.Add(60 * time.Minute)
	args := map[string]any{
		"symbol":    "TQBR:SBER",
		"asOf":      asOf.Format(time.RFC3339),
		"periodSec": int(300),
		"count":     int(300),
		"strategy":  "sma_cross",
		"grid":      map[string]any{"fast": []any{2, 3}, "slow": []any{5, 8}},
		"trainBars": int(20),
		"testBars":  int(10),
	}
	result, artifact, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameWalkForward, args)
	if err != nil {
		t.Fatalf("walk_forward: %v", err)
	}
	view, ok := result.(walkForwardView)
	if !ok {
		t.Fatalf("result type = %T, want walkForwardView", result)
	}
	if !view.AsOf.Equal(asOf) {
		t.Fatalf("asOf = %v, want %v", view.AsOf, asOf)
	}
	if view.OOS.To.After(asOf) {
		t.Fatalf("walk-forward OOS reaches %v, after asOf %v", view.OOS.To, asOf)
	}
	if view.ArtifactID == "" {
		t.Fatal("walk_forward returned no artifactId")
	}
	if artifact == nil || artifact.Kind != "walk_forward" {
		t.Fatalf("artifact = %+v, want kind walk_forward", artifact)
	}
	if _, ok := toolArtifacts.get(view.ArtifactID); !ok {
		t.Fatalf("artifact %q is not stored", view.ArtifactID)
	}
}

// TestPointInTimeFilters covers the shared point-in-time helpers used by all
// order-book and bar research tools.
func TestPointInTimeFilters(t *testing.T) {
	price := model.Decimal{Units: 100_000_000, Scale: 6}
	events := []lobreplay.Event{
		{TS: testNow, Kind: lobreplay.KindLevel, Side: lobreplay.SideBid, Price: price, Qty: 1},
		{TS: testNow.Add(time.Minute), Kind: lobreplay.KindLevel, Side: lobreplay.SideBid, Price: price, Qty: 2},
		{TS: testNow.Add(2 * time.Minute), Kind: lobreplay.KindLevel, Side: lobreplay.SideBid, Price: price, Qty: 3},
	}
	if got := eventsUpTo(events, testNow.Add(time.Minute)); len(got) != 2 {
		t.Fatalf("eventsUpTo = %d events, want 2", len(got))
	}
	bars := []backtest.Bar{
		{Time: testNow}, {Time: testNow.Add(time.Minute)}, {Time: testNow.Add(2 * time.Minute)},
	}
	// Closed 1-minute bars: the first two close at or before testNow+2m.
	if got := barsUpTo(bars, testNow.Add(2*time.Minute), 60); len(got) != 2 {
		t.Fatalf("barsUpTo = %d bars, want 2", len(got))
	}
}

// periodBars builds n flat OHLCV bars spaced period apart, starting at testNow.
// The explicit spacing keeps the open/close geometry of a real feed so the
// bar-close point-in-time rule is observable.
func periodBars(n int, period time.Duration) []backtest.Bar {
	bars := make([]backtest.Bar, n)
	for i := range bars {
		price := 100 + float64(i)*0.1
		bars[i] = backtest.Bar{
			Time:   testNow.Add(time.Duration(i) * period),
			Open:   price,
			High:   price + 0.1,
			Low:    price - 0.1,
			Close:  price + 0.05,
			Volume: int64(1000 + i),
		}
	}
	return bars
}

// TestBarsUpToClosedBarsOnly pins the point-in-time bar filter: a bar is known
// only once its candle is closed (close = open + period). A mid-bar asOf, a
// still forming candle, an unknown open time and an unknown period must all be
// excluded, so a research tool can never train on a bar that is not finished.
func TestBarsUpToClosedBarsOnly(t *testing.T) {
	const periodSec = 300
	period := time.Duration(periodSec) * time.Second
	bars := periodBars(3, period)
	bars[2].Partial = true
	bars = append(bars, backtest.Bar{Time: time.Time{}, Open: 1, Close: 1})

	// Mid-bar asOf: bar 0 closes at testNow+5m, bar 1 only at testNow+10m.
	midBar := testNow.Add(period + period/2)
	got := barsUpTo(bars, midBar, periodSec)
	if len(got) != 1 || !got[0].Time.Equal(bars[0].Time) {
		t.Fatalf("barsUpTo(mid-bar) = %d bars, want 1 (only the closed bar 0)", len(got))
	}
	// Exact close: bar 1 becomes known exactly at its close; the partial bar 2
	// opens at the same instant but is not closed yet and stays out.
	got = barsUpTo(bars, testNow.Add(2*period), periodSec)
	if len(got) != 2 {
		t.Fatalf("barsUpTo(exact close) = %d bars, want 2 (bar 0 and bar 1)", len(got))
	}
	for _, bar := range got {
		if bar.Partial {
			t.Fatalf("barsUpTo included the partial bar at %v", bar.Time)
		}
	}
	// Partial must stay excluded even after its nominal close; this isolates
	// the Partial flag from the time-based exclusion above.
	if got := barsUpTo(bars, testNow.Add(4*period), periodSec); len(got) != 2 {
		t.Fatalf("barsUpTo(past partial close) = %d bars, want 2", len(got))
	}
	// Mid-bar at the very open: the first bar is open but not closed.
	if got := barsUpTo(bars, testNow, periodSec); len(got) != 0 {
		t.Fatalf("barsUpTo(open) = %d bars, want 0", len(got))
	}
	// An unknown open time cannot be proven to be in the past: fail closed.
	if got := barsUpTo([]backtest.Bar{{Time: time.Time{}}}, testNow.Add(time.Hour), periodSec); len(got) != 0 {
		t.Fatalf("barsUpTo(zero time) = %d bars, want 0", len(got))
	}
	// An unknown period cannot prove the close: fail closed too.
	if got := barsUpTo(periodBars(3, period), midBar, 0); len(got) != 0 {
		t.Fatalf("barsUpTo(zero period) = %d bars, want 0", len(got))
	}
}

// volFakeDataSource adds the vintage layer to the deterministic bar fake so
// vol_forecast reaches the volatility oracle.
type volFakeDataSource struct{ *fakeDataSource }

func (volFakeDataSource) Releases(context.Context, time.Time, time.Time, ...string) ([]vintage.Release, error) {
	return nil, nil
}

func (volFakeDataSource) News(context.Context, time.Time, time.Time, ...string) ([]vintage.NewsItem, error) {
	return nil, nil
}

// TestWalkForwardPointInTimeUsesBarClose checks the walk_forward wiring of the
// bar period: the tool hands the actual period to the point-in-time filter and
// drops the forming candle. Eight closed 15-minute bars give exactly one fold;
// an open-time filter (or a wrong period) lets the partial and future bars
// through and produces more folds.
func TestWalkForwardPointInTimeUsesBarClose(t *testing.T) {
	e, data, _ := newTestEngine(t)
	const periodSec = 900
	period := time.Duration(periodSec) * time.Second
	bars := periodBars(12, period)
	bars[8].Partial = true
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{periodSec: bars}
	// Mid-bar asOf: bars 0..7 are closed, bar 8 is forming, 9..11 are future.
	asOf := testNow.Add(8*period + period/2)
	args := map[string]any{
		"symbol":    "TQBR:SBER",
		"asOf":      asOf.Format(time.RFC3339),
		"periodSec": periodSec,
		"count":     len(bars),
		"strategy":  "sma_cross",
		"grid":      map[string]any{"fast": []any{2, 3}, "slow": []any{5, 8}},
		"trainBars": 4,
		"testBars":  2,
		"stepBars":  3,
	}
	result, artifact, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameWalkForward, args)
	if err != nil {
		t.Fatalf("walk_forward: %v", err)
	}
	view, ok := result.(walkForwardView)
	if !ok {
		t.Fatalf("result type = %T, want walkForwardView", result)
	}
	if view.Folds != 1 {
		t.Fatalf("folds = %d, want 1: only the closed bars up to asOf may reach the folds", view.Folds)
	}
	if view.OOS.To.After(asOf) {
		t.Fatalf("walk-forward OOS reaches %v, after asOf %v", view.OOS.To, asOf)
	}
	if artifact == nil || artifact.Kind != "walk_forward" {
		t.Fatalf("artifact = %+v, want kind walk_forward", artifact)
	}
}

// TestVolForecastPointInTimeUsesBarClose checks the vol_forecast wiring of the
// bar period: the oracle must be trained on the closed bars only. Ten 15-minute
// bars with a mid-bar asOf leave seven closed bars; the fit is guaranteed to
// fail on such a short history and the wrapped error reports the geometry the
// tool handed to the oracle, which makes the point-in-time slice observable.
func TestVolForecastPointInTimeUsesBarClose(t *testing.T) {
	const periodSec = 900
	period := time.Duration(periodSec) * time.Second
	data := &volFakeDataSource{fakeDataSource: newFakeDataSource()}
	bars := periodBars(10, period)
	bars[7].Partial = true
	data.histories["TQBR:SBER"] = map[int][]backtest.Bar{periodSec: bars}
	e := newTestEngineWith(t, Config{DefaultSymbol: "TQBR:SBER"}, data, newFakeBroker(1_000_000))
	// Mid-bar asOf: bars 0..6 are closed, bar 7 is forming, 8..9 are future.
	asOf := testNow.Add(7*period + period/2)
	args := map[string]any{
		"symbol":    "TQBR:SBER",
		"asOf":      asOf.Format(time.RFC3339),
		"periodSec": periodSec,
		"count":     len(bars),
	}
	_, _, err := e.invokeTool(context.Background(), toolInvocation{}, toolNameVolForecast, args)
	if err == nil {
		t.Fatal("vol_forecast fitted the oracle on ten 15-minute bars, want a short-history error")
	}
	if want := "(period 900s, 7 bars)"; !strings.Contains(err.Error(), want) {
		t.Fatalf("fit error = %v, want it to report %q: the partial and future bars must not reach the oracle", err, want)
	}
}
