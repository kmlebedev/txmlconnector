package xmlcompat

import (
	"encoding/hex"
	"encoding/xml"
	"regexp"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/client/commands"
)

// TestSecurityXMLBytes compares the hand-rolled <security> emitter with the
// DLL callback text byte-for-byte, one element at a time, on the snapshot
// fixture (499 demo-server instruments).
func TestSecurityXMLBytes(t *testing.T) {
	st := loadStartup(t)
	payload := mustHexGz(t, "main1-demo.hex.gz")
	recs, err := Records(payload)
	if err != nil {
		t.Fatal(err)
	}
	dll := string(readGz(t, "securities1-demo.xml.gz"))
	re := regexp.MustCompile(`(?s)<security secid="\d+".*?</security>`)
	want := re.FindAllString(dll, -1)
	if len(want) != 499 {
		t.Fatalf("fixture has %d <security> elements", len(want))
	}
	var got []SecNew
	for _, r := range recs {
		if r.Tag == TagSecNew {
			s, err := ParseSecNew(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, s)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d, fixture %d", len(got), len(want))
	}
	for i, s := range got {
		if w := st.SecurityXML(s); w != want[i] {
			t.Fatalf("secid %d:\n got %s\nwant %s", s.SecID, w, want[i])
		}
	}
}

// TestSecurityDerivedFields checks the typed contract: commands.Security
// carries the same active/opmask/quotestype/MIC/ticker the DLL emits.
func TestSecurityDerivedFields(t *testing.T) {
	st := loadStartup(t)
	payload := mustHexGz(t, "main1-demo.hex.gz")
	recs, _ := Records(payload)
	var wrap struct {
		Items []struct {
			commands.Security
		} `xml:"security"`
	}
	if err := xml.Unmarshal(readGz(t, "securities1-demo.xml.gz"), &wrap); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if r.Tag != TagSecNew {
			continue
		}
		s, err := ParseSecNew(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		got := st.Security(s)
		want := wrap.Items[n].Security
		switch {
		case got.Active != want.Active:
			t.Fatalf("secid %d active: got %q want %q", s.SecID, got.Active, want.Active)
		case got.QuotesType != want.QuotesType:
			t.Fatalf("secid %d quotestype: got %d want %d", s.SecID, got.QuotesType, want.QuotesType)
		case got.MIC != want.MIC:
			t.Fatalf("secid %d MIC: got %q want %q", s.SecID, got.MIC, want.MIC)
		case got.Ticker != want.Ticker:
			t.Fatalf("secid %d ticker: got %q want %q", s.SecID, got.Ticker, want.Ticker)
		case got.Active == "true" && got.OpMask != want.OpMask:
			t.Fatalf("secid %d opmask: got %+v want %+v", s.SecID, got.OpMask, want.OpMask)
		}
		n++
	}
	if n != 499 {
		t.Fatalf("checked %d securities", n)
	}
}

// TestCatalogApplyMutations feeds synthetic attribute records through the
// catalog and checks that <security> output follows the DLL state machine:
// R_SEC_MKT_PERMIT (0x0e) toggles bymarket, R_SEC_ONOFF (0x0d) the tradability
// bits of the flags byte.
func TestCatalogApplyMutations(t *testing.T) {
	c := NewCatalog(loadStartup(t))
	// SHARE on TQBR with flags 0x0f: trading bits set and market permit set.
	s := SecNew{SecID: 700, Kind: 0, Market: 1, SecCode: "TEST",
		Class: 'E', Flags: 0x0f,
		Pits: []Pit{{Board: 37, Decimals: 2, MinStep: 1, LotSize: 1, LotDivider: 1, PointCost: 1, Default: true}}}
	c.AddSecurity(s)
	if xml1 := c.SecuritiesDumpXML(); !strings.Contains(xml1, `bymarket="yes"`) {
		t.Fatalf("flags 0x0f must report bymarket: %s", xml1)
	}
	// Market permit revoked by R_SEC_MKT_PERMIT (tag 0x0e) with value 0:
	// body is W secid (big-endian) + B value.
	body := []byte{byte(s.SecID >> 8), byte(s.SecID), 0x00}
	if _, err := c.ApplyMutations(Record{Tag: 0x0e, Body: body}); err != nil {
		t.Fatal(err)
	}
	if xml2 := c.SecuritiesDumpXML(); strings.Contains(xml2, `bymarket="yes"`) {
		t.Fatalf("R_SEC_MKT_PERMIT=0 must clear bymarket: %s", xml2)
	}
	// Trading off via R_SEC_ONOFF value 0.
	body[2] = 0x00
	if _, err := c.ApplyMutations(Record{Tag: 0x0d, Body: body}); err != nil {
		t.Fatal(err)
	}
	if xml3 := c.SecuritiesDumpXML(); !strings.Contains(xml3, `active="false"`) || strings.Contains(xml3, "<opmask") {
		t.Fatalf("R_SEC_ONOFF=0 must deactivate and drop opmask: %s", xml3)
	}
}

func TestPitsXMLEmitsEveryBoard(t *testing.T) {
	c := NewCatalog(loadStartup(t))
	c.AddSecurity(SecNew{SecID: 5, Kind: 0, Market: 1, SecCode: "ABBA", Class: 'E',
		Pits: []Pit{
			{Board: 37, Decimals: 2, MinStep: 1, LotSize: 10, LotDivider: 1, PointCost: 1},
			{Board: 39, Decimals: 2, MinStep: 1, LotSize: 10, LotDivider: 1, PointCost: 1, Default: true},
		}})
	out := c.Startup().PitsXML(c.List())
	for _, want := range []string{
		`<pit seccode="ABBA" board ="TQBR">`,
		`<pit seccode="ABBA" board ="TQIF">`,
		"<market>1</market>",
		"<decimals>2</decimals>",
		"<lotsize>10</lotsize>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("pits missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "</pit></pit>") {
		t.Fatal("empty pit element")
	}
}

func mustHexGz(t *testing.T, name string) []byte {
	t.Helper()
	p, err := hex.DecodeString(strings.TrimSpace(string(readGz(t, name))))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
