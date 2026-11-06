package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/chstore"
)

// fakeNewsStore is the NewsStore of the get_news tool tests.
type fakeNewsStore struct {
	mu     sync.Mutex
	symbol string
	since  time.Time
	limit  int
	items  []NewsItem
	err    error
}

func (f *fakeNewsStore) News(ctx context.Context, symbol string, since time.Time, limit int) ([]NewsItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.symbol, f.since, f.limit = symbol, since, limit
	if f.err != nil {
		return nil, f.err
	}
	var out []NewsItem
	for _, item := range f.items {
		if !item.FirstSeen.Before(since) {
			out = append(out, item)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeNewsStore) query() (symbol string, since time.Time, limit int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.symbol, f.since, f.limit
}

// TestGetNewsToolFiltersBySince is the plan Task 12 test, extended by Task 18
// with the point-in-time rule: get_news passes the since window to the store, and
// a story first seen after asOf is not known at that moment and is dropped.
func TestGetNewsToolFiltersBySince(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeNewsStore{items: []NewsItem{
		{ID: 1, FirstSeen: now.Add(-2 * time.Hour), Published: now.Add(-3 * time.Hour), Title: "старая"},
		{ID: 2, FirstSeen: now.Add(-30 * time.Minute), Published: now.Add(-40 * time.Minute), Title: "средняя"},
		{ID: 3, FirstSeen: now.Add(-time.Minute), Published: now.Add(-2 * time.Minute), Title: "свежая"},
		{ID: 4, FirstSeen: now.Add(time.Hour), Published: now.Add(time.Hour), Title: "будущая"},
	}}
	engine := &Engine{cfg: withDefaults(Config{Now: func() time.Time { return now }})}
	asOf := now.Format(time.RFC3339)
	since := now.Add(-time.Hour)
	items, err := engine.toolGetNews(context.Background(), store, map[string]any{
		"asOf":  asOf,
		"since": since.Format(time.RFC3339),
		"limit": 2,
	})
	if err != nil {
		t.Fatalf("toolGetNews: %v", err)
	}
	if len(items) != 2 || items[0].ID != 2 || items[1].ID != 3 {
		t.Fatalf("items = %+v, want the two news at or after since", items)
	}
	_, gotSince, gotLimit := store.query()
	if !gotSince.Equal(since) {
		t.Fatalf("store since = %v, want %v", gotSince, since)
	}
	if gotLimit != 2 {
		t.Fatalf("store limit = %d, want 2", gotLimit)
	}

	// Point-in-time: the story first seen an hour after asOf did not exist for
	// the research request, even though the store returns it.
	items, err = engine.toolGetNews(context.Background(), store, map[string]any{"asOf": asOf, "limit": 10})
	if err != nil {
		t.Fatalf("toolGetNews point-in-time: %v", err)
	}
	for _, item := range items {
		if item.ID == 4 {
			t.Fatalf("items = %+v, a story first seen after asOf leaked into the result", items)
		}
		if item.FirstSeen.After(now) {
			t.Fatalf("item %+v is later than asOf", item)
		}
	}

	// Without asOf the tool is an argument error with a hint: a research call has
	// no implicit "now".
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{"since": "2h"}); !errors.Is(err, errBadArgument) ||
		!strings.Contains(err.Error(), "asOf") {
		t.Fatalf("missing asOf error = %v, want errBadArgument mentioning asOf", err)
	}

	// The default window is the 24 hours before asOf; the limit is 50.
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{"asOf": asOf}); err != nil {
		t.Fatalf("toolGetNews default window: %v", err)
	}
	_, gotSince, gotLimit = store.query()
	if !gotSince.Equal(now.Add(-24*time.Hour)) || gotLimit != 50 {
		t.Fatalf("default since/limit = %v/%d, want %v/50", gotSince, gotLimit, now.Add(-24*time.Hour))
	}

	// A duration is accepted as a relative window ("2h" = the two hours before
	// asOf, not before the wall clock).
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{"asOf": asOf, "since": "2h"}); err != nil {
		t.Fatalf("toolGetNews duration window: %v", err)
	}
	if _, gotSince, _ = store.query(); !gotSince.Equal(now.Add(-2 * time.Hour)) {
		t.Fatalf("duration since = %v, want %v", gotSince, now.Add(-2*time.Hour))
	}

	// The optional symbol is trimmed and passed through as the text filter.
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{"asOf": asOf, "symbol": " SBER "}); err != nil {
		t.Fatalf("toolGetNews symbol: %v", err)
	}
	if symbol, _, _ := store.query(); symbol != "SBER" {
		t.Fatalf("store symbol = %q, want SBER", symbol)
	}

	// An unparsable since is an argument error for the model, not a silent
	// fallback; since after asOf is rejected too.
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{"asOf": asOf, "since": "вчера"}); !errors.Is(err, errBadArgument) {
		t.Fatalf("bad since error = %v, want errBadArgument", err)
	}
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{
		"asOf": asOf, "since": now.Add(2 * time.Hour).Format(time.RFC3339),
	}); !errors.Is(err, errBadArgument) {
		t.Fatalf("since after asOf error = %v, want errBadArgument", err)
	}
	if _, err := engine.toolGetNews(context.Background(), nil, map[string]any{"asOf": asOf}); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a nil news store must fail with a configuration error, got %v", err)
	}
}

