package session

import (
	"bytes"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

func readOptionsFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	out, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// TestOptionFamiliesPayloadMatchesDLL: RQ_OPTION_FAMILIES 149 is W secid
// (O fixture 001a for SBER).
func TestOptionFamiliesPayloadMatchesDLL(t *testing.T) {
	got := OptionFamiliesPayload(26)
	want := readOptionsFixture(t, "option-families-request.hex")
	if !bytes.Equal(got, want) {
		t.Fatalf("payload = %x, want %x", got, want)
	}
}

// TestOptionsReply: empty C-string -> success, non-empty -> DLL <message>.
func TestOptionsReply(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		want    string
		wantErr bool
	}{
		{"ok", readOptionsFixture(t, "option-families-reply-ok.hex"), "", false},
		{"empty", nil, "", false},
		{"error", readOptionsFixture(t, "option-families-reply-error.hex"), "No families", false},
	} {
		message, err := OptionsReply(tc.payload)
		if message != tc.want || (err != nil) != tc.wantErr {
			t.Fatalf("%s: message=%q err=%v", tc.name, message, err)
		}
	}
	if _, err := OptionsReply([]byte{0x41, 0xff}); err == nil {
		t.Fatal("malformed reply accepted")
	}
}

// TestOptionsPayloadsD pins the D builders of 153/150/151.
func TestOptionsPayloadsD(t *testing.T) {
	var w wire.Writer
	w.Uint16(26)
	w.Int32(5)
	if got := FamilyStrikesPayload(26, 5); !bytes.Equal(got, w.Bytes()) {
		t.Fatalf("153 payload = %x", got)
	}
	var family wire.Writer
	family.Uint16(26)
	family.String("FAM", 6)
	family.Int32(1)
	family.Int32(2)
	if got := FamilyOptionsPayload(26, "FAM", 1, 2); !bytes.Equal(got, family.Bytes()) {
		t.Fatalf("150 payload = %x", got)
	}
	var codes wire.Writer
	codes.String("OPT1", 25)
	codes.String("OPT2", 25)
	if got := CertainOptionsPayload([]string{"OPT1", "OPT2"}); !bytes.Equal(got, codes.Bytes()) {
		t.Fatalf("151 payload = %x", got)
	}
}
