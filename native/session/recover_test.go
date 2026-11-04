package session

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// The RQ_RELOGIN payload is Z12: the login up to 12 bytes plus NUL, sent on the
// default keys (RE-1, docs/protocol/connect.md).
func TestReloginPayload(t *testing.T) {
	if got := hex.EncodeToString(ReloginPayload("TESTLOGIN")); got != "544553544c4f47494e00" {
		t.Fatalf("ReloginPayload(TESTLOGIN) = %s", got)
	}
	if got := hex.EncodeToString(ReloginPayload("AAAAAAAAAAAAAAA")); got != "41414141414141414141414100" {
		t.Fatalf("ReloginPayload truncation = %s", got)
	}
}

// The RQ_RECOVER payload is the session token from the RQ_RELOGIN reply (no
// length prefix) followed by packed L: lastSeq, sent on the session keys.
func TestRecoverPayload(t *testing.T) {
	got := hex.EncodeToString(RecoverPayload([]byte{0xde, 0xad, 0xbe, 0xef}, 42))
	if got != "deadbeefa102" {
		t.Fatalf("RecoverPayload = %s", got)
	}
	if got := hex.EncodeToString(RecoverPayload(nil, 0)); got != "00" {
		t.Fatalf("empty-token RecoverPayload = %s", got)
	}
}

// The RQ_RELOGIN reply is B:len, Bn:token, B:sid.
func TestParseReloginReply(t *testing.T) {
	token := []byte{0xde, 0xad, 0xbe, 0xef}
	reply := append([]byte{byte(len(token))}, token...)
	reply = append(reply, 7)
	got, err := ParseReloginReply(reply)
	if err != nil {
		t.Fatalf("ParseReloginReply: %v", err)
	}
	if !bytes.Equal(got.Token, token) || got.SID != 7 {
		t.Fatalf("reply = %+v", got)
	}
	if _, err := ParseReloginReply([]byte{4, 1, 2}); err == nil {
		t.Fatal("expected a short-buffer error")
	}
}

// TestRecoverPayloadMatchesDLL compares the builders and the parser with the
// RE-2 E05 capture of a short reconnect (Wine + DLL against the demo server):
// RQ_RELOGIN carried TESTLOGIN+NUL, the reply carried the token and SID 0, and
// RQ_RECOVER carried that token plus lastSeq=61. The capture is taken by
// RE-engineer; without it the layout stays decompiler-verified (RE-1) only.
func TestRecoverPayloadMatchesDLL(t *testing.T) {
	const hexFile = "testdata/recover-short-live.hex"
	data, err := os.ReadFile(hexFile)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("RE-2 E05 capture native/session/testdata/recover-short-live.hex is not available; the layout is RE-1 (decompiler) only")
	}
	if err != nil {
		t.Fatal(err)
	}
	var relogin, reloginReply, recoverPayload []byte
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("line %d: %q", n+1, line)
		}
		typ, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("line %d: %q", n+1, line)
		}
		var payload []byte
		if len(fields) > 2 {
			if payload, err = hex.DecodeString(fields[2]); err != nil {
				t.Fatalf("line %d: %v", n+1, err)
			}
		}
		switch {
		case fields[0] == "C>S" && typ == int(RqRelogin):
			relogin = payload
		case fields[0] == "S>C" && typ == int(RqRelogin):
			reloginReply = payload
		case fields[0] == "C>S" && typ == int(RqRecover):
			recoverPayload = payload
		}
	}
	if relogin == nil || reloginReply == nil || recoverPayload == nil {
		t.Fatalf("capture misses a recovery frame: relogin %x, reply %x, recover %x", relogin, reloginReply, recoverPayload)
	}
	if got := ReloginPayload("TESTLOGIN"); !bytes.Equal(got, relogin) {
		t.Fatalf("RQ_RELOGIN payload = %x, capture %x", got, relogin)
	}
	reply, err := ParseReloginReply(reloginReply)
	if err != nil {
		t.Fatalf("RQ_RELOGIN reply: %v", err)
	}
	if reply.SID != 0 {
		t.Fatalf("captured SID = %d, want 0", reply.SID)
	}
	if got := RecoverPayload(reply.Token, 61); !bytes.Equal(got, recoverPayload) {
		t.Fatalf("RQ_RECOVER payload = %x, capture %x", got, recoverPayload)
	}
	// The lastSeq really is the packed tail after the token.
	if lastSeq, err := wire.NewReader(recoverPayload[len(reply.Token):]).Int32(); err != nil || lastSeq != 61 {
		t.Fatalf("captured lastSeq = %d, %v, want 61", lastSeq, err)
	}
	// The DLL emitted recover="true" right after connected="..." (the same shape
	// the connector renders); the E05 callbacks carry no other case changes.
	callbacks, err := os.ReadFile("testdata/recover-short-live.xml")
	if err != nil {
		t.Fatalf("callbacks: %v", err)
	}
	if !bytes.Contains(callbacks, []byte(`connected="true" recover="true"`)) {
		t.Fatal("the captured DLL callbacks do not carry connected=\"true\" recover=\"true\"")
	}
}
