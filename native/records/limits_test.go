package records_test

import (
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// TestClientLimitsParseSynthetic pins the D RQ_CLIENT_LIMITS response layout
// (limits.md). No oracle fixture exists yet, so the payload is synthetic (I).
func TestClientLimitsParseSynthetic(t *testing.T) {
	var w wire.Writer
	w.Int32(4096)
	w.Int64(100)
	w.Int64(200)
	w.Int64(300)
	w.Int64(400)
	w.Uint16(7)
	w.Int32(1234567)
	for i := 0; i < 15; i++ {
		w.Int64(int64((i + 1) * 100))
	}
	w.Int32(-1)
	limits, err := records.ParseClientLimits(w.Bytes(), 2|4, false)
	if err != nil {
		t.Fatalf("ParseClientLimits: %v", err)
	}
	if limits.ClientID != 4096 || limits.Coverage != 7 || limits.CBPLimit.Units != 100 {
		t.Fatalf("limits = %+v", limits)
	}
	if limits.MoneyFree.Units != 400 || limits.MoneyFree.Scale != 2 {
		t.Fatalf("moneyfree = %+v", limits.MoneyFree)
	}
	got := xmlcompat.ClientLimitsXML(model.ClientLimits{Client: "TESTCLIEN", CBPLimit: limits.CBPLimit})
	if want := "<clientlimits client=\"TESTCLIEN\"><cbplimit>1.00</cbplimit></clientlimits>"; got != want {
		t.Fatalf("XML = %s, want %s", got, want)
	}
}

func TestClientLimitsEmpty(t *testing.T) {
	var w wire.Writer
	w.Int32(4096)
	w.Int32(-1)
	limits, err := records.ParseClientLimits(w.Bytes(), 0, false)
	if err != nil || limits.ClientID != 4096 {
		t.Fatalf("empty limits = %+v, %v", limits, err)
	}
	if got := xmlcompat.ClientLimitsXML(limits); got != "<clientlimits client=\"\"></clientlimits>" {
		t.Fatalf("empty XML = %s", got)
	}
}
