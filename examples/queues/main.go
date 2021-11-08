package main

import (
	"context"
	"encoding/xml"
	"fmt"
	log "github.com/golang/glog"
	"github.com/streadway/amqp"
	"os"
	"strings"
	"time"

	. "github.com/kmlebedev/txmlconnector/client/commands"
	"github.com/kmlebedev/txmlconnector/server"
	"gocloud.dev/pubsub"
	_ "gocloud.dev/pubsub/rabbitpubsub"
)

var (
	topic        = &pubsub.Topic{}
	ctx          = context.Background()
	queue        = "txmlconnector"
	quotations   = []SubSecurity{}
	serverStatus = ServerStatus{}
	securities   = Securities{}
	isSubscribed = false
)

func queueDeclareAndBind() error {
	var conn *amqp.Connection
	if !topic.As(&conn) {
		return nil
	}
	ch, _ := conn.Channel()
	defer ch.Close()
	_, err := ch.QueueInspect(queue)
	if err != nil {
		if strings.HasPrefix(err.Error(), "Exception (404) Reason") {
			ch, _ := conn.Channel()
			defer ch.Close()
			if err := ch.ExchangeDeclare(
				"DLX."+queue, "fanout", false, false, false, false, nil); err != nil {
				log.Error(err)
				return err
			}
			if err := ch.ExchangeDeclare(
				queue, "fanout", false, false, false, false, nil); err != nil {
				log.Error(err)
				return err
			}
			if _, err := ch.QueueDeclare(
				queue, false, false, false, false,
				amqp.Table{"x-dead-letter-exchange": "DLX." + queue}); err != nil {
				log.Error(err)
				return err
			}
			if err := ch.QueueBind(queue, "", queue, false, nil); err != nil {
				log.Error(err)
				return err
			}
			if _, err := ch.QueueDeclare(
				"DLX."+queue, false, false, false, false,
				amqp.Table{"x-dead-letter-exchange": queue, "x-message-ttl": 600000}); err != nil {
				log.Error(err)
				return err
			}
			if err := ch.QueueBind("DLX."+queue, "", "DLX."+queue, false, nil); err != nil {
				log.Error(err)
				return err
			}
		} else {
			log.Error(err)
			return err
		}
	}
	return nil
}

func init() {
	if q := os.Getenv("QUEUE_NAME"); len(q) > 0 {
		queue = q
	}
	ctx := context.Background()
	var err error
	topic, err = pubsub.OpenTopic(ctx, "rabbit://"+queue)
	if err != nil {
		log.Fatal(err)
	}
	if err := queueDeclareAndBind(); err != nil {
		log.Fatal(err)
	}
}

func sendCmd(cmd interface{}) error {
	response := tcServer.TxmlSendCommand(EncodeRequest(cmd))
	result := Result{}
	if err := xml.Unmarshal([]byte(*response), &result); err != nil {
		return err
	}
	if result.Success != "true" {
		return fmt.Errorf(result.Message)
	}
	return nil
}

func main() {
	queueSecCodes := strings.Split(os.Getenv("QUEUE_SEC_CODES"), ",")
	defer topic.Shutdown(ctx)
	if err := sendCmd(Connect{
		Id:             "connect",
		Login:          os.Getenv("TC_LOGIN"),
		Password:       os.Getenv("TC_PASSWORD"),
		Host:           os.Getenv("TC_HOST"),
		Port:           os.Getenv("TC_PORT"),
		SessionTimeout: 60,
		RequestTimeout: 10,
		Rqdelay:        1000,
		PushUlimits:    30,
		PushPosEquity:  30,
	}); err != nil {
		log.Fatal(err)
	}
	defer tcServer.TxmlSendCommand(EncodeRequest(Command{Id: "disconnect"}))
	ticker := time.NewTicker(10 * time.Second)
	for {
		select {
		case msg := <-tcServer.Messages:
			xmlReader := strings.NewReader(msg)
			decoder := xml.NewDecoder(xmlReader)
			token, err := decoder.Token()
			if err != nil {
				log.Error("Decode Token ", err)
				continue
			}
			startElement := token.(xml.StartElement)
			msgData := []byte(msg)
			switch startElement.Name.Local {
			case "server_status":
				if err := xml.Unmarshal(msgData, &serverStatus); err != nil {
					log.Error("Decode serverStatus ", err, msg)
				}
				if len(queueSecCodes) == 0 || len(securities.Items) == 0 || serverStatus.Connected != "true" {
					continue
				}
				for _, sec := range securities.Items {
					for _, queueSecCode := range queueSecCodes {
						if queueSecCode == sec.SecCode ||
							strings.Contains(sec.SecCode, queueSecCode) ||
							queueSecCode == sec.ShortName || queueSecCode == "ALL" {
							secFound := false
							for _, quotation := range quotations {
								if quotation.SecId == sec.SecId {
									secFound = true
								}
							}
							if !secFound {
								quotations = append(quotations, SubSecurity{SecId: sec.SecId})
							}
						}
					}
				}
				if len(quotations) == 0 {
					continue
				}
				if !isSubscribed {
					if err := sendCmd(Command{Id: "subscribe", Quotations: quotations}); err != nil {
						log.Errorf("SendCommand: ", err)
					} else {
						isSubscribed = true
					}
				}
			case "securities":
				if err := xml.Unmarshal(msgData, &securities); err != nil {
					log.Error("Decode securities ", err, " msg: ", msg)
				}
			case "quotations":
				if err := topic.Send(ctx, &pubsub.Message{
					Body: msgData,
					Metadata: map[string]string{
						"name": startElement.Name.Local,
					},
				}); err != nil {
					log.Error("Sending quotations %s", err)
				}
			case "sec_info_upd":
			default:
				log.Warningf("skip msg: %s", startElement.Name.Local)
			}
		case <-ctx.Done():
			fmt.Println("Loop done", ctx.Err())
			break
		case done := <-tcServer.Done:
			if done {
				fmt.Println("Stop loop")
			}
			break
		case t := <-ticker.C:
			log.Infoln("no message received, Tick at", t)
			if !isSubscribed {
				if err := sendCmd(Command{Id: "server_status"}); err != nil {
					log.Error(err)
				}
			}
		}
	}
}
