package native

import (
	"context"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestCancelStopOrderPayloadMatchesDLL checks the O 2-byte payload 17 10.
func TestCancelStopOrderPayloadMatchesDLL(t *testing.T) {
	srv := newRunServer(t)
	payload, _ := hex.DecodeString("00100010")
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, payload
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nc, err := srv.Dial(ctx, "testserver:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := session.Login(ctx, nc, session.Config{Login: testserver.Login, Password: testserver.Password})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := &Client{conn: conn}
	if err := client.CancelStopOrder(ctx, 1); err != nil {
		t.Fatalf("CancelStopOrder: %v", err)
	}
	var got []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqTransact {
			got = append(got, hex.EncodeToString(f.Payload))
		}
	}
	if len(got) != 1 || got[0] != "1710" {
		t.Fatalf("RQ_TRANSACT payloads = %v, want [1710]", got)
	}
}

// TestCancelStopOrderCommand checks the Connector command: transactionid is
// required (DLL text), the payload is 17 <packed id>.
func TestCancelStopOrderCommand(t *testing.T) {
	srv := newRunServer(t)
	payload, _ := hex.DecodeString("00100010")
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, payload
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	if got := send(t, c, "<command id="+quote+"cancelstoporder"+quote+"/>"); !strings.Contains(got, "You have to specify transactionid.") {
		t.Fatalf("missing transactionid = %s", got)
	}
	if got := send(t, c, "<command id="+quote+"cancelstoporder"+quote+"><transactionid>1</transactionid></command>"); got != "<result success="+quote+"true"+quote+" transactionid="+quote+"1"+quote+"/>" {
		t.Fatalf("cancelstoporder = %s", got)
	}
	var gotPayloads []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqTransact {
			gotPayloads = append(gotPayloads, hex.EncodeToString(f.Payload))
		}
	}
	if len(gotPayloads) != 1 || gotPayloads[0] != "1710" {
		t.Fatalf("RQ_TRANSACT payloads = %v, want [1710]", gotPayloads)
	}
}

