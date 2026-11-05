package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tcClient "github.com/kmlebedev/txmlconnector/client"
	tcCommands "github.com/kmlebedev/txmlconnector/client/commands"
	log "github.com/sirupsen/logrus"
)

func main() {
	client, err := tcClient.NewTCClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// RE-4 smoke sequence (task 29): with TC_RE4_SEQUENCE=1 the example issues
	// connect (done by NewTCClient) -> wait for connected status ->
	// get_securities -> subscribe -> disconnect against the native backend.
	if os.Getenv("TC_RE4_SEQUENCE") != "" {
		if err := re4Sequence(client); err != nil {
			// log.Fatal skips deferred calls: release the server session first,
			// or the backend keeps it and the next login gets code 4.
			_ = client.Disconnect()
			client.Close()
			log.Fatalf("RE-4 %v", err)
		}
		log.Info("RE-4 sequence: get_securities -> subscribe -> disconnect OK")
		return
	}
	defer func() {
		if err := client.Disconnect(); err != nil {
			log.Warnf("disconnect: %v", err)
		}
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)

	for {
		select {
		case status := <-client.ServerStatusChan:
			log.Infof("server status: %+v", status)
		case update := <-client.SecInfoUpdChan:
			log.Debugf("security update: %+v", update)
		case <-client.ShutdownChannel:
			log.Info("response stream finished")
			return
		case <-interrupt:
			return
		}
	}
}

func re4Sequence(client *tcClient.TCClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), real4ConnectTimeout())
	defer cancel()
	if err := waitConnected(ctx, client); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if err := client.SendCommand(tcCommands.Command{Id: "get_securities"}); err != nil {
		return fmt.Errorf("get_securities: %w", err)
	}
	if err := client.SendCommand(tcCommands.Command{
		Id:        "subscribe",
		AllTrades: tcCommands.SubAllTrades{Items: []int{26}},
	}); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	if err := client.Disconnect(); err != nil {
		return fmt.Errorf("disconnect: %w", err)
	}
	return nil
}

// real4ConnectTimeout bounds the asynchronous connect of the RE-4 sequence.
func real4ConnectTimeout() time.Duration {
	if raw := os.Getenv("TC_RE4_CONNECT_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return 60 * time.Second
}

// waitConnected waits for the native/backend <server_status connected="true"/>
// callback before issuing commands that require the connection (the connect
// command itself returns success immediately, DLL contract). The status follows
// the initial load, so the typed channels the load fills are drained meanwhile:
// the client dispatches messages in order and blocks on a full channel.
func waitConnected(ctx context.Context, client *tcClient.TCClient) error {
	for {
		select {
		case <-client.SecInfoUpdChan:
		case <-client.SecInfoChan:
		case <-client.AllTradesChan:
		case <-client.QuotesChan:
		case status := <-client.ServerStatusChan:
			switch strings.TrimSpace(status.Connected) {
			case "true":
				return nil
			case "error":
				return fmt.Errorf("server connected=error: %s", status.Message)
			}
		case <-client.ShutdownChannel:
			return fmt.Errorf("response stream finished before connected=true")
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for connected=true: %w", ctx.Err())
		}
	}
}
