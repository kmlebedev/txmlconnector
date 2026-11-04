package records_test

import (
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestParseFondPortfolioPreliminary pins the preliminary D layout of the
// RQ_FOND_PORTFOLIO (86) reply: 2 packed-G + 7 W (docs/protocol/portfolios.md,
// I; the field meanings are not recovered).
func TestParseFondPortfolioPreliminary(t *testing.T) {
	var w wire.Writer
	w.Int64(11)
	w.Int64(22)
	for i := 0; i < 7; i++ {
		w.Uint16(uint16(100 + i))
	}
	port, err := records.ParseFondPortfolio(w.Bytes())
	if err != nil {
		t.Fatalf("ParseFondPortfolio: %v", err)
	}
	if port.RawG != [2]int64{11, 22} {
		t.Fatalf("RawG = %v", port.RawG)
	}
	for i := 0; i < 7; i++ {
		if port.RawW[i] != uint16(100+i) {
			t.Fatalf("RawW[%d] = %d", i, port.RawW[i])
		}
	}
}

// TestParseMarginPosList pins 85 = 7 × packed-G (D, I).
func TestParseMarginPosList(t *testing.T) {
	var w wire.Writer
	for i := 0; i < 7; i++ {
		w.Int64(int64((i + 1) * 10))
	}
	values, err := records.ParseMarginPosList(w.Bytes())
	if err != nil {
		t.Fatalf("ParseMarginPosList: %v", err)
	}
	if len(values) != 7 || values[6] != 70 {
		t.Fatalf("values = %v", values)
	}
}

// TestParseMarginPosPreliminary pins the ~19 preliminary fields of the
// RQ_MARGIN_POS (84) reply (D; the field meanings are I).
func TestParseMarginPosPreliminary(t *testing.T) {
	var w wire.Writer
	w.Int64(1)  // G+8
	w.Int64(2)  // G+0x10
	w.Uint16(3) // W+0x18
	w.Uint16(4) // W+0x40
	w.Uint16(5) // W+0x1a
	w.Int64(6)  // G+0x20
	w.Int64(7)  // G+0x28
	w.Int64(8)  // G+0x30
	w.Int64(9)  // G+0x38
	w.Uint16(10)
	w.Uint16(11)
	w.Uint16(12)
	w.Uint16(13)
	w.Byte(14) // B+0x88
	w.Int64(15)
	w.Int64(16)
	w.Int64(17)
	w.Raw([]byte{19, 20})
	pos, err := records.ParseMarginPos(w.Bytes())
	if err != nil {
		t.Fatalf("ParseMarginPos: %v", err)
	}
	if pos.G1 != 1 || pos.G9 != 17 || len(pos.RawTail) != 2 {
		t.Fatalf("pos = %+v", pos)
	}
}
