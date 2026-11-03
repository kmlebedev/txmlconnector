package cryptoprofile

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustCipher(t *testing.T, key []byte) *Cipher {
	t.Helper()
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The DLL answered challenge 0102030405060708 for password TESTPASS with this
// proof (RQ_PWD trace "B8:x211cf3ef536a73bf", experiment against a local stub).
func TestPasswordProofMatchesDLL(t *testing.T) {
	c := mustCipher(t, PasswordKey("TESTPASS"))
	challenge := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	got := make([]byte, len(challenge))
	c.Gamma(DefaultSync, got, challenge)
	if want := "211cf3ef536a73bf"; hex.EncodeToString(got) != want {
		t.Fatalf("proof = %x, want %s", got, want)
	}
}

func TestGammaIsInvolution(t *testing.T) {
	c := mustCipher(t, DefaultKey)
	src := bytes.Repeat([]byte("transaq!"), 5)[:37]
	enc := make([]byte, len(src))
	c.Gamma(DefaultSync, enc, src)
	c.Gamma(DefaultSync, enc, enc)
	if !bytes.Equal(enc, src) {
		t.Fatalf("gamma round trip = %q", enc)
	}
}

func TestCFBRoundTripInPlace(t *testing.T) {
	c := mustCipher(t, DefaultKey)
	for _, n := range []int{0, 1, 7, 8, 9, 40, 41} {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(i * 7)
		}
		buf := append([]byte(nil), src...)
		c.CFBEncrypt(DefaultSync, buf, buf)
		c.CFBDecrypt(DefaultSync, buf, buf)
		if !bytes.Equal(buf, src) {
			t.Fatalf("n=%d: round trip = %x, want %x", n, buf, src)
		}
	}
}

func TestPasswordKeyLength(t *testing.T) {
	if got := len(PasswordKey("")); got != KeySize {
		t.Fatalf("len = %d", got)
	}
}
