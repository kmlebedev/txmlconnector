package native

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// rpsOrderPayloadExpected builds the O payloads literally (client id comes from
// the session catalog) instead of patching the fixture, so the test is not
// circular (T28 review P3).
func rpsOrderPayloadExpected(kind string, clientID int64) []byte {
	var w wire.Writer
	switch kind {
	case "newrpsorder":
		w.Byte(0x04)
		w.Uint16(26)
		w.Uint16(37)
		w.Int64(clientID)
		w.Byte('B')
		w.Byte(0)
		w.Raw(make([]byte, 11))
		w.Int32(10000)
		w.Int32(1)
		w.Raw(make([]byte, 5))
	case "newrepoorder":
		w.Byte(0x0e)
		w.Uint16(26)
		w.Uint16(37)
		w.Int64(clientID)
		w.Byte('B')
		w.Byte(0)
		w.Raw(make([]byte, 11))
		w.Int32(1) // repoterm
		w.Byte(0)
		w.Byte(0)
		w.Int32(1000)  // reporate 10 * 100
		w.Int32(10000) // price
		w.Int32(1)     // quantity
		w.Int32(100)
		w.Int32(100)
		w.Int32(100)
		w.Byte('N')
		w.Raw(make([]byte, 2))
	}
	return w.Bytes()
}

