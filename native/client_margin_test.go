package native

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/hex"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// marginSnapshot builds the O store of margin-inject.hex (client TESTCLIEN,
// money 300 000 RUB, SBER 1000) plus the catalog security of clientSnapshot.
func marginSnapshot(t *testing.T) []byte {
	t.Helper()
	// margin-inject.hex is self-contained (task-52): client + money 300 000 RUB +
	// SBER position 1000 + R_SEC_RATE 0x0a SBER=29146 kopecks. Only the
	// SBER catalog identity is added from the synthetic R_SEC_NEW (no second
	// client record: one R_CLN_NEW per client is the real load invariant).
	return append(secNewRecord(t, "SBER", 26), readOrderFixture(t, "margin-inject.hex")...)
}

// TestMarginMatchDLL renders the two max-buy/sell forms from the synthetic
// store (margin-inject.hex + R_SEC_RATE P=29146) and compares the callbacks
// byte-for-byte with the O fixture margin-dll.xml (13210/15210, task-52).
func TestMarginMatchDLL(t *testing.T) {
	rawFixture, err := os.ReadFile("records/testdata/margin-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawFixture)), "\n")
	if len(lines) != 3 {
		t.Fatalf("margin-dll.xml lines = %d, want 3", len(lines))
	}
	wantMax, wantTPlus := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	srv := newRunServer(t)
	srv.Snapshot = marginSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := send(t, c, connectCommand()); !strings.Contains(got, quote+"true"+quote) {
		t.Fatalf("connect = %s", got)
	}
	rec.next(t, "<server_status")
	// O: all three commands are a local render; the capture has no 84/85/86/82
	// frames. Track every non-MAIN request and require zero new ones.
	requestKinds := func() int {
		n := 0
		for _, f := range srv.Requests() {
			if f.Type != session.RqMain {
				n++
			}
		}
		return n
	}
	baselineRequests := requestKinds()
	base := "<command id=" + quote + "get_max_buy_sell_tplus" + quote + " client=" + quote + "TESTCLIEN" + quote + ">"
	base += "<security><seccode>SBER</seccode><market>1</market></security></command>"
	if got := send(t, c, base); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_max_buy_sell_tplus = %s", got)
	}
	if got := rec.next(t, "<max_buy_sell_tplus"); got != wantTPlus {
		t.Fatalf("tplus XML\n got %s\nwant %s", got, wantTPlus)
	}
	maxCmd := "<command id=" + quote + "get_max_buy_sell" + quote + " client=" + quote + "TESTCLIEN" + quote + ">"
	maxCmd += "<security><seccode>SBER</seccode><market>1</market></security></command>"
	if got := send(t, c, maxCmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_max_buy_sell = %s", got)
	}
	if got := rec.next(t, "<max_buy_sell"); got != wantMax {
		t.Fatalf("max_buy_sell XML\n got %s\nwant %s", got, wantMax)
	}
	if got := requestKinds(); got != baselineRequests {
		t.Fatalf("local-render commands sent %d non-RQ_MAIN request(s), want 0", got-baselineRequests)
	}
}

// TestClnSecPermissionsStoredFormI renders the permissions form from the store;
// the expected false/0.00 values are the honest I fallback (O fixture has
// true/14.28, mask/rates store is not recovered). The permission
// mask and rate store of FUN_18001a700 are not present in RQ_MAIN (margin.md:
// mask formula D, source I), so the four rights fall back to false and the
// rates to 0.00; marketable is taken from the R_SEC_MKT_PERMIT bit (O).
//
// I-EXPECTATION: tradeable/longable/shortable=false and regrate*=0.00 are our
// store-derived values, NOT the DLL fixture (true/14.28). After task-52 switch
// to margin-dll.xml; never edit margin-dll.xml.
func TestClnSecPermissionsStoredFormI(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = marginSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	// O: one complete R_SEC_MKT_PERMIT 0x0e permit=yes record flips
	// <marketable> to true (a truncated two-record prefix would not prove it).
	srv.QueueMain([]byte{0x0e, 0x00, 0x1a, 0x01})
	cmd := "<command id=" + quote + "get_cln_sec_permissions" + quote + ">"
	cmd += "<security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security><client>TESTCLIEN</client></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_cln_sec_permissions = %s", got)
	}
	want := "<cln_sec_permissions client=" + quote + "TESTCLIEN" + quote + " secid=" + quote + "26" + quote + "><security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security><tradeable>false</tradeable><marketable>true</marketable><longable>false</longable><shortable>false</shortable><regratelong>0.00</regratelong><regrateshort>0.00</regrateshort></cln_sec_permissions>"
	if got := rec.next(t, "<cln_sec_permissions"); got != want {
		t.Fatalf("permissions XML\n got %s\nwant %s", got, want)
	}
}

