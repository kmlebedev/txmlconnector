// Package vintage is the point-in-time (vintage) layer of the AI-terminal
// research tasks: macro series as they were known at an instant, the release
// calendar and the annotated news feed. It reads the read-only views of the
// clickhouse-import-rosstat project through terminal/chstore (HTTP, no driver,
// no new dependencies) and returns typed values with every instant in UTC.
package vintage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/chstore"
)

const (
	// VintageLatestOnly marks a point of a series without a point-in-time
	// history: it is the latest known value, not the value published before
	// asOf. The caller must warn the user (LatestOnlyWarning) and may refuse
	// the series (plan Task 13).
	VintageLatestOnly = "latest-only"

	// dateLayout and dateTimeLayout are the ClickHouse Date/DateTime JSON texts.
	dateLayout     = "2006-01-02"
	dateTimeLayout = "2006-01-02 15:04:05"
)

// Point is one version of a macro series value.
type Point struct {
	Series      string
	Ref         time.Time // report period of the value (UTC midnight of the source date)
	Value       float64
	PublishedAt time.Time // instant the version became public (UTC)
	Vintage     string    // version id of the source release, or VintageLatestOnly
}

// Release is one scheduled macro event.
type Release struct {
	At       time.Time
	Category string // cbr_rate|ru_cpi|ru_gdp|us_cpi|us_nfp|fomc|…
	Title    string
	Country  string // RU|US
	Exact    bool   // false when the source knows the date only
}

// NewsItem is one annotated market news story.
type NewsItem struct {
	At        time.Time
	Source    string
	Title     string
	Sentiment float64  // -1..1 from the LLM annotation; 0 when the story is not annotated
	Tags      []string // "dimension:tag:direction" of the LLM annotation
}

// Store is the point-in-time data source of the research tasks (plan Task 13).
type Store interface {
	AsOf(ctx context.Context, series string, asOf time.Time) ([]Point, error)
	Releases(ctx context.Context, from, to time.Time, categories ...string) ([]Release, error)
	News(ctx context.Context, from, to time.Time, sources ...string) ([]NewsItem, error)
}

// ClickHouseStore reads the clickhouse-import-rosstat views through chstore.
type ClickHouseStore struct {
	client *chstore.Client
}

// New builds the ClickHouse-backed store. The client is required; a nil one is
// reported by every method as a configuration error.
func New(client *chstore.Client) *ClickHouseStore {
	return &ClickHouseStore{client: client}
}

// ready reports the configuration error of an unconfigured store: the terminal
// keeps working without ClickHouse, only the research data is unavailable.
func (s *ClickHouseStore) ready() error {
	if s == nil || s.client == nil {
		return errors.New("vintage: the store is not configured (CLICKHOUSE_URL is empty)")
	}
	return nil
}

// pitAsOfQuery reads the parameterized point-in-time view of the CBR DIP
// bulletin (the only vintaged source of the rosstat layer). The view call takes
// its argument as a SQL literal, so the date is formatted by this package and is
// never user text; the series is a bound parameter.
const pitAsOfQuery = "SELECT series, period_date, value, published_at, vintage " +
	"FROM v_point_in_time_asof(as_of = '%s') " +
	"WHERE series = {series:String} ORDER BY period_date"

// seriesPublicationQuery is the second point-in-time path: a series that lives
// in macro_series with its publication dates in series_publication. The as-of
// boundary is strict (published_at < asOf), matching the vintage rule.
const seriesPublicationQuery = `SELECT m.series AS series, m.date AS period_date, m.value AS value,
    toTimeZone(p.published_at, 'UTC') AS published_at, p.quality AS vintage
FROM (SELECT source, series, date, value FROM macro_series FINAL) AS m
INNER JOIN (SELECT source, series, period_date, published_at, quality FROM series_publication FINAL) AS p
    ON p.source = m.source AND p.series = m.series AND p.period_date = m.date
WHERE m.series = {series:String}
  AND p.published_at < toDateTime64({asOf:String}, 3, 'UTC')
ORDER BY m.date`

// latestOnlyQuery is the last-resort path: a series without any vintage layer
// (FRED/BLS/… rows imported as latest values). The points are flagged
// VintageLatestOnly and must not be trusted as an as-of slice.
const latestOnlyQuery = `SELECT series, date AS period_date, argMax(value, source) AS value
FROM (SELECT source, series, date, value FROM macro_series FINAL
      WHERE series = {series:String} AND date < {asOf:Date})
GROUP BY series, date
ORDER BY date`

