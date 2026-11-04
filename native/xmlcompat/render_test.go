package xmlcompat_test

import (
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// TestRenderAllTradesThroughRender checks the single Render entry point against
// the DLL <alltrades> callback: all R_ALLTRADE records of one RQ_MAIN reply
// become one batched message (testdata/alltrades-dll1.xml, milliseconds=true).
//
// VTBR 889 is not in the 499-instrument main1-demo snapshot, but the DLL knew
// it in the recorded session (the fixture prints seccode VTBR and price 0.098,
// i.e. decimals 3), so the test seeds it explicitly - the knowledge the old
// oracleCatalog helper held.
func TestRenderAllTradesThroughRender(t *testing.T) {
	oracle.CheckDLL(t, "main1-demo.hex.gz", "alltrades-inject1.hex", "alltrades-dll1.xml",
		records.SecNew{SecID: 889, SecCode: "VTBR", Pits: []records.Pit{{Board: 37, Decimals: 3, Default: true}}})
}
