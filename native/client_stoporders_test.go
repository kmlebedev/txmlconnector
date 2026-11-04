package native

import (
	"context"
	"encoding/hex"
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
