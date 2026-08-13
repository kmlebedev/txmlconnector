//go:build windows && amd64

package tcServer

import (
	"context"
	"sync"

	"github.com/kmlebedev/txmlconnector/connector"
	log "github.com/sirupsen/logrus"
)

// Deprecated: use connector.Connector with an explicit lifecycle.
var Messages = make(chan string, defaultSubscriberBuffer)

// Deprecated: process shutdown is now expressed through context cancellation.
var Done = make(chan bool, 1)

var legacyNativeMu sync.Mutex

var legacyConnector struct {
	native connector.Connector
}

// TxmlSendCommand preserves the pre-v2 API while delegating to the new native
// adapter. New code should use connector.Connector.SendCommand to retain errors.
//
// Deprecated: use connector.Connector.SendCommand.
func TxmlSendCommand(message string) *string {
	legacyNativeMu.Lock()
	defer legacyNativeMu.Unlock()
	if legacyConnector.native == nil {
		native, err := connector.New(connector.ConfigFromEnv())
		if err != nil {
			log.Errorf("create legacy connector: %v", err)
			return nil
		}
		if err := native.Start(func(message string) {
			select {
			case Messages <- message:
			default:
				log.Warn("legacy message channel is full; dropping connector message")
			}
		}); err != nil {
			log.Errorf("start legacy connector: %v", err)
			return nil
		}
		legacyConnector.native = native
	}
	response, err := legacyConnector.native.SendCommand(context.Background(), message)
	if err != nil {
		log.Errorf("send legacy command: %v", err)
		return nil
	}
	return &response
}