// TestNewsToolCatalog checks the tool descriptor the engine wiring will add.
func TestNewsToolCatalog(t *testing.T) {
	info := getNewsToolInfo()
	if info.Name != "get_news" || info.Title == "" || info.Description == "" {
		t.Fatalf("tool info = %+v", info)
	}
	schema, err := info.Input.MarshalJSON()
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, want := range []string{"symbol", "asOf", "since", "limit"} {
		if !strings.Contains(string(schema), want) {
			t.Fatalf("schema %s is missing %q", schema, want)
		}
	}
	if !strings.Contains(string(schema), "required") {
		t.Fatalf("schema %s must mark asOf as required", schema)
	}
}

// TestInvokeToolGetNewsDispatch checks the GAP-2 wiring: get_news is in the
// tool catalog and invokeTool dispatches it to the NewsStore of the engine
// config; without a store the tool is a clear configuration error. Task 18 adds
// the point-in-time asOf, the artifact with the full list and the compact model
// payload named by its artifactId.
func TestInvokeToolGetNewsDispatch(t *testing.T) {
	store := &fakeNewsStore{items: []NewsItem{
		{ID: 3, FirstSeen: testNow.Add(-time.Minute), Published: testNow.Add(-2 * time.Minute), Title: "свежая"},
		{ID: 4, FirstSeen: testNow.Add(time.Hour), Published: testNow.Add(time.Hour), Title: "будущая"},
	}}
	engine := newTestEngineWith(t, Config{NewsStore: store}, newFakeDataSource(), nil)
	found := false
	for _, tool := range engine.Tools() {
		if tool.Name == toolNameGetNews {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("get_news is missing from the tool catalog")
	}
	args := map[string]any{"asOf": testNow.Format(time.RFC3339)}
	result, artifact, err := engine.invokeTool(context.Background(), toolInvocation{}, toolNameGetNews, args)
	if err != nil {
		t.Fatalf("invokeTool(get_news): %v", err)
	}
	payload, ok := result.(newsResult)
	if !ok || len(payload.News) != 1 || payload.News[0].ID != 3 {
		t.Fatalf("get_news result = %#v", result)
	}
	if !payload.AsOf.Equal(testNow) || payload.ArtifactID == "" {
		t.Fatalf("get_news payload = %+v, want asOf=%v and an artifactId", payload, testNow)
	}
	if artifact == nil || artifact.Kind != "news" {
		t.Fatalf("get_news artifact = %+v, want the full news artifact", artifact)
	}
	if stored, ok := toolArtifacts.get(payload.ArtifactID); !ok {
		t.Fatalf("artifact %q is not stored", payload.ArtifactID)
	} else if full, ok := stored.(newsResult); !ok || len(full.News) != len(payload.News) {
		t.Fatalf("stored artifact = %#v, want the full news result", stored)
	}
	// The model payload is compact and names the full result by artifactId.
	content := toolMessageContent(ToolCall{Name: toolNameGetNews, Result: payload})
	if len(content) > 2048 {
		t.Fatalf("model payload = %d bytes, want <= 2048", len(content))
	}
	if !strings.Contains(content, payload.ArtifactID) {
		t.Fatalf("model payload lost the artifactId: %s", content)
	}
	// Without a configured store the dispatch reports the pipeline state.
	bare := newTestEngineWith(t, Config{}, newFakeDataSource(), nil)
	if _, _, err := bare.invokeTool(context.Background(), toolInvocation{}, toolNameGetNews, args); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Fatalf("get_news without a store error = %v", err)
	}
	// Without asOf the tool refuses to invent a point in time.
	if _, _, err := engine.invokeTool(context.Background(), toolInvocation{}, toolNameGetNews, map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "asOf") {
		t.Fatalf("get_news without asOf error = %v, want an asOf hint", err)
	}
}

