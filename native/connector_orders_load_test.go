package native

import (
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// liveCancelledOrder is the cancelled order of the demo load snapshot
// (records/testdata/order-short-cancelled-live.hex, two-byte order id) with its
// numeric client id replaced by clientID.
func liveCancelledOrder(t *testing.T, clientID int64) records.Record {
	t.Helper()
	body := readOrderFixture(t, "order-short-cancelled-live.hex")
	r := wire.NewReader(body)
	if _, err := r.Int32(); err != nil { // order id
		t.Fatal(err)
	}
	if err := r.Skip(4); err != nil { // W secid, W board
		t.Fatal(err)
	}
	head := len(body) - r.Len()
	if _, err := r.Int64(); err != nil { // G client id
		t.Fatal(err)
	}
	tail := body[len(body)-r.Len():]
	var w wire.Writer
	w.Int64(clientID)
	patched := append(append(append([]byte(nil), body[:head]...), w.Bytes()...), tail...)
	return records.Record{Tag: records.TagOrderNew, Body: patched}
}

// A8: an order of the day in a final state arrives in the load snapshot as a
// full R_ORD_NEW body; the DLL reports it with the end-of-load callbacks (S:
// demo, 4 cancelled orders in one <orders> before <server_status>). The
// connector used to drop it as a short body.
func TestConnectorLoadReportsFinalOrders(t *testing.T) {
	srv := newTestServer(t)
	snapshot := clientSnapshot(t)
	rec := liveCancelledOrder(t, snapshotClientID(t))
	snapshot = append(snapshot, rec.Tag, byte(len(rec.Body)>>8), byte(len(rec.Body)))
	snapshot = append(snapshot, rec.Body...)
	srv.Snapshot = snapshot
	c := New(Config{Dial: srv.Dial})
	rec2 := newRecorder()
	if err := c.Start(rec2.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	orders := rec2.next(t, "<orders>")
	for _, want := range []string{
		`<order transactionid="60">`, `<orderno>9480158</orderno>`, `<status>cancelled</status>`,
		`<time>05.11.2026 10:12:39</time>`, `<value>284.62</value>`, `<settlecode>Y1</settlecode>`,
		`<withdrawtime>05.11.2026 10:12:44</withdrawtime>`, `<condition>None</condition>`,
		`<within_pos>false</within_pos>`,
	} {
		if !strings.Contains(orders, want) {
			t.Fatalf("orders lacks %s:\n%s", want, orders)
		}
	}
	if strings.Contains(orders, "<accepttime>") {
		t.Fatalf("a non-conditional order has no <accepttime>: %s", orders)
	}
	rec2.next(t, `<server_status`)
	// DLL order at the end of the load: <client>, one <orders>, <positions>.
	rec2.mu.Lock()
	defer rec2.mu.Unlock()
	idx := map[string]int{}
	count := 0
	for i, m := range rec2.msgs {
		for _, p := range []string{"<client ", "<orders>", "<positions>"} {
			if strings.HasPrefix(m, p) {
				if _, seen := idx[p]; !seen {
					idx[p] = i
				}
				if p == "<orders>" {
					count++
				}
			}
		}
	}
	if count != 1 || idx["<client "] > idx["<orders>"] {
		t.Fatalf("load callbacks: %d <orders>, positions %v", count, idx)
	}
	if p, ok := idx["<positions>"]; ok && p < idx["<orders>"] {
		t.Fatalf("<orders> must precede <positions>: %v", idx)
	}
}
