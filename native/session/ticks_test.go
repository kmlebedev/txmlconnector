package session

import (
	"encoding/hex"
	"testing"
)

// TestTicksPayloadMatchesDLL pins the RQ_SUBSCRIBE_TICKS payloads of the live
// r15/r16 captures (S): B filter then {W secid, W board, G tradeno} per item.
func TestTicksPayloadMatchesDLL(t *testing.T) {
	cases := []struct {
		name   string
		filter bool
		items  []TickItem
		want   string
	}{
		{"one", true, []TickItem{{SecID: 26, Board: 37, TradeNo: 0}}, "01001a002500"},
		{"three", false, []TickItem{
			{SecID: 26, Board: 37, TradeNo: 0},
			{SecID: 379, Board: 37, TradeNo: 0},
			{SecID: 20583, Board: 42, TradeNo: 0},
		}, "00001a002500017b0025005067002a00"},
	}
	for _, c := range cases {
		if got := hex.EncodeToString(TicksPayload(c.filter, c.items)); got != c.want {
			t.Fatalf("%s: payload %s, want %s", c.name, got, c.want)
		}
	}
}
