package session

import (
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

func readSessionFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	out, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// TestChangePasswordPayloadMatchesDLL pins the 56-byte payload of the O fixture
// (unsalted SHA-1 of "OldPass2!"/"TestPass1!" + reserved zeros).
func TestChangePasswordPayloadMatchesDLL(t *testing.T) {
	got := ChangePasswordPayload("OldPass2!", "TestPass1!")
	want := readSessionFixture(t, "changepwd-request.hex")
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("payload = %x, want %x", got, want)
	}
	if len(got) != 56 {
		t.Fatalf("payload length = %d", len(got))
	}
}

// TestChangePasswordReplies parses the three O reply fixtures.
func TestChangePasswordReplies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  byte
		message string
	}{
		{"changepwd-reply-ok.hex", 0, ""},
		{"changepwd-reply-wrong-old.hex", 1, ""},
		{"changepwd-reply-error.hex", 2, "Server says: bad password"},
	} {
		reply, err := ParseChangePasswordReply(readSessionFixture(t, tc.name))
		if err != nil || reply.Status != tc.status || reply.Message != tc.message {
			t.Fatalf("%s: reply=%+v err=%v", tc.name, reply, err)
		}
	}
}

// TestChangePasswordEmptyReplyFatal: an empty reply is the fatal DLL path and
// closes the session connection (session keys stay unchanged, D).
func TestChangePasswordEmptyReplyFatal(t *testing.T) {
	if _, err := ParseChangePasswordReply(nil); !errors.Is(err, ErrChangePasswordFatal) {
		t.Fatalf("empty reply err = %v, want fatal", err)
	}
}

// TestChangePasswordStrictReply: only 0/1 single-byte and 2 + non-empty C-string
// are valid; everything else is the fatal DLL path.
func TestChangePasswordStrictReply(t *testing.T) {
	valid := [][]byte{{0}, {1}, {2, 'h', 'i', 0}}
	for _, payload := range valid {
		if _, err := ParseChangePasswordReply(payload); err != nil {
			t.Fatalf("valid %x: %v", payload, err)
		}
	}
	invalid := [][]byte{
		{}, {2}, {2, 0}, {2, 'h', 'i'}, {2, 'h', 'i', 0, 0},
		{0, 0}, {1, 0}, {3}, {0xff},
	}
	for _, payload := range invalid {
		if _, err := ParseChangePasswordReply(payload); !errors.Is(err, ErrChangePasswordFatal) {
			t.Fatalf("invalid %x: err=%v, want fatal", payload, err)
		}
	}
}
