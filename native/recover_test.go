package native

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// sizedRecord wraps a decoded record body into its RQ_MAIN form.
func sizedRecord(tag byte, body []byte) []byte {
	out := make([]byte, 3+len(body))
	out[0] = tag
	out[1] = byte(len(body) >> 8)
	out[2] = byte(len(body))
	copy(out[3:], body)
	return out
}

// mainPayload concatenates RQ_MAIN records.
func mainPayload(recs ...[]byte) []byte {
	var out []byte
	for _, r := range recs {
		out = append(out, r...)
	}
	return out
}

// secNewRecord returns the R_SEC_NEW record of a fixture security with the
// session-local secid replaced.
func secNewRecord(t *testing.T, secCode string, secID uint16) []byte {
	t.Helper()
	recs, err := records.Records(readFixture(t, "main1-demo.hex.gz"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Tag != records.TagSecNew {
			continue
		}
		s, err := records.ParseSecNew(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if s.SecCode != secCode {
			continue
		}
		body := append([]byte(nil), r.Body...)
		body[0], body[1] = byte(secID>>8), byte(secID&0xff)
		return sizedRecord(records.TagSecNew, body)
	}
	t.Fatalf("fixture has no %s R_SEC_NEW record", secCode)
	return nil
}

// tradeRecord returns one R_ALLTRADE record of the inject fixture with the
// session-local secid replaced.
func tradeRecord(t *testing.T, idx int, secID uint16) []byte {
	t.Helper()
	recs, err := records.Records(readFixture(t, "alltrades-inject1.hex"))
	if err != nil {
		t.Fatal(err)
	}
	var all [][]byte
	for _, r := range recs {
		if r.Tag == records.TagAllTrade {
			all = append(all, r.Body)
		}
	}
	if idx >= len(all) {
		t.Fatalf("fixture has %d R_ALLTRADE records", len(all))
	}
	body := append([]byte(nil), all[idx]...)
	body[0], body[1] = byte(secID>>8), byte(secID&0xff)
	return sizedRecord(records.TagAllTrade, body)
}

// TestRecoverResubscribesBySymbol checks the long path (Review Focus 3): the
// recovery window expires while the server is unreachable, Run reports the loss
// and returns a recovery error, and the explicit Client.Reconnect performs a
// full login: the catalog is rebuilt (SBER secid 26 -> 27), the subscriptions
// are restored by model.Symbol (001b002504) and the replayed trade is not
// delivered twice.
func TestRecoverResubscribesBySymbol(t *testing.T) {
	first := newTestServer(t)
	// 300 is milliseconds per the RQ_PWD normalization (values below 300 are
	// seconds): the recovery window stays short.
	first.SessionTimeout = 300
	first.Snapshot = mainPayload(secNewRecord(t, "SBER", 26))
	first.AfterSubscribe = mainPayload(tradeRecord(t, 0, 26))

	second := newTestServer(t)
	second.Snapshot = mainPayload(secNewRecord(t, "SBER", 27))
	second.AfterSubscribe = mainPayload(tradeRecord(t, 0, 27), tradeRecord(t, 1, 27))

	var mu sync.Mutex
	connects := 0
	allowReconnect := false
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		connects++
		n := connects
		allow := allowReconnect
		mu.Unlock()
		switch {
		case n == 1:
			return first.Dial(ctx, address)
		case !allow:
			// The new server is unreachable while the recovery window runs.
			return nil, errors.New("connection refused")
		default:
			return second.Dial(ctx, address)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if subs := subscribePayloads(first); len(subs) != 1 || subs[0] != "001a002504" {
		t.Fatalf("first RQ_SUBSCRIBE = %v, want [001a002504]", subs)
	}

	var hmu sync.Mutex
	var statuses []model.ServerStatus
	var trades []model.Trade
	snapshot := func() ([]model.ServerStatus, []model.Trade) {
		hmu.Lock()
		defer hmu.Unlock()
		return append([]model.ServerStatus(nil), statuses...), append([]model.Trade(nil), trades...)
	}
	handler := func(events []model.Event) error {
		hmu.Lock()
		defer hmu.Unlock()
		for _, ev := range events {
			switch e := ev.(type) {
			case model.ServerStatus:
				statuses = append(statuses, e)
			case *model.Trade:
				trades = append(trades, *e)
			}
		}
		return nil
	}
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx, RunOptions{RqDelay: 10 * time.Millisecond}, handler) }()
	waitFor(t, func() bool {
		_, tr := snapshot()
		return len(tr) >= 1
	})
	first.Drop()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the recovery window expired")
	}
	var re *RecoveryError
	if !errors.As(runErr, &re) {
		t.Fatalf("Run = %v, want a recovery error", runErr)
	}
	if errors.Is(runErr, session.ErrNotSameServer) {
		t.Fatalf("Run = %v: the dial failure must not look like a different server", runErr)
	}
	st, tr := snapshot()
	if len(tr) != 1 {
		t.Fatalf("trades before the reconnect = %d, want 1", len(tr))
	}
	if len(st) != 2 || !st[0].Connected || !st[0].Recover || st[1].Connected || st[1].Err == "" {
		t.Fatalf("statuses = %+v, want recover=true then connected=false with Err", st)
	}

	// The DLL leaves the full connect to the XML client; the typed API has an
	// explicit Reconnect for it.
	mu.Lock()
	allowReconnect = true
	mu.Unlock()
	if err := c.Reconnect(ctx); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if subs := subscribePayloads(second); len(subs) != 1 || subs[0] != "001b002504" {
		t.Fatalf("recovery RQ_SUBSCRIBE = %v, want [001b002504]", subs)
	}
	runCtx2, runCancel2 := context.WithCancel(ctx)
	defer runCancel2()
	done2 := make(chan error, 1)
	go func() { done2 <- c.Run(runCtx2, RunOptions{RqDelay: 10 * time.Millisecond}, handler) }()
	waitFor(t, func() bool {
		_, tr := snapshot()
		return len(tr) >= 2
	})
	_, tr = snapshot()
	runCancel2()
	if err := <-done2; !errors.Is(err, context.Canceled) {
		t.Fatalf("second Run = %v, want context.Canceled", err)
	}
	if len(tr) != 2 {
		t.Fatalf("trades = %d, want 2 (trade 1 once, trade 2): %+v", len(tr), tr)
	}
	if tr[0].TradeNo != 9000000001 || tr[1].TradeNo != 9000000002 {
		t.Fatalf("trade numbers = %d, %d, want 9000000001, 9000000002", tr[0].TradeNo, tr[1].TradeNo)
	}
	if tr[1].SecCode != "SBER" || tr[1].SecID != 27 {
		t.Fatalf("trade 2 = %+v, want SBER resolved from the rebuilt catalog (secid 27)", tr[1])
	}
}

