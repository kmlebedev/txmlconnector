package session

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// RqClientLimits is RQ_CLIENT_LIMITS (43). The request payload is one packed
// L client_id (limits.md, D+O); the reply is the limits stream.
const RqClientLimits byte = 43

// ClientLimitsRequest builds the RQ_CLIENT_LIMITS payload.
func ClientLimitsRequest(clientID int64) []byte {
	var w wire.Writer
	w.Int32(int32(clientID))
	return w.Bytes()
}

// ClientLimits sends RQ_CLIENT_LIMITS and returns the reply payload.
func (c *Conn) ClientLimits(ctx context.Context, clientID int64) ([]byte, error) {
	payload, err := c.Request(ctx, RqClientLimits, ClientLimitsRequest(clientID))
	if err != nil {
		return nil, fmt.Errorf("RQ_CLIENT_LIMITS: %w", err)
	}
	return payload, nil
}
