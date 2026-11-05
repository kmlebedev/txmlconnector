package main

import (
	"os"
	"os/signal"
	"syscall"

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
	defer func() {
		if err := client.Disconnect(); err != nil {
			log.Warnf("disconnect: %v", err)
		}
	}()

	// RE-4 smoke sequence (task 29): with TC_RE4_SEQUENCE=1 the example issues
	// connect (done by NewTCClient) -> get_securities -> subscribe -> disconnect
	// against the native backend, then exits.
	if os.Getenv("TC_RE4_SEQUENCE") != "" {
		if err := client.SendCommand(tcCommands.Command{Id: "get_securities"}); err != nil {
			log.Fatalf("RE-4 get_securities: %v", err)
		}
		if err := client.SendCommand(tcCommands.Command{
			Id:        "subscribe",
			AllTrades: tcCommands.SubAllTrades{Items: []int{26}},
		}); err != nil {
			log.Fatalf("RE-4 subscribe: %v", err)
		}
		if err := client.Disconnect(); err != nil {
			log.Fatalf("RE-4 disconnect: %v", err)
		}
		log.Info("RE-4 sequence: get_securities -> subscribe -> disconnect OK")
		return
	}

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