// TestRecoverStrictSID checks two DLL rules of the RQ_RELOGIN SID check: it is
// unconditional (a recorded SID of 0 must not skip it), and a mismatch (-0xe) is
// not fatal: the DLL keeps retrying until session_timeout and only then raises
// "Request timeout.".
func TestRecoverStrictSID(t *testing.T) {
	first := newTestServer(t)
	first.SID = 0               // the RQ_PWD reply reports SID 0
	first.SessionTimeout = 2000 // 2 s window, so several 1 s retries happen
	second := newTestServer(t)
	second.ReloginSID = 99 // always another server: every RQ_RELOGIN fails -0xe

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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Run(runCtx, RunOptions{RqDelay: 10 * time.Millisecond}, func([]model.Event) error { return nil })
	}()
	waitFor(t, func() bool { return countRequests(first, session.RqMain) >= 2 })
	start := time.Now()
	first.Drop()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	elapsed := time.Since(start)
	if !errors.Is(runErr, session.ErrNotSameServer) {
		t.Fatalf("Run = %v, want ErrNotSameServer even with SID 0", runErr)
	}
	if elapsed < time.Second {
		t.Fatalf("Run gave up after %v: -0xe must be retried until the window", elapsed)
	}
	var relogins int
	for _, f := range second.Requests() {
		if f.Type == session.RqRelogin {
			relogins++
		}
	}
	if relogins < 2 {
		t.Fatalf("RQ_RELOGIN attempts = %d, want at least 2 before the window expired", relogins)
	}
}

