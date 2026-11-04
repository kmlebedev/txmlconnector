package records_test

import (
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// TestStopOrdersMatchDLL renders the O R_STOP fixture byte for byte.
func TestStopOrdersMatchDLL(t *testing.T) {
	raw, err := os.ReadFile("testdata/stoporder-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hexDecode(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 1 || recs[0].Tag != records.TagStopOrder {
		t.Fatalf("records = %+v", recs)
	}
	stop, err := records.ParseStopOrder(recs[0].Body)
	if err != nil {
		t.Fatalf("ParseStopOrder: %v", err)
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	ev := stop.Event(catalog)
	ev.Client = "TESTCLIEN"
	ev.Author = "TESTLOGIN"
	got := xmlcompat.StopOrdersXML([]model.StopOrder{ev})
	wantRaw, err := os.ReadFile("testdata/stoporder-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(string(wantRaw)); got != want {
		t.Fatalf("stop order\n got %s\nwant %s", got, want)
	}
}

// TestStopOrderNegatives: short bodies and unrecovered mask bits are explicit
// errors, not silent swallows.
func TestStopOrderNegatives(t *testing.T) {
	if _, err := records.ParseStopOrder(nil); err == nil {
		t.Fatal("ParseStopOrder(nil) did not fail")
	}
	// L id=1, packed L mask=0x80 (the unrecovered status block).
	if _, err := records.ParseStopOrder([]byte{0x10, 0x08, 0x08}); err == nil {
		t.Fatal("mask 0x80 was accepted")
	}
	// L id=1, packed L mask=0x10000 (a bit above 0x8000).
	if _, err := records.ParseStopOrder([]byte{0x10, 0x02, 0x00, 0x10}); err == nil {
		t.Fatal("mask above 0x8000 was accepted")
	}
}
