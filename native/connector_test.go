package native

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
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
	// 300 is milliseconds per the RQ_PWD normalization (values below 300 are
	// seconds), so the recovery window stays short and the test does not wait.
	srv.SessionTimeout = 300
	var mu sync.Mutex
	connects := 0
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		connects++
		n := connects
		mu.Unlock()
		if n > 1 {
			return nil, errors.New("connection refused")
		}
		return srv.Dial(ctx, address)
	}
	c := New(Config{Dial: dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, `<server_status`)
	rec.mu.Lock()
	base := len(rec.msgs)
	rec.mu.Unlock()
	srv.Drop()
	// The DLL reports recover="true" on the loss, then connected="false" when
	// the session cannot be restored.
	wantRecord := `<server_status sys_ver="626" build="4" server_tz="Russian Standard Time" id="7" connected="true" recover="true"/>`
	if m := rec.next(t, "<server_status"); m != wantRecord {
		t.Fatalf("loss status = %s, want %s", m, wantRecord)
	}
	if m := rec.next(t, "<server_status"); !strings.HasSuffix(m, `connected="false"/>`) {
		t.Fatalf("give-up status = %s, want connected=\"false\"", m)
	}
	if got := send(t, c, `<command id="disconnect"/>`); !strings.Contains(got, "Соединение не установлено") {
		t.Fatalf("disconnect = %s", got)
	}
	// The give-up path has no overnight line before connected="false" (E05).
	rec.mu.Lock()
	messages := append([]string(nil), rec.msgs[base:]...)
	rec.mu.Unlock()
	for _, m := range messages {
		if strings.HasPrefix(m, "<overnight") {
			t.Fatalf("overnight line in the give-up callbacks: %q", messages)
		}
	}
}

