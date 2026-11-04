package session

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// SecSpecRequest builds RQ_SEC_SPEC (15): a plain list of W secids, no counter
// or terminator (RE-1 FUN_1803bac90; S: the r12 batch probe payload 5067017b001a
// is the DLL's reverse order of the XML subscription elements).
func SecSpecRequest(secIDs []uint16) []byte {
	var w wire.Writer
	for _, id := range secIDs {
		w.Uint16(id)
	}
	return w.Bytes()
}

// SecSpec sends RQ_SEC_SPEC and returns the reply payload (the spec records are
// parsed by native/records).
func (c *Conn) SecSpec(ctx context.Context, secIDs []uint16) ([]byte, error) {
	if len(secIDs) == 0 {
		return nil, nil
	}
	payload, err := c.Request(ctx, RqSecSpec, SecSpecRequest(secIDs))
	if err != nil {
		return nil, fmt.Errorf("RQ_SEC_SPEC: %w", err)
	}
	return payload, nil
}
