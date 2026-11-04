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

// TestSecInfoUpdMatchesDLL renders the R_SEC_PARAM records of the E07 capture
// with <sec_info_upd> and compares with the 200 DLL callbacks byte for byte.
//
// Limitation (docs/deepseek/task-index.md §8.12): the event window (records
// 15 000-15 599 of the live snapshot) is outside the recorded 499-security
// main1 chunk, and the fixture has no R_SEC_NEW for those secids. The catalog is
// therefore seeded from the expected DLL callbacks: seccode/market identity and
// the catalog point_cost come from those lines; the security Kind and the Pit
// table are NOT seeded (the OPT/FUT form falls back to the mask bit 0x4, see
// SecParam.SecInfoUpdate). The event set, field order, forms and all wire values
// are compared against the DLL output independently. A full independent oracle
// needs an R_SEC_NEW fixture for that window (separate RE card, not a blocker).
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

// TestSecInfoPointCostExponential pins the %.6g exponential branch: values like
// 1e6 and 1e-5 must not silently become zero.
func TestSecInfoPointCostExponential(t *testing.T) {
	cases := []struct {
		value float64
		want  model.Decimal
	}{
		{1e6, model.Decimal{Units: 1000000}},
		{1e-5, model.Decimal{Units: 1, Scale: 5}},
		{161.947, model.Decimal{Units: 161947, Scale: 3}},
	}
	for _, c := range cases {
		p := records.SecParam{SecID: 1, Mask: 0x9, HasPointCost: true, PointCost: c.value}
		u := p.SecInfoUpdate(records.SecNew{})
		if u.PointCost == nil || *u.PointCost != c.want {
			t.Fatalf("point cost %v = %+v, want %+v", c.value, u.PointCost, c.want)
		}
	}
}

// TestSecInfoUpdateKindFallback pins the documented fallback: without a known
// security kind the bgo_buy bit 0x4 selects the OPT form (the DLL would throw
// "Wrong sectype" for a kind other than 4/5, D).
func TestSecInfoUpdateKindFallback(t *testing.T) {
	p := records.SecParam{SecID: 1, Mask: 0x5, BgoC: 100, BgoBuy: 200, HasPointCost: true, PointCost: 100}
	u := p.SecInfoUpdate(records.SecNew{Kind: 1})
	if u.BgoC == nil || u.BgoBuy == nil || u.BuyDeposit != nil {
		t.Fatalf("unknown kind with bit 0x4 = %+v, want the OPT fallback", u)
	}
	if u.PointCost == nil || u.PointCost.Units != 100 {
		t.Fatalf("point cost = %+v", u.PointCost)
	}
}

// TestSecInfoUpdateMinMaxDecimals checks the min/max price scale comes from the
// default pit, not the first one.
func TestSecInfoUpdateMinMaxDecimals(t *testing.T) {
	sec := records.SecNew{Pits: []records.Pit{
		{Board: 1, Decimals: 2},
		{Board: 2, Decimals: 4, Default: true},
	}}
	p := records.SecParam{SecID: 1, Mask: 0x8 | 0x10, Board: 0x2a, HasPointCost: true, PointCost: 100,
		MinPrice: 38930, HasMin: true}
	u := p.SecInfoUpdate(sec)
	if u.MinPrice == nil || u.MinPrice.Scale != 4 {
		t.Fatalf("minprice scale = %+v, want the default pit decimals 4", u.MinPrice)
	}
}

// TestParseSecParamShortBodies is the table-driven negative path.
func TestParseSecParamShortBodies(t *testing.T) {
	var w wire.Writer
	w.Uint16(26)
	w.Int64(0x9)
	w.Int64(3272739)
	w.Uint16(0x2a)
	w.Raw(make([]byte, 8))
	full := w.Bytes()
	for _, n := range []int{0, 1, 2, 3, 4, 5, 8, len(full) - 9, len(full) - 8, len(full) - 1} {
		if _, err := records.ParseSecParam(full[:n]); err == nil {
			t.Fatalf("ParseSecParam(%d/%d bytes) did not fail", n, len(full))
		}
	}
	if p, err := records.ParseSecParam(full); err != nil || p.Board != 0x2a {
		t.Fatalf("full body = %+v, %v; want board 0x2a", p, err)
	}
}
