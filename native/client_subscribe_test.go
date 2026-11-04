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

// TestSubscribeQuotesMask checks the RQ_SUBSCRIBE masks of the r10 capture:
// quotes 0x02, alltrades+quotes 0x06, and the 0x00 unsubscribe form.
func TestSubscribeQuotesMask(t *testing.T) {
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
		{"quotes mask 0x02", func() error {
			return c.Subscribe(ctx, Subscription{Quotes: []model.Symbol{sber}})
		}, "001a002502"},
		{"alltrades+quotes mask 0x06", func() error {
			return c.Subscribe(ctx, Subscription{AllTrades: []model.Symbol{sber}})
		}, "001a002506"},
		{"unsubscribe quotes mask 0x04", func() error {
			return c.Unsubscribe(ctx, Subscription{Quotes: []model.Symbol{sber}})
		}, "001a002504"},
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

// TestConnectorQuotesNeedsConnection checks the S-nuance: a quotes subscription
// before the load is complete is rejected like the DLL, after the load it works.
func TestConnectorQuotesNeedsConnection(t *testing.T) {
	srv := newRunServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cmd := "<command id=" + quote + "subscribe" + quote + "><quotes><secid>26</secid></quotes></command>"
	if got := send(t, c, cmd); !strings.Contains(got, "without connection") {
		t.Fatalf("subscribe before connect = %s", got)
	}
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe after connect = %s", got)
	}
	payloads := subscribePayloads(srv)
	if got := payloads[len(payloads)-1]; got != "001a002502" {
		t.Fatalf("RQ_SUBSCRIBE = %s, want 001a002502", got)
	}
}

// TestConnectorQuotes is the e2e check: an R_QUOTES record fetched after the
// subscription reaches the Connector as a <quotes> callback.
func TestConnectorQuotes(t *testing.T) {
	srv := newRunServer(t)
	var w wire.Writer
	w.Byte(records.TagQuotes)
	w.Uint16(26) // SBER
	w.Uint16(37) // TQBR
	w.Byte('B')
	w.Int32(11203) // absolute price 112.03
	w.Int32(300)
	w.Byte(0xff)
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
	want := "<quotes><quote secid=" + quote + "26" + quote + "><board>TQBR</board>"
	want += "<seccode>SBER</seccode><price>112.03</price><yield>0</yield><buy>300</buy></quote></quotes>"
	if got := rec.next(t, "<quotes"); got != want {
		t.Fatalf("quotes = %s, want %s", got, want)
	}
}

// TestConnectorQuotationsCommand checks the XML <quotations> subscription
// matrix: mask 0x01, unsubscribe 0x00, combined alltrades+quotations mask 0x05,
// and the S r8 rule that only the first <quotations> block is accepted.
func TestConnectorQuotationsCommand(t *testing.T) {
	srv := newRunServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	success := "<result success=" + quote + "true" + quote + "/>"
	last := func() string {
		payloads := subscribePayloads(srv)
		if len(payloads) == 0 {
			t.Fatal("no RQ_SUBSCRIBE sent")
		}
		return payloads[len(payloads)-1]
	}
	steps := []struct {
		name string
		cmd  string
		want string
	}{
		{"subscribe quotations", "<command id=" + quote + "subscribe" + quote + "><quotations><secid>26</secid></quotations></command>", "001a002501"},
		{"unsubscribe quotations", "<command id=" + quote + "unsubscribe" + quote + "><quotations><secid>26</secid></quotations></command>", "001a002500"},
		{"combined alltrades+quotations", "<command id=" + quote + "subscribe" + quote + "><alltrades><security><board>TQBR</board><seccode>SBER</seccode></security></alltrades><quotations><secid>26</secid></quotations></command>", "001a002505"},
		{"first quotations block only", "<command id=" + quote + "subscribe" + quote + "><quotations><secid>379</secid></quotations><quotations><secid>26</secid></quotations><quotations><secid>20583</secid></quotations><quotations><secid>8326</secid></quotations></command>", "017b002501"},
	}
	for _, step := range steps {
		if got := send(t, c, step.cmd); got != success {
			t.Fatalf("%s: %s", step.name, got)
		}
		if got := last(); got != step.want {
			t.Fatalf("%s: RQ_SUBSCRIBE = %s, want %s", step.name, got, step.want)
		}
	}
}

// TestConnectorQuotesViaSubscription checks the <quotes> delivery path: mask
// 0x02 is sent for a <quotes> command (not alltrades) and the R_QUOTES record
// reaches the XML stream.
func TestConnectorQuotesViaSubscription(t *testing.T) {
	srv := newRunServer(t)
	var w wire.Writer
	w.Byte(records.TagQuotes)
	w.Uint16(26)
	w.Uint16(37)
	w.Byte('B')
	w.Int32(11203)
	w.Int32(300)
	w.Byte(0xff)
	srv.AfterSubscribe = w.Bytes()
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "subscribe" + quote + "><quotes><secid>26</secid></quotes></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe quotes = %s", got)
	}
	payloads := subscribePayloads(srv)
	if got := payloads[len(payloads)-1]; got != "001a002502" {
		t.Fatalf("RQ_SUBSCRIBE = %s, want 001a002502", got)
	}
	want := "<quotes><quote secid=" + quote + "26" + quote + "><board>TQBR</board>"
	want += "<seccode>SBER</seccode><price>112.03</price><yield>0</yield><buy>300</buy></quote></quotes>"
	if got := rec.next(t, "<quotes"); got != want {
		t.Fatalf("quotes = %s, want %s", got, want)
	}
}

// TestConnectorQuotesFirstElementOnly: several <quotes> blocks subscribe only
// the first one (by analogy with <quotations>; S is proven for one block only).
func TestConnectorQuotesFirstElementOnly(t *testing.T) {
	srv := newRunServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "subscribe" + quote + ">"
	cmd += "<quotes><secid>26</secid></quotes><quotes><secid>379</secid></quotes></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe = %s", got)
	}
	payloads := subscribePayloads(srv)
	if got := payloads[len(payloads)-1]; got != "001a002502" {
		t.Fatalf("RQ_SUBSCRIBE = %s, want only the first block 001a002502", got)
	}
}
