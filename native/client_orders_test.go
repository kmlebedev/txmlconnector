package native

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// readOrderFixture loads an order fixture of native/records/testdata.
func readOrderFixture(t *testing.T, name string) []byte {
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

// clientSnapshot builds a small load snapshot with the SBER security and the
// live R_CLN_NEW client record (so NewOrder can resolve the numeric client id).
func clientSnapshot(t *testing.T) []byte {
	t.Helper()
	var snapshot []byte
	add := func(rec records.Record) {
		snapshot = append(snapshot, rec.Tag, byte(len(rec.Body)>>8), byte(len(rec.Body)))
		snapshot = append(snapshot, rec.Body...)
	}
	secRecs, err := records.Records(secNewRecord(t, "SBER", 26))
	if err != nil || len(secRecs) != 1 {
		t.Fatalf("synthetic sec new record: %v", err)
	}
	add(secRecs[0])
	recs, err := records.Records(snapshotTail(t))
	if err != nil && !errors.As(err, new(*records.UnknownRecordError)) {
		t.Fatalf("snapshot tail: %v", err)
	}
	added := false
	for _, rec := range recs {
		if rec.Tag == records.TagClientNew {
			add(rec)
			added = true
			break
		}
	}
	if !added {
		t.Fatal("the snapshot tail has no R_CLN_NEW")
	}
	return snapshot
}

// transactHandler answers RQ_TRANSACT with the O success payload and can queue a
// follow-up RQ_MAIN payload (the order updates).
func transactHandler(t *testing.T, srv *testserver.Server, followUp []byte) {
	t.Helper()
	payload, err := hex.DecodeString("00100010")
	if err != nil {
		t.Fatal(err)
	}
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		if followUp != nil {
			srv.QueueMain(followUp)
		}
		return wire.KindReply | wire.FlagPlain, payload
	})
}

// TestNewOrderPayloadMatchesDLL checks the O neworder payload: B01,B00,W secid,W
// board,G client_id,B buysell,B00,L price,L quantity,9 zero bytes.
func TestNewOrderPayloadMatchesDLL(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	transactHandler(t, srv, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	cid, ok := c.clientNumericIDLocked("TESTCLIEN", "")
	if !ok {
		t.Fatalf("catalog has no numeric id for TESTCLIEN")
	}
	var wantWire wire.Writer
	wantWire.Byte(1)
	wantWire.Byte(0)
	wantWire.Uint16(26)
	wantWire.Uint16(37)
	wantWire.Int64(cid)
	wantWire.Byte('B')
	wantWire.Byte(0)
	wantWire.Int32(10000)
	wantWire.Int32(1)
	wantWire.Raw(make([]byte, 9))
	wantPayload := hex.EncodeToString(wantWire.Bytes())
	_, err = c.NewOrder(ctx, model.NewOrder{
		Client: "TESTCLIEN", Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"},
		BuySell: 'B', Price: model.Decimal{Units: 10000, Scale: 2}, Quantity: 1, Unfilled: "PutInQueue",
	})
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	var payloads []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqTransact {
			payloads = append(payloads, hex.EncodeToString(f.Payload))
		}
	}
	if len(payloads) != 1 {
		t.Fatalf("RQ_TRANSACT payloads = %v", payloads)
	}
	if payloads[0] != wantPayload {
		t.Fatalf("neworder payload = %s, want %s", payloads[0], wantPayload)
	}
}

// TestCancelOrderPayloadMatchesDLL checks the O cancelorder payload 02011000.
func TestCancelOrderPayloadMatchesDLL(t *testing.T) {
	srv := newRunServer(t)
	transactHandler(t, srv, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if err := c.CancelOrder(ctx, 1); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	var payloads []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqTransact {
			payloads = append(payloads, hex.EncodeToString(f.Payload))
		}
	}
	if len(payloads) != 1 || payloads[0] != "02011000" {
		t.Fatalf("cancelorder payloads = %v, want [02011000]", payloads)
	}
}

// TestConnectorOrders checks the XML path: neworder returns a transactionid and
// the order callback renders; the modify fixture updates the same order.
func TestConnectorOrders(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	transactHandler(t, srv, nil)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	srv.QueueMain(patchOrderClient(t, readOrderFixture(t, "order-new-inject.hex"), snapshotClientID(t)))
	cmd := "<command id=" + quote + "neworder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	cmd += "<price>100</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+" transactionid="+quote+"1"+quote+"/>" {
		t.Fatalf("neworder = %s", got)
	}
	want, err := os.ReadFile("records/testdata/order-new-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.next(t, "<orders"); got != strings.TrimSpace(string(want)) {
		t.Fatalf("new order callback = %s", got)
	}
	// The cancelorder command sends its payload and returns the server id.
	if got := send(t, c, "<command id="+quote+"cancelorder"+quote+"><transactionid>1</transactionid></command>"); !strings.Contains(got, "transactionid="+quote+"1"+quote) {
		t.Fatalf("cancelorder = %s", got)
	}
	if got := send(t, c, "<command id="+quote+"cancelorder"+quote+"/>"); !strings.Contains(got, "transactionid") {
		t.Fatalf("cancelorder without id = %s", got)
	}
}

