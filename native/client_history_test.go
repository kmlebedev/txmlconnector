package native

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

func readCandlesLive(t *testing.T) []byte {
	t.Helper()
	f, err := os.Open("records/testdata/candles-live.hex.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("candles fixture: %v", err)
	}
	return payload
}

func readHistoryPart(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return payload
}

// TestHistoryCollectsParts checks the multipart rule: status 2 parts are
// collected until status != 2, then History returns one chronological Candles
// and Run does not emit the parts as events.
func TestHistoryCollectsParts(t *testing.T) {
	srv := newRunServer(t)
	srv.HistoryParts = [][]byte{
		readHistoryPart(t, "candles-part1-inject.hex"),
		readHistoryPart(t, "candles-part2-inject.hex"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	var mu sync.Mutex
	var runEvents []model.Event
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = c.Run(ctx, RunOptions{}, func(events []model.Event) error {
			mu.Lock()
			runEvents = append(runEvents, events...)
			mu.Unlock()
			return nil
		})
	}()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	if sec, board, ok := c.Catalog().Lookup(sber.Board, sber.SecCode); ok {
		t.Logf("catalog: secid=%d board.Num=%d", sec.SecID, board.Num)
	}
	candles, err := c.History(ctx, HistoryRequest{Symbol: sber, Period: 1, Count: 2, Reset: true})
	if err != nil {
		var types []byte
		for _, f := range srv.Requests() {
			types = append(types, f.Type)
		}
		t.Fatalf("History: %v (requests: %v); runEvents=%d", err, types, len(runEvents))
	}
	if candles.Status != 1 || len(candles.Items) != 2 {
		t.Fatalf("candles = status %d, %d items", candles.Status, len(candles.Items))
	}
	if got := candles.Items[0].Time.UTC().Format("15:04"); got != "15:43" {
		t.Fatalf("first candle UTC time = %s, want 15:43Z", got)
	}
	if got := candles.Items[1].Time.UTC().Format("15:04"); got != "15:44" {
		t.Fatalf("second candle UTC time = %s, want 15:44Z", got)
	}
	got, ok := xmlcompat.Render(c.Catalog(), &candles, xmlcompat.Options{})
	if !ok {
		t.Fatal("no XML form")
	}
	want, err := os.ReadFile("records/testdata/candles-multipart-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.TrimSpace(string(want)) {
		t.Fatalf("candles\n got %s\nwant %s", got, strings.TrimSpace(string(want)))
	}
	mu.Lock()
	defer mu.Unlock()
	for _, ev := range runEvents {
		if _, isCandles := ev.(*model.Candles); isCandles {
			t.Fatalf("Run emitted a candles part: %+v", runEvents)
		}
	}
}

// TestConnectorGetHistoryData is the e2e XML command: both forms render exactly
// one <candles> callback from the collected parts.
func TestConnectorGetHistoryData(t *testing.T) {
	srv := newRunServer(t)
	srv.HistoryParts = [][]byte{
		readHistoryPart(t, "candles-part1-inject.hex"),
		readHistoryPart(t, "candles-part2-inject.hex"),
	}
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "gethistorydata" + quote + " secid=" + quote + "26" + quote
	cmd += " period=" + quote + "1" + quote + " count=" + quote + "2" + quote + " reset=" + quote + "true" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("gethistorydata attr = %s", got)
	}
	want, err := os.ReadFile("records/testdata/candles-multipart-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.next(t, "<candles"); got != strings.TrimSpace(string(want)) {
		t.Fatalf("candles = %s, want %s", got, strings.TrimSpace(string(want)))
	}
	// The <security> form resolves by board+seccode.
	cmd = "<command id=" + quote + "gethistorydata" + quote + "><security><board>TQBR</board>"
	cmd += "<seccode>SBER</seccode></security><period>1</period><count>2</count><reset>true</reset></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("gethistorydata security = %s", got)
	}
	if got := rec.next(t, "<candles"); got != strings.TrimSpace(string(want)) {
		t.Fatalf("candles security form = %s", got)
	}
}

