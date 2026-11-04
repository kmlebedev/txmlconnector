package native

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/cryptoprofile"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

const (
	testLogin    = "TESTLOGIN"
	testPassword = "TESTPASS"
)

// fakeServer speaks the server side of the protocol: it accepts any RQ_PWD
// for testPassword's key, issues a session key and answers RQ_TIME,
// RQ_STARTUP (demo-server fixture) and RQ_MAIN: first the snapshot (if set),
// then empty, and once after RQ_SUBSCRIBE the queued trades.
type fakeServer struct {
	t       *testing.T
	startup []byte

	mu         sync.Mutex
	logouts    int
	conns      []net.Conn
	mainDelay  time.Duration
	snapshot   []byte   // first RQ_MAIN reply
	trades     []byte   // RQ_MAIN reply after the next RQ_SUBSCRIBE
	subscribes [][]byte // RQ_SUBSCRIBE payloads received
	pending    []byte
}

func newFakeServer(t *testing.T) *fakeServer {
	raw, err := os.ReadFile("records/testdata/startup-demo.hex")
	if err != nil {
		t.Fatal(err)
	}
	startup, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeServer{t: t, startup: startup}
}

// dial connects over loopback TCP: socket buffers matter when a disconnect
// abandons a reply in flight, which an unbuffered net.Pipe cannot model.
func (s *fakeServer) dial(ctx context.Context, address string) (net.Conn, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		defer ln.Close()
		server, err := ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, server)
		s.mu.Unlock()
		s.serve(server)
	}()
	var d net.Dialer
	return d.DialContext(ctx, "tcp", ln.Addr().String())
}

// drop closes every server connection, simulating a lost link.
func (s *fakeServer) drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
}

func (s *fakeServer) serve(nc net.Conn) {
	defer nc.Close()
	keys := wire.DefaultKeys()
	pwKey, _ := cryptoprofile.NewCipher(cryptoprofile.PasswordKey(testPassword))
	sessionKey := make([]byte, cryptoprofile.KeySize)
	for i := range sessionKey {
		sessionKey[i] = byte(0x40 + i)
	}
	sessionSync := [8]byte{0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77}

	reply := func(typ byte, payload []byte) error {
		out, err := keys.Encode(wire.ServerToClient, wire.Frame{Flags: wire.KindReply, Type: typ, Payload: payload})
		if err != nil {
			return err
		}
		_, err = nc.Write(out)
		return err
	}
	for {
		f, err := keys.Decode(wire.ClientToServer, nc)
		if err != nil {
			return
		}
		switch f.Type {
		case session.RqLogin:
			err = reply(f.Type, []byte{0, 8, 1, 2, 3, 4, 5, 6, 7, 8})
		case session.RqPwd:
			var w wire.Writer
			w.Byte(0)
			encKey := make([]byte, len(sessionKey))
			pwKey.CFBEncrypt(cryptoprofile.DefaultSync, encKey, sessionKey)
			encSync := make([]byte, 8)
			pwKey.CFBEncrypt(cryptoprofile.DefaultSync, encSync, sessionSync[:])
			w.Raw(encKey)
			w.Raw(encSync)
			// Byte1..3, SessionTimeout, Byte4..7, Int1
			w.Raw([]byte{0, 0, 0})
			w.Int32(120)
			w.Raw([]byte{0, 0, 0, 0})
			w.Int32(0)
			// Flags, Byte8, SubBroker, Text1, Byte9, Text2, Text3, Byte10
			w.Raw([]byte{0, 0, 0, 0, 0, 0, 0, 0})
			w.Int32(4242) // UID
			w.Byte(0)     // Byte11
			w.Byte(7)     // SID
			w.Raw([]byte{0, 0})
			w.Int32(626) // SYS#
			w.Int32(4)   // BLD#
			if err = reply(f.Type, w.Bytes()); err == nil {
				keys, err = wire.NewKeys(sessionKey, sessionSync)
			}
		case session.RqTime:
			// Demo-server RQ_TIME reply: 2026-11-03 21:03:03, Russian Standard Time.
			p, _ := hex.DecodeString("74c0a4ae064cff0000c4ff00000000000000000000")
			err = reply(f.Type, p)
		case session.RqStartup:
			err = reply(f.Type, s.startup)
		case session.RqMain:
			s.mu.Lock()
			delay := s.mainDelay
			payload := s.snapshot
			s.snapshot = nil
			if payload == nil {
				payload, s.pending = s.pending, nil
			}
			s.mu.Unlock()
			time.Sleep(delay)
			err = reply(f.Type, payload)
		case session.RqSubscribe:
			s.mu.Lock()
			s.subscribes = append(s.subscribes, f.Payload)
			s.pending, s.trades = s.trades, nil
			s.mu.Unlock()
			err = reply(f.Type, nil)
		case session.RqLogout:
			s.mu.Lock()
			s.logouts++
			s.mu.Unlock()
			return
		default:
			s.t.Errorf("unexpected request type %d", f.Type)
			return
		}
		if err != nil {
			return
		}
	}
}

type recorder struct {
	mu   sync.Mutex
	msgs []string
	ch   chan string
}

func newRecorder() *recorder { return &recorder{ch: make(chan string, 64)} }

func (r *recorder) handle(m string) {
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
	r.ch <- m
}

// next waits for a message with the given prefix, skipping others.
func (r *recorder) next(t *testing.T, prefix string) string {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-r.ch:
			if strings.HasPrefix(m, prefix) {
				return m
			}
		case <-timeout:
			t.Fatalf("no message starting with %q", prefix)
		}
	}
}

