package session_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
)

// TestRestoreCloseRace is the P2-2 regression: Close must read the socket under
// the same lock Restore swaps it with. A Restore loop and a continuous Close loop
// race; run with -race.
func TestRestoreCloseRace(t *testing.T) {
	first := testserver.New(t)
	second := testserver.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nc, err := first.Dial(ctx, "testserver:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := session.Login(ctx, nc, session.Config{
		Login: testserver.Login, Password: testserver.Password,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	first.Drop()

	const restores = 100
	var wg sync.WaitGroup
	restoreDone := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer close(restoreDone)
		for i := 0; i < restores; i++ {
			if ctx.Err() != nil {
				return
			}
			nc, err := second.Dial(ctx, "testserver:0")
			if err != nil {
				continue
			}
			_ = conn.Restore(ctx, nc, testserver.Login)
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-restoreDone:
				return
			default:
				_ = conn.Close()
			}
		}
	}()
	wg.Wait()
	_ = conn.Close()
}
