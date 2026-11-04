package native

import (
	"bytes"
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
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		r = zr
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// marketServer serves the demo-server snapshot (499 securities, including
// SBER 26, GAZP 379 and LKOH 4 on TQBR) and, after RQ_SUBSCRIBE, the trade
// records the DLL decoded in the oracle run alltrades-dll<n>.xml.
func marketServer(t *testing.T, tradesFixture string) *testserver.Server {
	t.Helper()
	srv := newRunServer(t)
	srv.AfterSubscribe = readFixture(t, tradesFixture)
	return srv
}

// subscribePayloads returns the decoded RQ_SUBSCRIBE payloads the server saw.
func subscribePayloads(srv *testserver.Server) []string {
	var out []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqSubscribe {
			out = append(out, hex.EncodeToString(f.Payload))
		}
	}
	return out
}

func TestClientAllTrades(t *testing.T) {
	srv := marketServer(t, "alltrades-inject1.hex")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Catalog().Len() != 499 || c.ServerTZ() != "Russian Standard Time" {
		t.Fatalf("catalog %d, tz %q", c.Catalog().Len(), c.ServerTZ())
	}
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}, model.Symbol{Board: "TQBR", SecCode: "GAZP"}); err != nil {
		t.Fatal(err)
	}
	subs := subscribePayloads(srv)
	if len(subs) != 1 || subs[0] != "001a002504017b002504" { // SBER, GAZP on board 37, alltrades
		t.Fatalf("RQ_SUBSCRIBE = %v", subs)
	}
	events, _, err := c.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var trades []model.Trade
	for _, ev := range events {
		if tr, ok := ev.(*model.Trade); ok {
			trades = append(trades, *tr)
		}
	}
	if len(trades) != 5 {
		t.Fatalf("%d trades", len(trades))
	}
	sber, gazp, vtbr := trades[0], trades[2], trades[4]
	// 03.11.2026 23:53:20 MSK in the DLL output is 20:53:20 UTC.
	if sber.SecCode != "SBER" || sber.Board != "TQBR" || sber.TradeNo != 9000000001 ||
		sber.Price != (model.Decimal{Units: 30512, Scale: 2}) || sber.Quantity != 3 || sber.Period != 'B' || sber.BuySell != 0 ||
		!sber.Time.Equal(time.Date(2026, 11, 3, 20, 53, 20, 0, time.UTC)) {
		t.Fatalf("SBER = %+v", sber)
	}
	if gazp.SecCode != "GAZP" || !gazp.HasOpenInterest || gazp.OpenInterest != 777 ||
		!gazp.Time.Equal(time.Date(2026, 11, 3, 20, 53, 22, 250e6, time.UTC)) {
		t.Fatalf("GAZP = %+v", gazp)
	}
	if vtbr.SecCode != "" || vtbr.SecID != 889 { // VTBR is not in the 499-security fixture
		t.Fatalf("VTBR = %+v", vtbr)
	}
	if got := c.WallTime(sber).Format("02.01.2006 15:04:05"); got != "03.11.2026 23:53:20" {
		t.Fatalf("wall time %s", got)
	}

	if err := c.UnsubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "GAZP"}); err != nil {
		t.Fatal(err)
	}
	subs = subscribePayloads(srv)
	if len(subs) != 2 || subs[1] != "017b002500" {
		t.Fatalf("unsubscribe = %v", subs)
	}
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "NOPE"}); err == nil {
		t.Fatal("expected unknown security error")
	}
}

// The XML path must emit exactly what the DLL emitted for the same records.
func TestConnectorAllTradesMatchDLL(t *testing.T) {
	srv := marketServer(t, "alltrades-inject2.hex")
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	connect := strings.Replace(connectCommand(), "</rqdelay>", "</rqdelay><milliseconds>true</milliseconds>", 1)
	send(t, c, connect)
	rec.next(t, `<server_status`)
	sub := `<command id="subscribe"><alltrades><security><board>TQBR</board><seccode>SBER</seccode></security></alltrades></command>`
	if got := send(t, c, sub); got != `<result success="true"/>` {
		t.Fatalf("subscribe = %s", got)
	}
	want, err := os.ReadFile("records/testdata/alltrades-dll2.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.next(t, "<alltrades>"); got != strings.TrimSpace(string(want)) {
		t.Fatalf("alltrades\n got %s\nwant %s", got, want)
	}
	if got := send(t, c, `<command id="subscribe"><quotations><secid>26</secid></quotations></command>`); got != `<result success="true"/>` {
		t.Fatalf("quotations = %s", got)
	}
	if got := send(t, c, `<command id="subscribe"><quotes><secid>26</secid></quotes></command>`); !strings.Contains(got, "not implemented") {
		t.Fatalf("quotes = %s", got)
	}
}

