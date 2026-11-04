package records_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

func readTradeFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/trade-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hexDecode(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("trade fixture: %v", err)
	}
	return payload
}

func hexDecode(s string) ([]byte, error) {
	dec := make([]byte, len(s)/2)
	for i := 0; i < len(dec); i++ {
		hi, ok1 := hexNibble(s[2*i])
		lo, ok2 := hexNibble(s[2*i+1])
		if !ok1 || !ok2 {
			return nil, errors.New("bad hex")
		}
		dec[i] = hi<<4 | lo
	}
	return dec, nil
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// TestClientTradesMatchDLL renders the O R_TRD fixture byte for byte against the
// DLL <trades> callback.
func TestClientTradesMatchDLL(t *testing.T) {
	payload := readTradeFixture(t)
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 1 || recs[0].Tag != records.TagClientTrade {
		t.Fatalf("records = %+v", recs)
	}
	trade, err := records.ParseClientTrade(recs[0].Body)
	if err != nil {
		t.Fatalf("ParseClientTrade: %v", err)
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	ev := trade.Event(catalog)
	ev.Client = "TESTCLIEN" // the decoder maps the numeric id through the account store
	got := xmlcompat.TradesXML([]model.ClientTrade{ev})
	wantRaw, err := os.ReadFile("testdata/trade-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(wantRaw))
	if got != want {
		t.Fatalf("trade\n got %s\nwant %s", got, want)
	}
}

// TestClientTradeShortBody is the negative path.
func TestClientTradeShortBody(t *testing.T) {
	if _, err := records.ParseClientTrade(nil); !errors.Is(err, wire.ErrShortBuffer) {
		t.Fatalf("ParseClientTrade(nil) = %v, want ErrShortBuffer", err)
	}
	if _, err := records.ParseClientTrade([]byte{0x00, 0x1a}); !errors.Is(err, wire.ErrShortBuffer) {
		t.Fatalf("ParseClientTrade(short) = %v, want ErrShortBuffer", err)
	}
}
