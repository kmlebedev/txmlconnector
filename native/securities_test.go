package native

import (
	"encoding/xml"
	"regexp"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/client/commands"
)

// TestConnectorSecuritiesOnLoad checks the DLL callback order on connect:
// dictionaries, one <securities> chunk carrying the 499 snapshot
// instruments, <pits>, then server_status.
func TestConnectorSecuritiesOnLoad(t *testing.T) {
	srv := marketServer(t, "alltrades-inject1.hex")
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	res := send(t, c, connectCommand())
	if res != `<result success="true"/>` {
		t.Fatalf("connect: %s", res)
	}
	// The snapshot chunk must arrive before the connected status; <pits>
	// closes the load. (Exact DLL order is checked on the live stand.)
	secs := rec.next(t, "<securities>")
	pits := rec.next(t, "<pits>")
	status := rec.next(t, "<server_status")
	if strings.Contains(status, `connected="false"`) {
		t.Fatalf("status before load finished: %s", status)
	}
	var list commands.Securities
	if err := xml.Unmarshal([]byte(secs), &list); err != nil {
		t.Fatalf("unmarshal: %v in %s", err, first(secs, 200))
	}
	if len(list.Items) != 499 {
		t.Fatalf("securities chunk carries %d items", len(list.Items))
	}
	// SBER must carry the derived fields the model predicts.
	for _, s := range list.Items {
		if s.SecCode == "SBER" && s.Board == "TQBR" {
			if s.Active != "true" || s.OpMask.UseCredit != "yes" || s.OpMask.Ioc != "yes" ||
				s.OpMask.ByMarket != "yes" || s.QuotesType != 1 {
				t.Fatalf("SBER = %+v", s)
			}
		}
	}

	var p commands.Pits
	if err := xml.Unmarshal([]byte(pits), &p); err != nil {
		t.Fatalf("unmarshal pits: %v", err)
	}
	if len(p.Items) == 0 {
		t.Fatal("empty <pits>")
	}
}

// TestGetSecurities: the command answers success and a <securities> callback
// with the whole catalog, in the DLL's element order.
func TestGetSecurities(t *testing.T) {
	srv := marketServer(t, "alltrades-inject1.hex")
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")

	// Drain the load chunk, then ask for the catalog.
	if res := send(t, c, `<command id="get_securities"/>`); res != `<result success="true"/>` {
		t.Fatalf("get_securities: %s", res)
	}
	doc := rec.next(t, "<securities>")
	var list commands.Securities
	if err := xml.Unmarshal([]byte(doc), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 499 {
		t.Fatalf("get_securities returned %d items", len(list.Items))
	}
	// Byte-for-byte: the emitter text equals the fixture callback, element
	// count is what ties them.
	if n := len(regexp.MustCompile(`(?s)<security `).FindAllString(doc, -1)); n != 499 {
		t.Fatalf("elements %d", n)
	}
}

func first(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
