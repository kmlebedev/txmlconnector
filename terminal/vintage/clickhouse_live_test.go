package vintage_test

// Live ClickHouse integration tests: they read a real ClickHouse through the
// same read-only views the production wiring uses (the local dev instance of
// the clickhouse-import-rosstat project). Unit tests never touch the network;
// run these explicitly:
//
//	TXML_IT_CLICKHOUSE=1 TXML_CLICKHOUSE_URL=http://localhost:8123 \
//	  go test ./terminal/vintage -run Live -v
//
// The credentials are read from TXML_CLICKHOUSE_USER / TXML_CLICKHOUSE_PASSWORD
// and never logged; an empty user means the ClickHouse default user.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/chstore"
	"github.com/kmlebedev/txmlconnector/terminal/vintage"
)

// liveStore builds the store against the live ClickHouse or skips the test.
func liveStore(t *testing.T) *vintage.ClickHouseStore {
	t.Helper()
	if os.Getenv("TXML_IT_CLICKHOUSE") != "1" {
		t.Skip("set TXML_IT_CLICKHOUSE=1 and TXML_CLICKHOUSE_URL to run the live ClickHouse checks")
	}
	raw := os.Getenv("TXML_CLICKHOUSE_URL")
	if raw == "" {
		raw = os.Getenv("CLICKHOUSE_URL")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		t.Skip("TXML_CLICKHOUSE_URL must be an HTTP ClickHouse endpoint")
	}
	return vintage.New(chstore.New(raw, os.Getenv("TXML_CLICKHOUSE_USER"), os.Getenv("TXML_CLICKHOUSE_PASSWORD"), nil))
}

// TestLiveClickHouseAsOf verifies the point-in-time path against the real
// marts: the returned versions are strictly published before asOf and the
// series is ordered by the report period.
func TestLiveClickHouseAsOf(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	asOf := time.Date(2024, time.February, 7, 0, 0, 0, 0, time.UTC)
	points, err := store.AsOf(ctx, "unemployment_sa", asOf)
	if err != nil {
		t.Fatalf("AsOf: %v", err)
	}
	if len(points) == 0 {
		t.Skip("series unemployment_sa is not in this ClickHouse")
	}
	for i, point := range points {
		if i > 0 && !point.Ref.After(points[i-1].Ref) {
			t.Fatalf("point %d ref %s is not after %s", i, point.Ref, points[i-1].Ref)
		}
		if !point.PublishedAt.IsZero() && !point.PublishedAt.Before(asOf) {
			t.Fatalf("point %d published at %s, want strictly before asOf %s", i, point.PublishedAt, asOf)
		}
	}
	if !vintage.IsLatestOnly(points[len(points)-1]) && points[len(points)-1].Vintage == "" {
		t.Fatalf("vintaged point without a version: %+v", points[len(points)-1])
	}
}

// TestLiveClickHouseReleases verifies the calendar against the real
// v_events_calendar: the Rosstat weekly CPI and the US CPI share the source
// category cpi, and the contract must tell them apart (category and country).
func TestLiveClickHouseReleases(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	from := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)
	releases, err := store.Releases(ctx, from, to)
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if len(releases) == 0 {
		t.Skip("v_events_calendar is empty in this ClickHouse")
	}
	rosstat, usCPI, rateSummary, inflationRU := 0, 0, 0, 0
	for _, release := range releases {
		if release.At.Before(from) || release.At.After(to) {
			t.Fatalf("release %q at %s is outside the window", release.Title, release.At)
		}
		if release.Category == "" {
			t.Fatalf("release %q without a category", release.Title)
		}
		if release.Country != "RU" && release.Country != "US" {
			t.Fatalf("release %q has an unknown country %q", release.Title, release.Country)
		}
		if strings.Contains(release.Title, "Росстат") && strings.Contains(strings.ToLower(release.Title), "ипц") {
			rosstat++
			if release.Category != "ru_cpi" || release.Country != "RU" {
				t.Fatalf("Rosstat weekly CPI = %+v, want ru_cpi/RU", release)
			}
		}
		if strings.Contains(release.Title, "CPI США") {
			usCPI++
			if release.Category != "us_cpi" || release.Country != "US" {
				t.Fatalf("US CPI = %+v, want us_cpi/US", release)
			}
		}
		// v_cbr_publication_calendar entries: the mapping uses the CBR titles.
		if strings.Contains(release.Title, "Резюме обсуждения ключевой ставки") {
			rateSummary++
			if release.Category != "cbr_rate" || release.Country != "RU" {
				t.Fatalf("CBR rate summary = %+v, want cbr_rate/RU", release)
			}
		}
		if strings.Contains(release.Title, "Инфляция в России") {
			inflationRU++
			if release.Category != "ru_cpi" || release.Country != "RU" {
				t.Fatalf("CBR inflation release = %+v, want ru_cpi/RU", release)
			}
		}
	}
	t.Logf("calendar: rosstat=%d us_cpi=%d cbr_rate=%d ru_cpi=%d", rosstat, usCPI, rateSummary, inflationRU)
	if rosstat == 0 || usCPI == 0 {
		t.Skipf("calendar has no Rosstat/US CPI samples (rosstat=%d us=%d)", rosstat, usCPI)
	}
}

