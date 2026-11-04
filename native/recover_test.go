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
	"github.com/kmlebedev/txmlconnector/native/testserver"
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

// TestRecoverResubscribesBySymbol checks Review Focus 3: after a lost link the
// client logs in again, rebuilds the catalog (SBER changes secid 26 -> 27),
// re-subscribes by model.Symbol (001b002504) and drops the replay of an
// already delivered trade (same trade number under the new secid).
func TestRecoverResubscribesBySymbol(t *testing.T) {
	first := newTestServer(t)
	first.Snapshot = mainPayload(secNewRecord(t, "SBER", 26))
	first.AfterSubscribe = mainPayload(tradeRecord(t, 0, 26))

	second := newTestServer(t)
	second.Snapshot = mainPayload(secNewRecord(t, "SBER", 27))
	second.AfterSubscribe = mainPayload(tradeRecord(t, 0, 27), tradeRecord(t, 1, 27))
	// The new server does not recognise the old session (different SID), so the
	// short RQ_RELOGIN/RQ_RECOVER path fails and the client falls back to a full
	// login: that is the long path this test covers.
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
		Address: "testserver:0", Login: testserver.Login, Password: testserver.Password, Dial: dial,
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

	// Wait for the first trade, then break the link.
	waitFor(t, func() bool {
		_, tr := snapshot()
		return len(tr) >= 1
	})
	first.Drop()

	// The client logs in again, re-subscribes and reports the recovery; the
	// second session replays trade 1 and then sends trade 2.
	waitFor(t, func() bool {
		st, tr := snapshot()
		recovered := false
		for _, s := range st {
			if s.Connected && s.Recover {
				recovered = true
			}
		}
		for _, trd := range tr {
			if trd.TradeNo == 9000000002 {
				return recovered
			}
		}
		return false
	})
	st, tr := snapshot()
	runCancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}

	var recovered int
	for _, s := range st {
		if s.Connected && s.Recover {
			recovered++
		}
	}
	if recovered != 1 {
		t.Fatalf("recovery statuses = %d in %+v, want exactly one", recovered, st)
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
	if subs := subscribePayloads(second); len(subs) != 1 || subs[0] != "001b002504" {
		t.Fatalf("recovery RQ_SUBSCRIBE = %v, want [001b002504]", subs)
	}
	if subs := subscribePayloads(first); len(subs) != 1 || subs[0] != "001a002504" {
		t.Fatalf("first RQ_SUBSCRIBE = %v, want [001a002504]", subs)
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
	var recovered int
	for _, s := range st {
		if s.Connected && s.Recover {
			recovered++
		}
	}
	if recovered != 1 {
		t.Fatalf("recovery statuses = %d in %+v, want exactly one", recovered, st)
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
