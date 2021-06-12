package main

import (
	"context"
	"encoding/xml"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"strings"

	//"go.oneofone.dev/ta"
	"io"
	"os"

	. "github.com/kmlebedev/txmlconnector/client/commands"
	pb "github.com/kmlebedev/txmlconnector/proto"
)

var (
	// Todo Mutex
	client           = Client{}
	serverStatus     = ServerStatus{}
	markets          = Markets{}
	boards           = Boards{}
	candleKinds      = CandleKinds{}
	candles          = Candles{}
	securities       = Securities{}
	pits             = Pits{}
	positions        = Positions{}
	secInfoUpd       = SecInfoUpd{}
	messages         = Messages{}
	unions           []Union
	secInfoUpdChan   = make(chan SecInfoUpd)
	serverStatusChan = make(chan ServerStatus)
)

func init() {
	ll := log.InfoLevel
	if lvl, ok := os.LookupEnv("TC_LOG_LEVEL"); ok {
		if level, err := log.ParseLevel(lvl); err == nil {
			ll = level
		}
	}
	log.SetLevel(ll)
}

func main() {
	log.Println("Client running ...")
	conn, err := grpc.Dial(
		":50051",
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	)
	if err != nil {
		log.Fatalln("grpc.Dial()", err)
	}
	defer conn.Close()

	client := pb.NewConnectServiceClient(conn)
	connectReq := Connect{
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
	}
	request := &pb.SendCommandRequest{Message: EncodeRequest(connectReq)}

	ctx := context.Background()
	response, err := client.SendCommand(ctx, request)
	if err != nil {
		log.Error("SendCommand: ", err)
	}
	defer client.SendCommand(ctx, &pb.SendCommandRequest{Message: EncodeRequest(Command{Id: "disconnect"})})
	result := Result{}
	if err := xml.Unmarshal([]byte(response.GetMessage()), &result); err != nil {
		log.Error("Unmarshal(Result) ", err, response.GetMessage())
	}
	if result.Success != "true" {
		log.Error("Result: ", result.Message)
	}

	stream, err := client.FetchResponseData(ctx, &pb.DataRequest{})
	if err != nil {
		log.Fatalf("open stream error %v", err)
	}
	done := make(chan bool)
	go LoopReadingFromStream(&stream, &done)

	go func() {
		for {
			select {
			case status := <-serverStatusChan:
				log.Infof("Status %v", status)
			case upd := <-secInfoUpdChan:
				log.Debugf("secInfoUpd %v", upd)
			}
		}
	}()

	<-done //we will wait until all response is received
	log.Info("Loop stream finished")
}

func LoopReadingFromStream(stream *pb.ConnectService_FetchResponseDataClient, done *chan bool) {
	for {
		resp, err := (*stream).Recv()
		if err == io.EOF {
			log.Debug("Resp received: %s", resp.Message)
			*done <- true //means stream is finished
			return
		}
		if err != nil {
			log.Panicf("stream.Recv() cannot receive %v", err)
		}
		xmlReader := strings.NewReader(resp.Message)
		decoder := xml.NewDecoder(xmlReader)
		token, err := decoder.Token()
		if err != nil {
			log.Error("Decode Token ", err)
			continue
		}
		startElement := token.(xml.StartElement)
		msgData := []byte(resp.Message)

		switch startElement.Name.Local {
		case "sec_info_upd":
			if err := xml.Unmarshal(msgData, &secInfoUpd); err != nil {
				log.Error(err)
			}
			secInfoUpdChan <- secInfoUpd
		case "server_status":
			if err := xml.Unmarshal(msgData, &serverStatus); err != nil {
				log.Error("Decode serverStatus ", err, resp.Message)
			}
			serverStatusChan <- serverStatus
		case "client":
			if err := xml.Unmarshal(msgData, &client); err != nil {
				log.Error("Decode client ", err, resp.Message)
			}
		case "markets":
			if err := xml.Unmarshal(msgData, &markets); err != nil {
				log.Error("Decode markets ", err, " msg:", resp.Message)
			}
		case "boards":
			if err := xml.Unmarshal(msgData, &boards); err != nil {
				log.Error("Decode boards ", err, " msg:", resp.Message)
			}
		case "candles":
			if err := xml.Unmarshal(msgData, &candles); err != nil {
				log.Error("Decode candles ", err, " msg:", resp.Message)
			}
		case "candlekinds":
			if err := xml.Unmarshal(msgData, &candleKinds); err != nil {
				log.Error("Decode candlekinds ", err, " msg:", resp.Message)
			}
		case "securities":
			if err := xml.Unmarshal(msgData, &securities); err != nil {
				log.Error("Decode securities ", err, " msg:", resp.Message)
			}
		case "pits":
			if err := xml.Unmarshal(msgData, &pits); err != nil {
				log.Error("Decode pits ", err, " msg:", resp.Message)
			}
		case "positions":
			if err := xml.Unmarshal(msgData, &positions); err != nil {
				log.Error("Decode positions ", err, " msg:", resp.Message)
			}
		case "messages":
			if err := xml.Unmarshal(msgData, &messages); err != nil {
				log.Error("Decode messages ", err, " msg:", resp.Message)
			}
			log.Info("Message: ", messages)
		case "union":
			union := Union{}
			if err := xml.Unmarshal(msgData, &union); err != nil {
				log.Error("Decode union ", err, " msg:", resp.Message)
			}
			unions = append(unions, union)
		case "overnight":
			overnight := Overnight{}
			if err := xml.Unmarshal(msgData, &overnight); err != nil {
				log.Error("Decode overnight ", err, " msg:", resp.Message)
			}
		default:
			log.Warnf("Received unknown msg: %s", resp.Message)
		}
	}
}
