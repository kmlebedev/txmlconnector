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
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestCancelStopOrderPayloadMatchesDLL checks the O 2-byte payload 17 10.
func TestCancelStopOrderPayloadMatchesDLL(t *testing.T) {
	srv := newRunServer(t)
	payload, _ := hex.DecodeString("00100010")
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, payload
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nc, err := srv.Dial(ctx, "testserver:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := session.Login(ctx, nc, session.Config{Login: testserver.Login, Password: testserver.Password})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := &Client{conn: conn}
	if err := client.CancelStopOrder(ctx, 1); err != nil {
		t.Fatalf("CancelStopOrder: %v", err)
	}
	var got []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqTransact {
			got = append(got, hex.EncodeToString(f.Payload))
		}
	}
	if len(got) != 1 || got[0] != "1710" {
		t.Fatalf("RQ_TRANSACT payloads = %v, want [1710]", got)
	}
}

// TestCancelStopOrderCommand checks the Connector command: transactionid is
// required (DLL text), the payload is 17 <packed id>.
func TestCancelStopOrderCommand(t *testing.T) {
	srv := newRunServer(t)
	payload, _ := hex.DecodeString("00100010")
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, payload
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	if got := send(t, c, "<command id="+quote+"cancelstoporder"+quote+"/>"); !strings.Contains(got, "You have to specify transactionid.") {
		t.Fatalf("missing transactionid = %s", got)
	}
	if got := send(t, c, "<command id="+quote+"cancelstoporder"+quote+"><transactionid>1</transactionid></command>"); got != "<result success="+quote+"true"+quote+" transactionid="+quote+"1"+quote+"/>" {
		t.Fatalf("cancelstoporder = %s", got)
	}
	var gotPayloads []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqTransact {
			gotPayloads = append(gotPayloads, hex.EncodeToString(f.Payload))
		}
	}
	if len(gotPayloads) != 1 || gotPayloads[0] != "1710" {
		t.Fatalf("RQ_TRANSACT payloads = %v, want [1710]", gotPayloads)
	}
}

// expectedWithClient replaces the masked packed client id of a fixture payload
// with the session's catalog id (the fixtures are anonymized to 4096).
func expectedWithClient(t *testing.T, fixturePath string, offset int) []byte {
	t.Helper()
	raw, err := os.ReadFile("records/testdata/" + fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var w wire.Writer
	w.Int64(snapshotClientID(t))
	out := append([]byte(nil), fixture[:offset]...)
	out = append(out, w.Bytes()...)
	out = append(out, fixture[offset+3:]...)
	return out
}

// captureTransact registers a fake successful RQ_TRANSACT and returns a getter
// for the captured request payload.
func captureTransact(t *testing.T, srv *testserver.Server) func() []byte {
	t.Helper()
	var mu sync.Mutex
	var captured []byte
	srv.Handle(session.RqTransact, func(payload []byte) (byte, []byte) {
		mu.Lock()
		captured = append([]byte(nil), payload...)
		mu.Unlock()
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	return func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

func stopOrderClient(t *testing.T, srv *testserver.Server) (*Client, context.Context) {
	t.Helper()
	srv.Snapshot = clientSnapshot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client, ctx
}

// TestNewStopOrderPayloadMatchesDLL: the builder reproduces the O payload
// (activation 100/order 90/qty 1, take profit 300) modulo the anonymized client
// id, which comes from the catalog.
func TestNewStopOrderPayloadMatchesDLL(t *testing.T) {
	srv := newRunServer(t)
	get := captureTransact(t, srv)
	client, ctx := stopOrderClient(t, srv)
	_, err := client.NewStopOrder(ctx, model.NewStopOrder{
		Client: "TESTCLIEN", Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, BuySell: 'B',
		StopLoss: model.StopLoss{
			ActivationPrice: model.Decimal{Units: 10000, Scale: 2},
			OrderPrice:      model.Decimal{Units: 9000, Scale: 2},
			Quantity:        1,
		},
		TakeProfit: model.TakeProfit{ActivationPrice: model.Decimal{Units: 30000, Scale: 2}, Quantity: 1},
	})
	if err != nil {
		t.Fatalf("NewStopOrder: %v", err)
	}
	want := expectedWithClient(t, "transact-newstoporder-payload.hex", 7)
	if got := get(); !bytesEqual(got, want) {
		t.Fatalf("newstoporder payload\n got %x\nwant %x", got, want)
	}
}

// TestNewCondOrderPayloadMatchesDLL: the conditional order uses the base 0x01
// type with cond fields (price 100, qty 1, cond 290, two times).
func TestNewCondOrderPayloadMatchesDLL(t *testing.T) {
	srv := newRunServer(t)
	get := captureTransact(t, srv)
	client, ctx := stopOrderClient(t, srv)
	after := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	before := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	_, err := client.NewCondOrder(ctx, model.NewCondOrder{
		Client: "TESTCLIEN", Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, BuySell: 'B',
		Price: model.Decimal{Units: 10000, Scale: 2}, Quantity: 1,
		CondValue: model.Decimal{Units: 29000, Scale: 2}, ValidAfter: &after, ValidBefore: &before,
	})
	if err != nil {
		t.Fatalf("NewCondOrder: %v", err)
	}
	want := expectedWithClient(t, "transact-newcondorder-payload.hex", 6)
	if got := get(); !bytesEqual(got, want) {
		t.Fatalf("newcondorder payload\n got %x\nwant %x", got, want)
	}
}

// TestConnectorStopAndCondOrders: the XML commands produce the same payloads.
func TestConnectorStopAndCondOrders(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	get := captureTransact(t, srv)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	stopCmd := "<command id=" + quote + "newstoporder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	stopCmd += "<buysell>B</buysell><quantity>1</quantity>"
	stopCmd += "<stoploss><activationprice>100</activationprice><orderprice>90</orderprice><quantity>1</quantity></stoploss>"
	stopCmd += "<takeprofit><activationprice>300</activationprice><quantity>1</quantity></takeprofit></command>"
	if got := send(t, c, stopCmd); !strings.Contains(got, "transactionid") {
		t.Fatalf("newstoporder = %s", got)
	}
	if got, want := get(), expectedWithClient(t, "transact-newstoporder-payload.hex", 7); !bytesEqual(got, want) {
		t.Fatalf("connector newstoporder payload\n got %x\nwant %x", got, want)
	}
	condCmd := "<command id=" + quote + "newcondorder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	condCmd += "<price>100</price><quantity>1</quantity><buysell>B</buysell><cond_type>Bid</cond_type><cond_value>290</cond_value>"
	condCmd += "<validafter>05.11.2026 10:00:00</validafter><validbefore>05.11.2026 18:00:00</validbefore></command>"
	if got := send(t, c, condCmd); !strings.Contains(got, "transactionid") {
		t.Fatalf("newcondorder = %s", got)
	}
	if got, want := get(), expectedWithClient(t, "transact-newcondorder-payload.hex", 6); !bytesEqual(got, want) {
		t.Fatalf("connector newcondorder payload\n got %x\nwant %x", got, want)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
