package native

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// decoder turns one RQ_MAIN record body into events.
type decoder func(c *Client, body []byte) ([]model.Event, error)

// decoders holds the RQ_MAIN record decoders by tag.
//
// Convention for the record families of tasks 6-28: each family adds its
// decoder from init() in its own events_<family>.go file and never edits this
// file, so the families do not conflict and the poll loop stays untouched. A
// record tag without a decoder yields no event; its catalog mutations are
// still applied by Poll.
var decoders = map[byte]decoder{}

// registerDecoder adds the decoder of a RQ_MAIN record tag.
func registerDecoder(tag byte, d decoder) {
	if decoders[tag] != nil {
		panic(fmt.Sprintf("native: duplicate decoder for record tag %#02x", tag))
	}
	decoders[tag] = d
}
