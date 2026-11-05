package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/chstore"
)

// This file holds the get_news tool of plan Task 12 (Phase D): the agent reads
// the TRANSAQ news recorded by terminal/recorder into md.news, where first_seen
// is the point-in-time of the story.
//
// The tool follows the tools_research.go hook pattern: newsToolCatalog() is
// appended to buildTools(), invokeNewsTool() is called from invokeTool() before
// the core dispatch switch, and the NewsStore comes from agent.Config.NewsStore
// (cmd/terminal builds the ClickHouseNewsStore from CLICKHOUSE_URL/MD_CLICKHOUSE_*
// in cmd/terminal/wiring.go). A nil store means the recording pipeline is off:
// the tool reports a clear configuration error instead of failing the terminal.

// toolNameGetNews is the agent tool name of the recorded TRANSAQ news.
const toolNameGetNews = "get_news"

// Bounds of the get_news arguments.
const (
	// defaultNewsWindow is the since fallback: the last day of news.
	defaultNewsWindow = 24 * time.Hour
	// defaultNewsLimit and maxNewsLimit bound the returned story count.
	defaultNewsLimit = 50
	maxNewsLimit     = 500
)

// NewsItem is one get_news result row: the recorded TRANSAQ news. FirstSeen is
// the instant the recorder first saw the story (its point-in-time), Published is
// the exchange timestamp of the wire header.
type NewsItem struct {
	ID          int64     `json:"id"`
	Published   time.Time `json:"published"`
	FirstSeen   time.Time `json:"firstSeen"`
	Source      string    `json:"source"`
	Title       string    `json:"title"`
	Body        string    `json:"body,omitempty"`
	ContentHash uint64    `json:"contentHash,omitempty"`
}

// NewsStore is the news source of the get_news tool: the md.news table recorded
// by cmd/md-recorder. since is the first_seen lower bound, symbol an optional
// case-insensitive text filter over title and body.
type NewsStore interface {
	News(ctx context.Context, symbol string, since time.Time, limit int) ([]NewsItem, error)
}

// ClickHouseNewsStore reads md.news through the chstore HTTP client.
type ClickHouseNewsStore struct {
	client *chstore.Client
}

// NewClickHouseNewsStore builds the md.news store.
func NewClickHouseNewsStore(client *chstore.Client) *ClickHouseNewsStore {
	return &ClickHouseNewsStore{client: client}
}

