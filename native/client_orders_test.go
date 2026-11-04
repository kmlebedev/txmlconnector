package native

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
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
	srv.QueueMain(readOrderFixture(t, "order-new-inject.hex"))
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
