package native

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/testserver"
)

const (
	testLogin    = testserver.Login
	testPassword = testserver.Password
)

// newTestServer returns the shared test double with the demo-server
// dictionaries loaded: the native client needs a valid RQ_STARTUP reply.
func newTestServer(t *testing.T) *testserver.Server {
	t.Helper()
	srv := testserver.New(t)
	srv.Startup = readFixture(t, "startup-demo.hex")
	return srv
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
	srv := newTestServer(t)
	c := New(Config{Dial: srv.Dial})
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
	waitFor(t, func() bool { return srv.Logouts() == 1 })
}

// disconnect while an RQ_MAIN poll is in flight must still log out.
func TestDisconnectDuringPollLogsOut(t *testing.T) {
	srv := newTestServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, `<server_status`)
	srv.SetMainDelay(300 * time.Millisecond)
	time.Sleep(100 * time.Millisecond) // the next poll is now waiting for its reply
	if got := send(t, c, `<command id="disconnect"/>`); got != `<result success="true"/>` {
		t.Fatalf("disconnect = %s", got)
	}
	waitFor(t, func() bool { return srv.Logouts() == 1 })
}

func TestLostConnectionReportsDisconnected(t *testing.T) {
	srv := newTestServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, `<server_status`)
	srv.Drop()
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

// TestEventlessMainStillDeliversSecurities checks that an RQ_MAIN reply whose
// only content is a catalog mutation still flushes the <securities>/<pits>
// chunks: the DLL emits them for every poll, even when no market event came.
func TestEventlessMainStillDeliversSecurities(t *testing.T) {
	main := readFixture(t, "main1-demo.hex.gz")
	recs, err := records.Records(main)
	if err != nil {
		t.Fatal(err)
	}
	var secBody []byte
	for _, r := range recs {
		if r.Tag == records.TagSecNew {
			secBody = append([]byte(nil), r.Body...)
			break
		}
	}
	if secBody == nil {
		t.Fatal("snapshot fixture has no R_SEC_NEW record")
	}
	const newID = 65000
	secBody[0], secBody[1] = byte(newID>>8), byte(newID&0xff)
	payload := append([]byte{records.TagSecNew, byte(len(secBody) >> 8), byte(len(secBody))}, secBody...)

	srv := newTestServer(t)
	srv.Snapshot = main
	srv.AfterSubscribe = payload
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	sub := `<command id="subscribe"><alltrades><security><board>TQBR</board><seccode>SBER</seccode></security></alltrades></command>`
	if got := send(t, c, sub); got != `<result success="true"/>` {
		t.Fatalf("subscribe = %s", got)
	}
	msg := rec.next(t, "<securities>")
	if !strings.Contains(msg, `secid="65000"`) {
		t.Fatalf("securities chunk without the new instrument: %s", msg)
	}
	if strings.Contains(msg, "<trade") {
		t.Fatalf("unexpected trades in the securities chunk: %s", msg)
	}
}