// pointRow is one JSONEachRow row of any of the three point queries.
type pointRow struct {
	Series      string  `json:"series"`
	PeriodDate  string  `json:"period_date"`
	Value       float64 `json:"value"`
	PublishedAt *string `json:"published_at"`
	Vintage     string  `json:"vintage"`
}

// AsOf returns the macro series as it was known strictly before asOf.
//
// Three sources are tried in order: the parameterized v_point_in_time_asof view
// (vintaged CBR DIP bulletin releases), the macro_series/series_publication join
// (series with publication dates only), and finally the latest known values of a
// series without a vintage history — those are flagged VintageLatestOnly.
func (s *ClickHouseStore) AsOf(ctx context.Context, series string, asOf time.Time) ([]Point, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	series = strings.TrimSpace(series)
	if series == "" {
		return nil, errors.New("vintage: series is required")
	}
	if asOf.IsZero() {
		return nil, errors.New("vintage: asOf is required")
	}
	asOf = asOf.UTC()

	rows, err := s.queryPoints(ctx, fmt.Sprintf(pitAsOfQuery, asOf.Format(dateLayout)), map[string]string{"series": series})
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		return pointsFromRows(rows, "")
	}

	rows, err = s.queryPoints(ctx, seriesPublicationQuery, map[string]string{
		"series": series,
		"asOf":   chDateTime(asOf),
	})
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		return pointsFromRows(rows, "")
	}

	rows, err = s.queryPoints(ctx, latestOnlyQuery, map[string]string{
		"series": series,
		"asOf":   asOf.Format(dateLayout),
	})
	if err != nil {
		return nil, err
	}
	return pointsFromRows(rows, VintageLatestOnly)
}

// queryPoints runs one point query and decodes its JSONEachRow reply.
func (s *ClickHouseStore) queryPoints(ctx context.Context, sql string, params map[string]string) ([]pointRow, error) {
	var rows []pointRow
	if err := s.client.Query(ctx, sql, params, &rows); err != nil {
		return nil, fmt.Errorf("vintage: query: %w", err)
	}
	return rows, nil
}

// pointsFromRows maps the JSON rows to the typed points. A non-empty
// vintageOverride replaces the source version id (the latest-only fallback).
func pointsFromRows(rows []pointRow, vintageOverride string) ([]Point, error) {
	out := make([]Point, 0, len(rows))
	for _, row := range rows {
		ref, err := time.Parse(dateLayout, strings.TrimSpace(row.PeriodDate))
		if err != nil {
			return nil, fmt.Errorf("vintage: %s period_date %q: %w", row.Series, row.PeriodDate, err)
		}
		var published time.Time
		if row.PublishedAt != nil && strings.TrimSpace(*row.PublishedAt) != "" {
			published, err = parseCHTime(*row.PublishedAt)
			if err != nil {
				return nil, fmt.Errorf("vintage: %s published_at %q: %w", row.Series, *row.PublishedAt, err)
			}
		}
		vintage := row.Vintage
		if vintageOverride != "" {
			vintage = vintageOverride
		}
		out = append(out, Point{
			Series:      row.Series,
			Ref:         ref.UTC(),
			Value:       row.Value,
			PublishedAt: published,
			Vintage:     vintage,
		})
	}
	return out, nil
}

// IsLatestOnly reports whether the point has no point-in-time history.
func IsLatestOnly(point Point) bool {
	return point.Vintage == VintageLatestOnly
}

// LatestOnlyWarning is the user-facing warning of a latest-only slice: the
// values are the latest known ones, not the versions published before asOf.
func LatestOnlyWarning(series string) string {
	return fmt.Sprintf("vintage: ряд %q не имеет point-in-time истории (latest-only): "+
		"возвращены последние известные значения, а не версии на asOf; бэктест может отказаться от ряда", series)
}

// chDateTime renders a ClickHouse DateTime64(3) binding in UTC.
func chDateTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000")
}

// parseCHTime parses a ClickHouse Date or DateTime JSON text (both appear in
// the point queries: the view returns a Date, the joins a DateTime).
func parseCHTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{dateTimeLayout, dateLayout, time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparsable ClickHouse time")
}
