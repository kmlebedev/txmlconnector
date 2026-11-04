package wire

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCRC16(t *testing.T) {
	if got := CRC16([]byte("123456789")); got != 0x29b1 {
		t.Fatalf("CRC16 = %#04x", got)
	}
	if got := CRC16(nil); got != 0xffff {
		t.Fatalf("CRC16(nil) = %#04x", got)
	}
}

// RQ_LOGIN packets captured from the DLL connecting to a local listener.
var capturedLogin = []struct {
	login, payload, wire string
}{
	{"TESTLOGIN", "78004e0001021c544553544c4f47494e00ffe8",
		"e920e526b0d5603b9021ab352b9268fca36d964c861698e8f82e56"},
	{"AAAAAAAAAAAA", "78004e0001021c41414141414141414141414100ffe8",
		"e920e5233082ba419021ab352b9268e9a77f8341881090e7b990ff530c8d"},
	{"A", "78004e0001021c4100ffe8",
		"e920e53e108c5c5b9021ab352b9268e9e6c12a"},
}

func TestEncodeMatchesCapturedRequests(t *testing.T) {
	for _, c := range capturedLogin {
		got, err := DefaultKeys().Encode(ClientToServer, Frame{Flags: KindRequest, Type: 1, Payload: unhex(t, c.payload)})
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(got) != c.wire {
			t.Fatalf("%s: wire = %x, want %s", c.login, got, c.wire)
		}
		f, err := DefaultKeys().Decode(ClientToServer, bytes.NewReader(got))
		if err != nil {
			t.Fatal(err)
		}
		if f.Type != 1 || f.Kind() != KindRequest || hex.EncodeToString(f.Payload) != c.payload {
			t.Fatalf("%s: decoded %+v", c.login, f)
		}
	}
}

// The DLL accepted this server reply and traced its payload as 0,8,1,...,8.
func TestDecodeReplyAcceptedByDLL(t *testing.T) {
	raw := unhex(t, "d7c6c64985ed4716d5cfc74138835a3ee034")
	f, err := DefaultKeys().Decode(ServerToClient, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind() != KindReply || f.Type != 1 || hex.EncodeToString(f.Payload) != "00080102030405060708" {
		t.Fatalf("decoded %+v", f)
	}
	enc, err := DefaultKeys().Encode(ServerToClient, f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(enc, raw) {
		t.Fatalf("re-encoded %x", enc)
	}
}

func TestDecodeRejectsCorruption(t *testing.T) {
	raw := unhex(t, capturedLogin[0].wire)
	hdr := append([]byte(nil), raw...)
	hdr[6] ^= 1
	if _, err := DefaultKeys().Decode(ClientToServer, bytes.NewReader(hdr)); !errors.Is(err, ErrHeaderCRC) {
		t.Fatalf("header corruption: %v", err)
	}
	body := append([]byte(nil), raw...)
	body[len(body)-1] ^= 1
	if _, err := DefaultKeys().Decode(ClientToServer, bytes.NewReader(body)); !errors.Is(err, ErrPayloadCRC) {
		t.Fatalf("payload corruption: %v", err)
	}
}

// Encodings traced by the DLL as L:16782022, L:-2130466624 and L:0.
func TestPackedIntMatchesDLL(t *testing.T) {
	cases := []struct {
		v   int32
		hex string
	}{
		{16782022, "632c0110"},
		{-2130466624, "0c74c5ef07"},
		{0, "00"},
	}
	for _, c := range cases {
		var w Writer
		w.Int32(c.v)
		if hex.EncodeToString(w.Bytes()) != c.hex {
			t.Fatalf("Int32(%d) = %x, want %s", c.v, w.Bytes(), c.hex)
		}
		got, err := NewReader(w.Bytes()).Int32()
		if err != nil || got != c.v {
			t.Fatalf("read %d: %d, %v", c.v, got, err)
		}
	}
}

func TestPackedIntRoundTrip(t *testing.T) {
	for _, v := range []int64{1, -1, 15, 16, -16, 4242, math.MaxInt32, math.MinInt32 + 1,
		1<<52 - 1, 1 << 52, 1<<60 - 1, -(1<<60 - 1), math.MaxInt64, math.MinInt64 + 1} {
		var w Writer
		w.Int64(v)
		got, err := NewReader(w.Bytes()).Int64()
		if err != nil || got != v {
			t.Fatalf("%d: got %d, %v (% x)", v, got, err, w.Bytes())
		}
	}
	var w Writer
	w.Int32(math.MinInt32)
	if got, _ := NewReader(w.Bytes()).Int32(); got != math.MinInt32 {
		t.Fatalf("MinInt32 = %d", got)
	}
	// Count 7 means eight extra bytes for 64-bit fields (FUN_1801bd9b0).
	w = Writer{}
	w.Int64(math.MaxInt64)
	if b := w.Bytes(); len(b) != 9 || b[0]&7 != 7 {
		t.Fatalf("MaxInt64 = % x", b)
	}
}

func TestStringLimits(t *testing.T) {
	var w Writer
	w.String("TESTLOGINLONGER", 12)
	if string(w.Bytes()) != "TESTLOGINLON\x00" {
		t.Fatalf("truncated = %q", w.Bytes())
	}
	r := NewReader([]byte("abc\x00rest"))
	if _, err := r.String(2); err == nil {
		t.Fatal("expected error for string longer than max")
	}
	if s, err := r.String(3); err != nil || s != "abc" {
		t.Fatalf("String = %q, %v", s, err)
	}
	if r.Len() != 4 {
		t.Fatalf("Len = %d", r.Len())
	}
}

// TestEncodeServerPlainPayload checks the server side of FUN_1801bfb20: the
// S>C direction keeps the header flags (0x82 = reply + plain payload) and
// sends the payload unencrypted, while the client direction masks the plain
// bit - the client never sends plain payloads.
func TestEncodeServerPlainPayload(t *testing.T) {
	keys := DefaultKeys()
	payload := []byte("plain payload")
	raw, err := keys.Encode(ServerToClient, Frame{Flags: KindReply | FlagPlain, Type: 5, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw[HeaderSize:], payload) {
		t.Fatalf("server payload is encrypted on the wire: %x", raw[HeaderSize:])
	}
	f, err := keys.Decode(ServerToClient, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if f.Flags&FlagPlain == 0 || f.Kind() != KindReply || f.Type != 5 || !bytes.Equal(f.Payload, payload) {
		t.Fatalf("decoded server frame = %+v", f)
	}

	raw, err = keys.Encode(ClientToServer, Frame{Flags: KindRequest | FlagPlain, Type: 5, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw[HeaderSize:], payload) {
		t.Fatal("client payload must be encrypted: the plain bit is server-only")
	}
	f, err = keys.Decode(ClientToServer, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if f.Flags&FlagPlain != 0 || f.Kind() != KindRequest || f.Type != 5 || !bytes.Equal(f.Payload, payload) {
		t.Fatalf("decoded client frame = %+v", f)
	}
}