// newsRow is one JSONEachRow row of md.news as ClickHouse returns it: the
// DateTime64(3, 'UTC') values are strings.
type newsRow struct {
	FirstSeen   string `json:"first_seen"`
	Published   string `json:"published"`
	NewsID      int64  `json:"news_id"`
	Source      string `json:"source"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ContentHash uint64 `json:"content_hash"`
}

// newsQuery selects the stories first seen at or after {since:DateTime64(3)};
// the optional symbol is a text filter because TRANSAQ news is not tied to one
// security on the wire.
const newsQuery = "SELECT first_seen, published, news_id, source, title, body, content_hash FROM md.news " +
	"WHERE first_seen >= {since:DateTime64(3)}"

// News returns the recorded news first seen at or after since, newest first.
func (s *ClickHouseNewsStore) News(ctx context.Context, symbol string, since time.Time, limit int) ([]NewsItem, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("agent: news store is not configured")
	}
	if limit <= 0 || limit > maxNewsLimit {
		limit = defaultNewsLimit
	}
	sql := newsQuery
	params := map[string]string{
		"since": chNewsTime(since),
		"limit": strconv.Itoa(limit),
	}
	if symbol != "" {
		sql += " AND positionCaseInsensitiveUTF8(concat(title, ' ', body), {symbol:String}) > 0"
		params["symbol"] = symbol
	}
	sql += " ORDER BY first_seen DESC LIMIT {limit:UInt32}"
	var rows []newsRow
	if err := s.client.Query(ctx, sql, params, &rows); err != nil {
		return nil, fmt.Errorf("agent: news query: %w", err)
	}
	items := make([]NewsItem, 0, len(rows))
	for _, row := range rows {
		firstSeen, err := parseCHNewsTime(row.FirstSeen)
		if err != nil {
			return nil, fmt.Errorf("agent: news %d first_seen: %w", row.NewsID, err)
		}
		published, err := parseCHNewsTime(row.Published)
		if err != nil {
			return nil, fmt.Errorf("agent: news %d published: %w", row.NewsID, err)
		}
		items = append(items, NewsItem{
			ID:          row.NewsID,
			Published:   published,
			FirstSeen:   firstSeen,
			Source:      row.Source,
			Title:       row.Title,
			Body:        row.Body,
			ContentHash: row.ContentHash,
		})
	}
	return items, nil
}

// chNewsTime renders the DateTime64(3, 'UTC') binding of the since filter.
func chNewsTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000")
}

// parseCHNewsTime parses a ClickHouse DateTime64 text value: the default JSON
// output is "2006-01-02 15:04:05[.fff…]" in the column zone (UTC); an RFC3339
// value is accepted too in case the server is configured to emit it.
func parseCHNewsTime(raw string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if parsed, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparsable DateTime64 %q", raw)
}

// getNewsToolInfo describes the get_news tool for the engine catalog.
func getNewsToolInfo() ToolInfo {
	return ToolInfo{
		Name:  toolNameGetNews,
		Title: "Новости",
		Description: "Новости TRANSAQ, записанные рекордером с моментом первого появления (point-in-time): " +
			"источник, заголовок, время публикации и текст. Сортировка — от свежих к старым; since " +
			"фильтрует по first_seen.",
		Input: objSchema(map[string]any{
			"symbol": strProp("Необязательный тикер: оставить новости, где он упоминается в заголовке или тексте"),
			"since":  strProp("Начало окна: RFC3339, ГГГГ-ММ-ДД, ГГГГ-ММ-ДД ЧЧ:ММ:СС или длительность (24h); по умолчанию 24 часа"),
			"limit":  intProp(fmt.Sprintf("Сколько новостей вернуть, 1..%d (по умолчанию %d)", maxNewsLimit, defaultNewsLimit)),
		}),
	}
}

// newsToolCatalog returns the news tool catalog appended by the tools.go owner.
func newsToolCatalog() []ToolInfo {
	return []ToolInfo{getNewsToolInfo()}
}

// invokeNewsTool dispatches one news tool call. handled is false for every other
// name, so the core dispatch can fall through. store is the md.news source: nil
// means the recording pipeline is not configured (a clear tool error).
func (e *Engine) invokeNewsTool(ctx context.Context, store NewsStore, name string, args map[string]any) (result any, artifact *Artifact, handled bool, err error) {
	if name != toolNameGetNews {
		return nil, nil, false, nil
	}
	symbol, since, limit, err := e.parseNewsArgs(args)
	if err != nil {
		return nil, nil, true, err
	}
	if store == nil {
		return nil, nil, true, errors.New("agent: news store is not configured (md.news recording is off)")
	}
	if err := contextErr(ctx); err != nil {
		return nil, nil, true, err
	}
	items, err := store.News(ctx, symbol, since, limit)
	if err != nil {
		return nil, nil, true, err
	}
	return newsResult{Since: since, News: items}, nil, true, nil
}

// toolGetNews implements get_news on top of a NewsStore.
func (e *Engine) toolGetNews(ctx context.Context, store NewsStore, args map[string]any) ([]NewsItem, error) {
	symbol, since, limit, err := e.parseNewsArgs(args)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("agent: news store is not configured (md.news recording is off)")
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	return store.News(ctx, symbol, since, limit)
}

// parseNewsArgs validates the get_news arguments: an optional symbol text
// filter, the first_seen window (RFC3339/date/time/duration, default 24 h) and
// the row limit.
func (e *Engine) parseNewsArgs(args map[string]any) (symbol string, since time.Time, limit int, err error) {
	symbol = strings.TrimSpace(argString(args, "symbol"))
	limitArg, err := argIntStrict(args, "limit", defaultNewsLimit, 1, maxNewsLimit)
	if err != nil {
		return "", time.Time{}, 0, err
	}
	since, err = parseNewsSince(argString(args, "since"), e.now())
	if err != nil {
		return "", time.Time{}, 0, err
	}
	return symbol, since, int(limitArg), nil
}

// parseNewsSince parses the since argument: a relative duration is subtracted
// from now, an absolute value is taken as UTC.
func parseNewsSince(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return now.Add(-defaultNewsWindow).UTC(), nil
	}
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		return now.Add(-duration).UTC(), nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: since=%q: want RFC3339, YYYY-MM-DD, YYYY-MM-DD HH:MM:SS or a duration like 24h",
		errBadArgument, raw)
}

// newsResult is the get_news tool payload: the window and the stories.
type newsResult struct {
	Since time.Time  `json:"since"`
	News  []NewsItem `json:"news"`
}
