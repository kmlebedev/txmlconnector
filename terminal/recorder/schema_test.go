package recorder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/terminal/chstore"
)

// chStub is a minimal ClickHouse HTTP endpoint for the sink tests.
type chStub struct {
	mu    sync.Mutex
	paths []string
	query []string
	body  []string
}

func newCHStub(t *testing.T) (*chStub, *httptest.Server) {
	t.Helper()
	stub := &chStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.paths = append(stub.paths, r.URL.Path)
		stub.query = append(stub.query, r.URL.Query().Get("query"))
		stub.body = append(stub.body, string(body))
		stub.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return stub, srv
}

func (s *chStub) captured() (query, body []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.query...), append([]string(nil), s.body...)
}

// TestSchemaStatements pins the plan Task 11 DDL contract.
func TestSchemaStatements(t *testing.T) {
	ddl := strings.Join(Schema(), "\n")
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS md.book_events",
		"ts DateTime64(3, 'UTC')",
		"recv_ts DateTime64(6, 'UTC')",
		"session_id UUID",
		"seq UInt64",
		"kind Enum8('snapshot' = 1, 'level' = 2, 'gap' = 3)",
		"side Enum8('bid' = 1, 'ask' = 2, 'none' = 0)",
		"price Decimal64(6)",
		"ORDER BY (sec_code, ts, seq)",
		"PARTITION BY toYYYYMMDD(ts)",
		"CREATE TABLE IF NOT EXISTS md.trades",
		"trade_no Int64",
		"side Enum8('buy' = 1, 'sell' = 2, 'none' = 0)",
		"open_interest Int64",
		"ReplacingMergeTree",
		"ORDER BY (sec_code, trade_no)",
	} {
		if !strings.Contains(ddl, want) {
			t.Fatalf("schema is missing %q:\n%s", want, ddl)
		}
	}
	if strings.Contains(ddl, "Float") {
		t.Fatalf("schema must not use floating point prices:\n%s", ddl)
	}
}

// TestEnsureSchemaCreatesTables runs the DDL through the HTTP client.
func TestEnsureSchemaCreatesTables(t *testing.T) {
	stub, srv := newCHStub(t)
	client := chstore.New(srv.URL, "", "", srv.Client())
	if err := EnsureSchema(context.Background(), client); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	query, _ := stub.captured()
	if len(query) != 2 {
		t.Fatalf("requests = %d, want 2", len(query))
	}
	if !strings.Contains(query[0], "CREATE TABLE IF NOT EXISTS md.book_events") || !strings.Contains(query[1], "CREATE TABLE IF NOT EXISTS md.trades") {
		t.Fatalf("DDL requests = %q", query)
	}
	if err := EnsureSchema(context.Background(), nil); err == nil {
		t.Fatal("nil ClickHouse client must fail")
	}
}

// TestBookRowMapping checks the exact JSONEachRow values: millisecond and
// microsecond times, enum names and the fixed-scale decimal price.
func TestBookRowMapping(t *testing.T) {
	ts := time.Date(2026, 11, 5, 13, 0, 0, 123_000_000, time.UTC)
	recv := time.Date(2026, 11, 5, 13, 0, 0, 123_456_000, time.UTC)
	row, err := bookRowOf(BookEvent{
		TS: ts, RecvTS: recv, SessionID: "11111111-2222-4333-8444-555555555555", Seq: 7,
		Board: "TQBR", SecCode: "SBER", Kind: BookKindLevel, Side: SideAsk,
		Price: model.Decimal{Units: 29146, Scale: 2}, Qty: 30,
	})
	if err != nil {
		t.Fatalf("bookRowOf: %v", err)
	}
	if row.TS != "2026-11-05 13:00:00.123" || row.RecvTS != "2026-11-05 13:00:00.123456" {
		t.Fatalf("times = %q/%q", row.TS, row.RecvTS)
	}
	if row.Price != "291.460000" {
		t.Fatalf("price = %q, want the Decimal64(6) string", row.Price)
	}
	if row.Kind != "level" || row.Side != "ask" || row.Qty != 30 || row.Seq != 7 {
		t.Fatalf("row = %+v", row)
	}
	if _, err := bookRowOf(BookEvent{Kind: BookKind(9)}); err == nil {
		t.Fatal("an invalid book kind must fail, not write an Enum8 it does not have")
	}
}

