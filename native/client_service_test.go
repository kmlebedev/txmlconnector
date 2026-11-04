package native

import (
	"context"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// russianZone is the 16-byte time zone block of the demo server (bias -180
// minutes, daylight -60), the value RQ_TIME carries after the timestamp.
var russianZone = [16]byte{0x4c, 0xff, 0x00, 0x00, 0xc4, 0xff}

// TestServerTimeDifference uses a testserver clock shifted by -2 s: the DLL's
// get_servtime_difference reports the server clock minus the local one, so the
// result must be about -2 s.
func TestServerTimeDifference(t *testing.T) {
	srv := newTestServer(t)
	srv.Handle(session.RqTime, func([]byte) (byte, []byte) {
		var w wire.Writer
		w.Int32(int32(time.Now().Add(-2 * time.Second).Unix()))
		w.Raw(russianZone[:])
		return wire.KindReply, w.Bytes()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if tz := c.ServerTZ(); tz != "Russian Standard Time" {
		t.Fatalf("server tz = %q", tz)
	}
	diff, err := c.ServerTimeDifference(ctx)
	if err != nil {
		t.Fatalf("ServerTimeDifference: %v", err)
	}
	if diff > -1*time.Second || diff < -3*time.Second {
		t.Fatalf("ServerTimeDifference = %v, want -2s ±1s", diff)
	}
}

// TestServiceCommands checks the cached service data: the SID from RQ_PWD and
// the market dictionary from RQ_STARTUP.
func TestServiceCommands(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if id := c.ServerID(); id != 7 {
		t.Fatalf("ServerID = %d, want 7", id)
	}
	markets := c.Markets()
	if len(markets) != 9 {
		t.Fatalf("Markets = %d entries, want 9", len(markets))
	}
	if m := markets[0]; m.ID != 15 || m.Code != "ETS" {
		t.Fatalf("first market = %+v", m)
	}
	// The dictionary is returned as a copy.
	markets[0].Code = "CHANGED"
	if m := c.Markets()[0]; m.Code != "ETS" {
		t.Fatalf("Markets exposes the internal slice: %+v", m)
	}
}

// TestServiceCommandsMatchDLL compares the three commands rendered through the
// connector with the DLL callbacks. The DLL fixture (native/testdata/
// service-dll.xml) needs a live DLL session, which is not available without
// TC_PASSWORD and a recorded session (RE-2/RE-3).
func TestServiceCommandsMatchDLL(t *testing.T) {
	t.Skip("DLL callback fixture for get_servtime_difference/get_server_id/get_markets is missing: needs RE-2/RE-3 (TC_PASSWORD + recorded session)")
}

// TestConnectOptionsMatchDLL checks that the Go connect requests are byte-equal
// to the DLL capture for the same <connect> parameters. The parameters come
// from the connect handler RE-1 (R6, docs/protocol/connect.md) and need its
// capture, so the test is skipped until then.
func TestConnectOptionsMatchDLL(t *testing.T) {
	t.Skip("connect parameters and byte-equality with the DLL wait for R6/RE-1 (docs/protocol/connect.md)")
}