func send(t *testing.T, c *Connector, command string) string {
	t.Helper()
	res, err := c.SendCommand(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func connectCommand() string {
	return `<command id="connect"><login>` + testLogin + `</login><password>` + testPassword +
		`</password><host>127.0.0.1</host><port>3900</port><rqdelay>10</rqdelay></command>`
}

// Expected results are the DLL's answers recorded on the Finam demo server.
func TestServerStatusLifecycleMatchesDLL(t *testing.T) {
	srv := newFakeServer(t)
	c := New(Config{Dial: srv.dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	steps := []struct{ cmd, want string }{
		{`<command id="server_status"/>`, `<result success="false"><message>Cannot process this command without connection.</message></result>`},
		{`<command id="disconnect"/>`, `<result success="false"><message>Соединение не установлено...</message></result>`},
		{connectCommand(), `<result success="true"/>`},
	}
	for _, s := range steps {
		if got := send(t, c, s.cmd); got != s.want {
			t.Fatalf("%s:\n got %s\nwant %s", s.cmd, got, s.want)
		}
	}

	if m := rec.next(t, "<markets>"); !strings.Contains(m, `<market id="1">ММВБ</market>`) {
		t.Fatalf("markets = %.200s", m)
	}
	rec.next(t, "<boards>")
	rec.next(t, "<candlekinds>")
	const connected = `<server_status sys_ver="626" build="4" server_tz="Russian Standard Time" id="7" connected="true"/>`
	if m := rec.next(t, "<server_status"); m != connected {
		t.Fatalf("status = %s", m)
	}

	if got := send(t, c, `<command id="server_status"/>`); got != `<result success="true"/>` {
		t.Fatalf("server_status = %s", got)
	}
	if m := rec.next(t, "<server_status"); m != connected {
		t.Fatalf("status = %s", m)
	}
	if got := send(t, c, connectCommand()); got != `<result success="false"><message>Соединение уже установлено...</message></result>` {
		t.Fatalf("second connect = %s", got)
	}
	if got := send(t, c, `<command id="disconnect"/>`); got != `<result success="true"/>` {
		t.Fatalf("disconnect = %s", got)
	}
	const disconnected = `<server_status sys_ver="626" build="4" server_tz="Russian Standard Time" id="7" connected="false"/>`
	if m := rec.next(t, "<server_status"); m != disconnected {
		t.Fatalf("status = %s", m)
	}
	if got := send(t, c, `<command id="server_status"/>`); !strings.Contains(got, "without connection") {
		t.Fatalf("server_status after disconnect = %s", got)
	}
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.logouts == 1 })
}

// disconnect while an RQ_MAIN poll is in flight must still log out.
func TestDisconnectDuringPollLogsOut(t *testing.T) {
	srv := newFakeServer(t)
	c := New(Config{Dial: srv.dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, `<server_status`)
	srv.mu.Lock()
	srv.mainDelay = 300 * time.Millisecond
	srv.mu.Unlock()
	time.Sleep(100 * time.Millisecond) // the next poll is now waiting for its reply
	if got := send(t, c, `<command id="disconnect"/>`); got != `<result success="true"/>` {
		t.Fatalf("disconnect = %s", got)
	}
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.logouts == 1 })
}

func TestLostConnectionReportsDisconnected(t *testing.T) {
	srv := newFakeServer(t)
	c := New(Config{Dial: srv.dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, `<server_status`)
	srv.drop()
	if m := rec.next(t, "<server_status"); !strings.HasSuffix(m, `connected="false"/>`) {
		t.Fatalf("status = %s", m)
	}
	if got := send(t, c, `<command id="disconnect"/>`); !strings.Contains(got, "Соединение не установлено") {
		t.Fatalf("disconnect = %s", got)
	}
}

func TestConnectErrorStatus(t *testing.T) {
	c := New(Config{Dial: func(context.Context, string) (net.Conn, error) { return nil, errors.New("refused") }})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	if m := rec.next(t, "<server_status"); m != `<server_status connected="error">Ошибка подключения</server_status>` {
		t.Fatalf("status = %s", m)
	}
	// A failed connect leaves the connector ready for a new attempt.
	if got := send(t, c, `<command id="server_status"/>`); !strings.Contains(got, "without connection") {
		t.Fatalf("server_status = %s", got)
	}
}

func TestCommandDispatch(t *testing.T) {
	c := New(Config{})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cases := []struct{ cmd, want string }{
		{`<command id="bogus"/>`, `<result success="false"><message>Unknown command.</message></result>`},
		{`not xml`, `<result success="false"><message>Unknown command.</message></result>`},
		{`<command id="get_securities"/>`, `<result success="false"><message>Cannot process this command without connection.</message></result>`},
		{`<command id="set_csp"/>`, `<result success="false"><message>Command is not implemented by the native connector.</message></result>`},
		{`<command id="get_connector_version"/>`, `<result success="true"/>`},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); got != tc.want {
			t.Errorf("%s: got %s", tc.cmd, got)
		}
	}
	if m := rec.next(t, "<connector_version>"); m != "<connector_version>"+DefaultVersion+"</connector_version>" {
		t.Fatalf("version = %s", m)
	}
}

func TestSendCommandRequiresStart(t *testing.T) {
	if _, err := New(Config{}).SendCommand(context.Background(), `<command id="server_status"/>`); err == nil {
		t.Fatal("expected error")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