// TestHistorySinglePartOrder is the P1 regression through the real History path
// (not the direct Event call): one 10-candle part must come out 18:35..18:44.
func TestHistorySinglePartOrder(t *testing.T) {
	srv := newRunServer(t)
	srv.HistoryParts = [][]byte{readCandlesLive(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = c.Run(ctx, RunOptions{}, func([]model.Event) error { return nil })
	}()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	candles, err := c.History(ctx, HistoryRequest{Symbol: sber, Period: 1, Count: 10, Reset: true})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(candles.Items) != 10 {
		t.Fatalf("items = %d, want 10", len(candles.Items))
	}
	// Items keep UTC instants (15:35Z..15:44Z); the DLL wall clock stays 18:35..18:44.
	if first, last := candles.Items[0].Time.UTC().Format("15:04"), candles.Items[9].Time.UTC().Format("15:04"); first != "15:35" || last != "15:44" {
		t.Fatalf("order = %s..%s, want 15:35Z..15:44Z", first, last)
	}
	rendered, ok := xmlcompat.Render(c.Catalog(), &candles, xmlcompat.Options{})
	if !ok || !strings.Contains(rendered, "04.11.2026 18:35:00") || !strings.Contains(rendered, "04.11.2026 18:44:00") {
		t.Fatalf("rendered candles do not keep the wall clock: %q", rendered)
	}
	cancel()
	<-runDone
}

// TestHistoryWakesOnRecovery checks the P2.3 rule: a reset of the history state
// (relogin) wakes the waiting History with an error instead of leaving it until
// the context timeout.
func TestHistoryWakesOnRecovery(t *testing.T) {
	srv := newRunServer(t) // no HistoryParts: the collection never completes
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = c.Run(ctx, RunOptions{}, func([]model.Event) error { return nil })
	}()
	errCh := make(chan error, 1)
	go func() {
		_, err := c.History(ctx, HistoryRequest{Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, Period: 1, Count: 2, Reset: true})
		errCh <- err
	}()
	// Wait until the collector is registered, then reset it like a relogin does.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		pending := len(c.history)
		c.mu.Unlock()
		if pending > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("history collector was not registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	c.failHistoryParts(errors.New("transaq: connection was recovered"))
	c.mu.Unlock()
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "recovered") {
			t.Fatalf("History err = %v, want the recovery error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("History did not wake on the recovery reset")
	}
	cancel()
	<-runDone
}

// TestHistoryCatalogRace runs History repeatedly while Run polls (and mutates
// the catalog under the client lock): the -race build must stay clean.
func TestHistoryCatalogRace(t *testing.T) {
	srv := newRunServer(t)
	srv.HistoryParts = [][]byte{readHistoryPart(t, "candles-part1-inject.hex"), readHistoryPart(t, "candles-part2-inject.hex")}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = c.Run(ctx, RunOptions{}, func([]model.Event) error { return nil })
	}()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	for i := 0; i < 10; i++ {
		if _, err := c.History(ctx, HistoryRequest{Symbol: sber, Period: 1, Count: 2, Reset: true}); err != nil {
			t.Fatalf("History %d: %v", i, err)
		}
	}
	cancel()
	<-runDone
}

// TestConnectorHistoryResultBeforeCandles checks the P2.4 DLL order: the command
// returns <result success="true"/> immediately, the <candles> callback follows.
// The missing/invalid period and count are rejected as the DLL does.
func TestConnectorHistoryResultBeforeCandles(t *testing.T) {
	srv := newRunServer(t)
	srv.HistoryParts = [][]byte{readHistoryPart(t, "candles-part1-inject.hex"), readHistoryPart(t, "candles-part2-inject.hex")}
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "gethistorydata" + quote + " secid=" + quote + "26" + quote
	cmd += " period=" + quote + "1" + quote + " count=" + quote + "2" + quote + " reset=" + quote + "true" + quote + "/>"
	start := time.Now()
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("gethistorydata = %s", got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("result took %v: the command must not wait for the candles", elapsed)
	}
	rec.next(t, "<candles")
	if got := send(t, c, "<command id="+quote+"gethistorydata"+quote+" secid="+quote+"26"+quote+" count="+quote+"2"+quote+"/>"); !strings.Contains(got, "period") || !strings.Contains(got, "absent") {
		t.Fatalf("missing period = %s", got)
	}
	if got := send(t, c, "<command id="+quote+"gethistorydata"+quote+" secid="+quote+"26"+quote+" period="+quote+"1"+quote+" count="+quote+"0"+quote+"/>"); !strings.Contains(got, "count is out of range") {
		t.Fatalf("zero count = %s", got)
	}
}

// TestConnectorHistoryCloseDuringWait: Close during a background collection does
// not hang (the history waiter is woken).
func TestConnectorHistoryCloseDuringWait(t *testing.T) {
	srv := newRunServer(t) // no parts: the collection waits
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "gethistorydata" + quote + " secid=" + quote + "26" + quote
	cmd += " period=" + quote + "1" + quote + " count=" + quote + "10" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("gethistorydata = %s", got)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Close()
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung while a history collection was waiting")
	}
}

// The gRPC server and the harness cancel the command context as soon as
// SendCommand returns, and gethistorydata returns before the candles. The
// collection must outlive that context (oracle 2026-11-05: the cancelled
// RQ_HISTORY_EXT wait left its reply for RQ_MAIN and the session dropped).
func TestConnectorHistoryOutlivesCommandContext(t *testing.T) {
	srv := newRunServer(t)
	srv.MainDelay = 30 * time.Millisecond
	srv.HistoryParts = [][]byte{
		readHistoryPart(t, "candles-part1-inject.hex"),
		readHistoryPart(t, "candles-part2-inject.hex"),
	}
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "gethistorydata" + quote + " secid=" + quote + "26" + quote
	cmd += " period=" + quote + "1" + quote + " count=" + quote + "2" + quote + " reset=" + quote + "true" + quote + "/>"
	ctx, cancel := context.WithCancel(context.Background())
	got, err := c.SendCommand(ctx, cmd)
	cancel() // what a finished gRPC call does
	if err != nil || got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("gethistorydata = %s, %v", got, err)
	}
	want, err := os.ReadFile("records/testdata/candles-multipart-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.next(t, "<candles"); got != strings.TrimSpace(string(want)) {
		t.Fatalf("candles = %s", got)
	}
	if got := send(t, c, `<command id="server_status"/>`); !strings.Contains(got, `success="true"`) {
		t.Fatalf("session after history = %s", got)
	}
}
