package native

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"bytes"
	"encoding/hex"
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
// short recovery is refused by the new server (another SID), Run reports the
// loss and returns a recovery error, and the explicit Client.Reconnect performs
// a full login: the catalog is rebuilt (SBER secid 26 -> 27), the subscriptions
// are restored by model.Symbol (001b002504) and the replayed trade is not
// delivered twice.
func TestRecoverResubscribesBySymbol(t *testing.T) {
	first := newTestServer(t)
	first.Snapshot = mainPayload(secNewRecord(t, "SBER", 26))
	first.AfterSubscribe = mainPayload(tradeRecord(t, 0, 26))

	second := newTestServer(t)
	second.Snapshot = mainPayload(secNewRecord(t, "SBER", 27))
	second.AfterSubscribe = mainPayload(tradeRecord(t, 0, 27), tradeRecord(t, 1, 27))
	// The new server does not recognise the old session (another SID): the
	// short RQ_RELOGIN/RQ_RECOVER path fails at once (DLL error -0xe).
	second.ReloginSID = 99

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
		t.Fatal("Run did not return after the refused recovery")
	}
	var re *recoveryError
	if !errors.As(runErr, &re) {
		t.Fatalf("Run = %v, want a recovery error", runErr)
	}
	if !errors.Is(runErr, session.ErrNotSameServer) {
		t.Fatalf("Run = %v, want ErrNotSameServer", runErr)
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

// TestRecoverStrictSID checks the SID comparison the DLL does on RQ_RELOGIN: even
// with a recorded SID of 0, a reply carrying another SID is ErrNotSameServer.
func TestRecoverStrictSID(t *testing.T) {
	first := newTestServer(t)
	first.SID = 0 // the RQ_PWD reply reports SID 0
	second := newTestServer(t)
	second.ReloginSID = 99

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
	first.Drop()
	select {
	case err := <-done:
		if !errors.Is(err, session.ErrNotSameServer) {
			t.Fatalf("Run = %v, want ErrNotSameServer even with SID 0", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
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
