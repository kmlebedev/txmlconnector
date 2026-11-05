package records_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
)

// TestRPSLayoutExplicitI: the RPS/REPO record/XML layouts are not recovered, so
// the package exposes the explicit I error rather than a fabricated parser.
func TestRPSLayoutExplicitI(t *testing.T) {
	if !errors.Is(records.ErrRPSLayout, records.ErrRPSLayout) {
		t.Fatal("ErrRPSLayout is not usable with errors.Is")
	}
	if records.ErrRPSLayout == nil || !strings.Contains(records.ErrRPSLayout.Error(), "not recovered (I)") {
		t.Fatalf("ErrRPSLayout = %v", records.ErrRPSLayout)
	}
}
