package vintage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The two release sources of the rosstat layer: the trigger calendar (FOMC,
// CPI/NFP releases, CBR meetings, …) and the CBR publication plan. Both are
// plan-like sources: the date is exact, the time exists only where the source
// publishes it.
const (
	eventsCalendarQuery = `SELECT event_date, event_time, category, title, emergency
FROM v_events_calendar
WHERE event_date >= {from:Date} AND event_date <= {to:Date}
ORDER BY event_date, category, title`

	cbrPublicationCalendarQuery = `SELECT publication_date, publication_time, title, topic
FROM v_cbr_publication_calendar
WHERE publication_date >= {from:Date} AND publication_date <= {to:Date}
ORDER BY publication_date, title`
)

// eventCalendarRow is one v_events_calendar row.
type eventCalendarRow struct {
	EventDate string  `json:"event_date"`
	EventTime *string `json:"event_time"`
	Category  string  `json:"category"`
	Title     string  `json:"title"`
	Emergency uint8   `json:"emergency"`
}

// publicationCalendarRow is one v_cbr_publication_calendar row.
type publicationCalendarRow struct {
	PublicationDate string  `json:"publication_date"`
	PublicationTime *string `json:"publication_time"`
	Title           string  `json:"title"`
	Topic           string  `json:"topic"`
}

// eventCategories maps the trigger-calendar categories onto the open contract
// category names of the plan (unknown categories are passed through lowercased).
var eventCategories = map[string]string{
	"cbr":            "cbr_rate",
	"fomc":           "fomc",
	"cpi":            "us_cpi",
	"nfp":            "us_nfp",
	"eia":            "us_eia",
	"wgc":            "us_wgc",
	"moex_rebalance": "moex_rebalance",
	"gov_rf":         "ru_gov",
	"polyus_ir":      "polyus_ir",
	"bank_results":   "bank_results",
}

// usEventCategories are the contract categories of the US releases.
var usEventCategories = map[string]bool{
	"fomc":   true,
	"us_cpi": true,
	"us_nfp": true,
	"us_eia": true,
	"us_wgc": true,
}

// Releases returns the scheduled macro events published in [from, to].
//
// The source times are Europe/Moscow wall clock, so every instant is normalized
// to UTC; a row with only a date is Exact=false and lands at midnight MSK. The
// categories filter uses the contract category names (cbr_rate, ru_cpi, …) and
// is applied after the source-category mapping.
func (s *ClickHouseStore) Releases(ctx context.Context, from, to time.Time, categories ...string) ([]Release, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if from.IsZero() || to.IsZero() {
		return nil, errors.New("vintage: the release window is required")
	}
	from, to = from.UTC(), to.UTC()
	if to.Before(from) {
		return nil, errors.New("vintage: the release window is inverted (to < from)")
	}
	msk := moscow()
	params := map[string]string{
		"from": from.In(msk).Format(dateLayout),
		"to":   to.In(msk).Format(dateLayout),
	}
	var events []eventCalendarRow
	if err := s.client.Query(ctx, eventsCalendarQuery, params, &events); err != nil {
		return nil, fmt.Errorf("vintage: events calendar: %w", err)
	}
	var publications []publicationCalendarRow
	if err := s.client.Query(ctx, cbrPublicationCalendarQuery, params, &publications); err != nil {
		return nil, fmt.Errorf("vintage: CBR publication calendar: %w", err)
	}
	want := make(map[string]bool, len(categories))
	for _, category := range categories {
		if trimmed := strings.TrimSpace(category); trimmed != "" {
			want[trimmed] = true
		}
	}
	releases := make([]Release, 0, len(events)+len(publications))
	for _, row := range events {
		release, ok := releaseFromEvent(row, msk)
		if !ok || (len(want) > 0 && !want[release.Category]) {
			continue
		}
		releases = append(releases, release)
	}
	for _, row := range publications {
		release, ok := releaseFromPublication(row, msk)
		if !ok || (len(want) > 0 && !want[release.Category]) {
			continue
		}
		releases = append(releases, release)
	}
	sort.SliceStable(releases, func(i, j int) bool {
		if !releases[i].At.Equal(releases[j].At) {
			return releases[i].At.Before(releases[j].At)
		}
		return releases[i].Title < releases[j].Title
	})
	// The queries select whole MSK dates; the exact UTC window is applied here.
	out := releases[:0]
	for _, release := range releases {
		if release.At.Before(from) || release.At.After(to) {
			continue
		}
		out = append(out, release)
	}
	return out, nil
}

// releaseFromEvent maps one trigger-calendar row.
func releaseFromEvent(row eventCalendarRow, msk *time.Location) (Release, bool) {
	date, err := time.ParseInLocation(dateLayout, strings.TrimSpace(row.EventDate), msk)
	if err != nil {
		return Release{}, false
	}
	at, exact := releaseInstant(date, row.EventTime, msk)
	category := eventCategory(row.Category)
	country := "RU"
	if usEventCategories[category] {
		country = "US"
	}
	return Release{At: at.UTC(), Category: category, Title: strings.TrimSpace(row.Title), Country: country, Exact: exact}, true
}

// releaseFromPublication maps one CBR publication-plan row: the topic/title are
// mapped to the contract category (cbr_rate, ru_cpi, ru_gdp, …).
func releaseFromPublication(row publicationCalendarRow, msk *time.Location) (Release, bool) {
	date, err := time.ParseInLocation(dateLayout, strings.TrimSpace(row.PublicationDate), msk)
	if err != nil {
		return Release{}, false
	}
	at, exact := releaseInstant(date, row.PublicationTime, msk)
	return Release{
		At:       at.UTC(),
		Category: publicationCategory(row.Topic, row.Title),
		Title:    strings.TrimSpace(row.Title),
		Country:  "RU",
		Exact:    exact,
	}, true
}

// releaseInstant combines the source date and the optional "HH:MM" wall clock.
// Without a time the instant is the MSK midnight and the event is not exact.
func releaseInstant(date time.Time, clock *string, loc *time.Location) (time.Time, bool) {
	if clock == nil {
		return date, false
	}
	hour, minute, ok := parseClock(*clock)
	if !ok {
		return date, false
	}
	return time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc), true
}

// parseClock parses "HH:MM" or "HH:MM:SS".
func parseClock(raw string) (hour, minute int, ok bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) < 2 {
		return 0, 0, false
	}
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, false
	}
	minute, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// eventCategory maps one trigger category onto the contract name.
func eventCategory(category string) string {
	key := strings.ToLower(strings.TrimSpace(category))
	if mapped, ok := eventCategories[key]; ok {
		return mapped
	}
	return key
}

// publicationCategory maps the CBR topic/title onto the contract name.
func publicationCategory(topic, title string) string {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "ключев") || strings.Contains(lower, "заседани"):
		return "cbr_rate"
	case strings.Contains(lower, "инфляц") || strings.Contains(lower, "ипц"):
		return "ru_cpi"
	case strings.Contains(lower, "ввп"):
		return "ru_gdp"
	}
	if strings.EqualFold(strings.TrimSpace(topic), "analytics") {
		return "cbr_analytics"
	}
	return "cbr_statistics"
}

// moscow returns the Europe/Moscow zone; the embedded tzdata of terminal/feed may
// not be imported by a tool that uses this package, so a fixed UTC+3 fallback
// keeps the current CBR rules correct on hosts without zoneinfo.
func moscow() *time.Location {
	if loc, err := time.LoadLocation("Europe/Moscow"); err == nil {
		return loc
	}
	return time.FixedZone("MSK", 3*60*60)
}