// TestLiveClickHouseNews verifies the annotated news view: the UTC window, the
// nullable sentiment and the tag tuple shape of the real marts.
func TestLiveClickHouseNews(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	from := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.November, 6, 0, 0, 0, 0, time.UTC)
	news, err := store.News(ctx, from, to)
	if err != nil {
		t.Fatalf("News: %v", err)
	}
	if len(news) == 0 {
		t.Skip("v_news_events is empty in this ClickHouse window")
	}
	for _, item := range news {
		if item.At.Before(from) || item.At.After(to) {
			t.Fatalf("news %q at %s is outside the window", item.Title, item.At)
		}
		if item.Source == "" {
			t.Fatalf("news %q without a source", item.Title)
		}
		if item.Sentiment < -1 || item.Sentiment > 1 {
			t.Fatalf("news %q sentiment %v is outside -1..1", item.Title, item.Sentiment)
		}
		for _, tag := range item.Tags {
			if strings.Count(tag, ":") != 2 {
				t.Fatalf("news %q tag %q is not dimension:tag:direction", item.Title, tag)
			}
		}
	}
}

// TestLiveClickHouseLatestOnlyAndPublicationPaths covers the other two AsOf
// code paths on the real marts: a FRED series that has no publication history
// (latest-only) and a CBR series that has series_publication rows (the
// published version with its quality marker).
func TestLiveClickHouseLatestOnlyAndPublicationPaths(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	// Path 3: DGS10 lives in macro_series but has no series_publication rows.
	latest, err := store.AsOf(ctx, "DGS10", time.Now().UTC())
	if err != nil {
		t.Fatalf("AsOf latest-only: %v", err)
	}
	if len(latest) == 0 {
		t.Skip("series DGS10 is not a latest-only series in this ClickHouse")
	}
	for i, point := range latest {
		if !vintage.IsLatestOnly(point) {
			t.Fatalf("point %d of a latest-only series has vintage %q", i, point.Vintage)
		}
	}
	if warning := vintage.LatestOnlyWarning("DGS10"); !strings.Contains(warning, "latest-only") {
		t.Fatalf("latest-only warning = %q", warning)
	}

	// Path 2: the CBR survey series has publication dates in Europe/Moscow.
	published, err := store.AsOf(ctx, "price_expectations_sa", time.Date(2024, time.February, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("AsOf publication path: %v", err)
	}
	if len(published) == 0 {
		t.Skip("series with series_publication rows is not in this ClickHouse")
	}
	for i, point := range published {
		if vintage.IsLatestOnly(point) {
			t.Fatalf("point %d of a publication-path series is latest-only", i)
		}
		switch point.Vintage {
		case "date", "fallback", "exact_time":
		default:
			t.Fatalf("point %d has an unexpected quality %q", i, point.Vintage)
		}
		if !point.PublishedAt.Before(time.Date(2024, time.February, 7, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("point %d published at %s, want strictly before asOf", i, point.PublishedAt)
		}
	}
}
