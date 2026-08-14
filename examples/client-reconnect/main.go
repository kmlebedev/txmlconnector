package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/kmlebedev/txmlconnector/client/commands"
	log "github.com/sirupsen/logrus"
	"os"
	"time"

	tcClient "github.com/kmlebedev/txmlconnector/client"
)

type transaqSessionConfig struct {
	restore       func(*tcClient.TCClient) error
	eventHandlers transaqEventHandlers
}

type transaqEventHandlers struct {
	allTrades  func(context.Context, commands.AllTrades) error
	quotes     func(context.Context, commands.Quotes) error
	secInfo    func(context.Context, commands.SecInfo) error
	secInfoUpd func(context.Context, commands.SecInfoUpd) error
}

func main() {
	attempts := 0
	err := tcClient.RunWithReconnect(
		context.Background(),
		func() (*tcClient.TCClient, error) {
			attempts++
			switch attempts {
			case 1:
				return nil, errors.New("temporary factory failure")
			case 2:
				return nil, nil
			default:
				client, err := tcClient.NewTCClient()
				if err != nil {
					fmt.Fprintf(os.Stderr, "%+v", err)
				}
				return client, nil
			}
		},
		func(cxt context.Context, client *tcClient.TCClient) error {
			for {
				select {
				case status := <-client.ServerStatusChan:
					log.Infof("server status: %+v", status)
				case update := <-client.SecInfoUpdChan:
					log.Debugf("security update: %+v", update)
				case <-client.ShutdownChannel:
					log.Info("response stream finished")
				}
			}
			return nil
		},
		tcClient.ReconnectConfig{
			RetryMin:           time.Millisecond,
			RetryMax:           2 * time.Millisecond,
			SessionStableAfter: time.Hour,
			DisconnectTimeout:  time.Second,
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
}
