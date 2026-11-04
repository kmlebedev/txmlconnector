package records_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// TestSecInfoUpdMatchesDLL renders the R_SEC_PARAM records of the E07 capture
// with <sec_info_upd> and compares with the 200 DLL callbacks byte for byte.
//
// Limitation (docs/deepseek/task-index.md §8.12): the event window (records
// 15 000-15 599 of the live snapshot) is outside the recorded 499-security
// main1 chunk, and the fixture has no R_SEC_NEW for those secids. The seccode
// and market of every event are therefore seeded into the catalog from the
// expected DLL callbacks; everything else (the event set, the field order,
// forms and values) is compared against the DLL output independently.
func TestSecInfoUpdMatchesDLL(t *testing.T) {
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	payload := oracle.LoadFixture(t, "secparam-live.hex.gz")
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	want := strings.Split(strings.TrimSpace(oracle.LoadTextFixture(t, "sec_info_upd-dll.xml.gz")), "\n")
	if len(want) != 200 {
		t.Fatalf("DLL callbacks = %d, want 200", len(want))
	}

	secIDRe := regexp.MustCompile("<secid>([0-9]+)</secid>")
	secCodeRe := regexp.MustCompile("<seccode>([^<]*)</seccode>")
	marketRe := regexp.MustCompile("<market>([0-9]+)</market>")
	for _, line := range want {
		m := secIDRe.FindStringSubmatch(line)
		code := secCodeRe.FindStringSubmatch(line)
		market := marketRe.FindStringSubmatch(line)
		if m == nil || code == nil || market == nil {
			t.Fatalf("bad DLL callback %q", line)
		}
		id, _ := strconv.Atoi(m[1])
		mk, _ := strconv.Atoi(market[1])
		catalog.AddSecurity(records.SecNew{SecID: uint16(id), SecCode: code[1], Market: byte(mk)})
	}

	var (
		got   []string
		muted int
	)
	for _, rec := range recs {
		if rec.Tag != records.TagSecParam {
			continue
		}
		p, err := records.ParseSecParam(rec.Body)
		if err != nil {
			t.Fatalf("ParseSecParam: %v", err)
		}
		if p.Mask&0x8 == 0 {
			muted++
			continue
		}
		sec, ok := catalog.Security(p.SecID)
		if !ok {
			t.Fatalf("secid %d is not in the catalog", p.SecID)
		}
		update := p.SecInfoUpdate(sec)
		msg, ok := xmlcompat.Render(catalog, &update, xmlcompat.Options{})
		if !ok {
			t.Fatalf("secid %d has no <sec_info_upd> form", p.SecID)
		}
		got = append(got, msg)
	}
	if len(got) != len(want) {
		t.Fatalf("events = %d, DLL callbacks = %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d\n got %s\nwant %s", i, got[i], want[i])
		}
	}
	if muted != 400 {
		t.Fatalf("muted records = %d, want 400", muted)
	}
}

// TestSecInfoUpdSkipsMutedMasks checks the S-rule: only set bit 0x8 makes an
// event; the other 400 records of the window stay silent and the splitter sees
// no unknown record.
func TestSecInfoUpdSkipsMutedMasks(t *testing.T) {
	payload := oracle.LoadFixture(t, "secparam-live.hex.gz")
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	var events, muted int
	for _, rec := range recs {
		p, err := records.ParseSecParam(rec.Body)
		if err != nil {
			t.Fatalf("ParseSecParam: %v", err)
		}
		if p.Mask&0x8 != 0 {
			events++
		} else {
			muted++
		}
	}
	if events != 200 || muted != 400 {
		t.Fatalf("events/muted = %d/%d, want 200/400", events, muted)
	}
}

// TestParseSecParamShortBody checks the negative path.
func TestParseSecParamShortBody(t *testing.T) {
	if _, err := records.ParseSecParam(nil); err == nil {
		t.Fatal("ParseSecParam(nil) did not fail")
	}
}
