package records_test

import (
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestParseSecMktPermit pins the R_SEC_MKT_PERMIT (0x0e) body: W secid, B val.
func TestParseSecMktPermit(t *testing.T) {
	var w wire.Writer
	w.Uint16(26)
	w.Byte(1)
	permit, err := records.ParseSecMktPermit(w.Bytes())
	if err != nil {
		t.Fatalf("ParseSecMktPermit: %v", err)
	}
	if permit.SecID != 26 || !permit.Permit {
		t.Fatalf("permit = %+v", permit)
	}
	if _, err := records.ParseSecMktPermit([]byte{0x00}); err == nil {
		t.Fatal("short body accepted")
	}
}

// TestParseMarginPosReply pins the reachable D branch of RQ_MARGIN_POS (84):
// G,G,W,W,W,G,G,G,G,W,W,W,W,B, then optional G values while the flag is set.
func TestParseMarginPosReply(t *testing.T) {
	var w wire.Writer
	w.Int64(1)
	w.Int64(2)
	for i := 0; i < 3; i++ {
		w.Uint16(uint16(10 + i))
	}
	for i := 0; i < 4; i++ {
		w.Int64(int64(20 + i))
	}
	for i := 0; i < 4; i++ {
		w.Uint16(uint16(30 + i))
	}
	w.Byte(1)
	for i := 0; i < 7; i++ {
		w.Int64(int64(40 + i))
	}
	pos, err := records.ParseMarginPosReply(w.Bytes())
	if err != nil {
		t.Fatalf("ParseMarginPosReply: %v", err)
	}
	if pos.G1 != 1 || pos.B1 != 1 || len(pos.Extra) != 7 || pos.Extra[6] != 46 {
		t.Fatalf("pos = %+v", pos)
	}
}
