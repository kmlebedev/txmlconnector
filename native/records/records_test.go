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

func TestTradeDate(t *testing.T) {
	var w wire.Writer
	w.Int32(1793664000)
	d, err := TradeDate(Record{Tag: TagDate, Body: w.Bytes()})
	if err != nil || d != 1793664000 {
		t.Fatalf("TradeDate = %d, %v", d, err)
	}
}
