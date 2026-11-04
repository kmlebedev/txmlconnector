package native

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestCancelStopOrderPayloadMatchesDLL checks the O 2-byte payload 17 10.
func TestCancelStopOrderPayloadMatchesDLL(t *testing.T) {
	srv := testserver.New(t)
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