// TestMarketOrdEvent is the full O fixture: each 0x0e R_SEC_MKT_PERMIT record
// produces a <marketord> callback (no dedup), byte-exact against the DLL XML.
func TestMarketOrdEvent(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = marginSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	srv.QueueMain(readOrderFixture(t, "marketord-inject.hex"))
	want1 := "<marketord secid=" + quote + "26" + quote + " permit=" + quote + "yes" + quote + " seccode=" + quote + "SBER" + quote + "/>"
	want2 := "<marketord secid=" + quote + "26" + quote + " permit=" + quote + "no" + quote + " seccode=" + quote + "SBER" + quote + "/>"
	if got := rec.next(t, "<marketord"); got != want1 {
		t.Fatalf("marketord 1 = %s", got)
	}
	if got := rec.next(t, "<marketord"); got != want2 {
		t.Fatalf("marketord 2 = %s", got)
	}
}

// TestMarginErrors checks the DLL validation texts of the three commands.
func TestMarginErrors(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = marginSnapshot(t)
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
		{"tplus no client", "<command id=" + quote + "get_max_buy_sell_tplus" + quote + "/>", "Client not specified."},
		{"tplus unknown client", "<command id=" + quote + "get_max_buy_sell_tplus" + quote + " client=" + quote + "NOPE" + quote + "/>", "Wrong client specifited."},
		{"tplus unknown symbol", "<command id=" + quote + "get_max_buy_sell_tplus" + quote + " client=" + quote + "TESTCLIEN" + quote + "><security><seccode>NOPE</seccode><market>1</market></security></command>", "Failed to get symbol."},
		{"max no attrs", "<command id=" + quote + "get_max_buy_sell" + quote + "/>", "should not be empty"},
		{"permissions no board", "<command id=" + quote + "get_cln_sec_permissions" + quote + "><security><seccode>SBER</seccode><market>1</market></security><client>TESTCLIEN</client></command>", "Не указан элемент"},
		{"permissions no client", "<command id=" + quote + "get_cln_sec_permissions" + quote + "><security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security></command>", "Должен быть указан клиент и/или юнион"},
		{"permissions unknown client", "<command id=" + quote + "get_cln_sec_permissions" + quote + "><security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security><client>NOPE</client></command>", "Неизвестный код клиента"},
		{"permissions unknown symbol", "<command id=" + quote + "get_cln_sec_permissions" + quote + "><security><seccode>NOPE</seccode><market>1</market><board>TQBR</board></security><client>TESTCLIEN</client></command>", "Инструмент не найден"},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want text %q", tc.name, got, tc.want)
		}
	}
}

// mctSnapshot patches every R_CLN_NEW of the store to type 4 (mct) so the DLL
// MCT behavior of the three commands can be tested (margin.md, O).
func mctSnapshot(t *testing.T) []byte {
	t.Helper()
	raw := append(secNewRecord(t, "SBER", 26), readOrderFixture(t, "margin-inject.hex")...)
	recs, err := records.Records(raw)
	if err != nil {
		t.Fatalf("margin store: %v", err)
	}
	var out []byte
	for _, rec := range recs {
		body := append([]byte(nil), rec.Body...)
		if rec.Tag == records.TagClientNew {
			r := wire.NewReader(body)
			r.Int64()
			r.String(12)
			r.String(32)
			r.Byte()
			if offset := len(body) - r.Len(); offset < len(body) {
				body[offset] = 4
			}
			parsed, err := records.ParseClientNew(body)
			if err != nil || parsed.Type != 4 {
				t.Fatalf("MCT client patch: type=%d err=%v", parsed.Type, err)
			}
		}
		out = append(out, rec.Tag, byte(len(body)>>8), byte(len(body)))
		out = append(out, body...)
	}
	return out
}

