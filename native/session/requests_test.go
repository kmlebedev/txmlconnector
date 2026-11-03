package session

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// RQ_TIME reply payload from the Finam demo server (sent unencrypted, flags
// 0x82); the DLL logged it as "Server time:3/11/2026 21:03:03".
func TestParseTimeFromDemoServer(t *testing.T) {
	st, err := parseTime(unhex(t, "74c0a4ae064cff0000c4ff00000000000000000000"))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 3, 21, 3, 3, 0, time.UTC); !st.Wall.Equal(want) {
		t.Fatalf("Wall = %v, want %v", st.Wall, want)
	}
	if bias := int16(uint16(st.Zone[0]) | uint16(st.Zone[1])<<8); bias != -180 {
		t.Fatalf("zone bias = %d", bias)
	}
}

func testConn(t *testing.T) (*Conn, net.Conn) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	return &Conn{nc: client, timeout: time.Second, keys: wire.DefaultKeys()}, server
}

func TestMainReportsFullBuffer(t *testing.T) {
	for _, n := range []int{0, 65020, 65021, wire.ClientBufferSize} {
		c, server := testConn(t)
		go func() {
			if _, err := wire.DefaultKeys().Decode(wire.ClientToServer, server); err != nil {
				return
			}
			out, _ := wire.DefaultKeys().Encode(wire.ServerToClient, wire.Frame{Flags: wire.KindReply, Type: RqMain, Payload: make([]byte, n)})
			server.Write(out)
		}()
		r, err := c.Main(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if want := n > 65020; r.More != want || len(r.Payload) != n {
			t.Fatalf("n=%d: More=%v len=%d", n, r.More, len(r.Payload))
		}
	}
}

func TestLogoutSendsWithoutWaiting(t *testing.T) {
	c, server := testConn(t)
	got := make(chan wire.Frame, 1)
	go func() {
		f, _ := wire.DefaultKeys().Decode(wire.ClientToServer, server)
		got <- f
	}()
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f := <-got; f.Type != RqLogout || f.Kind() != wire.KindRequest || len(f.Payload) != 0 {
		t.Fatalf("frame = %+v", f)
	}
}