// newRunServer returns a testserver with the demo-server dictionaries and the
// 499-instrument snapshot, so the client can subscribe before Run starts.
func newRunServer(t *testing.T) *testserver.Server {
	t.Helper()
	srv := newTestServer(t)
	srv.Snapshot = readFixture(t, "main1-demo.hex.gz")
	return srv
}

// countRequests returns how many requests of a type the testserver has seen.
func countRequests(srv *testserver.Server, typ byte) int {
	n := 0
	for _, f := range srv.Requests() {
		if f.Type == typ {
			n++
		}
	}
	return n
}

// TestRunReportsUnknownRecord checks Review Focus 1: a record without a known
// layout ends decoding of its RQ_MAIN payload, the events before it are
// delivered, the tail is reported as model.UnknownRecord and polling goes on.
func TestRunReportsUnknownRecord(t *testing.T) {
	known := readFixture(t, "alltrades-inject2.hex")
	tail := []byte{0x7f, 0x01, 0x02, 0x03}
	payload := append(append([]byte{}, known...), tail...)

	srv := newRunServer(t)
	srv.AfterSubscribe = payload
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testserver.Login, Password: testserver.Password, Dial: srv.Dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	first := make(chan []model.Event, 1)
	runCtx, runCancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		errc <- c.Run(runCtx, RunOptions{RqDelay: 10 * time.Millisecond}, func(events []model.Event) error {
			select {
			case first <- events:
			default:
			}
			return nil
		})
	}()

	var events []model.Event
	select {
	case events = <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("Run delivered no batch after the subscription")
	}
	if len(events) < 2 {
		t.Fatalf("batch has %d events, want trades followed by UnknownRecord", len(events))
	}
	unknown, ok := events[len(events)-1].(model.UnknownRecord)
	if !ok {
		t.Fatalf("last event = %T, want model.UnknownRecord", events[len(events)-1])
	}
	if unknown.Tag != 0x7f || unknown.Offset != len(known) || !bytes.Equal(unknown.Payload, payload[unknown.Offset:]) {
		t.Fatalf("UnknownRecord = %+v, want tag 0x7f at offset %d with the payload tail", unknown, len(known))
	}
	for i := 0; i < len(events)-1; i++ {
		if _, ok := events[i].(*model.Trade); !ok {
			t.Fatalf("event %d = %T, want *model.Trade before the unknown record", i, events[i])
		}
	}
	if st := c.Stats(); st.UnknownRecords != 1 || st.LastUnknownTag != 0x7f {
		t.Fatalf("stats = %+v, want one unknown record with tag 0x7f", st)
	}

	// Polling continues after the payload with the unknown record: another
	// RQ_MAIN request must follow (the server answers it with an empty reply,
	// so no further callback is expected).
	atFirst := countRequests(srv, session.RqMain)
	deadline := time.Now().Add(2 * time.Second)
	for countRequests(srv, session.RqMain) <= atFirst {
		if time.Now().After(deadline) {
			t.Fatal("Run stopped polling after the unknown record")
		}
		time.Sleep(5 * time.Millisecond)
	}
	runCancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
}

// TestConcurrentRequestsDuringRun runs Run in parallel with 50 requests from
// other goroutines: the session must keep the frames unmixed (the testserver
// decodes every frame and reports a failure through t.Errorf) and the race
// detector must stay quiet.
func TestConcurrentRequestsDuringRun(t *testing.T) {
	srv := newRunServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testserver.Login, Password: testserver.Password, Dial: srv.Dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	symbol := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	if err := c.SubscribeAllTrades(ctx, symbol); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	runCtx, runCancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		errc <- c.Run(runCtx, RunOptions{RqDelay: 10 * time.Millisecond}, func([]model.Event) error {
			_ = c.Stats() // the handler runs without Client.mu held; else this deadlocks
			return nil
		})
	}()

	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = c.SubscribeAllTrades(runCtx, symbol)
			} else {
				err = c.UnsubscribeAllTrades(runCtx, symbol)
			}
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent request: %v", err)
	}
	runCancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if mains, subs := countRequests(srv, session.RqMain), countRequests(srv, session.RqSubscribe); mains < 2 || subs < 50 {
		t.Fatalf("testserver saw %d RQ_MAIN and %d RQ_SUBSCRIBE requests", mains, subs)
	}
}
