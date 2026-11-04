package native

import (
	"compress/gzip"
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
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// secinfoSpec loads the RQ_SEC_SPEC reply fixture of native/testdata.
func secinfoSpec(t *testing.T) []byte {
	t.Helper()
	f, err := os.Open("testdata/secinfo-live.hex.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("secinfo spec hex: %v", err)
	}
	return payload
}

// secinfoSBER is the DLL <sec_info> of SBER from the r11 fixture.
const secinfoSBER = `<sec_info secid="26"><secname>Сбербанк России ПАО ао</secname><seccode>SBER</seccode><market>1</market><facevalue>3.00</facevalue><point_cost>1</point_cost><isin>RU0009029540</isin><currencyid>RUB</currencyid></sec_info>`

// TestSecuritiesInfoFromSpecCache checks the local cache path: the subscribe
// sends RQ_SEC_SPEC (W secid list) automatically, the reply fills the cache and
// SecuritiesInfo renders <sec_info> without any further network request.
func TestSecuritiesInfoFromSpecCache(t *testing.T) {
	srv := newRunServer(t)
	srv.SecSpec = secinfoSpec(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	if err := c.SubscribeAllTrades(ctx, sber); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	var specPayloads []string
	for _, f := range srv.Requests() {
		if f.Type == session.RqSecSpec {
			specPayloads = append(specPayloads, hex.EncodeToString(f.Payload))
		}
	}
	if len(specPayloads) != 1 || specPayloads[0] != "001a" {
		t.Fatalf("RQ_SEC_SPEC payloads = %v, want [001a]", specPayloads)
	}
	infos, err := c.SecuritiesInfo(ctx, sber)
	if err != nil {
		t.Fatalf("SecuritiesInfo: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("infos = %d, want 1", len(infos))
	}
	got, ok := xmlcompat.Render(c.Catalog(), &infos[0], xmlcompat.Options{})
	if !ok || got != secinfoSBER {
		t.Fatalf("sec_info = %q, %v; want %s", got, ok, secinfoSBER)
	}
}

// TestConnectorGetSecuritiesInfo is the e2e XML command: get_securities_info
// renders the cached <sec_info> locally and sends no request to the server.
func TestConnectorGetSecuritiesInfo(t *testing.T) {
	srv := newRunServer(t)
	srv.SecSpec = secinfoSpec(t)
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
	before := countNonMain(srv.Requests())
	cmd := "<command id=" + quote + "get_securities_info" + quote + "><secid>26</secid></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_securities_info = %s", got)
	}
	if got := rec.next(t, "<sec_info"); got != secinfoSBER {
		t.Fatalf("sec_info = %s, want %s", got, secinfoSBER)
	}
	// The security form resolves by market+seccode the same local way.
	cmd = "<command id=" + quote + "get_securities_info" + quote + ">"
	cmd += "<security><market>1</market><seccode>SBER</seccode></security></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_securities_info security form = %s", got)
	}
	if got := rec.next(t, "<sec_info"); got != secinfoSBER {
		t.Fatalf("sec_info security form = %s", got)
	}
	if delta := countNonMain(srv.Requests()) - before; delta != 0 {
		t.Fatalf("get_securities_info sent %d non-RQ_MAIN request(s), want 0", delta)
	}
}

// TestSubscribeDoesNotRequestSpecOnUnsubscribe: RQ_SEC_SPEC is sent only after a
// successful subscribe, never on unsubscribe (DLL behaviour, S r11).
func TestSubscribeDoesNotRequestSpecOnUnsubscribe(t *testing.T) {
	srv := newRunServer(t)
	srv.SecSpec = secinfoSpec(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	if err := c.SubscribeAllTrades(ctx, sber); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	specCount := func() int {
		n := 0
		for _, f := range srv.Requests() {
			if f.Type == session.RqSecSpec {
				n++
			}
		}
		return n
	}
	afterSubscribe := specCount()
	if afterSubscribe == 0 {
		t.Fatal("subscribe did not send RQ_SEC_SPEC")
	}
	if err := c.UnsubscribeAllTrades(ctx, sber); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if got := specCount(); got != afterSubscribe {
		t.Fatalf("unsubscribe sent %d extra RQ_SEC_SPEC, want 0", got-afterSubscribe)
	}
}

// TestSpecErrorKeepsSubscription: a failed best-effort spec refresh does not
// fail the subscribe and does not corrupt the subscription state.
func TestSpecErrorKeepsSubscription(t *testing.T) {
	srv := newRunServer(t)
	srv.SecSpecError = true
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	if err := c.SubscribeAllTrades(ctx, sber); err != nil {
		t.Fatalf("subscribe with a failing spec refresh: %v", err)
	}
	if err := c.UnsubscribeAllTrades(ctx, sber); err != nil {
		t.Fatalf("unsubscribe after a failing spec refresh: %v", err)
	}
}

// TestSpecRefetchedOnReconnect: the session-local secids change on relogin, so
// the spec cache must be refetched after the resubscribe.
func TestSpecRefetchedOnReconnect(t *testing.T) {
	spec := secinfoSpec(t)
	first := newRunServer(t)
	first.SecSpec = spec
	second := newRunServer(t)
	second.SecSpec = spec
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
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	sber := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	if err := c.SubscribeAllTrades(ctx, sber); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := c.Reconnect(ctx); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	// The second server must have received the RQ_SEC_SPEC refresh.
	found := false
	for _, f := range second.Requests() {
		if f.Type == session.RqSecSpec && hex.EncodeToString(f.Payload) == "001a" {
			found = true
		}
	}
	if !found {
		t.Fatal("reconnect did not refetch RQ_SEC_SPEC from the new session")
	}
	if _, err := c.SecuritiesInfo(ctx, sber); err != nil {
		t.Fatalf("SecuritiesInfo after reconnect: %v", err)
	}
}

// TestConnectorSecIDOutOfRange checks the uint16 range guard of <secid>.
func TestConnectorSecIDOutOfRange(t *testing.T) {
	srv := newRunServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	for _, id := range []string{"70000", "-5"} {
		cmd := "<command id=" + quote + "get_securities_info" + quote + "><secid>" + id + "</secid></command>"
		if got := send(t, c, cmd); !strings.Contains(got, "out of range") {
			t.Fatalf("secid %s: %s, want out-of-range failure", id, got)
		}
	}
}
