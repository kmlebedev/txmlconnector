package main

import (
	"os"
	"os/signal"
	"syscall"

	tcClient "github.com/kmlebedev/txmlconnector/client"
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