// patchOrderClient rewrites the numeric client id of the R_ORD_NEW fixture
// (body: L order_id, W secid, W board, G client_id, ...).
func patchOrderClient(t *testing.T, payload []byte, clientID int64) []byte {
	t.Helper()
	recs, err := records.Records(payload)
	if err != nil || len(recs) != 1 {
		t.Fatalf("order fixture: %v", err)
	}
	body := recs[0].Body
	if len(body) < 9 {
		t.Fatalf("order body = %x", body)
	}
	// L order_id is one packed byte in the fixture (0x10); then W secid, W board.
	prefix := body[:5]
	var w wire.Writer
	w.Int64(clientID)
	patched := append([]byte(nil), prefix...)
	patched = append(patched, w.Bytes()...)
	patched = append(patched, body[8:]...)
	record := make([]byte, 3+len(patched))
	record[0] = records.TagOrderNew
	record[1], record[2] = byte(len(patched)>>8), byte(len(patched))
	copy(record[3:], patched)
	return record
}

// snapshotClientID returns the numeric id of the client record in the load
// snapshot used by clientSnapshot.
func snapshotClientID(t *testing.T) int64 {
	t.Helper()
	recs, err := records.Records(snapshotTail(t))
	if err != nil {
		t.Fatalf("snapshot tail: %v", err)
	}
	for _, rec := range recs {
		if rec.Tag != records.TagClientNew {
			continue
		}
		cn, err := records.ParseClientNew(rec.Body)
		if err != nil {
			t.Fatalf("ParseClientNew: %v", err)
		}
		return cn.ID
	}
	t.Fatal("the snapshot tail has no R_CLN_NEW")
	return 0
}

