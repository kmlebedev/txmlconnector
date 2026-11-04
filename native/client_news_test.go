package native

import (
	"context"
	"strings"
	"testing"
)

// TestNewsInterfacesAreExplicitI: RQ_GETNEWS/R_NEWS layouts are not recovered
// yet, so the T24 Client methods return the explicit error (process.md I).
func TestNewsInterfacesAreExplicitI(t *testing.T) {
	c := &Client{}
	if err := c.OldNews(context.Background(), 10); err == nil || !strings.Contains(err.Error(), "layout not recovered (I)") {
		t.Fatalf("OldNews err = %v", err)
	}
	if _, err := c.NewsBody(context.Background(), 42); err == nil || !strings.Contains(err.Error(), "layout not recovered (I)") {
		t.Fatalf("NewsBody err = %v", err)
	}
}
