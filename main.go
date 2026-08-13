package main

import (
	"github.com/kmlebedev/txmlconnector/server"
	log "github.com/sirupsen/logrus"
)

func main() {
	if err := tcServer.Run(); err != nil {
		log.Fatal(err)
	}
}
