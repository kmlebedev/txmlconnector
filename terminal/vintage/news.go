package vintage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// newsQuery reads the annotated news feed view of the rosstat layer: the view
// itself joins v_news_events with the LLM annotation (news_annotation), so the
// nullable sentiment and the tag tuples arrive already resolved.
const newsQuery = `SELECT toTimeZone(date, 'UTC') AS date, source, title, description, sentiment, tags
FROM v_news_events
WHERE date >= toDateTime64({from:String}, 3, 'UTC')
  AND date <= toDateTime64({to:String}, 3, 'UTC')%s
ORDER BY date DESC`

// newsRow is one JSONEachRow row of v_news_events.
type newsRow struct {
	Date        string     `json:"date"`
	Source      string     `json:"source"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Sentiment   *float64   `json:"sentiment"`
	Tags        [][]string `json:"tags"`
}

// News returns the annotated news stories published in [from, to], newest
// first. sources is the optional source filter of the feed; a story without an
// LLM annotation has Sentiment 0 and no Tags (NULL/[] in the view), which means
// "not annotated", not "neutral".
func (s *ClickHouseStore) News(ctx context.Context, from, to time.Time, sources ...string) ([]NewsItem, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if from.IsZero() || to.IsZero() {
		return nil, errors.New("vintage: the news window is required")
	}
	from, to = from.UTC(), to.UTC()
	if to.Before(from) {
		return nil, errors.New("vintage: the news window is inverted (to < from)")
	}
	params := map[string]string{
		"from": chDateTime(from),
		"to":   chDateTime(to),
	}
	filter := ""
	var placeholders []string
	for i, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		name := fmt.Sprintf("src%d", i)
		params[name] = source
		placeholders = append(placeholders, "{"+name+":String}")
	}
	if len(placeholders) > 0 {
		filter = "\n  AND source IN (" + strings.Join(placeholders, ", ") + ")"
	}
	var rows []newsRow
	if err := s.client.Query(ctx, fmt.Sprintf(newsQuery, filter), params, &rows); err != nil {
		return nil, fmt.Errorf("vintage: news: %w", err)
	}
	items := make([]NewsItem, 0, len(rows))
	for _, row := range rows {
		at, err := parseCHTime(row.Date)
		if err != nil {
			return nil, fmt.Errorf("vintage: news date %q: %w", row.Date, err)
		}
		item := NewsItem{At: at, Source: row.Source, Title: row.Title}
		if row.Sentiment != nil {
			item.Sentiment = *row.Sentiment
		}
		for _, tag := range row.Tags {
			if len(tag) == 0 {
				continue
			}
			item.Tags = append(item.Tags, strings.Join(tag, ":"))
		}
		items = append(items, item)
	}
	return items, nil
}
