package records_test

import (
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

func familyRecord(secID uint16, code string, first, second int64) []byte {
	var w wire.Writer
	w.Uint16(secID)
	w.String(code, 6)
	w.Int32(int32(first))
	w.Int32(int32(second))
	return append([]byte{records.TagFamily}, w.Bytes()...)
}

func strikesRecord(secID uint16, familyID int64) []byte {
	var w wire.Writer
	w.Uint16(secID)
	w.Int32(int32(familyID))
	w.String("OPT1", 26)
	w.Byte('C')
	w.Int32(100)
	w.String("", 26) // end of strikes
	w.Byte(1)
	return append([]byte{records.TagStrikes}, w.Bytes()...)
}

func endOfFamilyRecord(secID uint16, code string) []byte {
	var w wire.Writer
	w.Uint16(secID)
	w.String(code, 6)
	w.Int32(4)
	w.String("bad option", 512)
	return append([]byte{records.TagEndOfFamily}, w.Bytes()...)
}

// TestOptionsRecordsSplit: the unsized 0x6f/0x71/0x78 records split and decode.
func TestOptionsRecordsSplit(t *testing.T) {
	stream := append(familyRecord(26, "SBER", 0, 1), strikesRecord(26, 5)...)
	stream = append(stream, endOfFamilyRecord(26, "OPT1")...)
	recs, err := records.Records(stream)
	if err != nil || len(recs) != 3 {
		t.Fatalf("records = %+v, %v", recs, err)
	}
	family, err := records.ParseFamily(recs[0].Body)
	if err != nil || family.SecID != 26 || family.OptCode != "SBER" || family.Second != 1 {
		t.Fatalf("family = %+v, %v", family, err)
	}
	strikes, err := records.ParseStrikes(recs[1].Body)
	if err != nil || strikes.SecID != 26 || strikes.FamilyID != 5 || len(strikes.Items) != 1 {
		t.Fatalf("strikes = %+v, %v", strikes, err)
	}
	if strikes.Items[0] != (records.StrikeItem{OptCode: "OPT1", CP: 'C', Price: 100}) {
		t.Fatalf("strike = %+v", strikes.Items[0])
	}
	end, err := records.ParseEndOfFamily(recs[2].Body)
	if err != nil || end.Text != "bad option" || end.Code != 4 {
		t.Fatalf("end = %+v, %v", end, err)
	}
}

// TestOptionsParseNegatives: short/truncated bodies are explicit errors.
func TestOptionsParseNegatives(t *testing.T) {
	if _, err := records.ParseFamily(nil); err == nil {
		t.Fatal("empty family body accepted")
	}
	if _, err := records.ParseStrikes([]byte{0x00, 0x1a}); err == nil {
		t.Fatal("short strikes body accepted")
	}
	if _, err := records.ParseEndOfFamily([]byte{0x00, 0x1a}); err == nil {
		t.Fatal("short end body accepted")
	}
}
