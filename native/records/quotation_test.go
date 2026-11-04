package records_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

var (
	quotationSecIDRe   = regexp.MustCompile("<quotation secid=\"([0-9]+)\">")
	quotationBoardRe   = regexp.MustCompile("<board>([^<]*)</board>")
	quotationSecCodeRe = regexp.MustCompile("<seccode>([^<]*)</seccode>")
	quotationCostRe    = regexp.MustCompile("<point_cost>([^<]*)</point_cost>")
)

// Limitation: docs/deepseek/task-index.md §8.12 (the fixture window is outside
// the recorded main1 chunk; the catalog identity is seeded from the callbacks).
// quotationDecimals is the price scale per secid from the r9 capture (S): the
// quotation fixture's window securities are outside the recorded main1 chunk,
// so the catalog identity is seeded from the DLL callback lines, while all the
// field values are compared independently.
var quotationDecimals = map[uint16]uint8{20583: 0, 20573: 0, 379: 2, 2: 4, 8326: 2}

// TestQuotationsMatchDLL renders the 16 R_QUOTATION records of the r9 capture
// and compares them with the 16 DLL callbacks byte for byte.
func TestQuotationsMatchDLL(t *testing.T) {
	payload := oracle.LoadFixture(t, "quotation-live.hex")
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	want := strings.Split(strings.TrimSpace(oracle.LoadTextFixture(t, "quotations-dll.xml")), "\n")
	if len(recs) != 16 || len(want) != 16 {
		t.Fatalf("records/callbacks = %d/%d, want 16/16", len(recs), len(want))
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	seen := map[uint16]bool{}
	for _, line := range want {
		m := quotationSecIDRe.FindStringSubmatch(line)
		board := quotationBoardRe.FindStringSubmatch(line)
		code := quotationSecCodeRe.FindStringSubmatch(line)
		cost := quotationCostRe.FindStringSubmatch(line)
		if m == nil || board == nil || code == nil {
			t.Fatalf("bad DLL callback %q", line)
		}
		id, _ := strconv.Atoi(m[1])
		secid := uint16(id)
		if seen[secid] {
			continue
		}
		seen[secid] = true
		b, ok := catalog.BoardByCode(board[1])
		if !ok {
			t.Fatalf("board %q is not in the startup dictionary", board[1])
		}
		var pointCost float64
		if cost != nil {
			pointCost, _ = strconv.ParseFloat(cost[1], 64)
		}
		catalog.AddSecurity(records.SecNew{
			SecID: secid, SecCode: code[1],
			Pits: []records.Pit{{Board: b.Num, Decimals: quotationDecimals[secid], PointCost: pointCost, Default: true}},
		})
	}
	for i, rec := range recs {
		if rec.Tag != records.TagQuotation {
			t.Fatalf("record %d has tag %#02x", i, rec.Tag)
		}
		q, err := records.ParseQuotation(rec.Body)
		if err != nil {
			t.Fatalf("ParseQuotation %d: %v", i, err)
		}
		ev := q.Event(catalog)
		got, ok := xmlcompat.Render(catalog, &ev, xmlcompat.Options{})
		if !ok {
			t.Fatalf("record %d has no XML form", i)
		}
		if got != want[i] {
			t.Fatalf("record %d\n got %s\nwant %s", i, got, want[i])
		}
	}
}

// TestQuotationUsesBoardDecimals is Review Focus 4: the same security in two
// modes with decimals 4 and 2 renders the same raw value with the mode scale.
func TestQuotationUsesBoardDecimals(t *testing.T) {
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	tqbr, ok := catalog.BoardByCode("TQBR")
	if !ok {
		t.Fatal("no TQBR board")
	}
	mct, ok := catalog.BoardByCode("MCT")
	if !ok {
		t.Fatal("no MCT board")
	}
	catalog.AddSecurity(records.SecNew{
		SecID: 1000, SecCode: "HYDR",
		Pits: []records.Pit{{Board: tqbr.Num, Decimals: 4}, {Board: mct.Num, Decimals: 2}},
	})
	build := func(board uint16, value int32) model.Quotation {
		var w wire.Writer
		w.Uint16(1000)
		w.Uint16(board)
		w.Int64(1 << 14) // bit 14 = last
		w.Int32(value)
		q, err := records.ParseQuotation(w.Bytes())
		if err != nil {
			t.Fatalf("ParseQuotation: %v", err)
		}
		return q.Event(catalog)
	}
	tqbrEv := build(tqbr.Num, 4968)
	mctEv := build(mct.Num, 3590)
	if tqbrEv.Fields.Last == nil || tqbrEv.Fields.Last.Scale != 4 || tqbrEv.Fields.Last.Units != 4968 {
		t.Fatalf("TQBR last = %+v, want Decimal{4968,4}", tqbrEv.Fields.Last)
	}
	if mctEv.Fields.Last == nil || mctEv.Fields.Last.Scale != 2 || mctEv.Fields.Last.Units != 3590 {
		t.Fatalf("MCT last = %+v, want Decimal{3590,2}", mctEv.Fields.Last)
	}
	got, _ := xmlcompat.Render(catalog, &tqbrEv, xmlcompat.Options{})
	if !strings.Contains(got, "<last>0.4968</last>") {
		t.Fatalf("TQBR quotation = %s", got)
	}
	got, _ = xmlcompat.Render(catalog, &mctEv, xmlcompat.Options{})
	if !strings.Contains(got, "<last>35.9</last>") {
		t.Fatalf("MCT quotation = %s", got)
	}
}