// TestRPSPayloads: the four O payloads (newrpsorder, newrepoorder, cancel*)
// are byte-exact through the Connector.
func TestRPSPayloads(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var captured []byte
	srv.Handle(session.RqTransact, func(payload []byte) (byte, []byte) {
		captured = append([]byte(nil), payload...)
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	client.mu.Lock()
	clientID, ok := client.clientNumericIDLocked("TESTCLIEN", "")
	client.mu.Unlock()
	if !ok {
		t.Fatal("no numeric client id")
	}
	cases := []struct {
		name string
		cmd  string
		kind string
	}{
		{"newrpsorder", "<command id=" + quote + "newrpsorder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>1</quantity><price>100</price><repoterm>1</repoterm><reporate>10</reporate></command>", "newrpsorder"},
		{"newrepoorder", "<command id=" + quote + "newrepoorder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>1</quantity><price>100</price><repoterm>1</repoterm><reporate>10</reporate></command>", "newrepoorder"},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, "transactionid") {
			t.Fatalf("%s = %s", tc.name, got)
		}
		want := rpsOrderPayloadExpected(tc.kind, clientID)
		if hex.EncodeToString(captured) != hex.EncodeToString(want) {
			t.Fatalf("%s payload\n got %x\nwant %x", tc.name, captured, want)
		}
	}
	// cancelnegdeal / cancelreport4 share the O payload 08 10 (only three .hex
	// fixtures for four payloads: the cancel pair is one file).
	for _, cmd := range []string{
		"<command id=" + quote + "cancelnegdeal" + quote + "><transactionid>1</transactionid></command>",
		"<command id=" + quote + "cancelreport4" + quote + "><reportid>1</reportid></command>",
	} {
		if got := send(t, c, cmd); !strings.Contains(got, "transactionid") {
			t.Fatalf("cancel = %s", got)
		}
		if hex.EncodeToString(captured) != "0810" {
			t.Fatalf("cancel payload = %x, want 0810", captured)
		}
	}
}

// TestRPSErrors: the DLL local texts and the explicit I errors of the
// unrecovered layouts.
func TestRPSErrors(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"rps no secid", "<command id=" + quote + "newrpsorder" + quote + "/>", "Не указан элемент"},
		{"rps no client", "<command id=" + quote + "newrpsorder" + quote + "><secid>26</secid></command>", "Client not specified."},
		{"rps no buysell", "<command id=" + quote + "newrpsorder" + quote + "><secid>26</secid><client>TESTCLIEN</client></command>", "Cannot find buysell tag."},
		{"rps bad buysell", "<command id=" + quote + "newrpsorder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>X</buysell></command>", "Buisell can be only"},
		{"rps no qty", "<command id=" + quote + "newrpsorder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell></command>", "Cannot find quantity."},
		{"mrepo I", "<command id=" + quote + "newmrepoorder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>1</quantity></command>", "newmrepoorder payload layout is not recovered (I)"},
		{"repo value I", "<command id=" + quote + "newrepoorder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>1</quantity><price>100</price><repoterm>1</repoterm><reporate>10</reporate><value>100</value></command>", "newrepoorder value layout is not recovered (I)"},
		{"rps unknown client", "<command id=" + quote + "newrpsorder" + quote + "><secid>26</secid><client>NOPE</client><buysell>B</buysell><quantity>1</quantity><price>100</price></command>", "Wrong client specifited."},
		{"bad quantity", "<command id=" + quote + "newrpsorder" + quote + "><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>x</quantity></command>", "Quantity have to be a number."},
		{"report no leg", "<command id=" + quote + "newreport4" + quote + "><reportid>1</reportid></command>", `ustrade_leg`},
		{"report I", "<command id=" + quote + "newreport4" + quote + "><ustrade_leg></ustrade_leg></command>", "newreport4 payload layout is not recovered (I)"},
		{"cancelneg no id", "<command id=" + quote + "cancelnegdeal" + quote + "/>", "You have to specify transactionid."},
		{"cancelneg bad id", "<command id=" + quote + "cancelnegdeal" + quote + "><transactionid>x</transactionid></command>", "Transaction identifier have to be a number."},
		{"cancelrep no id", "<command id=" + quote + "cancelreport4" + quote + "/>", "You have to specify reportid."},
		{"cancelrep bad id", "<command id=" + quote + "cancelreport4" + quote + "><reportid>x</reportid></command>", "Reportid identifier have to be a number."},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRPSRenderD pins the D-literal XML forms (no O capture; order is I).
func TestRPSRenderD(t *testing.T) {
	deal := model.RPSTradeForExecution{
		TransactionID: 1, SecID: 26, Client: "TESTCLIEN", Board: "TQBR", SecCode: "SBER", BuySell: 'B',
		Price: model.Decimal{Units: 10000, Scale: 2}, Volume: 1,
		RepoTerm: 1, RepoRate: model.Decimal{Units: 1000, Scale: 2},
	}
	wantDeal := "<negdeal transactionid=" + quote + "1" + quote + "><secid>26</secid><union></union><client>TESTCLIEN</client><board>TQBR</board><seccode>SBER</seccode><buysell>B</buysell><price>100.0</price><volume>1</volume><repoterm>1</repoterm><reporate>10.0</reporate><repovalue>0</repovalue><repo2value>0</repo2value></negdeal>"
	if got := xmlcompat.NegDealXML(deal); got != wantDeal {
		t.Fatalf("NegDealXML = %s, want %s", got, wantDeal)
	}
	report := model.ExecutionReport{TransactionID: 2, ReportNo: 7, ReportKind: 1, ReportType: 2, RepoTradeNo: 3}
	wantReport := "<report transactionid=" + quote + "2" + quote + "><reportno>7</reportno><reportkind>1</reportkind><reporttype>2</reporttype><repotradeno>3</repotradeno><repovalue>0</repovalue><repo2value>0</repo2value></report>"
	if got := xmlcompat.ReportXML(report); got != wantReport {
		t.Fatalf("ReportXML = %s, want %s", got, wantReport)
	}
}

// TestRepoOrderFieldsPayload checks the fields whose payload layout is known:
// repoterm, reporate (x100), start/lower/upper discounts (x100) and
// blocksecurities; unknown fields are refused explicitly (value/refundrate/...).
func TestRepoOrderFieldsPayload(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var captured []byte
	srv.Handle(session.RqTransact, func(payload []byte) (byte, []byte) {
		captured = append([]byte(nil), payload...)
		return wire.KindReply | wire.FlagPlain, mustHex(t, "00100010")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	client.mu.Lock()
	clientID, ok := client.clientNumericIDLocked("TESTCLIEN", "")
	client.mu.Unlock()
	if !ok {
		t.Fatal("no numeric client id")
	}
	cmd := "<command id=" + quote + "newrepoorder" + quote + "><secid>26</secid><client>TESTCLIEN</client>"
	cmd += "<buysell>B</buysell><quantity>1</quantity><price>100</price><repoterm>3</repoterm>"
	cmd += "<reporate>12.5</reporate><startdiscount>2</startdiscount><lowerdiscount>0.5</lowerdiscount>"
	cmd += "<upperdiscount>3</upperdiscount><blocksecurities>Y</blocksecurities></command>"
	if got := send(t, c, cmd); !strings.Contains(got, "transactionid") {
		t.Fatalf("newrepoorder fields = %s", got)
	}
	var w wire.Writer
	w.Byte(0x0e)
	w.Uint16(26)
	w.Uint16(37)
	w.Int64(clientID)
	w.Byte('B')
	w.Byte(0)
	w.Raw(make([]byte, 11))
	w.Int32(3) // repoterm
	w.Byte(0)
	w.Byte(0)
	w.Int32(1250)  // reporate 12.5 * 100
	w.Int32(10000) // price
	w.Int32(1)     // quantity
	w.Int32(200)   // startdiscount 2.00
	w.Int32(50)    // lowerdiscount 0.50
	w.Int32(300)   // upperdiscount 3.00
	w.Byte('Y')    // blocksecurities
	w.Raw(make([]byte, 2))
	if hex.EncodeToString(captured) != hex.EncodeToString(w.Bytes()) {
		t.Fatalf("repo fields payload\n got %x\nwant %x", captured, w.Bytes())
	}
}
