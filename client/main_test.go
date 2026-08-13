package tcClient

import (
	"errors"
	"testing"

	"github.com/kmlebedev/txmlconnector/client/commands"
)

func TestHandleMessageRoutesQuotes(t *testing.T) {
	tc := TCClient{QuotesChan: make(chan commands.Quotes, 1)}
	if err := tc.handleMessage(`<quotes><quote secid="42"><price>123.45</price></quote></quotes>`); err != nil {
		t.Fatal(err)
	}
	quotes := <-tc.QuotesChan
	if len(quotes.Items) != 1 || quotes.Items[0].SecId != 42 || quotes.Items[0].Price != 123.45 {
		t.Fatalf("quotes = %+v", quotes)
	}
	if quotes.Time.IsZero() {
		t.Fatal("quotes timestamp was not set")
	}
}

func TestHandleMessageStoresPositionsAndNotifies(t *testing.T) {
	tc := TCClient{ResponseChannel: make(chan string, 1)}
	if err := tc.handleMessage(`<positions></positions>`); err != nil {
		t.Fatal(err)
	}
	if tc.Data.Positions == nil {
		t.Fatal("positions were not stored")
	}
	if got := <-tc.ResponseChannel; got != "positions" {
		t.Fatalf("notification = %q", got)
	}
}

func TestHandleMessageRejectsMalformedAndUnknownXML(t *testing.T) {
	tc := TCClient{}
	if err := tc.handleMessage(`<quotes>`); err == nil {
		t.Fatal("malformed XML was accepted")
	}
	if err := tc.handleMessage(`<new_message/>`); !errors.Is(err, errUnknownMessage) {
		t.Fatalf("unknown message error = %v", err)
	}
}
