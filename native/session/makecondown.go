package session

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// RqMakeCondOwn is RQ_MAKE_COND_OWN, the request behind makeordown.
const RqMakeCondOwn byte = 112

// ServerTextError is a non-empty C-string reply: the server's CP1251 text.
type ServerTextError struct{ Message string }

func (e *ServerTextError) Error() string { return e.Message }

// MakeCondOwn sends RQ_MAKE_COND_OWN (112): the payload is the packed
// transaction id (D: FUN_1803861e0; O: "10" for id 1). The reply is a C-string
// of up to 0x200 bytes; an empty reply or string is success (O: the DLL answers
// success="true" to an empty reply).
func (c *Conn) MakeCondOwn(ctx context.Context, transactionID int32) error {
	var w wire.Writer
	w.Int32(transactionID)
	reply, err := c.Request(ctx, RqMakeCondOwn, w.Bytes())
	if err != nil {
		return fmt.Errorf("RQ_MAKE_COND_OWN: %w", err)
	}
	if len(reply) == 0 {
		return nil
	}
	text, err := wire.NewReader(reply).String(0x200)
	if err != nil {
		return fmt.Errorf("RQ_MAKE_COND_OWN invalid buffer: %w", err)
	}
	if text != "" {
		return &ServerTextError{Message: text}
	}
	return nil
}