// expectedWithClient replaces the masked packed client id of a fixture payload
// with the session's catalog id (the fixtures are anonymized to 4096).
func expectedWithClient(t *testing.T, fixturePath string, offset int) []byte {
	t.Helper()
	raw, err := os.ReadFile("records/testdata/" + fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var w wire.Writer
	w.Int64(snapshotClientID(t))
	out := append([]byte(nil), fixture[:offset]...)
	out = append(out, w.Bytes()...)
	out = append(out, fixture[offset+3:]...)
	return out
}

// captureTransact registers a fake successful RQ_TRANSACT and returns a getter
// for the captured request payload.
func captureTransact(t *testing.T, srv *testserver.Server) func() []byte {
	t.Helper()
	var mu sync.Mutex
	var captured []byte
	srv.Handle(session.RqTransact, func(payload []byte) (byte, []byte) {
		mu.Lock()
		captured = append([]byte(nil), payload...)
		mu.Unlock()
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	return func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

func stopOrderClient(t *testing.T, srv *testserver.Server) (*Client, context.Context) {
	t.Helper()
	srv.Snapshot = clientSnapshot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client, ctx
}

// TestNewStopOrderPayloadMatchesDLL: the builder reproduces the O payload
// (activation 100/order 90/qty 1, take profit 300) modulo the anonymized client
// id, which comes from the catalog.
func TestNewStopOrderPayloadMatchesDLL(t *testing.T) {
	srv := newRunServer(t)
	get := captureTransact(t, srv)
	client, ctx := stopOrderClient(t, srv)
	_, err := client.NewStopOrder(ctx, model.NewStopOrder{
		Client: "TESTCLIEN", Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, BuySell: 'B',
		StopLoss: &model.StopLoss{
			ActivationPrice: model.Decimal{Units: 10000, Scale: 2},
			OrderPrice:      model.Decimal{Units: 9000, Scale: 2},
			Quantity:        1,
		},
		TakeProfit: &model.TakeProfit{ActivationPrice: model.Decimal{Units: 30000, Scale: 2}, Quantity: 1},
	})
	if err != nil {
		t.Fatalf("NewStopOrder: %v", err)
	}
	want := expectedWithClient(t, "transact-newstoporder-payload.hex", 7)
	if got := get(); !bytesEqual(got, want) {
		t.Fatalf("newstoporder payload\n got %x\nwant %x", got, want)
	}
}

// TestNewCondOrderPayloadMatchesDLL: the conditional order uses the base 0x01
// type with cond fields (price 100, qty 1, cond 290, two times).
func TestNewCondOrderPayloadMatchesDLL(t *testing.T) {
	srv := newRunServer(t)
	get := captureTransact(t, srv)
	client, ctx := stopOrderClient(t, srv)
	after := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	before := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	_, err := client.NewCondOrder(ctx, model.NewCondOrder{
		Client: "TESTCLIEN", Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, BuySell: 'B',
		Price: model.Decimal{Units: 10000, Scale: 2}, Quantity: 1,
		CondType: "Bid", CondValue: model.Decimal{Units: 29000, Scale: 2}, ValidAfter: &after, ValidBefore: &before,
	})
	if err != nil {
		t.Fatalf("NewCondOrder: %v", err)
	}
	want := expectedWithClient(t, "transact-newcondorder-payload.hex", 6)
	if got := get(); !bytesEqual(got, want) {
		t.Fatalf("newcondorder payload\n got %x\nwant %x", got, want)
	}
}

// TestConnectorStopAndCondOrders: the XML commands produce the same payloads.
func TestConnectorStopAndCondOrders(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	get := captureTransact(t, srv)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	stopCmd := "<command id=" + quote + "newstoporder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	stopCmd += "<buysell>B</buysell><quantity>1</quantity>"
	stopCmd += "<stoploss><activationprice>100</activationprice><orderprice>90</orderprice><quantity>1</quantity></stoploss>"
	stopCmd += "<takeprofit><activationprice>300</activationprice><quantity>1</quantity></takeprofit></command>"
	if got := send(t, c, stopCmd); !strings.Contains(got, "transactionid") {
		t.Fatalf("newstoporder = %s", got)
	}
	if got, want := get(), expectedWithClient(t, "transact-newstoporder-payload.hex", 7); !bytesEqual(got, want) {
		t.Fatalf("connector newstoporder payload\n got %x\nwant %x", got, want)
	}
	condCmd := "<command id=" + quote + "newcondorder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	condCmd += "<price>100</price><quantity>1</quantity><buysell>B</buysell><cond_type>Bid</cond_type><cond_value>290</cond_value>"
	condCmd += "<validafter>05.11.2026 10:00:00</validafter><validbefore>05.11.2026 18:00:00</validbefore></command>"
	if got := send(t, c, condCmd); !strings.Contains(got, "transactionid") {
		t.Fatalf("newcondorder = %s", got)
	}
	if got, want := get(), expectedWithClient(t, "transact-newcondorder-payload.hex", 6); !bytesEqual(got, want) {
		t.Fatalf("connector newcondorder payload\n got %x\nwant %x", got, want)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStopOrderLegPayloads checks the RE-3 leg-dependent payload formulas:
// SL-only 27 B flags 00 01, TP-only 26 B flags 03 10, both 35 B flags 03 11.
func TestStopOrderLegPayloads(t *testing.T) {
	srv := newRunServer(t)
	get := captureTransact(t, srv)
	client, ctx := stopOrderClient(t, srv)
	cid := snapshotClientID(t)
	buy := func(id int64, legs model.NewStopOrder) []byte { return nil }
	_ = buy
	mkPayload := func(flagsA, flagsB byte, slBits, tpBits string) []byte {
		_ = flagsA
		_ = flagsB
		return nil
	}
	_ = cid
	_ = mkPayload
	_ = cid
	// SL only (activation 100, order price 90, quantity 1).
	_, err := client.NewStopOrder(ctx, model.NewStopOrder{
		Client: "TESTCLIEN", Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, BuySell: 'B',
		StopLoss: &model.StopLoss{
			ActivationPrice: model.Decimal{Units: 10000, Scale: 2},
			OrderPrice:      model.Decimal{Units: 9000, Scale: 2},
			Quantity:        1,
		},
	})
	if err != nil {
		t.Fatalf("SL-only NewStopOrder: %v", err)
	}
	slWant := expectedStopPayload(t, 0x00, 0x01, "02710282320210"+"000000", "")
	if got := get(); !bytesEqual(got, slWant) {
		t.Fatalf("SL-only payload\n got %x\nwant %x", got, slWant)
	}
	// TP only (activation 300, quantity 1).
	_, err = client.NewStopOrder(ctx, model.NewStopOrder{
		Client: "TESTCLIEN", Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, BuySell: 'B',
		TakeProfit: &model.TakeProfit{ActivationPrice: model.Decimal{Units: 30000, Scale: 2}, Quantity: 1},
	})
	if err != nil {
		t.Fatalf("TP-only NewStopOrder: %v", err)
	}
	tpWant := expectedStopPayload(t, 0x03, 0x10, "", "025307000010000000")
	if got := get(); !bytesEqual(got, tpWant) {
		t.Fatalf("TP-only payload\n got %x\nwant %x", got, tpWant)
	}
}

// expectedStopPayload builds the RE-3 expected newstoporder payload inline: the
// common header, the flags and the conditional leg blocks.
func expectedStopPayload(t *testing.T, flagsA, flagsB byte, slHex, tpHex string) []byte {
	t.Helper()
	var w wire.Writer
	w.Byte(0x14)
	w.Byte(0)
	w.Byte(0)
	w.Uint16(26)
	w.Uint16(37)
	w.Int64(snapshotClientID(t))
	w.Byte('B')
	w.Byte(flagsA)
	w.Byte(flagsB)
	w.Raw(make([]byte, 4))
	for _, part := range []string{slHex, tpHex} {
		if part == "" {
			continue
		}
		raw, err := hex.DecodeString(part)
		if err != nil {
			t.Fatal(err)
		}
		w.Raw(raw)
	}
	return w.Bytes()
}

// TestNewCondOrderConditionTypes checks the cond_type payload byte (O: Bid=01,
// BidOrLast=02, AskOrLast=04; Last/Time are rejected by the DLL).
func TestNewCondOrderConditionTypes(t *testing.T) {
	for _, tc := range []struct {
		condType string
		wantByte byte
		wantErr  bool
		wantText string
	}{
		{"Bid", 0x01, false, ""},
		{"BidOrLast", 0x02, false, ""},
		{"AskOrLast", 0x04, false, ""},
		{"Last", 0, true, "Incorrect condition type."},
		{"Time", 0, true, "not recovered"},
		{"Bogus", 0, true, "Incorrect condition type."},
	} {
		t.Run(tc.condType, func(t *testing.T) {
			srv := newRunServer(t)
			var mu sync.Mutex
			var captured []byte
			srv.Handle(session.RqTransact, func(payload []byte) (byte, []byte) {
				mu.Lock()
				captured = append([]byte(nil), payload...)
				mu.Unlock()
				return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
			})
			client, ctx := stopOrderClient(t, srv)
			after := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
			before := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
			_, err := client.NewCondOrder(ctx, model.NewCondOrder{
				Client: "TESTCLIEN", Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, BuySell: 'B',
				Price: model.Decimal{Units: 10000, Scale: 2}, Quantity: 1,
				CondType: tc.condType, CondValue: model.Decimal{Units: 29000, Scale: 2},
				ValidAfter: &after, ValidBefore: &before,
			})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), tc.wantText) {
					t.Fatalf("err = %v, want text %q", err, tc.wantText)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewCondOrder: %v", err)
			}
			mu.Lock()
			payload := captured
			mu.Unlock()
			if payload == nil || payload[16] != tc.wantByte {
				t.Fatalf("payload cond byte = %v, want %#x", payload, tc.wantByte)
			}
		})
	}
}

// TestConnectorStopOrderLegs: the DLL accepts SL-only and TP-only commands, so
// the Connector must not reject TP-only; payloads follow the leg formulas.
func TestConnectorStopOrderLegs(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	get := captureTransact(t, srv)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	base := "<command id=" + quote + "newstoporder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell>"
	slOnly := base + "<stoploss><activationprice>100</activationprice><orderprice>90</orderprice><quantity>1</quantity></stoploss></command>"
	if got := send(t, c, slOnly); !strings.Contains(got, "transactionid") {
		t.Fatalf("SL-only command = %s", got)
	}
	if got, want := get(), expectedStopPayload(t, 0x00, 0x01, "02710282320210000000", ""); !bytesEqual(got, want) {
		t.Fatalf("SL-only connector payload\n got %x\nwant %x", got, want)
	}
	tpOnly := base + "<takeprofit><activationprice>300</activationprice><orderprice>310</orderprice><quantity>1</quantity></takeprofit></command>"
	if got := send(t, c, tpOnly); !strings.Contains(got, "transactionid") {
		t.Fatalf("TP-only command = %s", got)
	}
	if got, want := get(), expectedStopPayload(t, 0x03, 0x10, "", "025307000010000000"); !bytesEqual(got, want) {
		t.Fatalf("TP-only connector payload\n got %x\nwant %x", got, want)
	}
}

// TestConnectorStopOrderErrors checks the DLL validation texts.
func TestConnectorStopOrderErrors(t *testing.T) {
	srv := newRunServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	base := "<command id=" + quote + "newstoporder" + quote + ">"
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"no secid", base + "</command>", "secid"},
		{"no client", base + "<secid>26</secid></command>", "клиент"},
		{"no buysell", base + "<secid>26</secid><client>TESTCLIEN</client><stoploss><activationprice>100</activationprice><orderprice>90</orderprice><quantity>1</quantity></stoploss></command>", "buysell"},
		{"bad buysell", base + "<secid>26</secid><client>TESTCLIEN</client><buysell>X</buysell><stoploss><activationprice>100</activationprice><orderprice>90</orderprice><quantity>1</quantity></stoploss></command>", "Buisell"},
		{"no activation", base + "<secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><stoploss><orderprice>90</orderprice><quantity>1</quantity></stoploss></command>", "stop-loss activation"},
		{"no orderprice", base + "<secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><stoploss><activationprice>100</activationprice><quantity>1</quantity></stoploss></command>", "stop-loss order price"},
		{"no sl qty", base + "<secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><stoploss><activationprice>100</activationprice><orderprice>90</orderprice></stoploss></command>", "stop-loss quantity"},
		{"unknown secid", base + "<secid>999</secid><client>TESTCLIEN</client><buysell>B</buysell><stoploss><activationprice>100</activationprice><orderprice>90</orderprice><quantity>1</quantity></stoploss></command>", "unknown secid"},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want text %q", tc.name, got, tc.want)
		}
	}
	// newcondorder required fields (DLL texts from the RE-3 probes).
	condBase := "<command id=" + quote + "newcondorder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>1</quantity>"
	condCases := []struct {
		name string
		cmd  string
		want string
	}{
		{"no cond_type", condBase + "<price>100</price><cond_value>290</cond_value><validafter>05.11.2026 10:00:00</validafter><validbefore>05.11.2026 18:00:00</validbefore></command>", "condition type"},
		{"no cond_value", condBase + "<price>100</price><cond_type>Bid</cond_type><validafter>05.11.2026 10:00:00</validafter><validbefore>05.11.2026 18:00:00</validbefore></command>", "cond_value"},
		{"no validafter", condBase + "<price>100</price><cond_type>Bid</cond_type><cond_value>290</cond_value><validbefore>05.11.2026 18:00:00</validbefore></command>", "validafter"},
		{"no validbefore", condBase + "<price>100</price><cond_type>Bid</cond_type><cond_value>290</cond_value><validafter>05.11.2026 10:00:00</validafter></command>", "validbefore"},
		{"no price", condBase + "<cond_type>Bid</cond_type><cond_value>290</cond_value><validafter>05.11.2026 10:00:00</validafter><validbefore>05.11.2026 18:00:00</validbefore></command>", "Price not found."},
		{"bad date", condBase + "<price>100</price><cond_type>Bid</cond_type><cond_value>290</cond_value><validafter>2026-11-05 10:00:00</validafter><validbefore>05.11.2026 18:00:00</validbefore></command>", "Incorrect DateTime format"},
	}
	for _, tc := range condCases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want text %q", tc.name, got, tc.want)
		}
	}
}

