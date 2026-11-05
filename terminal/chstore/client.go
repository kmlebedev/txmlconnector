// Package chstore is the minimal ClickHouse HTTP client of the market-data
// recorder: JSONEachRow inserts and queries over net/http, without a driver and
// without dependencies outside the standard library.
//
// Credentials travel in the X-ClickHouse-User / X-ClickHouse-Key headers and
// never in the URL, so a logged request line cannot leak them.
package chstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const (
	// maxInsertBatch is the row count of one INSERT request: the plan fixes the
	// ceiling at 10 000 rows per request.
	maxInsertBatch = 10000
	// maxResponseBytes bounds one reply, so a rogue server cannot exhaust memory.
	maxResponseBytes = 64 << 20
	// maxErrorBody bounds the server message stored in an Error.
	maxErrorBody = 8 << 10
	// defaultTimeout bounds one HTTP exchange when the caller passes no client.
	defaultTimeout = 60 * time.Second
	// jsonEachRowFormat is appended to every Insert/Query statement.
	jsonEachRowFormat = "FORMAT JSONEachRow"
)

// tableNameRE accepts "table" and "database.table" SQL identifiers only: the
// table name is interpolated into the INSERT statement, so it must not be able
// to carry SQL.
var tableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(.[A-Za-z_][A-Za-z0-9_]*)?$`)

// Client is a ClickHouse HTTP endpoint. It is safe for concurrent use.
type Client struct {
	baseURL  string
	user     string
	password string
	http     *http.Client
}

// New builds a client for baseURL, for example http://127.0.0.1:8123 or
// http://127.0.0.1:8123/?database=md. A nil hc gets a client bounded by
// defaultTimeout. The URL is validated on the first request, so New has no
// error to return.
func New(baseURL, user, password string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		user:     user,
		password: password,
		http:     hc,
	}
}

// Error is one non-2xx ClickHouse HTTP reply. Body is a bounded excerpt of the
// server message (usually a ClickHouse exception); it never contains the
// credentials.
type Error struct {
	StatusCode int
	Body       string
}

func (e *Error) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("chstore: clickhouse HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("chstore: clickhouse HTTP %d: %s", e.StatusCode, body)
}

// Insert writes rows as JSONEachRow into table, in batches of at most 10 000
// rows. rows must be a slice or an array of JSON-encodable values; an empty
// slice sends no request at all.
func (c *Client) Insert(ctx context.Context, table string, rows any) error {
	if !tableNameRE.MatchString(table) {
		return fmt.Errorf("chstore: unsafe table name %q (want a table or database.table identifier)", table)
	}
	rv := reflect.ValueOf(rows)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return fmt.Errorf("chstore: Insert rows must be a slice or array, got %T", rows)
	}
	total := rv.Len()
	query := "INSERT INTO " + table + " " + jsonEachRowFormat
	for start := 0; start < total; start += maxInsertBatch {
		end := start + maxInsertBatch
		if end > total {
			end = total
		}
		var buf bytes.Buffer
		for i := start; i < end; i++ {
			line, err := json.Marshal(rv.Index(i).Interface())
			if err != nil {
				return fmt.Errorf("chstore: marshal %s row %d: %w", table, i, err)
			}
			buf.Write(line)
			buf.WriteByte('\n')
		}
		if _, err := c.do(ctx, query, nil, buf.Bytes()); err != nil {
			return fmt.Errorf("chstore: insert into %s: %w", table, err)
		}
	}
	return nil
}

// Query runs sql and decodes the JSONEachRow reply into dst, which must be a
// non-nil pointer to a slice (a nil dst discards the rows). Placeholder
// parameters are bound as param_<name> query arguments for the {name:Type}
// placeholders of the statement; a trailing FORMAT JSONEachRow is appended
// unless the caller already wrote one.
func (c *Client) Query(ctx context.Context, sql string, params map[string]string, dst any) error {
	data, err := c.do(ctx, jsonEachRowsSQL(sql), params, nil)
	if err != nil {
		return fmt.Errorf("chstore: query: %w", err)
	}
	if dst == nil {
		return nil
	}
	return decodeJSONEachRow(data, dst)
}

// Exec runs a statement (CREATE/ALTER/DROP) and discards its result rows.
func (c *Client) Exec(ctx context.Context, sql string) error {
	if _, err := c.do(ctx, trimSQL(sql), nil, nil); err != nil {
		return fmt.Errorf("chstore: exec: %w", err)
	}
	return nil
}

// trimSQL removes the trailing semicolon: ClickHouse HTTP rejects it.
func trimSQL(sql string) string {
	return strings.TrimRight(strings.TrimSpace(sql), "; \t\r\n")
}

// jsonEachRowsSQL appends FORMAT JSONEachRow unless it is already there.
func jsonEachRowsSQL(sql string) string {
	sql = trimSQL(sql)
	if strings.Contains(strings.ToUpper(sql), "FORMAT JSONEACHROW") {
		return sql
	}
	return sql + " " + jsonEachRowFormat
}

// decodeJSONEachRow turns newline-separated JSON objects into a JSON array and
// decodes it into dst (the ClickHouse JSONEachRow reply is not an array).
func decodeJSONEachRow(data []byte, dst any) error {
	trimmed := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if trimmed == "" {
		trimmed = "[]"
	} else if trimmed[0] != '[' {
		lines := strings.Split(trimmed, "\n")
		kept := lines[:0]
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				kept = append(kept, line)
			}
		}
		trimmed = "[" + strings.Join(kept, ",") + "]"
	}
	if err := json.Unmarshal([]byte(trimmed), dst); err != nil {
		return fmt.Errorf("chstore: decode JSONEachRow: %w", err)
	}
	return nil
}

// do performs one POST with the SQL (and bound parameters) in the URL query and
// the optional JSONEachRow rows as the request body.
func (c *Client) do(ctx context.Context, query string, params map[string]string, body []byte) ([]byte, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid base URL %q: scheme and host are required", c.baseURL)
	}
	values := u.Query()
	values.Set("query", query)
	for name, value := range params {
		values.Set("param_"+name, value)
	}
	u.RawQuery = values.Encode()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	if c.user != "" {
		req.Header.Set("X-ClickHouse-User", c.user)
	}
	if c.password != "" {
		req.Header.Set("X-ClickHouse-Key", c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read reply: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{StatusCode: resp.StatusCode, Body: errorExcerpt(data)}
	}
	return data, nil
}

// errorExcerpt bounds the server message kept in an Error.
func errorExcerpt(data []byte) string {
	text := strings.TrimSpace(string(data))
	if len(text) > maxErrorBody {
		return text[:maxErrorBody] + "…"
	}
	return text
}
