package records

import (
	"errors"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

func TestRecordsStopsAtUnknownLayout(t *testing.T) {
	_, err := Records([]byte{0x62, 0, 0})
	if err == nil || !strings.Contains(err.Error(), "0x62") {
		t.Fatalf("err = %v", err)
	}
	var unknown *UnknownRecordError
	if !errors.As(err, &unknown) || unknown.Tag != 0x62 {
		t.Fatalf("err = %v, want UnknownRecordError for 0x62", err)
	}
}

// After subscribing, RQ_MAIN replies interleave security attribute updates
// (R_SEC_RATE, 0x0a) with sized records; the splitter must not stop at them.
func TestRecordsAttributeUpdates(t *testing.T) {
	var w wire.Writer
	w.Byte(TagSecRate)
	w.Uint16(0x1e16)
	w.Int32(5380)
	w.Raw([]byte{0x0d, 0x00, 0x1a, 0x01})
	w.Raw([]byte{TagAllTrade, 0, 0})
	recs, err := Records(w.Bytes())
	if err != nil || len(recs) != 3 || recs[0].Tag != TagSecRate || recs[1].Tag != 0x0d || recs[2].Tag != TagAllTrade {
		t.Fatalf("records = %+v, %v", recs, err)
	}
}

func TestTradeDate(t *testing.T) {
	var w wire.Writer
	w.Int32(1793664000)
	d, err := TradeDate(Record{Tag: TagDate, Body: w.Bytes()})
	if err != nil || d != 1793664000 {
		t.Fatalf("TradeDate = %d, %v", d, err)
	}
}
