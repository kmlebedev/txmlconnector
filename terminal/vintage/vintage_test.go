package vintage_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/chstore"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

// stubRequest is one request the store sent to the ClickHouse stub.
type stubRequest struct {
	query  string
	params url.Values
}

// clickHouseStub answers every chstore request through respond; the request SQL
// and the bound parameters are kept for assertions.
type clickHouseStub struct {
	t        *testing.T
	mu       sync.Mutex
	requests []stubRequest
	respond  func(query string, params url.Values) string
}

func newStub(t *testing.T, respond func(query string, params url.Values) string) (*clickHouseStub, *httptest.Server) {
	t.Helper()
	stub := &clickHouseStub{t: t, respond: respond}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		stub.mu.Lock()
		stub.requests = append(stub.requests, stubRequest{query: query, params: r.URL.Query()})
		stub.mu.Unlock()
		_, _ = io.WriteString(w, stub.respond(query, r.URL.Query()))
	}))
	t.Cleanup(srv.Close)
	return stub, srv
}

func (s *clickHouseStub) captured() []stubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stubRequest(nil), s.requests...)
}

// requestWith returns the first request whose SQL contains fragment.
func (s *clickHouseStub) requestWith(fragment string) (stubRequest, bool) {
	for _, request := range s.captured() {
		if strings.Contains(request.query, fragment) {
			return request, true
		}
	}
	return stubRequest{}, false
}

