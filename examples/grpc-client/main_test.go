package main

import (
	"context"
	"testing"
	"time"

	tcCommands "github.com/kmlebedev/txmlconnector-native/commands"
	tcClient "github.com/kmlebedev/txmlconnector/client"
)

// The connected status comes after the initial load, which floods the typed
// channels (hundreds of <sec_info_upd> on the live servers). waitConnected must
// keep them drained, or the client's dispatcher blocks on a full channel and
// the status never arrives (RE-4 timeout on demo, 2026-11-05).
func TestWaitConnectedDrainsLoadUpdates(t *testing.T) {
	c := &tcClient.TCClient{
		ServerStatusChan: make(chan tcCommands.ServerStatus, 16),
		SecInfoUpdChan:   make(chan tcCommands.SecInfoUpd, 16),
		SecInfoChan:      make(chan tcCommands.SecInfo, 16),
		AllTradesChan:    make(chan tcCommands.AllTrades, 16),
		QuotesChan:       make(chan tcCommands.Quotes, 16),
		ShutdownChannel:  make(chan bool, 1),
	}
	go func() { // the client's single dispatcher: blocking sends in order
		for i := 0; i < 500; i++ {
			c.SecInfoUpdChan <- tcCommands.SecInfoUpd{}
		}
		c.ServerStatusChan <- tcCommands.ServerStatus{Connected: "true"}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitConnected(ctx, c); err != nil {
		t.Fatal(err)
	}
}
