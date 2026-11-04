package native

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestPortfolioLayoutsAreExplicitI: the command request/response layouts that
// the RE doc marks I must return the explicit error, never fabricated data.
func TestPortfolioLayoutsAreExplicitI(t *testing.T) {
	c := &Client{}
	checks := []struct {
		name string
		call func() error
	}{
		{"PortfolioTPlus", func() error { _, err := c.PortfolioTPlus(context.Background(), "TESTCLIEN"); return err }},
		{"PortfolioMCT", func() error { _, err := c.PortfolioMCT(context.Background(), "TESTCLIEN"); return err }},
		{"MCPortfolio", func() error {
			_, err := c.MCPortfolio(context.Background(), model.MCPortfolioRequest{Client: "TESTCLIEN"})
			return err
		}},
		{"UnitedPortfolio", func() error { _, err := c.UnitedPortfolio(context.Background(), "TESTCLIEN", "TESTUNION"); return err }},
		{"UnitedEquity", func() error { _, err := c.UnitedEquity(context.Background(), "TESTUNION"); return err }},
		{"UnitedGO", func() error { _, err := c.UnitedGO(context.Background(), "TESTUNION"); return err }},
	}
	for _, check := range checks {
		err := check.call()
		if err == nil || !strings.Contains(err.Error(), "layout not recovered (I)") {
			t.Fatalf("%s err = %v, want the explicit I error", check.name, err)
		}
	}
}

// moneyRecord builds the demo R_CLIENT_ASSET money record (reg 1, open balance
// 30000000 = 300000.00, currency RUR) for the given numeric client id.
func moneyRecord(t *testing.T, clientID int64) []byte {
	t.Helper()
	var w wire.Writer
	w.Int64(clientID)
	w.Uint16(0xffff)
	w.Byte(1)    // reg
	w.Byte(0x01) // mask: open_balance
	w.Int64(30000000)
	w.Byte(0xff) // end of registers
	w.Byte(0x04) // mask2: currency
	w.String("RUR", 4)
	body := w.Bytes()
	out := []byte{records.TagClientAsset, byte(len(body) >> 8), byte(len(body))}
	return append(out, body...)
}

// TestPortfolioLocalRender checks the O local-render semantics: get_portfolio
// and get_mc_portfolio answer success and render from the loaded stores, with
// no network frames, byte-exactly against the oracle fixtures.
func TestPortfolioLocalRender(t *testing.T) {
	currencyFixture, err := os.ReadFile("records/testdata/portfolio-currency-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	mcFixture, err := os.ReadFile("records/testdata/portfolio-mc-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), moneyRecord(t, snapshotClientID(t))...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	countRequests := func() int {
		n := 0
		for _, f := range srv.Requests() {
			if f.Type != 5 { // RQ_MAIN polls are expected; no portfolio request
				n++
			}
		}
		return n
	}
	baseline := countRequests()
	if got := send(t, c, "<command id="+quote+"get_portfolio"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_portfolio = %s", got)
	}
	if got := rec.next(t, "<portfolio_currency"); got != strings.TrimSpace(string(currencyFixture)) {
		t.Fatalf("portfolio_currency\n got %s\nwant %s", got, strings.TrimSpace(string(currencyFixture)))
	}
	cmd := "<command id=" + quote + "get_mc_portfolio" + quote + " client=" + quote + "TESTCLIEN" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_mc_portfolio = %s", got)
	}
	if got := rec.next(t, "<mc_portfolio"); got != strings.TrimSpace(string(mcFixture)) {
		t.Fatalf("mc_portfolio\n got %s\nwant %s", got, strings.TrimSpace(string(mcFixture)))
	}
	// O errors: the MCT command rejects the client code format; MC requires client/union.
	if got := send(t, c, "<command id="+quote+"get_portfolio_mct"+quote+" client="+quote+"TESTCLIEN"+quote+"/>"); !strings.Contains(got, "szClient") {
		t.Fatalf("get_portfolio_mct = %s", got)
	}
	if got := send(t, c, "<command id="+quote+"get_mc_portfolio"+quote+"/>"); !strings.Contains(got, "Attributes union and client are empty") {
		t.Fatalf("get_mc_portfolio without attrs = %s", got)
	}
	if delta := countRequests() - baseline; delta != 0 {
		t.Fatalf("portfolio commands sent %d non-RQ_MAIN request(s), want 0 (local render)", delta)
	}
}

// TestPortfolioNoData: without a money store the command succeeds without
// rendering a fabricated portfolio.
func TestPortfolioNoData(t *testing.T) {
	srv := newRunServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	if got := send(t, c, "<command id="+quote+"get_portfolio"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_portfolio = %s", got)
	}
	// Flush the callback queue: no portfolio_currency must be present.
	send(t, c, "<command id="+quote+"server_status"+quote+"/>")
	rec.next(t, "<server_status")
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, msg := range rec.msgs {
		if strings.HasPrefix(msg, "<portfolio_currency") || strings.HasPrefix(msg, "<mc_portfolio") {
			t.Fatalf("fabricated portfolio callback without store data: %q", msg)
		}
	}
}