func newStore(srv *httptest.Server) *vintage.ClickHouseStore {
	return vintage.New(chstore.New(srv.URL, "md_reader", "md_reader_password", srv.Client()))
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func dayClock(y int, m time.Month, d, hour, minute int) time.Time {
	return time.Date(y, m, d, hour, minute, 0, 0, time.UTC)
}

// TestVintageAsOfExcludesLaterRevision is the Review Focus 1 test: the same
// report month of unemployment_sa has two releases; an as_of between them sees
// only the first version, a later as_of sees the revision. The ClickHouse stub
// emulates v_point_in_time_asof (strict publication_date < as_of, argMax by
// (published_at, vintage)).
func TestVintageAsOfExcludesLaterRevision(t *testing.T) {
	revisions := []struct {
		value     string
		published string
		vintage   string
	}{
		{"2.885274", "2024-01-24", "24-01"},
		{"2.916127", "2026-06-05", "26-06"},
	}
	asOfRE := regexp.MustCompile("as_of = '([0-9-]+)'")
	stub, srv := newStub(t, func(query string, params url.Values) string {
		if !strings.Contains(query, "v_point_in_time_asof") {
			return ""
		}
		match := asOfRE.FindStringSubmatch(query)
		if match == nil {
			t.Errorf("query without an as_of argument: %s", query)
			return ""
		}
		asOf := match[1]
		// The view returns argMax(value, (published_at, vintage)) per period, so
		// the stub keeps the last revision published strictly before asOf.
		best := -1
		for i, revision := range revisions {
			if revision.published < asOf {
				best = i
			}
		}
		if best < 0 {
			return ""
		}
		revision := revisions[best]
		return fmt.Sprintf(`{"source":"cbr_dip_bulletin","series":"unemployment_sa","period_date":"2023-11-30","value":%s,"published_at":"%s","vintage":"%s"}`, revision.value, revision.published, revision.vintage) + "\n"
	})
	store := newStore(srv)
	ctx := context.Background()

	first, err := store.AsOf(ctx, "unemployment_sa", day(2024, time.February, 7))
	if err != nil {
		t.Fatalf("AsOf(2024-02-07): %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("points at 2024-02-07 = %+v, want the first release only", first)
	}
	point := first[0]
	if point.Series != "unemployment_sa" || !point.Ref.Equal(day(2023, time.November, 30)) {
		t.Fatalf("point = %+v", point)
	}
	if point.Value != 2.885274 {
		t.Fatalf("value = %v, want the first release 2.885274 (not the revision)", point.Value)
	}
	if !point.PublishedAt.Equal(day(2024, time.January, 24)) || point.Vintage != "24-01" {
		t.Fatalf("published_at/vintage = %v/%q", point.PublishedAt, point.Vintage)
	}
	request, ok := stub.requestWith("v_point_in_time_asof")
	if !ok {
		t.Fatal("the point-in-time view was not queried")
	}
	if !strings.Contains(request.query, "as_of = '2024-02-07'") {
		t.Fatalf("query = %q, want the as-of call argument", request.query)
	}
	if got := request.params.Get("param_series"); got != "unemployment_sa" {
		t.Fatalf("param_series = %q", got)
	}

	// A later as_of includes the revision.
	revised, err := store.AsOf(ctx, "unemployment_sa", day(2026, time.September, 2))
	if err != nil {
		t.Fatalf("AsOf(2026-09-02): %v", err)
	}
	if len(revised) != 1 || revised[0].Value != 2.916127 || revised[0].Vintage != "26-06" {
		t.Fatalf("revised as-of = %+v, want the 26-06 revision", revised)
	}

	// The boundary is strict: a release published exactly on as_of is not known.
	boundary, err := store.AsOf(ctx, "unemployment_sa", day(2024, time.January, 24))
	if err != nil {
		t.Fatalf("AsOf(boundary): %v", err)
	}
	if len(boundary) != 0 {
		t.Fatalf("points at the publication date = %+v, want none (strict published_at < as_of)", boundary)
	}
}

// TestVintageSeriesPublicationPath checks the second point-in-time path: a
// series with series_publication dates but no v_point_in_time rows (the macro
// series of the CBR releases) is sliced by the MSK publication instant and the
// published_at is normalized to UTC.
func TestVintageSeriesPublicationPath(t *testing.T) {
	stub, srv := newStub(t, func(query string, params url.Values) string {
		switch {
		case strings.Contains(query, "v_point_in_time_asof"):
			return ""
		case strings.Contains(query, "series_publication"):
			// The SQL converts published_at to UTC, so the stub returns UTC
			// wall times; the "strictly before as_of" filter is the view's job,
			// emulated here through the param_asOf binding.
			asOf := params.Get("param_asOf")
			var lines []string
			for _, row := range []struct{ value, published string }{
				{"8.1", "2026-11-05 13:20:00"}, // 16:20 MSK
				{"8.3", "2026-12-10 16:20:00"}, // the December revision
			} {
				if row.published < asOf {
					lines = append(lines, fmt.Sprintf(`{"series":"ru_cpi","period_date":"2026-10-31","value":%s,"published_at":"%s","vintage":"date"}`, row.value, row.published))
				}
			}
			return strings.Join(lines, "\n") + "\n"
		default:
			return ""
		}
	})
	store := newStore(srv)
	points, err := store.AsOf(context.Background(), "ru_cpi", day(2026, time.November, 6))
	if err != nil {
		t.Fatalf("AsOf: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("points = %+v, want one point", points)
	}
	if points[0].Value != 8.1 || points[0].Vintage != "date" {
		t.Fatalf("point = %+v, want the November release", points[0])
	}
	if !points[0].PublishedAt.Equal(time.Date(2026, time.November, 5, 13, 20, 0, 0, time.UTC)) {
		t.Fatalf("published_at = %v, want 13:20 UTC (16:20 MSK)", points[0].PublishedAt)
	}
	request, ok := stub.requestWith("series_publication")
	if !ok {
		t.Fatal("series_publication was not queried")
	}
	if got := request.params.Get("param_asOf"); got != "2026-11-06 00:00:00.000" {
		t.Fatalf("param_asOf = %q, want the UTC instant binding", got)
	}
}

// TestVintageLatestOnlyFlagged is the plan test for the rows without vintages:
// the store falls back to macro_series, flags every point latest-only and says
// so through the warning helper (a backtest may refuse the series).
func TestVintageLatestOnlyFlagged(t *testing.T) {
	stub, srv := newStub(t, func(query string, params url.Values) string {
		switch {
		case strings.Contains(query, "v_point_in_time_asof"):
			return ""
		case strings.Contains(query, "series_publication"):
			return ""
		case strings.Contains(query, "macro_series"):
			return `{"series":"DFII10","period_date":"2026-10-30","value":1.85,"source":"fred"}
{"series":"DFII10","period_date":"2026-11-02","value":1.87,"source":"fred"}`
		default:
			return ""
		}
	})
	store := newStore(srv)
	points, err := store.AsOf(context.Background(), "DFII10", day(2026, time.November, 5))
	if err != nil {
		t.Fatalf("AsOf: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("points = %+v, want two latest-only points", points)
	}
	for _, point := range points {
		if point.Vintage != vintage.VintageLatestOnly {
			t.Fatalf("vintage = %q, want %q", point.Vintage, vintage.VintageLatestOnly)
		}
		if !vintage.IsLatestOnly(point) {
			t.Fatalf("IsLatestOnly(%+v) = false", point)
		}
	}
	if warning := vintage.LatestOnlyWarning("DFII10"); !strings.Contains(warning, "latest-only") || !strings.Contains(warning, "DFII10") {
		t.Fatalf("warning = %q", warning)
	}
	request, ok := stub.requestWith("macro_series")
	if !ok {
		t.Fatal("macro_series fallback was not queried")
	}
	if got := request.params.Get("param_series"); got != "DFII10" {
		t.Fatalf("param_series = %q", got)
	}
}

// TestReleasesNormalizedToUTC is the plan test: the CBR publication calendar
// carries MSK wall-clock dates/times, the Release instants are UTC; a date-only
// row is Exact=false.
func TestReleasesNormalizedToUTC(t *testing.T) {
	stub, srv := newStub(t, func(query string, params url.Values) string {
		switch {
		case strings.Contains(query, "v_events_calendar"):
			return ""
		case strings.Contains(query, "v_cbr_publication_calendar"):
			return `{"publication_date":"2026-11-05","publication_time":"13:30","title":"Заседание Совета директоров Банка России по ключевой ставке","topic":"analytics"}
{"publication_date":"2026-11-06","publication_time":null,"title":"Информация об инфляции","topic":"statistics"}
{"publication_date":"2026-11-20","publication_time":"16:00","title":"ВВП: предварительная оценка","topic":"statistics"}`
		default:
			return ""
		}
	})
	store := newStore(srv)
	releases, err := store.Releases(context.Background(), day(2026, time.November, 1), day(2026, time.November, 30))
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if len(releases) != 3 {
		t.Fatalf("releases = %+v, want 3", releases)
	}
	if !releases[0].At.Equal(dayClock(2026, time.November, 5, 10, 30)) {
		t.Fatalf("first release At = %v, want 2026-11-05T10:30Z (13:30 MSK)", releases[0].At)
	}
	if !releases[0].Exact || releases[0].Category != "cbr_rate" || releases[0].Country != "RU" {
		t.Fatalf("first release = %+v", releases[0])
	}
	// A date-only row: midnight MSK = 21:00 UTC of the previous day, Exact=false.
	if !releases[1].At.Equal(dayClock(2026, time.November, 5, 21, 0)) {
		t.Fatalf("date-only release At = %v, want 2026-11-05T21:00Z (midnight MSK)", releases[1].At)
	}
	if releases[1].Exact || releases[1].Category != "ru_cpi" {
		t.Fatalf("date-only release = %+v", releases[1])
	}
	if releases[2].Category != "ru_gdp" || !releases[2].Exact {
		t.Fatalf("GDP release = %+v", releases[2])
	}
	request, ok := stub.requestWith("v_cbr_publication_calendar")
	if !ok {
		t.Fatal("the CBR publication calendar was not queried")
	}
	if got := request.params.Get("param_from"); got != "2026-11-01" {
		t.Fatalf("param_from = %q, want the MSK date of the UTC lower bound", got)
	}
	if got := request.params.Get("param_to"); got != "2026-11-30" {
		t.Fatalf("param_to = %q", got)
	}

	// The categories filter narrows the list after the source-category mapping.
	onlyRate, err := store.Releases(context.Background(), day(2026, time.November, 1), day(2026, time.November, 30), "cbr_rate")
	if err != nil {
		t.Fatalf("Releases(cbr_rate): %v", err)
	}
	if len(onlyRate) != 1 || onlyRate[0].Category != "cbr_rate" {
		t.Fatalf("filtered releases = %+v", onlyRate)
	}
}

// TestReleasesReadsEventsCalendar checks the trigger calendar path: the agent
// category is mapped onto the contract category and the country.
func TestReleasesReadsEventsCalendar(t *testing.T) {
	_, srv := newStub(t, func(query string, params url.Values) string {
		switch {
		case strings.Contains(query, "v_events_calendar"):
			return `{"event_date":"2026-11-04","event_time":"21:00","category":"fomc","title":"ФРС: заседание FOMC","emergency":0}
{"event_date":"2026-11-05","event_time":null,"category":"cbr","title":"СД ЦБ РФ: заседание по ключевой ставке","emergency":1}`
		default:
			return ""
		}
	})
	store := newStore(srv)
	releases, err := store.Releases(context.Background(), day(2026, time.November, 1), day(2026, time.November, 30))
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %+v, want 2", releases)
	}
	byCategory := map[string]vintage.Release{}
	for _, release := range releases {
		byCategory[release.Category] = release
	}
	if fomc := byCategory["fomc"]; fomc.Country != "US" || !fomc.Exact || !fomc.At.Equal(dayClock(2026, time.November, 4, 18, 0)) {
		t.Fatalf("FOMC release = %+v, want 21:00 MSK = 18:00Z", fomc)
	}
	if rate := byCategory["cbr_rate"]; rate.Country != "RU" || rate.Exact {
		t.Fatalf("CBR release = %+v", rate)
	}
}

// TestNewsFiltersAndDecodes checks the news feed path: the UTC window and the
// source list are query parameters, the nullable LLM sentiment and the tag
// tuples are decoded into the contract type.
func TestNewsFiltersAndDecodes(t *testing.T) {
	stub, srv := newStub(t, func(query string, params url.Values) string {
		if !strings.Contains(query, "v_news_events") {
			return ""
		}
		return `{"date":"2026-11-05 10:00:00","source":"interfax","title":"ЦБ повысил ставку","description":"текст","sentiment":-0.4,"tags":[["theme","key_rate","proinflationary"],["country","ru","neutral"]]}
{"date":"2026-11-04 09:30:00","source":"rbc","title":"Без разметки","description":"","sentiment":null,"tags":[]}`
	})
	store := newStore(srv)
	from := time.Date(2026, time.November, 4, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.November, 6, 0, 0, 0, 0, time.UTC)
	items, err := store.News(context.Background(), from, to, "interfax", "rbc")
	if err != nil {
		t.Fatalf("News: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("news = %+v, want 2", items)
	}
	if !items[0].At.Equal(time.Date(2026, time.November, 5, 10, 0, 0, 0, time.UTC)) || items[0].Source != "interfax" || items[0].Title != "ЦБ повысил ставку" {
		t.Fatalf("first news = %+v", items[0])
	}
	if items[0].Sentiment != -0.4 {
		t.Fatalf("sentiment = %v", items[0].Sentiment)
	}
	if len(items[0].Tags) != 2 || items[0].Tags[0] != "theme:key_rate:proinflationary" {
		t.Fatalf("tags = %v", items[0].Tags)
	}
	// An unannotated item has no sentiment and no tags (NULL in the view).
	if items[1].Sentiment != 0 || len(items[1].Tags) != 0 {
		t.Fatalf("unannotated news = %+v", items[1])
	}
	request, ok := stub.requestWith("v_news_events")
	if !ok {
		t.Fatal("v_news_events was not queried")
	}
	if got := request.params.Get("param_from"); got != "2026-11-04 00:00:00.000" {
		t.Fatalf("param_from = %q", got)
	}
	if got := request.params.Get("param_src1"); got != "rbc" {
		t.Fatalf("param_src1 = %q", got)
	}
}
