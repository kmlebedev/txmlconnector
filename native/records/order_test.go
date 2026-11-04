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

// loadPlainText reads a plain text fixture of native/records/testdata.
func loadPlainText(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readOrderFixture(t *testing.T, name string) []byte {
	t.Helper()
	return readPlainHex(t, name)
}

// TestOrdersMatchDLL renders the O order fixtures byte for byte: the new order
// callback and the modified (status disabled + result text) callback.
func TestOrdersMatchDLL(t *testing.T) {
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	newPayload := readOrderFixture(t, "order-new-inject.hex")
	recs, err := records.Records(newPayload)
	if err != nil {
		t.Fatalf("Records new: %v", err)
	}
	if len(recs) != 1 || recs[0].Tag != records.TagOrderNew {
		t.Fatalf("new records = %+v", recs)
	}
	parsed, err := records.ParseOrderNew(recs[0].Body)
	if err != nil {
		t.Fatalf("ParseOrderNew: %v", err)
	}
	order := parsed.Order
	if sec, ok := catalog.Security(order.SecID); ok {
		order.SecCode = sec.SecCode
	}
	if board, ok := catalog.Board(parsed.BoardID); ok {
		order.Board = board.Code
	}
	got := xmlcompat.OrdersXML([]model.Order{order})
	want := strings.TrimSpace(string(loadPlainText(t, "order-new-dll.xml")))
	if got != want {
		t.Fatalf("new order\n got %s\nwant %s", got, want)
	}
	// Apply the 0x10 modification to the stored order.
	modPayload := readOrderFixture(t, "order-modify-inject.hex")
	modRecs, err := records.Records(modPayload)
	if err != nil {
		t.Fatalf("Records modify: %v", err)
	}
	if len(modRecs) != 2 || modRecs[0].Tag != records.TagOrderNew || modRecs[1].Tag != records.TagOrderModify {
		t.Fatalf("modify records = %+v", modRecs)
	}
	modNew, err := records.ParseOrderNew(modRecs[0].Body)
	if err != nil {
		t.Fatalf("ParseOrderNew modify: %v", err)
	}
	updated := modNew.Order
	if sec, ok := catalog.Security(updated.SecID); ok {
		updated.SecCode = sec.SecCode
	}
	if board, ok := catalog.Board(modNew.BoardID); ok {
		updated.Board = board.Code
	}
	mod, err := records.ParseOrderModify(modRecs[1].Body)
	if err != nil {
		t.Fatalf("ParseOrderModify: %v", err)
	}
	if mod.TransactionID != updated.TransactionID {
		t.Fatalf("modify target = %d, want %d", mod.TransactionID, updated.TransactionID)
	}
	mod.Apply(&updated)
	got = xmlcompat.OrdersXML([]model.Order{updated})
	want = strings.TrimSpace(string(loadPlainText(t, "order-modify-dll.xml")))
	if got != want {
		t.Fatalf("modified order\n got %s\nwant %s", got, want)
	}
}