// TestMarginMCTBehavior: tplus rejects an MCT client, max_buy_sell answers
// success with zeros, permissions works for it (margin.md, O).
func TestMarginMCTBehavior(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = mctSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	tplus := "<command id=" + quote + "get_max_buy_sell_tplus" + quote + " client=" + quote + "TESTCLIEN" + quote + ">"
	tplus += "<security><seccode>SBER</seccode><market>1</market></security></command>"
	if got := send(t, c, tplus); !strings.Contains(got, "MCT клиента") {
		t.Fatalf("MCT tplus = %s", got)
	}
	maxCmd := "<command id=" + quote + "get_max_buy_sell" + quote + " client=" + quote + "TESTCLIEN" + quote + ">"
	maxCmd += "<security><seccode>SBER</seccode><market>1</market></security></command>"
	if got := send(t, c, maxCmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("MCT max_buy_sell = %s", got)
	}
	if got := rec.next(t, "<max_buy_sell"); !strings.Contains(got, "<maxbuy>0</maxbuy><maxsell>0</maxsell>") {
		t.Fatalf("MCT max_buy_sell XML = %s", got)
	}
	perm := "<command id=" + quote + "get_cln_sec_permissions" + quote + "><security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security><client>TESTCLIEN</client></command>"
	if got := send(t, c, perm); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("MCT permissions = %s", got)
	}
	if got := rec.next(t, "<cln_sec_permissions"); !strings.Contains(got, "secid="+quote+"26"+quote) {
		t.Fatalf("MCT permissions XML = %s", got)
	}
}

// TestMarketOrdUnknownSecID: the secid not in the catalog still yields a
// callback with the DLL's 12-question-mark seccode placeholder (margin.md, D).
func TestMarketOrdUnknownSecID(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = marginSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	srv.QueueMain([]byte{0x0e, 0x27, 0x0f, 0x01}) // secid 9999, permit=yes
	want := "<marketord secid=" + quote + "9999" + quote + " permit=" + quote + "yes" + quote + " seccode=" + quote + "????????????" + quote + "/>"
	if got := rec.next(t, "<marketord"); got != want {
		t.Fatalf("unknown-secid marketord = %s, want %s", got, want)
	}
}

// TestMarketOrdEveryRecord: every 0x0e record is reported, without dedup (O).
func TestMarketOrdEveryRecord(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = marginSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	srv.QueueMain([]byte{
		0x0e, 0x00, 0x1a, 0x01,
		0x0e, 0x00, 0x1a, 0x00,
		0x0e, 0x00, 0x1a, 0x01,
	})
	for i, permit := range []string{"yes", "no", "yes"} {
		want := "<marketord secid=" + quote + "26" + quote + " permit=" + quote + permit + quote + " seccode=" + quote + "SBER" + quote + "/>"
		if got := rec.next(t, "<marketord"); got != want {
			t.Fatalf("marketord %d = %s, want %s", i+1, got, want)
		}
	}
}

