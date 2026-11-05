package native

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestConnectorSubscribeTicks checks the XML command: the D/S payload form
// (filter + security/tradeno), the mask-0 form without securities and the
// "Cannot find tradeno element." error of the DLL.
func TestConnectorSubscribeTicks(t *testing.T) {
	srv := newRunServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "subscribe_ticks" + quote + "><filter>true</filter>"
	cmd += "<security><board>TQBR</board><seccode>SBER</seccode><tradeno>0</tradeno></security></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe_ticks = %s", got)
	}
	var payloads []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqSubscribeTicks {
			payloads = append(payloads, hex.EncodeToString(f.Payload))
		}
	}
	if len(payloads) != 1 || payloads[0] != "01001a002500" {
		t.Fatalf("RQ_SUBSCRIBE_TICKS payloads = %v, want [01001a002500]", payloads)
	}
	// No <security> elements: the DLL sends the filter byte only (mask 0).
	empty := "<command id=" + quote + "subscribe_ticks" + quote + "><filter>false</filter></command>"
	if got := send(t, c, empty); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("empty subscribe_ticks = %s", got)
	}
	// Missing <tradeno> is the DLL error.
	bad := "<command id=" + quote + "subscribe_ticks" + quote + "><filter>true</filter>"
	bad += "<security><board>TQBR</board><seccode>SBER</seccode></security></command>"
	if got := send(t, c, bad); !strings.Contains(got, "Cannot find tradeno element.") {
		t.Fatalf("missing tradeno = %s", got)
	}
}

func syntheticTicksReply(t *testing.T) []byte {
	t.Helper()
	var w wire.Writer
	w.Uint16(26)
	w.Uint16(37)
	w.Int64(0)
	w.Int32(29896980)
	w.Int32(291420)
	w.Byte(1)
	w.Int32(10)
	w.Uint16(26)
	w.Uint16(37)
	w.Int64(1)
	w.Int32(1)
	w.Int32(10)
	w.Byte(1)
	w.Int32(20)
	w.Uint16(0xffff)
	return w.Bytes()
}

// TestPollTicks checks the RQ_TICKS direct reply-frame path (D format, I
// semantics until the live capture): the synthetic reply is parsed and typed.
func TestPollTicks(t *testing.T) {
	srv := newRunServer(t)
	srv.TicksReply = syntheticTicksReply(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	ticks, err := c.PollTicks(ctx, true, TickRequest{Symbol: sber, TradeNo: 0})
	if err != nil {
		t.Fatalf("PollTicks: %v", err)
	}
	if len(ticks) != 2 {
		t.Fatalf("ticks = %d, want 2", len(ticks))
	}
	if ticks[0].Symbol != sber || ticks[0].TradeNo != 0 || ticks[0].Quantity != 10 {
		t.Fatalf("tick0 = %+v", ticks[0])
	}
	if ticks[1].TradeNo != 1 || ticks[1].Price.Units != 291430 || ticks[1].Price.Scale != 2 {
		t.Fatalf("tick1 = %+v (accumulated tradeno/price, I)", ticks[1])
	}
	if got := ticks[0].Time.Format("2006-01-02T15:04:05Z"); got != "1970-12-12T22:16:20Z" {
		t.Logf("synthetic tick time = %s (wire time units are I until the live capture)", got)
	}
}
