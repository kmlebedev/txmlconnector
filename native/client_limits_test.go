package native

import (
	"context"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestClientLimitsCommand checks the D+O limits request payload (one packed
// L client_id) and the <clientlimits> callback of the Connector command.
func TestClientLimitsCommand(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Handle(session.RqClientLimits, func(payload []byte) (byte, []byte) {
		// Echo the client id from the request (the test catalog's id).
		var r wire.Writer
		r.Int32(4096)
		if len(payload) > 0 {
			var w wire.Writer
			// Rebuild the synthetic limits reply with the request's client id.
			r = wire.Writer{}
			_ = w
		}
		var limits wire.Writer
		// client id: copy request payload as-is (packed L).
		limits.Raw(payload)
		// flags & 2 block.
		limits.Int64(100)
		limits.Int64(200)
		limits.Int64(300)
		limits.Int64(400)
		// flags & 4 block: coverage W, liquidity L, then 15 money G.
		limits.Uint16(7)
		limits.Int32(1234567)
		for i := 0; i < 15; i++ {
			limits.Int64(int64((i + 1) * 100))
		}
		limits.Int32(-1)
		return wire.KindReply | wire.FlagPlain, limits.Bytes()
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "get_client_limits" + quote + " client=" + quote + "TESTCLIEN" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_client_limits = %s", got)
	}
	msg := rec.next(t, "<clientlimits")
	for _, want := range []string{
		"<clientlimits client=" + quote + "TESTCLIEN" + quote + ">",
		"<cbplimit>1.00</cbplimit>",
		"<coverage>7</coverage>",
		"<liquidity_c>12.34567</liquidity_c>",
		"<profit>1.00</profit>",
		"<money_free>4.00</money_free>",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("limits = %s, want substring %s", msg, want)
		}
	}
	if got := send(t, c, "<command id="+quote+"get_client_limits"+quote+"/>"); !strings.Contains(got, "Client not specified.") {
		t.Fatalf("missing client = %s", got)
	}
	// The RQ_CLIENT_LIMITS request carries only the packed client id.
	var sawLimits bool
	for _, f := range srv.Requests() {
		if f.Type == session.RqClientLimits {
			sawLimits = true
			if len(f.Payload) == 0 {
				t.Fatalf("RQ_CLIENT_LIMITS payload is empty")
			}
		}
	}
	if !sawLimits {
		t.Fatal("no RQ_CLIENT_LIMITS request")
	}
}

// TestClientLimitsUnknownClient: an unknown client code fails before any request.
func TestClientLimitsUnknownClient(t *testing.T) {
	c := &Client{}
	if _, err := c.ClientLimits(context.Background(), "NOPE"); err == nil || !strings.Contains(err.Error(), "unknown client") {
		t.Fatalf("err = %v, want unknown client", err)
	}
}
