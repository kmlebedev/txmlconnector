package native

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// patchTradeClient rewrites the numeric client id of the trade fixture record.
func patchTradeClient(t *testing.T, payload []byte, clientID int64) []byte {
	t.Helper()
	recs, err := records.Records(payload)
	if err != nil || len(recs) != 1 {
		t.Fatalf("trade fixture: %v", err)
	}
	body := recs[0].Body
	if len(body) < 7 {
		t.Fatalf("trade body = %x", body)
	}
	var w wire.Writer
	w.Int64(clientID)
	patched := append([]byte(nil), body[:4]...)
	patched = append(patched, w.Bytes()...)
	patched = append(patched, body[7:]...)
	record := make([]byte, 3+len(patched))
	record[0] = records.TagClientTrade
	binary.BigEndian.PutUint16(record[1:3], uint16(len(patched)))
	copy(record[3:], patched)
	return record
}

// TestClientTradesFilter is the O filter: the decoder drops another numeric
// client's trade (unit-level, deterministic) and the Run path delivers the own
// trade.
func TestClientTradesFilter(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	clientID := snapshotClientID(t)
	ownRecord := patchTradeClient(t, readOrderFixture(t, "trade-inject.hex"), clientID)
	foreignRecord := patchTradeClient(t, readOrderFixture(t, "trade-inject.hex"), clientID+12345)
	// Deterministic decoder check: foreign is dropped, own has an event.
	for _, tc := range []struct {
		name   string
		record []byte
		want   bool
	}{
		{"own", ownRecord, true},
		{"foreign", foreignRecord, false},
	} {
		recs, err := records.Records(tc.record)
		if err != nil || len(recs) != 1 {
			t.Fatalf("%s: %v", tc.name, err)
		}
		events, err := decodeClientTrade(c, recs[0].Body)
		if err != nil {
			t.Fatalf("%s decode: %v", tc.name, err)
		}
		if got := len(events) > 0; got != tc.want {
			t.Fatalf("%s events = %d, want delivered=%v", tc.name, len(events), tc.want)
		}
	}
	trades := make(chan model.ClientTrade, 4)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = c.Run(ctx, RunOptions{}, func(events []model.Event) error {
			for _, ev := range events {
				if trade, ok := ev.(*model.ClientTrade); ok {
					trades <- *trade
				}
			}
			return nil
		})
	}()
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	srv.QueueMain(ownRecord)
	select {
	case trade := <-trades:
		if trade.Client != "TESTCLIEN" || trade.SecCode != "SBER" || trade.Quantity != 4444 || trade.TradeNo != 2222 {
			t.Fatalf("trade = %+v", trade)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the own-client trade was not delivered")
	}
	cancel()
	<-runDone
}

// TestClientTradesMulti: two trades in one RQ_MAIN reply produce two events.
func TestClientTradesMulti(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	record := patchTradeClient(t, readOrderFixture(t, "trade-inject.hex"), snapshotClientID(t))
	trades := make(chan model.ClientTrade, 4)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = c.Run(ctx, RunOptions{}, func(events []model.Event) error {
			for _, ev := range events {
				if trade, ok := ev.(*model.ClientTrade); ok {
					trades <- *trade
				}
			}
			return nil
		})
	}()
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	srv.QueueMain(append(append([]byte(nil), record...), record...))
	for i := 0; i < 2; i++ {
		select {
		case <-trades:
		case <-time.After(5 * time.Second):
			t.Fatalf("trade %d was not delivered", i+1)
		}
	}
	cancel()
	<-runDone
}

// TestConnectorTradesCallback: the Connector renders the own trade callback.
func TestConnectorTradesCallback(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	record := patchTradeClient(t, readOrderFixture(t, "trade-inject.hex"), snapshotClientID(t))
	srv.QueueMain(record)
	msg := rec.next(t, "<trades")
	if !strings.Contains(msg, "<seccode>SBER</seccode>") || !strings.Contains(msg, "<quantity>4444</quantity>") {
		t.Fatalf("trades = %s", msg)
	}
}