// TestTradeRowMapping checks the trade enum names (buy/sell, not bid/ask) and
// the exact decimal conversion, including a negative value.
func TestTradeRowMapping(t *testing.T) {
	ts := time.Date(2026, 11, 5, 13, 0, 1, 500_000_000, time.UTC)
	row, err := tradeRowOf(TradeEvent{
		TS: ts, RecvTS: ts, Board: "TQBR", SecCode: "SBER", TradeNo: 42,
		Price: model.Decimal{Units: 2914600, Scale: 4}, Qty: 10, Side: SideBid, OpenInterest: 777,
	})
	if err != nil {
		t.Fatalf("tradeRowOf: %v", err)
	}
	if row.Price != "291.460000" || row.Side != "buy" || row.OpenInterest != 777 {
		t.Fatalf("row = %+v", row)
	}
	negative := model.Decimal{Units: -123456, Scale: 5}
	if got := decimalString(negative, priceScale); got != "-1.234560" {
		t.Fatalf("negative decimal = %q", got)
	}
	if got := decimalString(model.Decimal{Units: 7, Scale: 8}, priceScale); got != "0.000000" {
		t.Fatalf("over-precise decimal = %q, want the documented truncation", got)
	}
	if _, err := tradeRowOf(TradeEvent{Side: Side(9)}); err == nil {
		t.Fatal("an invalid trade side must fail")
	}
}

// TestClickHouseSinkInserts wires the typed events through the HTTP client into
// the two md tables.
func TestClickHouseSinkInserts(t *testing.T) {
	stub, srv := newCHStub(t)
	client := chstore.New(srv.URL, "md-user", "md-password", srv.Client())
	sink := NewClickHouseSink(context.Background(), client)
	if err := sink.Book([]BookEvent{{
		TS: testNow, RecvTS: testNow, SessionID: "11111111-2222-4333-8444-555555555555", Seq: 1,
		Board: "TQBR", SecCode: "SBER", Kind: BookKindSnapshot, Side: SideBid,
		Price: model.Decimal{Units: 29146, Scale: 2}, Qty: 10,
	}}); err != nil {
		t.Fatalf("Book: %v", err)
	}
	if err := sink.Trades([]TradeEvent{{
		TS: testNow, RecvTS: testNow, Board: "TQBR", SecCode: "SBER", TradeNo: 42,
		Price: model.Decimal{Units: 29146, Scale: 2}, Qty: 5, Side: SideBid,
	}}); err != nil {
		t.Fatalf("Trades: %v", err)
	}
	query, body := stub.captured()
	if len(query) != 2 {
		t.Fatalf("requests = %d, want 2", len(query))
	}
	if !strings.Contains(query[0], "INSERT INTO md.book_events FORMAT JSONEachRow") {
		t.Fatalf("book insert query = %q", query[0])
	}
	if !strings.Contains(query[1], "INSERT INTO md.trades FORMAT JSONEachRow") {
		t.Fatalf("trades insert query = %q", query[1])
	}
	var book map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body[0])), &book); err != nil {
		t.Fatalf("book row is not JSON: %v", err)
	}
	if book["kind"] != "snapshot" || book["side"] != "bid" || book["price"] != "291.460000" {
		t.Fatalf("book row = %v", book)
	}
	var trade map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body[1])), &trade); err != nil {
		t.Fatalf("trade row is not JSON: %v", err)
	}
	if trade["side"] != "buy" || trade["trade_no"] != float64(42) {
		t.Fatalf("trade row = %v", trade)
	}
}

// TestNewSessionIDIsUUIDv4 pins the session id shape: ClickHouse UUID columns
// reject anything else.
func TestNewSessionIDIsUUIDv4(t *testing.T) {
	goUUID := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 8; i++ {
		id, err := newSessionID()
		if err != nil {
			t.Fatalf("newSessionID: %v", err)
		}
		if !goUUID.MatchString(id) {
			t.Fatalf("session id = %q, want UUIDv4", id)
		}
	}
}
