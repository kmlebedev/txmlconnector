package native

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestMoveOrderPayloadMatchesS: the S payload is 02 01 <packed L txid> 00
// (transact-moveorder-live.hex: 02 01 41 01 00 for txid=20).
func TestMoveOrderPayloadMatchesS(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var captured []byte
	srv.Handle(session.RqTransact, func(payload []byte) (byte, []byte) {
		captured = append([]byte(nil), payload...)
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.MoveOrder(ctx, model.MoveOrder{TransactionID: 20, Price: model.Decimal{Units: 100, Scale: 2}, MoveFlag: 0}); err != nil {
		t.Fatalf("MoveOrder: %v", err)
	}
	want, _ := hex.DecodeString("0201410100")
	if hex.EncodeToString(captured) != hex.EncodeToString(want) {
		t.Fatalf("moveorder payload = %x, want %x", captured, want)
	}
	if err := c.MoveOrder(ctx, model.MoveOrder{TransactionID: 0}); err == nil || !strings.Contains(err.Error(), "Wrong transactionid value.") {
		t.Fatalf("txid validation = %v", err)
	}
	if err := c.MoveOrder(ctx, model.MoveOrder{TransactionID: 1, MoveFlag: 4}); err == nil || !strings.Contains(err.Error(), "Wrong moveflag value.") {
		t.Fatalf("moveflag validation = %v", err)
	}
	if err := c.MoveOrder(ctx, model.MoveOrder{TransactionID: 1, MoveFlag: 2}); err == nil || !strings.Contains(err.Error(), "Cannot find quantity.") {
		t.Fatalf("quantity validation = %v", err)
	}
	_ = errors.New
}

// TestMoveOrderConnectorT19: XML elements, DLL validation texts and success.
func TestMoveOrderConnectorT19(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var captured []byte
	srv.Handle(session.RqTransact, func(payload []byte) (byte, []byte) {
		captured = append([]byte(nil), payload...)
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
	base := "<command id=" + quote + "moveorder" + quote + ">"
	cases := []struct{ name, inner, want string }{
		{"ok", "<transactionid>20</transactionid><price>100</price><moveflag>0</moveflag>", "<result success=" + quote + "true" + quote + " transactionid=" + quote + "20" + quote + "/>"},
		{"no txid", "<price>100</price><moveflag>0</moveflag>", "Cannot find transactionid."},
		{"bad txid", "<transactionid>x</transactionid><price>100</price><moveflag>0</moveflag>", "Wrong transactionid value."},
		{"no price", "<transactionid>20</transactionid><moveflag>0</moveflag>", "Price not found."},
		{"bad price", "<transactionid>20</transactionid><price>x</price><moveflag>0</moveflag>", "Price have to be a number."},
		{"no moveflag", "<transactionid>20</transactionid><price>100</price>", "Cannot find moveflag."},
		{"bad moveflag", "<transactionid>20</transactionid><price>100</price><moveflag>7</moveflag>", "Wrong moveflag value."},
		{"flag1 no qty", "<transactionid>20</transactionid><price>100</price><moveflag>1</moveflag>", "Cannot find quantity."},
	}
	for _, tc := range cases {
		got := send(t, c, base+tc.inner+"</command>")
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want %q", tc.name, got, tc.want)
		}
	}
	want, _ := hex.DecodeString("0201410100")
	if hex.EncodeToString(captured) != hex.EncodeToString(want) {
		t.Fatalf("payload = %x, want %x", captured, want)
	}
}

// TestMakeOrdOwnConnectorT19: makeordown sends RQ_MAKE_COND_OWN (112) with
// the packed transactionid (D: FUN_1803861e0; O: payload 10 for txid 1) only
// for a known conditional order that is watching (O: success for the
// order-new-inject record, refusal after the 0x10 cancel and for unknown ids);
// otherwise the DLL text, and nothing is sent.
func TestMakeOrdOwnConnectorT19(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var (
		mu       sync.Mutex
		captured [][]byte
		reply    []byte
	)
	srv.Handle(session.RqMakeCondOwn, func(payload []byte) (byte, []byte) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, append([]byte(nil), payload...))
		return wire.KindReply | wire.FlagPlain, reply
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	refused := "<result success=" + quote + "false" + quote + "><message>Cannot make specified transaction_id an own conditional order</message></result>"
	for _, cmd := range []string{
		"<command id=" + quote + "makeordown" + quote + " transactionid=" + quote + "1" + quote + "/>", // unknown yet
		"<command id=" + quote + "makeordown" + quote + " transactionid=" + quote + "x" + quote + "/>",
		"<command id=" + quote + "makeordown" + quote + "/>",
		"<command id=" + quote + "makeordown" + quote + "><transactionid>1</transactionid></command>", // element is ignored (D)
	} {
		if got := send(t, c, cmd); got != refused {
			t.Fatalf("%s = %s, want %s", cmd, got, refused)
		}
	}
	srv.QueueMain(patchOrderClient(t, readOrderFixture(t, "order-new-inject.hex"), snapshotClientID(t)))
	rec.next(t, "<orders")
	cmd := "<command id=" + quote + "makeordown" + quote + " transactionid=" + quote + "1" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("makeordown = %s", got)
	}
	mu.Lock()
	if len(captured) != 1 || hex.EncodeToString(captured[0]) != "10" {
		t.Fatalf("112 payloads = %x, want [10]", captured)
	}
	reply = append([]byte("\xcd\xe5\xf2"), 0) // CP1251 "Нет"
	mu.Unlock()
	if got := send(t, c, cmd); got != "<result success="+quote+"false"+quote+"><message>Нет</message></result>" {
		t.Fatalf("makeordown with server text = %s", got)
	}
}