// TestRecoverShortPathKeepsSession checks the DLL short recovery: the client
// redials, sends RQ_RELOGIN (default keys) and RQ_RECOVER (session keys), keeps
// the existing catalog and subscriptions (no second RQ_TIME/RQ_STARTUP/
// RQ_SUBSCRIBE) and reports model.ServerStatus{Connected: true, Recover: true}.
func TestRecoverShortPathKeepsSession(t *testing.T) {
	first := newTestServer(t)
	first.Snapshot = mainPayload(secNewRecord(t, "SBER", 26))
	first.AfterSubscribe = mainPayload(tradeRecord(t, 0, 26))
	second := newTestServer(t) // accepts RQ_RELOGIN for the same SID and RQ_RECOVER

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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var hmu sync.Mutex
	var statuses []model.ServerStatus
	var trades []model.Trade
	snapshot := func() ([]model.ServerStatus, []model.Trade) {
		hmu.Lock()
		defer hmu.Unlock()
		return append([]model.ServerStatus(nil), statuses...), append([]model.Trade(nil), trades...)
	}
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Run(runCtx, RunOptions{RqDelay: 10 * time.Millisecond}, func(events []model.Event) error {
			hmu.Lock()
			defer hmu.Unlock()
			for _, ev := range events {
				switch e := ev.(type) {
				case model.ServerStatus:
					statuses = append(statuses, e)
				case *model.Trade:
					trades = append(trades, *e)
				}
			}
			return nil
		})
	}()
	waitFor(t, func() bool {
		_, tr := snapshot()
		return len(tr) >= 1
	})
	first.Drop()
	waitFor(t, func() bool {
		st, _ := snapshot()
		for _, s := range st {
			if s.Connected && s.Recover {
				return true
			}
		}
		return false
	})
	// The run keeps polling over the restored connection; give it a moment.
	waitFor(t, func() bool {
		for _, f := range second.Requests() {
			if f.Type == session.RqMain {
				return true
			}
		}
		return false
	})
	st, _ := snapshot()
	runCancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	// DLL sequence: recover="true" right on the loss, then a plain connected
	// status after "Recover succeeded.".
	recoverAt := -1
	for i, s := range st {
		if s.Connected && s.Recover {
			if recoverAt >= 0 {
				t.Fatalf("more than one recover status in %+v", st)
			}
			recoverAt = i
		}
	}
	if recoverAt < 0 {
		t.Fatalf("no recover status in %+v", st)
	}
	if recoverAt+1 >= len(st) || !st[recoverAt+1].Connected || st[recoverAt+1].Recover {
		t.Fatalf("status after the recovery = %+v, want a plain connected one", st)
	}
	sec, _, ok := c.Catalog().Lookup("TQBR", "SBER")
	if !ok {
		t.Fatal("the catalog must survive the short recovery")
	}
	if sec.SecID != 26 {
		t.Fatalf("catalog was rebuilt: SBER secid = %d, want the original 26", sec.SecID)
	}

	var relogins, recovers, polls int
	for _, f := range second.Requests() {
		switch f.Type {
		case session.RqRelogin:
			relogins++
			if got := hex.EncodeToString(f.Payload); got != "544553544c4f47494e00" {
				t.Fatalf("RQ_RELOGIN payload = %s, want TESTLOGIN+NUL", got)
			}
		case session.RqRecover:
			recovers++
			token := second.ReloginToken
			if len(f.Payload) < len(token) || !bytes.Equal(f.Payload[:len(token)], token) {
				t.Fatalf("RQ_RECOVER payload = %x, want token %x + lastSeq", f.Payload, token)
			}
			if seq, err := wire.NewReader(f.Payload[len(token):]).Int32(); err != nil || seq <= 0 {
				t.Fatalf("RQ_RECOVER lastSeq = %d, %v", seq, err)
			}
		case session.RqMain:
			polls++
		case session.RqTime, session.RqStartup, session.RqSubscribe:
			t.Fatalf("short recovery must not resend type %d", f.Type)
		}
	}
	if relogins != 1 || recovers != 1 {
		t.Fatalf("short recovery frames: %d RQ_RELOGIN, %d RQ_RECOVER", relogins, recovers)
	}
	if polls == 0 {
		t.Fatal("the client did not resume RQ_MAIN polling after the recovery")
	}
}

// TestRecoverFatalClassification pins the DLL fatal conditions of the recovery
// loop (docs/protocol/connect.md, "Recover unavailable."): BYE and a wrong
// RELOGIN/RECOVER response throw at once; a payload CRC is fatal only when the
// damaged frame is the RQ_RECOVER reply (type 0x40); a SID mismatch and a
// header CRC are retried until the window expires.
func TestRecoverFatalClassification(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		fatal bool
	}{
		{"bye", session.ErrBye, true},
		{"incorrect relogin", session.ErrIncorrectRelogin, true},
		{"incorrect recover", session.ErrIncorrectRecover, true},
		{"payload crc of rq_recover", &wire.PayloadCRCError{Frame: wire.Frame{Type: session.RqRecover}}, true},
		{"payload crc of rq_main", &wire.PayloadCRCError{Frame: wire.Frame{Type: session.RqMain}}, false},
		{"header crc", wire.ErrHeaderCRC, false},
		{"not same server", session.ErrNotSameServer, false},
		{"dial", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		if got := isFatalRecoverError(c.err); got != c.fatal {
			t.Fatalf("%s: isFatalRecoverError(%v) = %v, want %v", c.name, c.err, got, c.fatal)
		}
	}
}

