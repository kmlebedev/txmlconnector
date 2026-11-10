package native

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/testserver"
)

// TestRunReportsSecuritiesAdded: after a night restart the broker sends the
// catalog in sections, and R_SEC_NEW keeps arriving after Dial. Run reports
// those securities as one model.SecuritiesAdded per RQ_MAIN reply, ahead of
// the reply's other events (the DLL emits <securities> first), and never
// repeats the securities Dial already loaded.
func TestRunReportsSecuritiesAdded(t *testing.T) {
	srv := newRunServer(t)
	srv.AfterSubscribe = mainPayload(secNewRecord(t, "GAZP", 60000))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testserver.Login, Password: testserver.Password, Dial: srv.Dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	loaded := c.Catalog().Len()
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	added := make(chan *model.SecuritiesAdded, 4)
	runCtx, runCancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		errc <- c.Run(runCtx, RunOptions{RqDelay: 10 * time.Millisecond}, func(events []model.Event) error {
			for i, ev := range events {
				if a, ok := ev.(*model.SecuritiesAdded); ok {
					if i != 0 {
						t.Errorf("SecuritiesAdded at %d, want first in the batch", i)
					}
					added <- a
				}
			}
			return nil
		})
	}()

	var got *model.SecuritiesAdded
	select {
	case got = <-added:
	case <-time.After(5 * time.Second):
		t.Fatal("Run reported no SecuritiesAdded for the R_SEC_NEW after Dial")
	}
	if len(got.Items) != 1 || got.Items[0].SecID != 60000 || got.Items[0].SecCode != "GAZP" {
		t.Fatalf("SecuritiesAdded = %+v, want only GAZP with secid 60000 (Dial loaded %d already)", got.Items, loaded)
	}
	if c.Catalog().Len() != loaded+1 {
		t.Fatalf("catalog len = %d, want %d", c.Catalog().Len(), loaded+1)
	}

	// Further polls carry no new R_SEC_NEW: no second event.
	select {
	case again := <-added:
		t.Fatalf("second SecuritiesAdded without new records: %+v", again.Items)
	case <-time.After(100 * time.Millisecond):
	}
	runCancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
}
