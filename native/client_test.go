package native

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		r = zr
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// marketServer serves the demo-server snapshot (499 securities, including
// SBER 26, GAZP 379 and LKOH 4 on TQBR) and, after RQ_SUBSCRIBE, the trade
// records the DLL decoded in the oracle run alltrades-dll<n>.xml.
func marketServer(t *testing.T, tradesFixture string) *fakeServer {
	srv := newFakeServer(t)
	srv.snapshot = readFixture(t, "main1-demo.hex.gz")
	srv.trades = readFixture(t, tradesFixture)
	return srv
}

func TestClientAllTrades(t *testing.T) {
	srv := marketServer(t, "alltrades-inject1.hex")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "fake:3900", Login: testLogin, Password: testPassword, Dial: srv.dial})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Catalog().Len() != 499 || c.ServerTZ() != "Russian Standard Time" {
		t.Fatalf("catalog %d, tz %q", c.Catalog().Len(), c.ServerTZ())
	}
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}, model.Symbol{Board: "TQBR", SecCode: "GAZP"}); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	sub := hex.EncodeToString(srv.subscribes[0])
	srv.mu.Unlock()
	if sub != "001a002504017b002504" { // SBER, GAZP on board 37, alltrades
		t.Fatalf("RQ_SUBSCRIBE = %s", sub)
	}
	trades, _, err := c.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 5 {
		t.Fatalf("%d trades", len(trades))
	}
	sber, gazp, vtbr := trades[0], trades[2], trades[4]
	// 03.11.2026 23:53:20 MSK in the DLL output is 20:53:20 UTC.
	if sber.SecCode != "SBER" || sber.Board != "TQBR" || sber.TradeNo != 9000000001 ||
		sber.Price != (model.Decimal{Units: 30512, Scale: 2}) || sber.Quantity != 3 || sber.Period != 'B' || sber.BuySell != 0 ||
		!sber.Time.Equal(time.Date(2026, 11, 3, 20, 53, 20, 0, time.UTC)) {
		t.Fatalf("SBER = %+v", sber)
	}
	if gazp.SecCode != "GAZP" || !gazp.HasOpenInterest || gazp.OpenInterest != 777 ||
		!gazp.Time.Equal(time.Date(2026, 11, 3, 20, 53, 22, 250e6, time.UTC)) {
		t.Fatalf("GAZP = %+v", gazp)
	}
	if vtbr.SecCode != "" || vtbr.SecID != 889 { // VTBR is not in the 499-security fixture
		t.Fatalf("VTBR = %+v", vtbr)
	}
	if got := c.WallTime(sber).Format("02.01.2006 15:04:05"); got != "03.11.2026 23:53:20" {
		t.Fatalf("wall time %s", got)
	}

	if err := c.UnsubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "GAZP"}); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	unsub := hex.EncodeToString(srv.subscribes[1])
	srv.mu.Unlock()
	if unsub != "017b002500" {
		t.Fatalf("unsubscribe = %s", unsub)
	}
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "NOPE"}); err == nil {
		t.Fatal("expected unknown security error")
	}
}

// The XML path must emit exactly what the DLL emitted for the same records.
func TestConnectorAllTradesMatchDLL(t *testing.T) {
	srv := marketServer(t, "alltrades-inject2.hex")
	c := New(Config{Dial: srv.dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	connect := strings.Replace(connectCommand(), "</rqdelay>", "</rqdelay><milliseconds>true</milliseconds>", 1)
	send(t, c, connect)
	rec.next(t, `<server_status`)
	sub := `<command id="subscribe"><alltrades><security><board>TQBR</board><seccode>SBER</seccode></security></alltrades></command>`
	if got := send(t, c, sub); got != `<result success="true"/>` {
		t.Fatalf("subscribe = %s", got)
	}
	want, err := os.ReadFile("records/testdata/alltrades-dll2.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.next(t, "<alltrades>"); got != strings.TrimSpace(string(want)) {
		t.Fatalf("alltrades\n got %s\nwant %s", got, want)
	}
	if got := send(t, c, `<command id="subscribe"><quotations><secid>26</secid></quotations></command>`); !strings.Contains(got, "not implemented") {
		t.Fatalf("quotations = %s", got)
	}
}
