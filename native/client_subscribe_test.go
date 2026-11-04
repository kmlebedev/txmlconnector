package native

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestSubscribeMasksMatchDLL checks the RQ_SUBSCRIBE masks of the live capture
// (r8): quotations 0x01, alltrades+quotations 0x05, unsubscribe 0x00, sent as
// W secid, W board, B mask.
func TestSubscribeMasksMatchDLL(t *testing.T) {
	srv := newRunServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	steps := []struct {
		name string
		run  func() error
		want string
	}{
		{"quotations mask 0x01", func() error {
			return c.Subscribe(ctx, Subscription{Quotations: []model.Symbol{sber}})
		}, "001a002501"},
		{"alltrades+quotations mask 0x05", func() error {
			return c.Subscribe(ctx, Subscription{AllTrades: []model.Symbol{sber}})
		}, "001a002505"},
		{"unsubscribe quotations mask 0x04", func() error {
			return c.Unsubscribe(ctx, Subscription{Quotations: []model.Symbol{sber}})
		}, "001a002504"},
		{"unsubscribe alltrades mask 0x00", func() error {
			return c.UnsubscribeAllTrades(ctx, sber)
		}, "001a002500"},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		payloads := subscribePayloads(srv)
		if got := payloads[len(payloads)-1]; got != step.want {
			t.Fatalf("%s: RQ_SUBSCRIBE = %s, want %s", step.name, got, step.want)
		}
	}
}

// TestConnectorQuotationsFirstElementOnly checks the S-rule that the DLL takes
// only the first <quotations> element of one XML command.
func TestConnectorQuotationsFirstElementOnly(t *testing.T) {
	srv := newRunServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "subscribe" + quote + "><quotations>"
	cmd += "<secid>26</secid><secid>379</secid></quotations></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe = %s", got)
	}
	payloads := subscribePayloads(srv)
	if len(payloads) != 1 || payloads[0] != "001a002501" {
		t.Fatalf("RQ_SUBSCRIBE payloads = %v, want [001a002501] (first element only)", payloads)
	}
}

// TestConnectorQuotation checks the XML path: a R_QUOTATION record after the
// subscription reaches the Connector handler as a <quotations> callback.
func TestConnectorQuotation(t *testing.T) {
	srv := newRunServer(t)
	var w wire.Writer
	w.Byte(records.TagQuotation)
	w.Uint16(26) // SBER
	w.Uint16(37) // TQBR board
	w.Int64(1<<0 | 1<<33)
	w.Int32(300) // biddeptht
	w.Int32(65)  // status 'A'
	srv.AfterSubscribe = w.Bytes()
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	sub := "<command id=" + quote + "subscribe" + quote + "><alltrades>"
	sub += "<security><board>TQBR</board><seccode>SBER</seccode></security></alltrades></command>"
	if got := send(t, c, sub); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe = %s", got)
	}
	msg := rec.next(t, "<quotations")
	for _, want := range []string{
		"<quotation secid=" + quote + "26" + quote + ">",
		"<board>TQBR</board><seccode>SBER</seccode>",
		"<biddeptht>300</biddeptht>",
		"<status>A</status>",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("quotation = %s, want substring %s", msg, want)
		}
	}
}
