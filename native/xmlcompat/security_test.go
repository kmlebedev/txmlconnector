package xmlcompat

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/client/commands"
)

func readGz(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
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
	payload, err := hex.DecodeString(string(readGz(t, "main1-demo.hex.gz")))
	if err != nil {
		t.Fatal(err)
	}
	if n := compareSecurities(t, [][]byte{payload}, [][]byte{readGz(t, "securities1-demo.xml.gz")}); n != 499 {
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
	var got []SecNew
	for i, payload := range payloads {
		recs, err := Records(payload)
		if err != nil && !(errors.Is(err, ErrUnknownRecord) && i > 0) {
			t.Fatalf("payload %d: %v", i, err)
		}
		for _, r := range recs {
			if r.Tag != TagSecNew {
				continue
			}
			s, err := ParseSecNew(r.Body)
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
		g := st.Security(s)
		p, _ := s.DefaultPit()
		check := func(field string, got, want any) {
			if got != want {
				t.Errorf("secid %d %s: got %v, DLL %v", s.SecID, field, got, want)
			}
		}
		check("secid", g.SecId, w.SecId)
		check("active", g.Active, w.Active)
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
		check("sec_tz", g.SecTZ.Name, w.SecTZ.Name)
		check("quotestype", g.QuotesType, w.QuotesType)
		check("MIC", g.MIC, w.MIC)
		check("ticker", g.Ticker, w.Ticker)
		if w.Active == "true" {
			check("opmask", g.OpMask, w.OpMask)
		}
		if w.LotSizeText != "" {
			check("lotsize", strconv.Itoa(g.LotSize), w.LotSizeText)
		}
		if w.MinStepText != "" {
			check("minstep text", FormatMinStep(g.MinStep, int(p.Decimals)), w.MinStepText)
		}
		if w.PointCostText != "" {
			check("point_cost text", FormatPointCost(g.PointCost), w.PointCostText)
		}
	}
	return len(got)
}

func TestRecordsStopsAtUnknownLayout(t *testing.T) {
	_, err := Records([]byte{0x62, 0, 0})
	if err == nil || !strings.Contains(err.Error(), "0x62") {
		t.Fatalf("err = %v", err)
	}
}

func TestFormatting(t *testing.T) {
	cases := []struct{ got, want string }{
		{FormatMinStep(0.01, 2), "0.01"},
		{FormatMinStep(0, 2), "0.0"},
		{FormatMinStep(1, 2), "1.0"},
		{FormatMinStep(0.000002, 6), "0.000002"},
		{FormatMinStep(5, 0), "5"},
		{FormatPointCost(1.0000000000000002), "1"},
		{FormatPointCost(161.1146), "161.115"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
