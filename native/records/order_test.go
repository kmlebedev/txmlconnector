package records_test

import (
	"encoding/hex"
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
	// The DLL renders <client> from the client store by the numeric id, not
	// from the Z12 string (O: mutating the string changes nothing).
	order.Client = "TESTCLIEN"
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
	updated.Client = "TESTCLIEN" // from the client store, as above
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
	// Short modify bodies are skipped (Short=true) instead of aborting the
	// live load; malformed bodies may also keep the old error.
	if out, err := records.ParseOrderModify(nil); err == nil && !out.Short {
		t.Fatal("ParseOrderModify(nil) neither failed nor marked Short")
	}
	if out, err := records.ParseOrderModify([]byte{0x10, 0x00, 0x00}); err == nil && !out.Short {
		t.Fatal("ParseOrderModify(short mask) neither failed nor marked Short")
	}
}

// TestOrderShortCancelledLive: the cancelled live body (status 9, mask 0x116)
// used to be skipped as Short by the linear 52-read parser; the mask-driven
// D layout reads it completely (TestOrderFinalCancelledLiveMatchesDLL).
func TestOrderShortCancelledLive(t *testing.T) {
	raw, err := os.ReadFile("testdata/order-short-cancelled-live.hex")
	if err != nil {
		t.Fatal(err)
	}
	body, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := records.ParseOrderNew(body)
	if err != nil {
		t.Fatalf("ParseOrderNew short: %v", err)
	}
	if out.Short || out.Order.Status != 9 {
		t.Fatalf("cancelled order = %+v", out)
	}
}

// TestOrderFinalCancelledLiveMatchesDLL: a cancelled order of the load
// snapshot (S: demo, 2026-11-05) is a full R_ORD_NEW body whose W mask (0x116)
// selects value/settlecode (2), withdrawtime (4), the 0x10 pair and the 0x100
// words (D: ReadOrderBody FUN_18039b390). The DLL renders it as
// order-final-cancelled-dll.xml (S: the same order on the same session).
func TestOrderFinalCancelledLiveMatchesDLL(t *testing.T) {
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	body, err := hex.DecodeString(strings.TrimSpace(string(loadPlainText(t, "order-short-cancelled-live.hex"))))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := records.ParseOrderNew(body)
	if err != nil {
		t.Fatalf("ParseOrderNew: %v", err)
	}
	if parsed.Short {
		t.Fatal("cancelled order body must parse fully")
	}
	o := parsed.Order
	if o.Status != 9 || o.OrderNo != 9480158 || o.Quantity != 1 || o.Balance != 1 ||
		o.Value != (model.Decimal{Units: 28462, Scale: 2}) || o.SettleCode != "Y1" || o.BrokerRef != "" ||
		o.Time != 1793873559 || o.WithdrawTime != 1793873564 || o.Condition != 0 || parsed.ClientID != 4096 {
		t.Fatalf("order = %+v client id %d", o, parsed.ClientID)
	}
	if sec, ok := catalog.Security(o.SecID); ok {
		o.SecCode = sec.SecCode
	}
	if board, ok := catalog.Board(parsed.BoardID); ok {
		o.Board = board.Code
	}
	o.Client, o.Union = "TESTCLIENT", "TESTUNION" // the DLL takes them from the client store (O)
	got := xmlcompat.OrdersXML([]model.Order{o})
	want := strings.TrimSpace(string(loadPlainText(t, "order-final-cancelled-dll.xml")))
	if got != want {
		t.Fatalf("cancelled order\n got %s\nwant %s", got, want)
	}
}

// TestOrderStatusNames: the DLL status enum (O: oracle injection of statuses
// 0..15 into the cancelled live body, 2026-11-05).
func TestOrderStatusNames(t *testing.T) {
	want := map[int]string{0: "none", 1: "watching", 2: "disabled", 3: "expired", 5: "forwarding",
		7: "failed", 8: "active", 9: "cancelled", 10: "removed", 11: "matched", 12: "refused",
		13: "wait", 14: "inactive", 15: "erorr: unknown status"}
	for status, name := range want {
		got := xmlcompat.OrderXML(model.Order{Status: status})
		if !strings.Contains(got, "<status>"+name+"</status>") {
			t.Errorf("status %d: %s", status, got)
		}
	}
}
