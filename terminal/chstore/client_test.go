package chstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kmlebedev/txmlconnector/terminal/chstore"
)

// clickHouseStub is an httptest ClickHouse: it records every request and answers
// with the configured body/status, so the client is tested without a database.
type clickHouseStub struct {
	t        *testing.T
	mu       sync.Mutex
	requests []stubRequest
	status   int
	response string
}

type stubRequest struct {
	method string
	query  map[string][]string
	user   string
	key    string
	body   string
}

func newClickHouseStub(t *testing.T, response string) (*clickHouseStub, *httptest.Server) {
	t.Helper()
	stub := &clickHouseStub{t: t, status: http.StatusOK, response: response}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		stub.mu.Lock()
		stub.requests = append(stub.requests, stubRequest{
			method: r.Method,
			query:  r.URL.Query(),
			user:   r.Header.Get("X-ClickHouse-User"),
			key:    r.Header.Get("X-ClickHouse-Key"),
			body:   string(body),
		})
		status, response := stub.status, stub.response
		stub.mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return stub, srv
}

func (s *clickHouseStub) captured() []stubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stubRequest(nil), s.requests...)
}

func (s *clickHouseStub) setResponse(status int, body string) {
	s.mu.Lock()
	s.status, s.response = status, body
	s.mu.Unlock()
}

// TestChstoreInsertBatchesJSONEachRow is the plan test: Insert sends
// INSERT ... FORMAT JSONEachRow in batches of at most 10 000 rows and passes the
// credentials in the ClickHouse headers, never in the URL.
func TestChstoreInsertBatchesJSONEachRow(t *testing.T) {
	stub, srv := newClickHouseStub(t, "")
	type bookRow struct {
		TS      string `json:"ts"`
		SecCode string `json:"sec_code"`
		Price   string `json:"price"`
		Qty     int64  `json:"qty"`
	}
	const total = 10001 // one full batch plus a short tail
	rows := make([]bookRow, total)
	for i := range rows {
		rows[i] = bookRow{TS: "2026-11-05 10:00:00.000", SecCode: "SBER", Price: "291.460000", Qty: int64(i)}
	}
	client := chstore.New(srv.URL, "md-user", "md-password", srv.Client())
	if err := client.Insert(context.Background(), "md.book_events", rows); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got := stub.captured()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want 2 batches (10000 + 1)", len(got))
	}
	for i, req := range got {
		if req.method != http.MethodPost {
			t.Fatalf("request %d method = %s, want POST", i, req.method)
		}
		if want := "INSERT INTO md.book_events FORMAT JSONEachRow"; !strings.Contains(req.query["query"][0], want) {
			t.Fatalf("request %d query = %q, want %q", i, req.query["query"][0], want)
		}
		if req.user != "md-user" || req.key != "md-password" {
			t.Fatalf("request %d credentials headers = %q/%q", i, req.user, req.key)
		}
		if strings.Contains(req.query["query"][0], "md-password") {
			t.Fatalf("request %d leaks the password in the URL", i)
		}
		lines := strings.Split(strings.TrimSpace(req.body), "\n")
		wantLines := 10000
		if i == 1 {
			wantLines = 1
		}
		if len(lines) != wantLines {
			t.Fatalf("request %d lines = %d, want %d", i, len(lines), wantLines)
		}
		var row bookRow
		if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
			t.Fatalf("request %d row is not JSON: %v", i, err)
		}
		if row.SecCode != "SBER" || row.Price != "291.460000" {
			t.Fatalf("request %d row = %+v", i, row)
		}
	}
	// No rows: no request at all (an empty INSERT is a ClickHouse syntax error).
	if err := client.Insert(context.Background(), "md.book_events", []bookRow{}); err != nil {
		t.Fatalf("empty Insert: %v", err)
	}
	if n := len(stub.captured()); n != 2 {
		t.Fatalf("empty rows sent %d requests, want none", n-2)
	}
	// Anything that is not a slice is a programming error, not a silent no-op.
	if err := client.Insert(context.Background(), "md.book_events", bookRow{}); err == nil {
		t.Fatal("Insert of a single struct must fail")
	}
	// The table name is a SQL identifier: reject injection attempts.
	if err := client.Insert(context.Background(), "md.book_events FORMAT JSONEachRow; DROP TABLE md.trades", rows[:1]); err == nil {
		t.Fatal("Insert with an unsafe table name must fail")
	}
	// The separator in database.table is a literal dot, not "any character":
	// a hyphenated name is not a valid identifier and must be rejected.
	if err := client.Insert(context.Background(), "md-book_events", rows[:1]); err == nil {
		t.Fatal("Insert with a non-identifier table name must fail")
	}
	if err := client.Insert(context.Background(), "md.book_events", rows[:1]); err != nil {
		t.Fatalf("Insert into database.table: %v", err)
	}
}