// recoveryStatusLines returns the <server_status> callback lines of an E05
// fixture, in file order.
func recoveryStatusLines(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<server_status") {
			out = append(out, line)
		}
	}
	if len(out) < 3 {
		t.Fatalf("%s: %d server_status callbacks, want the load end and both recovery ones", file, len(out))
	}
	return out
}

// maskFixtureID rebases an E05 fixture status (taken on the demo server, id=0)
// onto the testserver SID 7, the only difference of the deterministic parts.
func maskFixtureID(line string) string {
	return strings.ReplaceAll(line, "id="+quote+"0"+quote, "id="+quote+"7"+quote)
}

// TestConnectorRecoveryCallbacksMatchDLL renders the real Connector around a
// loss and compares the callbacks with the DLL captures: the short open ended
// recovery gives recover="true" then a plain connected status, the long one (the
// window expires) gives recover="true" then connected="false".
func TestConnectorRecoveryCallbacksMatchDLL(t *testing.T) {
	shortLines := recoveryStatusLines(t, "session/testdata/recover-short-live.xml")
	longLines := recoveryStatusLines(t, "session/testdata/recover-long-live.xml")

	t.Run("short", func(t *testing.T) {
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
		rec.next(t, "<server_status") // the load end callback
		first.Drop()
		if got, want := rec.next(t, "<server_status"), maskFixtureID(shortLines[1]); got != want {
			t.Fatalf("loss callback = %s, want %s", got, want)
		}
		if got, want := rec.next(t, "<server_status"), maskFixtureID(shortLines[2]); got != want {
			t.Fatalf("recovered callback = %s, want %s", got, want)
		}
	})

	t.Run("long", func(t *testing.T) {
		srv := newTestServer(t)
		srv.SessionTimeout = 300 // 300 ms window, the proxy stays down
		var mu sync.Mutex
		connects := 0
		dial := func(ctx context.Context, address string) (net.Conn, error) {
			mu.Lock()
			connects++
			n := connects
			mu.Unlock()
			if n == 1 {
				return srv.Dial(ctx, address)
			}
			return nil, errors.New("connection refused")
		}
		c := New(Config{Dial: dial})
		rec := newRecorder()
		if err := c.Start(rec.handle); err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		send(t, c, connectCommand())
		rec.next(t, "<server_status") // the load end callback
		srv.Drop()
		if got, want := rec.next(t, "<server_status"), maskFixtureID(longLines[1]); got != want {
			t.Fatalf("loss callback = %s, want %s", got, want)
		}
		if got, want := rec.next(t, "<server_status"), maskFixtureID(longLines[2]); got != want {
			t.Fatalf("give-up callback = %s, want %s", got, want)
		}
	})
}

// TestDisconnectDuringRecoveryReportsOneFalse is the P2-1 regression: a
// disconnect issued while the recovery loop runs reports connected="false"
// exactly once (the disconnect callback), not once from the cancelled recovery
// and once from the disconnect.
func TestDisconnectDuringRecoveryReportsOneFalse(t *testing.T) {
	srv := newTestServer(t)
	srv.SessionTimeout = 300 // 300 ms window
	var mu sync.Mutex
	connects := 0
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		connects++
		n := connects
		mu.Unlock()
		if n == 1 {
			return srv.Dial(ctx, address)
		}
		return nil, errors.New("connection refused")
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
	srv.Drop()
	if got := rec.next(t, "<server_status"); !strings.Contains(got, "recover="+quote+"true"+quote) {
		t.Fatalf("loss callback = %s, want recover=true", got)
	}
	if got := send(t, c, "<command id="+quote+"disconnect"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("disconnect = %s", got)
	}
	time.Sleep(100 * time.Millisecond)
	rec.mu.Lock()
	messages := append([]string(nil), rec.msgs[base:]...)
	rec.mu.Unlock()
	var falseCount int
	for _, m := range messages {
		if strings.HasSuffix(m, "connected="+quote+"false"+quote+"/>") {
			falseCount++
		}
	}
	if falseCount != 1 {
		t.Fatalf("connected=false callbacks = %d, want exactly one; messages: %q", falseCount, messages)
	}
}
