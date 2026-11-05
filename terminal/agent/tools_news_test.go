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

// TestGetNewsToolFiltersBySince is the plan Task 12 test: get_news passes the
// since filter (and the limit) to the news store, which returns only news first
// seen at or after it.
func TestGetNewsToolFiltersBySince(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeNewsStore{items: []NewsItem{
		{ID: 1, FirstSeen: now.Add(-2 * time.Hour), Published: now.Add(-3 * time.Hour), Title: "старая"},
		{ID: 2, FirstSeen: now.Add(-30 * time.Minute), Published: now.Add(-40 * time.Minute), Title: "средняя"},
		{ID: 3, FirstSeen: now.Add(-time.Minute), Published: now.Add(-2 * time.Minute), Title: "свежая"},
	}}
	engine := &Engine{cfg: withDefaults(Config{Now: func() time.Time { return now }})}
	since := now.Add(-time.Hour)
	items, err := engine.toolGetNews(context.Background(), store, map[string]any{
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

	// Without arguments the window is the last 24 hours and the limit 50.
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{}); err != nil {
		t.Fatalf("toolGetNews default window: %v", err)
	}
	_, gotSince, gotLimit = store.query()
	if !gotSince.Equal(now.Add(-24*time.Hour)) || gotLimit != 50 {
		t.Fatalf("default since/limit = %v/%d, want %v/50", gotSince, gotLimit, now.Add(-24*time.Hour))
	}

	// A duration is accepted as a relative window ("2h" = last two hours).
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{"since": "2h"}); err != nil {
		t.Fatalf("toolGetNews duration window: %v", err)
	}
	if _, gotSince, _ = store.query(); !gotSince.Equal(now.Add(-2 * time.Hour)) {
		t.Fatalf("duration since = %v, want %v", gotSince, now.Add(-2*time.Hour))
	}

	// The optional symbol is trimmed and passed through as the text filter.
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{"symbol": " SBER "}); err != nil {
		t.Fatalf("toolGetNews symbol: %v", err)
	}
	if symbol, _, _ := store.query(); symbol != "SBER" {
		t.Fatalf("store symbol = %q, want SBER", symbol)
	}

	// An unparsable since is an argument error for the model, not a silent
	// fallback; a missing store is a configuration error.
	if _, err := engine.toolGetNews(context.Background(), store, map[string]any{"since": "вчера"}); !errors.Is(err, errBadArgument) {
		t.Fatalf("bad since error = %v, want errBadArgument", err)
	}
	if _, err := engine.toolGetNews(context.Background(), nil, nil); err == nil {
		t.Fatal("a nil news store must fail")
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
	for _, want := range []string{"symbol", "since", "limit"} {
		if !strings.Contains(string(schema), want) {
			t.Fatalf("schema %s is missing %q", schema, want)
		}
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
