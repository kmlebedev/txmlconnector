package session

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Tick feed request types (PDF 3.19): RQ_SUBSCRIBE_TICKS toggles the feed,
// RQ_TICKS is a direct reply-frame poll (docs/protocol/history.md).
const (
	RqSubscribeTicks byte = 120
	RqTicks          byte = 13
)

// TickItem is one {W secid, W board, G tradeno} element of a tick request.
type TickItem struct {
	SecID   uint16
	Board   uint16
	TradeNo int64
}

// TicksPayload builds B filter; { W secid; W board; G tradeno }* (S for
// RQ_SUBSCRIBE_TICKS, D for the shared RQ_TICKS builder). The caller passes the
// items in wire order; the DLL writes its list in reverse XML order.
func TicksPayload(filter bool, items []TickItem) []byte {
	var w wire.Writer
	if filter {
		w.Byte(1)
	} else {
		w.Byte(0)
	}
	for _, item := range items {
		w.Uint16(item.SecID)
		w.Uint16(item.Board)
		w.Int64(item.TradeNo)
	}
	return w.Bytes()
}

// SubscribeTicks sends RQ_SUBSCRIBE_TICKS and expects an empty reply.
func (c *Conn) SubscribeTicks(ctx context.Context, payload []byte) error {
	if _, err := c.Request(ctx, RqSubscribeTicks, payload); err != nil {
		return fmt.Errorf("RQ_SUBSCRIBE_TICKS: %w", err)
	}
	return nil
}

// PollTicks sends RQ_TICKS and returns its reply payload (parsed by
// native/records).
func (c *Conn) PollTicks(ctx context.Context, payload []byte) ([]byte, error) {
	reply, err := c.Request(ctx, RqTicks, payload)
	if err != nil {
		return nil, fmt.Errorf("RQ_TICKS: %w", err)
	}
	return reply, nil
}
