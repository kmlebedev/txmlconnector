package session

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// A request whose context is cancelled after it was written still gets its
// reply from the server. The next exchange must skip that stale reply instead
// of taking it for its own (oracle 2026-11-05: gethistorydata returned, its
// context cancelled the RQ_HISTORY_EXT wait, and the next RQ_MAIN failed with
// "incorrect server response: type 0x94").
func TestAbandonedReplyIsSkipped(t *testing.T) {
	client, server := pipeTCP(t)
	c := &Conn{nc: client, timeout: 5 * time.Second, keys: wire.DefaultKeys()}
	release := make(chan struct{})
	go func() {
		keys := wire.DefaultKeys()
		reply := func(typ byte, payload []byte) {
			out, _ := keys.Encode(wire.ServerToClient, wire.Frame{Flags: wire.KindReply, Type: typ, Payload: payload})
			server.Write(out)
		}
		first, err := keys.Decode(wire.ClientToServer, server)
		if err != nil {
			return
		}
		<-release // answer only after the client gave up waiting
		reply(first.Type, nil)
		second, err := keys.Decode(wire.ClientToServer, server)
		if err != nil {
			return
		}
		reply(second.Type, []byte{0x42})
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := c.Request(ctx, 148, []byte{1}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	close(release)
	got, err := c.Request(context.Background(), RqMain, nil)
	if err != nil {
		t.Fatalf("next request: %v", err)
	}
	if len(got) != 1 || got[0] != 0x42 {
		t.Fatalf("next reply = %x, want 42", got)
	}
}

// pipeTCP returns a connected loopback TCP pair; unlike net.Pipe it buffers,
// so a server reply does not block the client's next write.
func pipeTCP(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		nc, _ := ln.Accept()
		accepted <- nc
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}
