// Package records_test holds the oracle tests: they decode the binary fixtures
// with records and compare the result against the XML the vendor DLL emitted.
// The external test package keeps native/records itself free of the XML layer
// and of client/commands.
package records_test

import (
	"encoding/hex"
	"encoding/xml"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/client/commands"
	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// loadStartup parses the shared RQ_STARTUP fixture; the binary fixture files
// themselves are read through the oracle helper.
func loadStartup(t *testing.T) *model.Startup {
	t.Helper()
	s, err := records.ParseStartup(oracle.LoadFixture(t, "startup-demo.hex"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// dllSecurity keeps minstep, point_cost and lotsize as text to check the
// DLL's number formatting; these shadow the embedded numeric fields.
type dllSecurity struct {
	commands.Security
	MinStepText   string `xml:"minstep"`
	PointCostText string `xml:"point_cost"`
	LotSizeText   string `xml:"lotsize"`
}

// testdata/main1-demo.hex.gz is the first RQ_MAIN reply of a demo-server
// session; securities1-demo.xml.gz is the <securities> callback the DLL
// produced from it (499 instruments).
func TestSecuritiesMatchDLL(t *testing.T) {
	payload := oracle.LoadFixture(t, "main1-demo.hex.gz")
	docs := []byte(oracle.LoadTextFixture(t, "securities1-demo.xml.gz"))
	if n := compareSecurities(t, [][]byte{payload}, [][]byte{docs}); n != 499 {
		t.Fatalf("compared %d securities", n)
	}
}

// TestSecuritiesFullCapture compares a whole session kept outside the repo:
// TXML_DEMO_MAIN holds one hex RQ_MAIN payload per line, TXML_DEMO_XML the
// harness output with "CALLBACK: <securities ..." lines.
func TestSecuritiesFullCapture(t *testing.T) {
	mainPath, xmlPath := os.Getenv("TXML_DEMO_MAIN"), os.Getenv("TXML_DEMO_XML")
	if mainPath == "" || xmlPath == "" {
		t.Skip("TXML_DEMO_MAIN/TXML_DEMO_XML not set")
	}
	read := func(path string) []string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	var payloads, docs [][]byte
	for _, line := range read(mainPath) {
		p, err := hex.DecodeString(strings.TrimSpace(line))
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	for _, line := range read(xmlPath) {
		if _, doc, ok := strings.Cut(line, "CALLBACK: <securities>"); ok {
			docs = append(docs, []byte("<securities>"+doc))
		}
	}
	t.Logf("compared %d securities", compareSecurities(t, payloads, docs))
}

// compareSecurities decodes R_SEC_NEW records from RQ_MAIN payloads and checks
// every verified field against the DLL's <securities> documents, in order.
func compareSecurities(t *testing.T, payloads, docs [][]byte) int {
	t.Helper()
	st := loadStartup(t)
	var want []dllSecurity
	for _, doc := range docs {
		var v struct {
			Items []dllSecurity `xml:"security"`
		}
		if err := xml.Unmarshal(doc, &v); err != nil {
			t.Fatal(err)
		}
		want = append(want, v.Items...)
	}
	var got []records.SecNew
	for i, payload := range payloads {
		recs, err := records.Records(payload)
		if err != nil && !(errors.Is(err, records.ErrUnknownRecord) && i > 0) {
			t.Fatalf("payload %d: %v", i, err)
		}
		for _, r := range recs {
			if r.Tag != records.TagSecNew {
				continue
			}
			s, err := records.ParseSecNew(r.Body)
			if err != nil {
				t.Fatalf("payload %d record %d: %v", i, len(got), err)
			}
			if len(s.Extra) != 0 {
				t.Fatalf("secid %d: %d undecoded bytes", s.SecID, len(s.Extra))
			}
			got = append(got, s)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d securities, DLL reported %d", len(got), len(want))
	}
	for i, s := range got {
		w := want[i]
		g := xmlcompat.Security(st, s)
		p, _ := s.DefaultPit()
		check := func(field string, got, want any) {
			if got != want {
				t.Errorf("secid %d %s: got %v, DLL %v", s.SecID, field, got, want)
			}
		}
		check("secid", g.SecID, w.SecId)
		check("active", g.Active, w.Active == "true")
		check("seccode", g.SecCode, w.SecCode)
		check("shortname", g.ShortName, w.ShortName)
		check("instrclass", g.InstrClass, w.InstrClass)
		check("board", g.Board, w.Board)
		check("market", g.Market, w.Market)
		check("decimals", g.Decimals, w.Decimals)
		check("sectype", g.SecType, w.SecType)
		check("currency", g.Currency, w.Currency)
		check("currencyid", g.CurrencyID, w.CurrencyID)
		check("lotdivider", g.LotDivider, w.LotDivider)
		check("sec_tz", g.SecTZ, w.SecTZ.Name)
		check("quotestype", g.QuotesType, w.QuotesType)
		check("MIC", g.MIC, w.MIC)
		check("ticker", g.Ticker, w.Ticker)
		if w.Active == "true" {
			check("opmask", g.OpMask, expectedOpMask(w.OpMask))
		}
		if w.LotSizeText != "" {
			check("lotsize", strconv.Itoa(g.LotSize), w.LotSizeText)
		}
		if w.MinStepText != "" {
			check("minstep text", xmlcompat.FormatMinStep(g.MinStep, int(p.Decimals)), w.MinStepText)
		}
		if w.PointCostText != "" {
			check("point_cost text", xmlcompat.FormatPointCost(g.PointCost), w.PointCostText)
		}
	}
	return len(got)
}

// expectedOpMask converts the yes/no XML form to the typed model.
func expectedOpMask(m commands.OpMask) model.SecurityOpMask {
	yes := func(s string) bool { return s == "yes" }
	return model.SecurityOpMask{
		UseCredit: yes(m.UseCredit), ByMarket: yes(m.ByMarket), NoSplit: yes(m.NoSplit),
		Fok: yes(m.Fok), Ioc: yes(m.Ioc), ImmOrCancel: yes(m.ImmOrCancel),
		CancelBalance: yes(m.CancelBalance),
	}
}
