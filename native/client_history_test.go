package native

import (
	"context"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

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
	if got := candles.Items[0].Time.Format("15:04"); got != "18:43" {
		t.Fatalf("first candle time = %s", got)
	}
	if got := candles.Items[1].Time.Format("15:04"); got != "18:44" {
		t.Fatalf("second candle time = %s", got)
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