// TestMarketOrdDuringLoad: a 0x0e record inside the load snapshot is reported
// from the end-of-load stream too (O semantics: every record).
func TestMarketOrdDuringLoad(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = append(marginSnapshot(t), readOrderFixture(t, "marketord-inject.hex")...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	// Load callbacks come before the first <server_status>: read them first so
	// the status search does not consume them.
	if got := rec.next(t, "<marketord"); !strings.Contains(got, "permit="+quote+"yes"+quote) {
		t.Fatalf("load marketord 1 = %s", got)
	}
	if got := rec.next(t, "<marketord"); !strings.Contains(got, "permit="+quote+"no"+quote) {
		t.Fatalf("load marketord 2 = %s", got)
	}
	rec.next(t, "<server_status")
}

// TestMaxBuySellFormulaPoints pins the exact task-52 formula against the O
// tables of margin.md (money sweep, position sweep incl. the short branch, V
// injection and the GAZP cross position; V(SBER)=29146, R=0.1428).
func TestMaxBuySellFormulaPoints(t *testing.T) {
	type point struct {
		name                       string
		money, pos, value, lotSize float64
		buy, sell                  int64
	}
	moneySweep := []point{
		{"money0", 0, 0, 29146, 1, 0, 0},
		{"money100k", 10_000_000, 0, 29146, 1, 2402, 2402},
		{"money200k", 20_000_000, 0, 29146, 1, 4805, 4805},
		{"money300k", 30_000_000, 0, 29146, 1, 7207, 7207},
		{"money400k", 40_000_000, 0, 29146, 1, 9610, 9610},
		{"money450k", 45_000_000, 0, 29146, 1, 10811, 10811},
		{"money600k", 60_000_000, 0, 29146, 1, 14415, 14415},
		{"money900k", 90_000_000, 0, 29146, 1, 21623, 21623},
		{"money1200k", 120_000_000, 0, 29146, 1, 28831, 28831},
	}
	posSweep := []point{
		{"pos1000", 30_000_000, 1000, 29146, 1, 13210, 15210},
		{"pos2000", 30_000_000, 2000, 29146, 1, 19213, 23213},
		{"pos3000", 30_000_000, 3000, 29146, 1, 25216, 31216},
		{"pos0", 30_000_000, 0, 29146, 1, 7207, 7207},
		{"pos-100", 30_000_000, -100, 29146, 1, 6607, 6407},
		{"pos-500", 30_000_000, -500, 29146, 1, 4206, 3206},
		{"pos-900", 30_000_000, -900, 29146, 1, 1805, 5},
		{"pos-950_short", 30_000_000, -950, 29146, 1, 950, 0},
		{"pos-1000_short", 30_000_000, -1000, 29146, 1, 1000, 0},
		{"pos-2000_short", 30_000_000, -2000, 29146, 1, 2000, 0},
		{"pos-5000_short", 30_000_000, -5000, 29146, 1, 5000, 0},
	}
	vSweep := []point{
		{"v2500", 30_000_000, 0, 2500, 1, 84033, 84033},
		{"v5000", 30_000_000, 0, 5000, 1, 42016, 42016},
		{"v10000_pos1000", 30_000_000, 1000, 10000, 1, 27011, 29011},
		// GAZP query: lot size 10, V=14041 -> q0=1496.4, own 600.3/800.3.
		{"gazp_lot10", 30_000_000, 1000, 14041, 10, 2096, 2296},
	}
	for _, tc := range append(append(moneySweep, posSweep...), vSweep...) {
		buy, sell := marginMaxBuySell(tc.money, tc.pos, tc.value, tc.lotSize, nil)
		if buy != tc.buy || sell != tc.sell {
			t.Fatalf("%s: got %d/%d, want %d/%d", tc.name, buy, sell, tc.buy, tc.sell)
		}
	}
	// Cross position: GAZP pos 1000 (lot size 10) contributes to BOTH T terms
	// (formula T=q0+cross) -> SBER 9518/9518 with V=11216.6; margin.md's DLL
	// cross column shows +2309 (9516), the exact V of that replay point is not
	// stored (I) - the formula point here is the model value.
	buy, sell := marginMaxBuySell(30_000_000, 0, 29146, 1, []marginOther{
		{position: 1000, value: 11216.6, lotSize: 10},
	})
	if buy != 9518 || sell != 9518 {
		t.Fatalf("cross position: got %d/%d, want 9518/9518", buy, sell)
	}
}

// TestMarginSnapshotIncludesRate makes the self-contained O fixture explicit:
// margin-inject.hex carries the 0x0a R_SEC_RATE record (P=29146).
func TestMarginSnapshotIncludesRate(t *testing.T) {
	recs, err := records.Records(marginSnapshot(t))
	if err != nil {
		t.Fatalf("margin snapshot: %v", err)
	}
	found := false
	for _, rec := range recs {
		if rec.Tag == records.TagSecRate {
			rate, err := records.ParseSecRate(rec.Body)
			if err != nil || rate.SecID != 26 || rate.Value != 29146 {
				t.Fatalf("rate = %+v, %v", rate, err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("margin-inject.hex has no R_SEC_RATE 0x0a record")
	}
}

// TestMarginRateUnknownFails: without a 0x0a record the maxbuy/maxsell formula
// must return the explicit I failure, not success 0/0.
func TestMarginRateUnknownFails(t *testing.T) {
	raw := marginSnapshot(t)
	recs, err := records.Records(raw)
	if err != nil {
		t.Fatal(err)
	}
	var store []byte
	for _, rec := range recs {
		if rec.Tag == records.TagSecRate {
			continue
		}
		store = append(store, rec.Tag, byte(len(rec.Body)>>8), byte(len(rec.Body)))
		store = append(store, rec.Body...)
	}
	srv := newRunServer(t)
	srv.Snapshot = store
	c := New(Config{Dial: srv.Dial})
	recorder := newRecorder()
	if err := c.Start(recorder.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	recorder.next(t, "<server_status")
	cmd := "<command id=" + quote + "get_max_buy_sell" + quote + " client=" + quote + "TESTCLIEN" + quote + ">"
	cmd += "<security><seccode>SBER</seccode><market>1</market></security></command>"
	if got := send(t, c, cmd); !strings.Contains(got, "rate is not known for secid 26 (I)") {
		t.Fatalf("get_max_buy_sell without 0x0a = %s", got)
	}
}

// TestReloginResetsMarginRates: a real Reconnect installs a new session and
// must not keep per-secid R_SEC_RATE prices from the previous one.
func TestReloginResetsMarginRates(t *testing.T) {
	first := newRunServer(t)
	first.Snapshot = marginSnapshot(t)
	second := newRunServer(t)
	second.Snapshot = clientSnapshot(t)
	var mu sync.Mutex
	connects := 0
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		connects++
		n := connects
		mu.Unlock()
		if n == 1 {
			return first.Dial(ctx, address)
		}
		return second.Dial(ctx, address)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.mu.Lock()
	before := len(c.marginRates)
	c.mu.Unlock()
	if before == 0 {
		t.Fatal("initial session has no marginRates")
	}
	if err := c.Reconnect(ctx); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	c.mu.Lock()
	after := c.marginRates
	c.mu.Unlock()
	if after != nil {
		t.Fatalf("marginRates after Reconnect = %v, want nil", after)
	}
}

// TestCrossUnknownRateFails: a non-zero cross position without R_SEC_RATE must
// be an explicit failure (a zero cross term would inflate the result).
func TestCrossUnknownRateFails(t *testing.T) {
	c := &Client{secPositions: map[positionKey]model.SecPosition{
		{client: 1, sym: 27}: {SaldoIn: 10},
	}}
	if bad, ok := c.crossUnknownRateLocked(1, 26); !ok || bad != 27 {
		t.Fatalf("unknown cross rate = %d, %v; want 27, true", bad, ok)
	}
	c.marginRates = map[uint16]int64{27: 100}
	if bad, ok := c.crossUnknownRateLocked(1, 26); ok {
		t.Fatalf("known cross rate reported as unknown %d", bad)
	}
}

// TestClnSecPermissionsFromMarginTemplate: with a union category the client
// fetches the RQ_INQUIRE_UT template after the load and answers in the DLL's
// risk form (S, production 2026-11-05: SBER riskrate 15.00, all rights true,
// reserate 0.00, no union attribute for a <client> request).
func TestClnSecPermissionsFromMarginTemplate(t *testing.T) {
	srv := newRunServer(t)
	var union wire.Writer
	union.Byte(records.TagUnion)
	union.Int64(42)
	union.String("TESTUNION", 20)
	union.String("TESTCAT", 32)
	union.Byte(0x12)
	srv.Snapshot = append(marginSnapshot(t), union.Bytes()...)
	var doc bytes.Buffer
	zw := zlib.NewWriter(&doc)
	zw.Write([]byte(`<template id="18" name="TEST" short="100" long="100" shortable="0" longable="1" marketable="0" tradeable="1"><instruments>` +
		`<instr seccode="SBER" W="100" mkt="1" tradeable="1" longable="1" shortable="1" marketable="1" long="15.00" short="15.00" BA="SBER"/>` +
		`</instruments></template>`))
	zw.Close()
	full := 0
	{
		zr, _ := zlib.NewReader(bytes.NewReader(doc.Bytes()))
		b, _ := io.ReadAll(zr)
		full = len(b)
	}
	var asked []string
	var mu sync.Mutex
	srv.Handle(session.RqInquireUT, func(payload []byte) (byte, []byte) {
		mu.Lock()
		asked = append(asked, hex.EncodeToString(payload))
		mu.Unlock()
		var w wire.Writer
		w.Byte(0x0f)
		w.Int32(int32(full))
		w.Int32(int32(doc.Len()))
		w.Int32(int32(doc.Len()))
		w.Raw(doc.Bytes())
		return wire.KindReply | wire.FlagPlain, w.Bytes()
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	mu.Lock()
	if len(asked) != 1 || asked[0] != "1200" {
		t.Fatalf("RQ_INQUIRE_UT requests = %v, want [1200]", asked)
	}
	mu.Unlock()
	cmd := "<command id=" + quote + "get_cln_sec_permissions" + quote + ">"
	cmd += "<security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security><client>TESTCLIEN</client></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_cln_sec_permissions = %s", got)
	}
	want := `<cln_sec_permissions client="TESTCLIEN" secid="26"><security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security>` +
		`<tradeable>true</tradeable><marketable>true</marketable><longable>true</longable><shortable>true</shortable>` +
		`<riskrate_long>15.00</riskrate_long><reserate_long>0.00</reserate_long><riskrate_short>15.00</riskrate_short><reserate_short>0.00</reserate_short></cln_sec_permissions>`
	if got := rec.next(t, "<cln_sec_permissions"); got != want {
		t.Fatalf("permissions\n got %s\nwant %s", got, want)
	}
}