// TestNewOrderPriceScale is the P1 regression: the command <price>100</price>
// (Decimal scale 0) must reach the payload as 10000 for the TQBR decimals.
func TestNewOrderPriceScale(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var mu sync.Mutex
	var captured []byte
	srv.Handle(session.RqTransact, func(payload []byte) (byte, []byte) {
		mu.Lock()
		captured = append([]byte(nil), payload...)
		mu.Unlock()
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "neworder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	cmd += "<price>100</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+" transactionid="+quote+"1"+quote+"/>" {
		t.Fatalf("neworder = %s", got)
	}
	mu.Lock()
	payload := captured
	mu.Unlock()
	if payload == nil {
		t.Fatal("no RQ_TRANSACT captured")
	}
	r := wire.NewReader(payload)
	r.Byte()
	r.Byte()
	if _, err := r.Uint16(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Uint16(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Int64(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Byte(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Byte(); err != nil {
		t.Fatal(err)
	}
	price, err := r.Int32()
	if err != nil {
		t.Fatal(err)
	}
	if price != 10000 {
		t.Fatalf("payload price = %d, want 10000 (Decimal scale normalized)", price)
	}
}

// TestOrderForeignClientFiltered: an order of another numeric client is
// dropped; the own order after it is still delivered.
func TestOrderForeignClientFiltered(t *testing.T) {
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
	own := patchOrderClient(t, readOrderFixture(t, "order-new-inject.hex"), clientID)
	foreign := patchOrderClient(t, readOrderFixture(t, "order-new-inject.hex"), clientID+12345)
	orders := make(chan model.Order, 4)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = c.Run(ctx, RunOptions{}, func(events []model.Event) error {
			for _, ev := range events {
				if order, ok := ev.(*model.Order); ok {
					orders <- *order
				}
			}
			return nil
		})
	}()
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	srv.QueueMain(foreign)
	srv.QueueMain(own)
	select {
	case order := <-orders:
		if order.TransactionID != 1 || order.Client != "TESTCLIEN" {
			t.Fatalf("foreign order leaked: %+v", order)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the own order was not delivered")
	}
	cancel()
	<-runDone
}

// TestTransactionErrorMessages is the P2(4) check: DLL message without the Go
// error prefix, unknown outcome as "Unknown error".
func TestTransactionErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		want    string
	}{
		{"rsm", []byte{1}, "<result success=" + quote + "false" + quote + "><message>Клиент не допущен к торгам.</message></result>"},
		{"empty", nil, "<result success=" + quote + "false" + quote + "><message>Unknown error</message></result>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRunServer(t)
			srv.Snapshot = clientSnapshot(t)
			srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
				return wire.KindReply | wire.FlagPlain, tc.payload
			})
			c := New(Config{Dial: srv.Dial})
			rec := newRecorder()
			if err := c.Start(rec.handle); err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			send(t, c, connectCommand())
			rec.next(t, "<server_status")
			cmd := "<command id=" + quote + "neworder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
			cmd += "<price>100</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled></command>"
			if got := send(t, c, cmd); got != tc.want {
				t.Fatalf("result = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestNewOrderRejectsUnsupportedFlags: flags without an oracle payload mapping
// are rejected explicitly instead of silently becoming PutInQueue bytes.
func TestNewOrderRejectsUnsupportedFlags(t *testing.T) {
	c := &Client{}
	_, err := c.NewOrder(context.Background(), model.NewOrder{Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, BuySell: 'B', Unfilled: "FOK"})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err = %v, want the explicit unsupported-flag error", err)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	payload, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// patchOrderModifyClient rewrites the client id of the first 0x03 record inside
// the order-modify batch (0x03+0x10) and keeps the modification record.
func patchOrderModifyClient(t *testing.T, payload []byte, clientID int64) []byte {
	t.Helper()
	recs, err := records.Records(payload)
	if err != nil || len(recs) != 2 {
		t.Fatalf("order-modify fixture: %v", err)
	}
	var out []byte
	for i, rec := range recs {
		body := rec.Body
		if i == 0 {
			prefix := body[:5]
			var w wire.Writer
			w.Int64(clientID)
			patched := append([]byte(nil), prefix...)
			patched = append(patched, w.Bytes()...)
			patched = append(patched, body[8:]...)
			body = patched
		}
		out = append(out, rec.Tag, byte(len(body)>>8), byte(len(body)))
		out = append(out, body...)
	}
	return out
}

// TestOrderModifyBatchOneCallback is the P2(2) regression: 0x03+0x10 in one
// RQ_MAIN reply render as ONE final <orders> callback (no intermediate watching).
func TestOrderModifyBatchOneCallback(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	srv.QueueMain(patchOrderModifyClient(t, readOrderFixture(t, "order-modify-inject.hex"), snapshotClientID(t)))
	msg := rec.next(t, "<orders")
	if !strings.Contains(msg, "<status>disabled</status>") || !strings.Contains(msg, "{37} Заявка отменена") {
		t.Fatalf("batch callback = %s, want the final disabled order", msg)
	}
	time.Sleep(200 * time.Millisecond)
	rec.mu.Lock()
	count := 0
	for _, m := range rec.msgs {
		if strings.HasPrefix(m, "<orders") {
			count++
		}
	}
	rec.mu.Unlock()
	if count != 1 {
		t.Fatalf("<orders> callbacks = %d, want exactly 1", count)
	}
}

// TestLoadOrdersEmitted: orders decoded while the snapshot loads are emitted at
// the end of the load (their events are dropped by the load path).
func TestLoadOrdersEmitted(t *testing.T) {
	srv := newRunServer(t)
	order := patchOrderClient(t, readOrderFixture(t, "order-new-inject.hex"), snapshotClientID(t))
	srv.Snapshot = append(clientSnapshot(t), order...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	msg := rec.next(t, "<orders")
	if !strings.Contains(msg, "<status>watching</status>") {
		t.Fatalf("load order callback = %s", msg)
	}
}

// TestNewOrderExpDateRejected: the expdate payload field is not recovered (I);
// the Connector must reject it explicitly instead of silently dropping it.
func TestNewOrderExpDateRejected(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "neworder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	cmd += "<price>100</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled>"
	cmd += "<expdate>05.11.2026</expdate></command>"
	if got := send(t, c, cmd); !strings.Contains(got, "expdate") || !strings.Contains(got, "not encoded") {
		t.Fatalf("neworder expdate = %s, want the explicit I rejection", got)
	}
}

// TestNewOrderHiddenRejected: the hidden payload field is not recovered (I); the
// command must be rejected explicitly, not silently ignored.
func TestNewOrderHiddenRejected(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "neworder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	cmd += "<price>100</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled>"
	cmd += "<hidden>1</hidden></command>"
	if got := send(t, c, cmd); !strings.Contains(got, "hidden") || !strings.Contains(got, "not encoded") {
		t.Fatalf("neworder hidden = %s, want the explicit I rejection", got)
	}
}
