package session

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"testing"
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

// TestRecoverPayloadMatchesDLL compares the payloads with the RE-2 E05 capture
// of a short reconnect. The capture is taken by RE-engineer (Wine + DLL); until
// the files exist the layout is only decompiler-verified (RE-1).
func TestRecoverPayloadMatchesDLL(t *testing.T) {
	files, err := filepath.Glob("testdata/recover-*-live.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("RE-2 E05 capture (native/session/testdata/recover-{short,long}-live.*) is not available; the layout is RE-1 (decompiler) only")
	}
	t.Skipf("capture files %v are present but the fixture format is not wired yet", files)
}