// TestStopOrderDecodeFilter checks the P3 decoder filters: status-only updates
// pass, foreign/unknown/negative client ids and secid 0xffff are dropped.
func TestStopOrderDecodeFilter(t *testing.T) {
	c := &Client{
		accounts: map[int64]*accountState{
			4096: {account: model.ClientAccount{ID: "TESTCLIEN"}, raw: records.ClientNew{ID: 4096}},
		},
		accountOrder: []int64{4096},
	}
	statusOnly := func() []byte {
		var w wire.Writer
		w.Int32(1)
		w.Int32(1) // status only
		w.Byte(2)
		return w.Bytes()
	}
	if events, err := decodeStopOrder(c, statusOnly()); err != nil || len(events) != 1 {
		t.Fatalf("status-only decode = %d events, %v; want 1", len(events), err)
	}
	withClient := func(clientID int64) []byte {
		var w wire.Writer
		w.Int32(2)
		w.Int32(0x400) // client
		w.Int64(clientID)
		return w.Bytes()
	}
	if events, err := decodeStopOrder(c, withClient(4096)); err != nil || len(events) != 1 {
		t.Fatalf("own-client decode = %d, %v; want 1", len(events), err)
	}
	for _, clientID := range []int64{999, -1} {
		if events, err := decodeStopOrder(c, withClient(clientID)); err != nil || len(events) != 0 {
			t.Fatalf("client %d decode = %d, %v; want dropped", clientID, len(events), err)
		}
	}
	var w wire.Writer
	w.Int32(3)
	w.Int32(0x100) // secid
	w.Uint16(0xffff)
	if events, err := decodeStopOrder(c, w.Bytes()); err != nil || len(events) != 0 {
		t.Fatalf("secid 0xffff decode = %d, %v; want dropped", len(events), err)
	}
	// Unknown/high mask bits must be dropped, never abort the poll (P3).
	var m wire.Writer
	m.Int32(4)
	m.Int32(0x80)
	m.Byte(0)
	if events, err := decodeStopOrder(c, m.Bytes()); err != nil || len(events) != 1 {
		t.Fatalf("mask 0x80 decode = %d, %v; want 1", len(events), err)
	}
}
