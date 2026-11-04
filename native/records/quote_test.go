package records_test

import (
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/records"
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
