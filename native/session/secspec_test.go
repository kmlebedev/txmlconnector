package session

import (
	"encoding/hex"
	"os"
	"testing"
)

// TestSecSpecPayloadMatchesDLL checks the RQ_SEC_SPEC request of the r12 batch
// probe: a plain W secid list, no counter or terminator (S).
func TestSecSpecPayloadMatchesDLL(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/secspec-request.hex")
	if err != nil {
		t.Fatal(err)
	}
	want := string(fixture)
	want = want[:len(want)-1]
	// The DLL sends the list in the reverse order of the XML elements.
	if got := hex.EncodeToString(SecSpecRequest([]uint16{20583, 379, 26})); got != want {
		t.Fatalf("SecSpecRequest = %s, want %s", got, want)
	}
}