// TestClickHouseNewsStoreQuery checks the md.news query: first_seen is the
// point-in-time filter, the symbol is an optional case-insensitive text filter,
// and the parameters travel as param_<name> bindings (no SQL interpolation).
func TestClickHouseNewsStoreQuery(t *testing.T) {
	var gotQuery, gotSince, gotSymbol, gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()
		gotQuery = values.Get("query")
		gotSince, gotSymbol, gotLimit = values.Get("param_since"), values.Get("param_symbol"), values.Get("param_limit")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"first_seen\":\"2026-11-05 10:00:00.000\",\"published\":\"2026-11-05 09:00:00.000\",\"news_id\":5,\"source\":\"SRC\",\"title\":\"Title\",\"body\":\"Body\",\"content_hash\":42}\n"))
	}))
	defer srv.Close()
	store := NewClickHouseNewsStore(chstore.New(srv.URL, "md-user", "md-password", srv.Client()))
	since := time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC)
	items, err := store.News(context.Background(), "SBER", since, 10)
	if err != nil {
		t.Fatalf("News: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want 1", items)
	}
	item := items[0]
	if item.ID != 5 || item.Source != "SRC" || item.Title != "Title" || item.Body != "Body" || item.ContentHash != 42 {
		t.Fatalf("item = %+v", item)
	}
	if !item.FirstSeen.Equal(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("first_seen = %v", item.FirstSeen)
	}
	if !item.Published.Equal(time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("published = %v", item.Published)
	}
	for _, want := range []string{"FROM md.news", "first_seen >= {since:DateTime64(3)}", "positionCaseInsensitiveUTF8", "ORDER BY first_seen DESC", "FORMAT JSONEachRow"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q is missing %q", gotQuery, want)
		}
	}
	if gotSince != "2026-11-05 09:30:00.000" {
		t.Fatalf("param_since = %q, want the ClickHouse DateTime64(3) text", gotSince)
	}
	if gotSymbol != "SBER" || gotLimit != "10" {
		t.Fatalf("param_symbol/param_limit = %q/%q", gotSymbol, gotLimit)
	}

	// Without a symbol the SQL keeps the since filter only.
	gotSymbol = "sentinel"
	if _, err := store.News(context.Background(), "", since, 5); err != nil {
		t.Fatalf("News without symbol: %v", err)
	}
	if gotSymbol != "" {
		t.Fatalf("param_symbol = %q, want no symbol binding", gotSymbol)
	}
	if strings.Contains(gotQuery, "positionCaseInsensitiveUTF8") {
		t.Fatalf("query without symbol keeps the text filter: %q", gotQuery)
	}
}
