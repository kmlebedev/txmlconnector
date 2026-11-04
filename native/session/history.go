package session

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// History request types (PDF 3.7): RQ_HISTORY_EXT is the one the DLL sends live.
const (
	RqHistory    byte = 11
	RqHistoryExt byte = 148
)

// HistoryExtPayload builds the RQ_HISTORY_EXT body the DLL sends (live payloads
// 01 001a 0025 01 000a 18 00 for count 10 and ...03e8... for count 1000):
// B reset, W secid, W board, B period, W count, L -1, L 0.
func HistoryExtPayload(reset bool, secID, board uint16, period byte, count uint16) []byte {
	var w wire.Writer
	if reset {
		w.Byte(1)
	} else {
		w.Byte(0)
	}
	w.Uint16(secID)
	w.Uint16(board)
	w.Byte(period)
	w.Uint16(count)
	w.Int32(-1)
	w.Int32(0)
	return w.Bytes()
}

// HistoryExt sends RQ_HISTORY_EXT; the server replies with an empty payload and
// streams the candles through RQ_MAIN (S).
func (c *Conn) HistoryExt(ctx context.Context, payload []byte) error {
	if _, err := c.Request(ctx, RqHistoryExt, payload); err != nil {
		return fmt.Errorf("RQ_HISTORY_EXT: %w", err)
	}
	return nil
}
