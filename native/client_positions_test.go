package native

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

var errOddHex = errTest{"odd hex"}

type errTest struct{ s string }

func (e errTest) Error() string { return e.s }

func readPositionFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hexDecodeTrade(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return payload
}

func hexDecodeTrade(s string) ([]byte, error) {
	table := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	if len(s)%2 != 0 {
		return nil, errOddHex
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		hi, ok1 := table(s[2*i])
		lo, ok2 := table(s[2*i+1])
		if !ok1 || !ok2 {
			return nil, errOddHex
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

// TestPositionsMatchDLL checks the O position fixtures byte for byte: the
// sec_position stream callback (R_CLIENT_ASSET sym != 0xffff) and the
// forts_position + forts_money reply of get_forts_positions (RQ_CLIENT_HOLDINGS
// type 42).
func TestPositionsMatchDLL(t *testing.T) {
	// sec_position: the fixture carries the client record and the paper asset.
	secFixture := readPositionFixture(t, "position-sec-inject.hex")
	wantSec, err := os.ReadFile("records/testdata/position-sec-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	secLines := strings.Split(strings.TrimSpace(string(wantSec)), "\n")
	if len(secLines) != 2 {
		t.Fatalf("sec fixture lines = %d", len(secLines))
	}
	srv := newRunServer(t)
	srv.Snapshot = append(secNewRecord(t, "SBER", 26), secFixture...)
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
	if !containsMessage(messages, secLines[0]) {
		t.Fatalf("client callback = %q, want %q", messages, secLines[0])
	}
	if !containsMessage(messages, secLines[1]) {
		t.Fatalf("sec_position callback = %q, want %q", messages, secLines[1])
	}
	// forts: the reply of RQ_CLIENT_HOLDINGS is injected by the testserver.
	fortsReply := readPositionFixture(t, "position-forts-inject.hex")
	wantForts, err := os.ReadFile("records/testdata/position-forts-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	// Client-level: the FORTS reply carries the numeric client id of the
	// session account, so patch it like the other injected records do.
	srv2 := newRunServer(t)
	srv2.Snapshot = clientSnapshot(t)
	var replyMu sync.Mutex
	var replyPayload []byte
	srv2.Handle(session.RqClientHoldings, func([]byte) (byte, []byte) {
		replyMu.Lock()
		defer replyMu.Unlock()
		return wire.KindReply | wire.FlagPlain, replyPayload
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv2.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	client.Catalog().AddSecurity(records.SecNew{SecID: 20583, Market: 4, SecCode: "SiZ6", ShortName: "SiZ6"})
	clientID, ok := client.clientNumericIDLocked("TESTCLIEN", "")
	if !ok {
		t.Fatal("no numeric client id")
	}
	replyMu.Lock()
	replyPayload = patchHoldingsClient(t, fortsReply, clientID)
	replyMu.Unlock()
	positions, money, err := client.GetFortsPositions(ctx, "TESTCLIEN")
	if err != nil {
		t.Fatalf("GetFortsPositions: %v", err)
	}
	if got := xmlcompat.FortsPositionsXML(positions, money); got != strings.TrimSpace(string(wantForts)) {
		t.Fatalf("forts positions\n got %s\nwant %s", got, strings.TrimSpace(string(wantForts)))
	}
}

// patchHoldingsClient rewrites the numeric client id of the RQ_CLIENT_HOLDINGS
// payload (8 one-byte zero headers, then the packed client id).
func patchHoldingsClient(t *testing.T, payload []byte, clientID int64) []byte {
	t.Helper()
	var w wire.Writer
	w.Int64(clientID)
	patched := append([]byte(nil), payload[:8]...)
	patched = append(patched, w.Bytes()...)
	patched = append(patched, payload[11:]...)
	return patched
}

func containsMessage(messages []string, want string) bool {
	for _, m := range messages {
		if m == want {
			return true
		}
	}
	return false
}