// TestConnectorReportsRecoveryStatus checks the callback the DLL emits after a
// lost link is restored: <server_status> with connected="true" recover="true".
func TestConnectorReportsRecoveryStatus(t *testing.T) {
	first := newTestServer(t)
	second := newTestServer(t)
	var mu sync.Mutex
	connects := 0
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		connects++
		n := connects
		mu.Unlock()
		if n == 1 {
			return first.Dial(ctx, address)
		}
		return second.Dial(ctx, address)
	}
	c := New(Config{Dial: dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	first.Drop()
	// The DLL reports recover="true" right on the loss and a plain connected
	// status after "Recover succeeded.".
	wantRecover := `<server_status sys_ver="626" build="4" server_tz="Russian Standard Time" id="7" connected="true" recover="true"/>`
	if m := rec.next(t, "<server_status"); m != wantRecover {
		t.Fatalf("loss status = %s, want %s", m, wantRecover)
	}
	wantConnected := `<server_status sys_ver="626" build="4" server_tz="Russian Standard Time" id="7" connected="true"/>`
	if m := rec.next(t, "<server_status"); m != wantConnected {
		t.Fatalf("recovered status = %s, want %s", m, wantConnected)
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

// snapshotTail returns the decoded E07 end-of-load RQ_MAIN tail (task 7).
func snapshotTail(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/snapshot-tail-live.hex")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(data)), "")
	payload, err := hex.DecodeString(text)
	if err != nil {
		t.Fatalf("snapshot tail hex: %v", err)
	}
	return payload
}

// snapshotTailXML returns the non-comment callback lines of the E07 fixture.
func snapshotTailXML(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/snapshot-tail-live.xml")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	inComment := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<!--") {
			inComment = true
		}
		if inComment {
			if strings.HasSuffix(line, "-->") {
				inComment = false
			}
			continue
		}
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// TestSnapshotTailMatchesDLL decodes the E07 end-of-load fixture through the
// task 7 decoders and compares the renders with the DLL callbacks of
// native/testdata/snapshot-tail-live.xml (comments stripped). The remainder of
// the tail after R_CLIENT_ASSET (the 0x5b limits/parameters block) is not mapped
// yet: that is task 12 ("прочие записи снапшота"), so Records stops there.
func TestSnapshotTailMatchesDLL(t *testing.T) {
	payload := snapshotTail(t)
	recs, err := records.Records(payload)
	var unknown *records.UnknownRecordError
	if !errors.As(err, &unknown) {
		t.Fatalf("Records: %v, want the documented 0x5b boundary", err)
	}
	// The mapped part of the E07 tail ends at the 0x5b limits/parameters block
	// (its layout is task 12): pin the boundary so a silent shift is noticed.
	if unknown.Tag != 0x5b || unknown.Offset != 249 {
		t.Fatalf("unmapped tail starts at tag %#02x offset %d, want 0x5b at 249", unknown.Tag, unknown.Offset)
	}
	if len(recs) != 7 {
		t.Fatalf("decoded %d records before the boundary, want 7: %v", len(recs), recs)
	}
	c := &Client{}
	c.cfg.OnDictionaries = nil
	c.resetAccountsLocked()
	for _, rec := range recs {
		if decode := decoders[rec.Tag]; decode != nil {
			if _, err := decode(c, rec.Body); err != nil {
				t.Fatalf("decode %#02x: %v", rec.Tag, err)
			}
		}
	}
	accounts := c.Accounts()
	if len(accounts) != 1 || accounts[0].ID != "TESTCLIEN" {
		t.Fatalf("accounts = %+v", accounts)
	}
	positions := c.MoneyPositions()
	if len(positions) != 1 {
		t.Fatalf("money positions = %+v", positions)
	}
	got := []string{
		xmlcompat.ClientXML(accounts[0]),
		xmlcompat.PositionsXML(positions, false),
		xmlcompat.OvernightXML(c.Overnight().Status),
	}
	want := snapshotTailXML(t)
	if len(want) < len(got) {
		t.Fatalf("fixture has %d lines", len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("message %d\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}

// TestConnectorLoadTailOrder checks the DLL callback order at the end of the
// load on the testserver: the last <securities>/<pits>, then <client>,
// <positions>, <overnight>, <server_status connected="true"/>, and the overnight
// line before every later server_status.
func TestConnectorLoadTailOrder(t *testing.T) {
	// The full demo RQ_MAIN payload is 65 379 bytes, so the tail is appended to
	// a one-security snapshot: the ordering does not depend on the snapshot size.
	snapshot := append(secNewRecord(t, "SBER", 26), snapshotTail(t)...)
	srv := newTestServer(t)
	srv.Snapshot = snapshot
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	rec.mu.Lock()
	messages := append([]string(nil), rec.msgs...)
	rec.mu.Unlock()
	index := func(prefix string) int {
		for i, m := range messages {
			if strings.HasPrefix(m, prefix) {
				return i
			}
		}
		t.Fatalf("no %q callback in %d messages: %q", prefix, len(messages), messages)
		return -1
	}
	client := index("<client")
	positions := index("<positions")
	overnight := index("<overnight")
	status := index("<server_status")
	securities := index("<securities")
	pits := index("<pits")
	if securities > client || pits > client {
		t.Fatalf("end-of-load order broken: securities %d pits %d client %d", securities, pits, client)
	}
	if !(client < positions && positions < overnight && overnight < status) {
		t.Fatalf("end-of-load order: client %d positions %d overnight %d status %d", client, positions, overnight, status)
	}
	if got, want := messages[client], snapshotTailXML(t)[0]; got != want {
		t.Fatalf("client = %s, want %s", got, want)
	}
	// The server_status command repeats the overnight line before the status.
	if got := send(t, c, "<command id="+quote+"server_status"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("server_status = %s", got)
	}
	rec.next(t, "<overnight")
	rec.next(t, "<server_status")

	// The disconnect callback carries no overnight line (E07 fixture).
	rec.mu.Lock()
	base := len(rec.msgs)
	rec.mu.Unlock()
	if got := send(t, c, "<command id="+quote+"disconnect"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("disconnect = %s", got)
	}
	rec.mu.Lock()
	tail := append([]string(nil), rec.msgs[base:]...)
	rec.mu.Unlock()
	var falseN, overnightN int
	for _, m := range tail {
		if strings.HasPrefix(m, "<overnight") {
			overnightN++
		}
		if strings.HasSuffix(m, "connected="+quote+"false"+quote+"/>") {
			falseN++
		}
	}
	if falseN != 1 || overnightN != 0 {
		t.Fatalf("disconnect callbacks: %d false, %d overnight; want 1 and 0: %q", falseN, overnightN, tail)
	}
}

// TestNoOvernightBeforeRecoveryStatuses is the P2 regression: the DLL puts the
// overnight line only before the load-end and command server_status callbacks,
// not before the recovery/disconnect ones (E05/E07 fixtures).
func TestNoOvernightBeforeRecoveryStatuses(t *testing.T) {
	first := newTestServer(t)
	second := newTestServer(t)
	var mu sync.Mutex
	connects := 0
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		connects++
		n := connects
		mu.Unlock()
		if n == 1 {
			return first.Dial(ctx, address)
		}
		return second.Dial(ctx, address)
	}
	c := New(Config{Dial: dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	rec.mu.Lock()
	base := len(rec.msgs)
	rec.mu.Unlock()
	first.Drop()
	rec.next(t, "<server_status") // recover="true"
	rec.next(t, "<server_status") // plain connected="true"
	rec.mu.Lock()
	messages := append([]string(nil), rec.msgs[base:]...)
	rec.mu.Unlock()
	for _, m := range messages {
		if strings.HasPrefix(m, "<overnight") {
			t.Fatalf("overnight line in the recovery callbacks: %q", messages)
		}
	}
}

// TestOvernightRecordReachesStatus checks R_OVERNIGHT=1: the load-end status is
// prefixed with <overnight status="true"/>.
func TestOvernightRecordReachesStatus(t *testing.T) {
	srv := newTestServer(t)
	var w wire.Writer
	// The session starts with a one-security snapshot and the overnight record.
	srv.Snapshot = append(secNewRecord(t, "SBER", 26), []byte{records.TagOvernight, 1}...)
	_ = w
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	if got := rec.next(t, "<overnight"); got != "<overnight status="+quote+"true"+quote+"/>" {
		t.Fatalf("overnight = %s, want status=true", got)
	}
	rec.next(t, "<server_status")
}

// TestUnionRecordReachesLoadEnd checks the non-empty union path: the record has
// no live capture (I), but it must not be lost; the callback is emitted after
// <positions> and before the overnight line.
func TestUnionRecordReachesLoadEnd(t *testing.T) {
	srv := newTestServer(t)
	var w wire.Writer
	w.Byte(records.TagUnion)
	w.Int64(42)
	w.String("TESTUNION", 20)
	w.String("TESTCAT", 32)
	w.Byte(1)
	srv.Snapshot = w.Bytes()
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	if got := rec.next(t, "<union"); got != "<union id="+quote+"TESTUNION"+quote+" remove="+quote+"false"+quote+"/>" {
		t.Fatalf("union = %s", got)
	}
	rec.next(t, "<overnight")
	rec.next(t, "<server_status")
}

// secParamRecord builds one sized R_SEC_PARAM record for the testserver: mask
// 0x9 (bit0 money value + bit3 point_cost), as validated against the DLL.
func secParamRecord(t *testing.T, secid uint16, money int64, pointCost float64) []byte {
	t.Helper()
	var w wire.Writer
	w.Uint16(secid)
	w.Int64(0x9) // bits 0x1 (money) and 0x8 (point_cost)
	w.Int64(money)
	w.Raw([]byte{0, 0}) // the two header bytes of the point_cost block
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], math.Float64bits(pointCost))
	w.Raw(raw[:])
	body := w.Bytes()
	rec := make([]byte, 3+len(body))
	rec[0] = records.TagSecParam
	rec[1] = byte(len(body) >> 8)
	rec[2] = byte(len(body))
	copy(rec[3:], body)
	return rec
}

// TestConnectorSecInfoUpd checks the XML path end to end: a R_SEC_PARAM fetched
// after the subscription reaches the Connector handler as <sec_info_upd>.
func TestConnectorSecInfoUpd(t *testing.T) {
	// The catalog comes from the recorded demo snapshot, which knows SBER 26.
	srv := newRunServer(t)
	srv.AfterSubscribe = secParamRecord(t, 26, 3272739, 100)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	sub := "<command id=" + quote + "subscribe" + quote + "><alltrades>"
	sub += "<security><board>TQBR</board><seccode>SBER</seccode></security></alltrades></command>"
	if got := send(t, c, sub); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe = %s", got)
	}
	// The values are hardcoded (the DLL money format: hundredths with exactly
	// two decimals; point_cost %g) and only the catalog identity comes from the
	// demo snapshot.
	want := "<sec_info_upd><secid>26</secid><seccode>SBER</seccode><market>1</market>"
	want += "<buy_deposit>32727.39</buy_deposit><point_cost>100</point_cost></sec_info_upd>"
	if got := rec.next(t, "<sec_info_upd"); got != want {
		t.Fatalf("sec_info_upd = %s, want %s", got, want)
	}
}
