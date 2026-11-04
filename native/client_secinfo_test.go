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
