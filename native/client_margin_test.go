package native

import (
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// marginSnapshot builds the O store of margin-inject.hex (client TESTCLIEN,
// money 300 000 RUB, SBER 1000) plus the catalog security of clientSnapshot.
func marginSnapshot(t *testing.T) []byte {
	t.Helper()
	return append(clientSnapshot(t), readOrderFixture(t, "margin-inject.hex")...)
}

// TestMarginMatchDLL renders the two max-buy/sell forms from the synthetic
// store. The empirical maxbuy/maxsell formula needs the price/rate stores that
// the DLL keeps internally (margin.md: I), so the values are 0 with an explicit
// I note; the identity fields are byte-exact against margin-dll.xml.
func TestMarginMatchDLL(t *testing.T) {
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
	base := "<command id=" + quote + "get_max_buy_sell_tplus" + quote + " client=" + quote + "TESTCLIEN" + quote + ">"
	base += "<security><seccode>SBER</seccode><market>1</market></security></command>"
	if got := send(t, c, base); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_max_buy_sell_tplus = %s", got)
	}
	wantTPlus := "<max_buy_sell_tplus client=" + quote + "TESTCLIEN" + quote + "><security secid=" + quote + "26" + quote + "><market>1</market><seccode>SBER</seccode><maxbuy>0</maxbuy><maxsell>0</maxsell></security></max_buy_sell_tplus>"
	if got := rec.next(t, "<max_buy_sell_tplus"); got != wantTPlus {
		t.Fatalf("tplus XML\n got %s\nwant %s", got, wantTPlus)
	}
	maxCmd := "<command id=" + quote + "get_max_buy_sell" + quote + " client=" + quote + "TESTCLIEN" + quote + ">"
	maxCmd += "<security><seccode>SBER</seccode><market>1</market></security></command>"
	if got := send(t, c, maxCmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_max_buy_sell = %s", got)
	}
	wantMax := "<max_buy_sell client=" + quote + "TESTCLIEN" + quote + "><security secid=" + quote + "26" + quote + "><market>1</market><seccode>SBER</seccode><maxbuy>0</maxbuy><maxsell>0</maxsell></security></max_buy_sell>"
	if got := rec.next(t, "<max_buy_sell"); got != wantMax {
		t.Fatalf("max_buy_sell XML\n got %s\nwant %s", got, wantMax)
	}
}

// TestClnSecPermissionsMatchDLL renders the permissions form. The permission
// mask and rate store of FUN_18001a700 are not present in RQ_MAIN (margin.md:
// mask formula D, source I), so the four rights fall back to false and the
// rates to 0.00; marketable is taken from the R_SEC_MKT_PERMIT bit (O).
func TestClnSecPermissionsMatchDLL(t *testing.T) {
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
	// O: R_SEC_MKT_PERMIT 0x0e permit=yes flips <marketable> to true.
	srv.QueueMain(readOrderFixture(t, "marketord-inject.hex")[:5])
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
	raw := append(clientSnapshot(t), readOrderFixture(t, "margin-inject.hex")...)
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
