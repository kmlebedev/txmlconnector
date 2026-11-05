package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestConcurrentStatusSubscribeAndAutonomy exercises the race detector across
// the autonomy goroutine, Status/Backtests/Hints readers and SSE subscribers.
func TestConcurrentStatusSubscribeAndAutonomy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.stepDelay = func(float64) time.Duration { return time.Millisecond }
	if err := e.StartAutonomy(context.Background(), AutonomyConfig{
		Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
	}); err != nil {
		t.Fatalf("StartAutonomy: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				_ = e.Status()
				_ = e.Backtests()
				if _, err := e.Hints(context.Background(), "TQBR:SBER", 300); err != nil {
					t.Errorf("Hints: %v", err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 15; j++ {
			cancel := e.Subscribe(func(Event) {})
			time.Sleep(time.Millisecond)
			cancel()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := e.Chat(context.Background(), ChatRequest{Message: "протестируй SBER 5м"}); err != nil {
			t.Errorf("Chat: %v", err)
		}
	}()
	wg.Wait()

	if err := e.StopAutonomy(); err != nil {
		t.Fatalf("StopAutonomy: %v", err)
	}
	if e.Status().Autonomy.Running {
		t.Error("autonomy still running")
	}
}

// TestConcurrentStopAndStart drives the kill-switch from several goroutines and
// starts a new run afterwards.
func TestConcurrentStopAndStart(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.stepDelay = func(float64) time.Duration { return time.Millisecond }
	for round := 0; round < 2; round++ {
		if err := e.StartAutonomy(context.Background(), AutonomyConfig{
			Symbol: "TQBR:SBER", PeriodSec: 300, Strategy: "sma_cross", Speed: 60, Budget: 100_000,
		}); err != nil {
			t.Fatalf("StartAutonomy round %d: %v", round, err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := e.StopAutonomy(); err != nil {
					t.Errorf("StopAutonomy: %v", err)
				}
			}()
		}
		wg.Wait()
		if e.Status().Autonomy.Running {
			t.Fatalf("round %d: autonomy still running", round)
		}
	}
}

// TestConcurrentChats runs the deterministic local analyst in parallel.
func TestConcurrentChats(t *testing.T) {
	e, _, _ := newTestEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Chat(context.Background(), ChatRequest{Message: "проанализируй SBER 5м"}); err != nil {
				t.Errorf("Chat: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := len(e.Backtests()); got < 4 {
		t.Errorf("Backtests() = %d, want at least 4 recorded runs", got)
	}
}

// TestConcurrentSubscribeWhilePublishing checks the subscriber map under load.
func TestConcurrentSubscribeWhilePublishing(t *testing.T) {
	e, _, _ := newTestEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				cancel := e.Subscribe(func(Event) {})
				_, _ = e.executeToolCall(context.Background(), "list_instruments", nil)
				cancel()
			}
		}()
	}
	wg.Wait()
}
