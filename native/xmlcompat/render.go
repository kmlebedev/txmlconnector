package xmlcompat

import (
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// Options selects the optional forms of the DLL output.
type Options struct {
	// Milliseconds renders <time> with the .fff suffix, as a connection opened
	// with <milliseconds>true</milliseconds> does.
	Milliseconds bool
}

// Render returns the XML message the DLL emits for an event, and false when the
// event has no XML form. It is the single entry point of the XML layer: each
// record family adds its case here on top of its decoder in
// native/events_<family>.go (tasks 6-28).
func Render(c *records.Catalog, ev model.Event, opt Options) (string, bool) {
	switch e := ev.(type) {
	case *model.Trade:
		return AllTradesXML([]model.Trade{*e}, opt.Milliseconds), true
	}
	return "", false
}
