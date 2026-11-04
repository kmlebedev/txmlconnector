package records_test

import (
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// TestQuotesMatchDLL renders the two R_QUOTES records of the r10 capture and
// compares them with the two DLL <quotes> callbacks byte for byte (40 levels:
// price deltas accumulated across the B -> S transition, decimals of TQBR).
func TestQuotesMatchDLL(t *testing.T) {
	payload := oracle.LoadFixture(t, "quote-live.hex")
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	want := strings.Split(strings.TrimSpace(oracle.LoadTextFixture(t, "quotes-dll.xml")), "\n")
	if len(recs) != 2 || len(want) != 2 {
		t.Fatalf("records/callbacks = %d/%d, want 2/2", len(recs), len(want))
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	levels := 0
	for i, rec := range recs {
		if rec.Tag != records.TagQuotes {
			t.Fatalf("record %d has tag %#02x", i, rec.Tag)
		}
		q, err := records.ParseQuotes(rec.Body)
		if err != nil {
			t.Fatalf("ParseQuotes %d: %v", i, err)
		}
		levels += len(q.Levels)
		ev := q.Event(catalog)
		got, ok := xmlcompat.Render(catalog, &ev, xmlcompat.Options{})
		if !ok {
			t.Fatalf("record %d has no XML form", i)
		}
		if got != want[i] {
			t.Fatalf("record %d\n got %s\nwant %s", i, got, want[i])
		}
	}
	if levels != 40 {
		t.Fatalf("levels = %d, want 40", levels)
	}
}

// TestQuotesDeletionSemantics pins the PDF 4.9 deletion form (I: not observed
// live): a level whose accumulated price stays below 1 renders buy/sell = -1.
func TestQuotesDeletionSemantics(t *testing.T) {
	q := records.Quotes{SecID: 1, Board: 2, Levels: []records.QuoteLevel{
		{Side: 'B', Price: 0, Qty: 0, Deleted: true},
		{Side: 'S', Price: 0, Qty: 0, Deleted: true},
	}}
	ev := q.Event(nil)
	if len(ev.Levels) != 2 || ev.Levels[0].Buy != -1 || ev.Levels[1].Sell != -1 {
		t.Fatalf("deletion levels = %+v", ev.Levels)
	}
}

// TestQuotesParseDeletion pins the price<1 deletion branch: the level is marked
// deleted, keeps the accumulated price, and Event exposes buy/sell = -1.
func TestQuotesParseDeletion(t *testing.T) {
	var w wire.Writer
	w.Uint16(26)
	w.Uint16(37)
	w.Byte('B')
	w.Int32(0) // accumulated price stays 0 (<1 -> deletion)
	w.Int32(0)
	w.Byte(0xff)
	parsed, err := records.ParseQuotes(w.Bytes())
	if err != nil {
		t.Fatalf("ParseQuotes: %v", err)
	}
	if len(parsed.Levels) != 1 || !parsed.Levels[0].Deleted || parsed.Levels[0].Price != 0 {
		t.Fatalf("levels = %+v", parsed.Levels)
	}
	ev := parsed.Event(nil)
	if len(ev.Levels) != 1 || ev.Levels[0].Buy != -1 {
		t.Fatalf("event levels = %+v", ev.Levels)
	}
	if ev.Levels[0].Price.Units != 0 {
		t.Fatalf("deletion price = %+v", ev.Levels[0].Price)
	}
}

// TestParseQuotesNegatives is the table-driven error path: short bodies, a bad
// side byte and a missing 0xff terminator.
func TestParseQuotesNegatives(t *testing.T) {
	valid := []byte{0x00, 0x1a, 0x00, 0x25, 'B', 0x00, 0x00, 0x01, 0xff}
	cases := [][]byte{
		nil,
		valid[:2],
		valid[:4],
		valid[:5],
		valid[:7],
		[]byte{0x00, 0x1a, 0x00, 0x25, 'X', 0x00, 0x00, 0xff},
		valid[:len(valid)-1], // no 0xff terminator
	}
	for i, body := range cases {
		if _, err := records.ParseQuotes(body); err == nil {
			t.Fatalf("case %d (%x) did not fail", i, body)
		}
	}
}
