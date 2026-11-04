package records_test

import (
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
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

// TestOrderModifyMaskFields applies the mask branches to a stored order (I for
// the field mapping, status is O) and never aborts the poll.
func TestOrderModifyMaskFields(t *testing.T) {
	var w wire.Writer
	w.Int32(1) // order id
	mask := uint32(1 | 4 | 8 | 0x10 | 0x40 | 0x400)
	w.Raw([]byte{byte(mask >> 24), byte(mask >> 16), byte(mask >> 8), byte(mask)})
	w.Byte(2)      // status
	w.Int32(12345) // price
	w.Int32(7)     // quantity
	w.Int32(9)     // balance
	w.String("REF", 12)
	w.Byte(0) // special block flag
	mod, err := records.ParseOrderModify(w.Bytes())
	if err != nil {
		t.Fatalf("ParseOrderModify: %v", err)
	}
	order := model.Order{TransactionID: 1}
	mod.Apply(&order)
	if order.Status != 2 || order.Price.Units != 12345 || order.Quantity != 7 || order.Balance != 9 || order.BrokerRef != "REF" {
		t.Fatalf("applied order = %+v", order)
	}
}

// TestOrderParseShortBodies is the negative path.
func TestOrderParseShortBodies(t *testing.T) {
	if _, err := records.ParseOrderNew(nil); err == nil {
		t.Fatal("ParseOrderNew(nil) did not fail")
	}
	if _, err := records.ParseOrderNew([]byte{0x10}); err == nil {
		t.Fatal("ParseOrderNew(short) did not fail")
	}
	if _, err := records.ParseOrderModify(nil); err == nil {
		t.Fatal("ParseOrderModify(nil) did not fail")
	}
	if _, err := records.ParseOrderModify([]byte{0x10, 0x00, 0x00}); err == nil {
		t.Fatal("ParseOrderModify(short mask) did not fail")
	}
}
