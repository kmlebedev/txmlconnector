package session

import (
	"encoding/hex"
	"testing"
)

// TestHistoryPayloadMatchesDLL checks the RQ_HISTORY_EXT payloads of the live
// captures: count 10 -> 01 001a 0025 01 000a 18 00, count 1000 -> ...03e8....
func TestHistoryPayloadMatchesDLL(t *testing.T) {
	cases := []struct {
		count int
		hex   string
	}{
		{10, "01001a002501000a1800"},
		{1000, "01001a00250103e81800"},
	}
	for _, c := range cases {
		got := HistoryExtPayload(true, 26, 37, 1, uint16(c.count))
		if hex.EncodeToString(got) != c.hex {
			t.Fatalf("count %d: payload %x, want %s", c.count, got, c.hex)
		}
	}
}
