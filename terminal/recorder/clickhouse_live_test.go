package recorder_test

// Live end-to-end test of the recorder: a real native client talks to the
// server-side test double (native/testserver) and the ClickHouseSink writes the
// decoded book/trade events into the local dev ClickHouse of the
// clickhouse-import-rosstat project. This is the T11 pipeline (exchange ->
// native decode -> recorder -> md.book_events/trades) without the demo window.
//
//	TXML_IT_CLICKHOUSE=1 TXML_CLICKHOUSE_URL=http://localhost:8123 \
//	  go test ./terminal/recorder -run Live -v
//
// The test truncates nothing: it compares row counts before and after, so it is
// safe against a shared md database. Without TXML_IT_CLICKHOUSE it skips.

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/terminal/chstore"
	"github.com/kmlebedev/txmlconnector/terminal/recorder"
)

func liveClickHouse(t *testing.T) *chstore.Client {
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
	return chstore.New(raw, os.Getenv("TXML_CLICKHOUSE_USER"), os.Getenv("TXML_CLICKHOUSE_PASSWORD"), nil)
}

// readHexPayload decodes a hex capture from native/records/testdata.
func readHexPayload(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "native", "records", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	decoded, err := hex.DecodeString(strings.Join(strings.Fields(string(raw)), ""))
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return decoded
}

func bookCounts(t *testing.T, cl *chstore.Client) map[string]uint64 {
	t.Helper()
	var rows []struct {
		Kind string `json:"kind"`
		N    uint64 `json:"n"`
	}
	if err := cl.Query(context.Background(), "SELECT kind, count() AS n FROM md.book_events GROUP BY kind", nil, &rows); err != nil {
		t.Fatalf("book counts: %v", err)
	}
	out := map[string]uint64{}
	for _, row := range rows {
		out[row.Kind] = row.N
	}
	return out
}

func tradeCount(t *testing.T, cl *chstore.Client) uint64 {
	t.Helper()
	var rows []struct {
		N uint64 `json:"n"`
	}
	if err := cl.Query(context.Background(), "SELECT count() AS n FROM md.trades", nil, &rows); err != nil {
		t.Fatalf("trade count: %v", err)
	}
	if len(rows) == 0 {
		return 0
	}
	return rows[0].N
}

// TestLiveRecorderWritesBookAndTradesToClickHouse is the T11 end-to-end check.
func TestLiveRecorderWritesBookAndTradesToClickHouse(t *testing.T) {
	cl := liveClickHouse(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := recorder.EnsureSchema(ctx, cl); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	baseBook := bookCounts(t, cl)
	baseTrades := tradeCount(t, cl)

	srv := testserver.New(t)
	srv.Startup = readHexPayload(t, "startup-demo.hex")
	client, err := native.Dial(ctx, native.ClientConfig{
		Address:  "127.0.0.1:3900",
		Login:    testserver.Login,
		Password: testserver.Password,
		Dial:     srv.Dial,
	})
	if err != nil {
		t.Fatalf("native.Dial: %v", err)
	}
	defer client.Close()

	// The startup fixture carries the board dictionaries but no securities: add
	// the TQBR:SBER catalog entry the recorder subscription resolves (the demo
	// server would deliver it in the startup catalog).
	client.Catalog().AddSecurity(records.SecNew{
		SecID: 26, Market: 1, Kind: 1, SecCode: "SBER", ShortName: "SBER",
		Pits:     []records.Pit{{Board: 37, Decimals: 2, Default: true, Text: "TQBR", Currency: "RUB"}},
		Currency: "RUB",
	})

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		done <- recorder.Run(runCtx, client, recorder.NewClickHouseSink(ctx, cl), []string{"TQBR:SBER"})
	}()

	// Wait for the recorder subscription, then push a live order book and
	// trades into the next RQ_MAIN reply (the fixtures are anonymized demo
	// captures: GAZP 379 + SBER 26 boards, SBER trades).
	deadline := time.Now().Add(10 * time.Second)
	for {
		subscribed := false
		for _, frame := range srv.Requests() {
			if frame.Type == session.RqSubscribe {
				subscribed = true
				break
			}
		}
		if subscribed {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("recorder.Run exited before subscribing: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorder did not subscribe: %v", srv.Requests())
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The first <quotes> callback after a subscription is the full snapshot;
	// the same SBER record delivered again becomes level deltas.
	srv.QueueMain(readHexPayload(t, "quote-live.hex"))
	srv.QueueMain(readHexPayload(t, "quote-live.hex"))
	srv.QueueMain(readHexPayload(t, "alltrades-inject1.hex"))

	// Wait for the batch insert of both events.
	for {
		book := bookCounts(t, cl)
		trades := tradeCount(t, cl)
		if book["snapshot"] > baseBook["snapshot"] && book["level"] > baseBook["level"] && trades > baseTrades {
			break
		}
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatalf("no rows in ClickHouse: book before=%v after=%v, trades before=%d after=%d", baseBook, book, baseTrades, trades)
		}
		time.Sleep(50 * time.Millisecond)
	}

	stop()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("recorder.Run: %v", err)
	}

	// The row contract: the SBER board rows are decoded with a Decimal price.
	var rows []struct {
		Kind    string  `json:"kind"`
		Board   string  `json:"board"`
		SecCode string  `json:"sec_code"`
		Price   float64 `json:"price"`
		Qty     int64   `json:"qty"`
	}
	if err := cl.Query(context.Background(),
		"SELECT kind, board, sec_code, price, qty FROM md.book_events WHERE sec_code = 'SBER' ORDER BY ts DESC LIMIT 5", nil, &rows); err != nil {
		t.Fatalf("read book rows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("no SBER book rows after the recorder batch")
	}
	for _, row := range rows {
		if row.Board != "TQBR" || row.SecCode != "SBER" {
			t.Fatalf("unexpected book row: %+v", row)
		}
		if row.Price <= 0 {
			t.Fatalf("book row without a price: %+v", row)
		}
	}
	t.Logf("recorder wrote %v book rows and %d trades", bookCounts(t, cl), tradeCount(t, cl))
}
