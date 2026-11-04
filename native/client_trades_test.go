package native

import (
	"context"
	"encoding/binary"
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

// TestClientTradesFilter is the O filter: a trade of the session client is
// delivered as a ClientTrade event, a trade of another numeric client id is
// dropped.
func TestClientTradesFilter(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	payload := readOrderFixture(t, "trade-inject.hex")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	clientID, ok := c.clientNumericIDLocked("TESTCLIEN", "")
	if !ok {
		t.Fatal("the client snapshot has no numeric client id")
	}
	// The trade fixture is masked to client id 4096; make an own-client record
	// with the session's numeric id before delivering it.
	own := patchTradeClient(t, payload, clientID)
	trades := make(chan model.ClientTrade, 4)
	runDone := make(chan struct{})
	var runErr error
	go func() {
		defer close(runDone)
		runErr = c.Run(ctx, RunOptions{}, func(events []model.Event) error {
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
	// Queue the own-client record after the subscribe: the subscribe reply
	// replaces the server's pending RQ_MAIN payload with AfterSubscribe.
	srv.QueueMain(own)
	select {
	case trade := <-trades:
		if trade.Client != "TESTCLIEN" || trade.SecCode != "SBER" || trade.Quantity != 4444 || trade.TradeNo != 2222 {
			t.Fatalf("trade = %+v", trade)
		}
	case <-time.After(5 * time.Second):
		var types []byte
		for _, f := range srv.Requests() {
			types = append(types, f.Type)
		}
		t.Fatalf("the own-client trade was not delivered (runErr=%v requests=%v accounts=%+v)", runErr, types, c.Accounts())
	}
	// A foreign numeric client id must be filtered out.
	foreign := patchTradeClient(t, payload, clientID+12345)
	srv.QueueMain(foreign)
	select {
	case trade := <-trades:
		t.Fatalf("foreign trade was not filtered: %+v", trade)
	case <-time.After(500 * time.Millisecond):
	}
	cancel()
	<-runDone
}
