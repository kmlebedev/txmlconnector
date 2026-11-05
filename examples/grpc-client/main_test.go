package main

import (
	"context"
	"testing"
	"time"

	tcClient "github.com/kmlebedev/txmlconnector/client"
	"github.com/kmlebedev/txmlconnector/client/commands"
)

// TestWaitConnected covers the RE-4 asynchronous-connect wait: the command
// sequence must not run before <server_status connected="true"/>.
func TestWaitConnected(t *testing.T) {
	t.Run("connected", func(t *testing.T) {
		c := &tcClient.TCClient{
			ServerStatusChan: make(chan commands.ServerStatus, 2),
			ShutdownChannel:  make(chan bool, 1),
		}
		c.ServerStatusChan <- commands.ServerStatus{Connected: "true", Recover: "true"}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := waitConnected(ctx, c); err != nil {
			t.Fatalf("waitConnected = %v", err)
		}
	})
	t.Run("error-status", func(t *testing.T) {
		c := &tcClient.TCClient{
			ServerStatusChan: make(chan commands.ServerStatus, 1),
			ShutdownChannel:  make(chan bool, 1),
		}
		c.ServerStatusChan <- commands.ServerStatus{Connected: "error", Message: "blocked"}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := waitConnected(ctx, c); err == nil {
			t.Fatal("connected=error accepted")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		c := &tcClient.TCClient{
			ServerStatusChan: make(chan commands.ServerStatus),
			ShutdownChannel:  make(chan bool),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if err := waitConnected(ctx, c); err == nil {
			t.Fatal("timeout accepted")
		}
	})
}