// TestChstoreQueryParams is the plan test: Query sends the SQL with
// FORMAT JSONEachRow appended, binds parameters as param_<name>, keeps the base
// URL query (database=md) and decodes the JSONEachRow reply into dst.
func TestChstoreQueryParams(t *testing.T) {
	const response = "{\"symbol\":\"TQBR:SBER\",\"last\":\"293.22\"}\n{\"symbol\":\"TQBR:GAZP\",\"last\":\"150.10\"}\n"
	stub, srv := newClickHouseStub(t, response)
	client := chstore.New(srv.URL+"?database=md", "md-user", "md-password", srv.Client())
	var dst []struct {
		Symbol string `json:"symbol"`
		Last   string `json:"last"`
	}
	sql := "SELECT symbol, last FROM md.trades WHERE sec_code = {code:String} AND ts >= {since:DateTime64(3)}"
	params := map[string]string{"code": "SBER", "since": "2026-11-05T10:00:00Z"}
	if err := client.Query(context.Background(), sql, params, &dst); err != nil {
		t.Fatalf("Query: %v", err)
	}
	got := stub.captured()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	req := got[0]
	if req.query["param_code"][0] != "SBER" || req.query["param_since"][0] != "2026-11-05T10:00:00Z" {
		t.Fatalf("bound params = %v", req.query)
	}
	if !strings.Contains(req.query["query"][0], "FORMAT JSONEachRow") {
		t.Fatalf("query = %q, missing FORMAT JSONEachRow", req.query["query"][0])
	}
	if req.query["database"][0] != "md" {
		t.Fatalf("base URL query lost: %v", req.query)
	}
	if len(dst) != 2 || dst[0].Symbol != "TQBR:SBER" || dst[1].Last != "150.10" {
		t.Fatalf("decoded dst = %+v", dst)
	}
}

// TestChstoreExec sends a DDL statement: no FORMAT and no result decoding.
func TestChstoreExec(t *testing.T) {
	stub, srv := newClickHouseStub(t, "")
	client := chstore.New(srv.URL, "", "", srv.Client())
	ddl := "CREATE TABLE IF NOT EXISTS md.trades (ts DateTime64(3, 'UTC')) ENGINE = MergeTree ORDER BY ts"
	if err := client.Exec(context.Background(), ddl); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	got := stub.captured()
	if len(got) != 1 || !strings.Contains(got[0].query["query"][0], "CREATE TABLE IF NOT EXISTS md.trades") {
		t.Fatalf("requests = %+v", got)
	}
	if strings.Contains(got[0].query["query"][0], "FORMAT") {
		t.Fatalf("Exec must not append FORMAT: %q", got[0].query["query"][0])
	}
}

// TestChstoreReportsHTTPError checks that a ClickHouse error body reaches the
// caller (bounded, without credentials) instead of a bare "status 500".
func TestChstoreReportsHTTPError(t *testing.T) {
	stub, srv := newClickHouseStub(t, "")
	stub.setResponse(http.StatusInternalServerError, "Code: 60. DB::Exception: Table md.book_events doesn't exist")
	client := chstore.New(srv.URL, "md-user", "md-password", srv.Client())
	err := client.Exec(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("Exec must fail on HTTP 500")
	}
	var apiErr *chstore.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("error = %v, want *chstore.Error with status 500", err)
	}
	if !strings.Contains(err.Error(), "doesn't exist") {
		t.Fatalf("error = %v, want the ClickHouse exception text", err)
	}
	if strings.Contains(err.Error(), "md-password") {
		t.Fatalf("error leaks the password: %v", err)
	}
	// A nil destination discards the response.
	stub.setResponse(http.StatusOK, "{\"x\":1}\n")
	if err := client.Query(context.Background(), "SELECT 1", nil, nil); err != nil {
		t.Fatalf("Query with nil dst: %v", err)
	}
}

// TestChstoreRejectsInvalidBaseURL keeps the constructor signature simple
// (no error) but reports the broken URL on the first request.
func TestChstoreRejectsInvalidBaseURL(t *testing.T) {
	client := chstore.New("://bad url", "", "", http.DefaultClient)
	if err := client.Exec(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("Exec with an invalid base URL must fail")
	}
}
