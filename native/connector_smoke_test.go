package native

import (
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// syntheticStopOrder builds one R_STOP record body with a known client id:
// L id, L mask=0xF00 (secid, board, client, buysell), then those fields.
func syntheticStopOrder(t *testing.T, clientID int64) []byte {
	t.Helper()
	var w wire.Writer
	w.Int32(7)
	w.Int32(0xF00)
	w.Uint16(26)
	w.Uint16(37)
	w.Int32(int32(clientID))
	w.Byte('B')
	body := w.Bytes()
	out := []byte{records.TagStopOrder, byte(len(body) >> 8), byte(len(body))}
	return append(out, body...)
}

// TestConnectorSmokeImplementedCallbacks is a regression smoke across the
// accepted command families: orders, trades, stop orders, limits and the local
// portfolio/marketord renders. It does not add features; it only checks that the
// Connector still routes one flow of each family end-to-end.
func TestConnectorSmokeImplementedCallbacks(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), readOrderFixture(t, "margin-inject.hex")...)
	// RQ_CLIENT_LIMITS fake reply (same shape as TestClientLimitsCommand).
	srv.Handle(session.RqClientLimits, func(payload []byte) (byte, []byte) {
		var limits wire.Writer
		limits.Raw(payload) // client id, as sent
		limits.Int64(100)
		limits.Int64(200)
		limits.Int64(300)
		limits.Int64(400)
		limits.Uint16(7)
		limits.Int32(1234567)
		for i := 0; i < 15; i++ {
			limits.Int64(int64((i + 1) * 100))
		}
		limits.Int32(-1)
		return wire.KindReply | wire.FlagPlain, limits.Bytes()
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	clientID := snapshotClientID(t)

	// Local portfolio render from the loaded money store.
	if got := send(t, c, "<command id="+quote+"get_portfolio"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_portfolio = %s", got)
	}
	if got := rec.next(t, "<portfolio_currency"); !strings.Contains(got, "currency="+quote+"RUB"+quote) {
		t.Fatalf("portfolio = %s", got)
	}

	// Orders (0x03), trades (0x04) and stop orders (0x54) event families.
	srv.QueueMain(patchOrderClient(t, readOrderFixture(t, "order-new-inject.hex"), clientID))
	if got := rec.next(t, "<orders"); !strings.Contains(got, "<status>watching</status>") {
		t.Fatalf("orders = %s", got)
	}
	srv.QueueMain(patchTradeClient(t, readOrderFixture(t, "trade-inject.hex"), clientID))
	if got := rec.next(t, "<trades"); !strings.Contains(got, "<quantity>4444</quantity>") {
		t.Fatalf("trades = %s", got)
	}
	srv.QueueMain(syntheticStopOrder(t, clientID))
	// The DLL wraps stop orders in <orders> (same envelope as 0x03/0x10).
	if got := rec.next(t, "<orders"); !strings.Contains(got, "<stoporder transactionid="+quote+"7"+quote+">") || !strings.Contains(got, "<buysell>B</buysell>") {
		t.Fatalf("stoporders = %s", got)
	}

	// Limits command round-trip.
	if got := send(t, c, "<command id="+quote+"get_client_limits"+quote+" client="+quote+"TESTCLIEN"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_client_limits = %s", got)
	}
	if got := rec.next(t, "<clientlimits"); !strings.Contains(got, "<clientlimits client="+quote+"TESTCLIEN"+quote+">") {
		t.Fatalf("clientlimits = %s", got)
	}

	// 0x0e <marketord> event still reaches the XML stream.
	srv.QueueMain([]byte{0x0e, 0x00, 0x1a, 0x01})
	if got := rec.next(t, "<marketord"); !strings.Contains(got, "permit="+quote+"yes"+quote) {
		t.Fatalf("marketord = %s", got)
	}
}
