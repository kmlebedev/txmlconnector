package tcClient

import (
	"context"
	"encoding/xml"
	"fmt"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"strings"

	"io"
	"os"

	. "github.com/kmlebedev/txmlconnector/client/commands"
	pb "github.com/kmlebedev/txmlconnector/proto"
)

type TCClient struct {
	Client pb.ConnectServiceClient
	Data   struct {
		Client          Client
		ServerStatus    ServerStatus
		Markets         Markets
		Boards          Boards
		Candlekinds     Candlekinds
		Securities      Securities
		Pits            Pits
		Positions       Positions
		UnitedPortfolio *UnitedPortfolio
		UnitedEquity    UnitedEquity
		SecInfoUpd      SecInfoUpd
		NewsHeader      NewsHeader
		Messages        Messages
		Unions          []Union
	}
	SecInfoUpdChan   chan SecInfoUpd
	ServerStatusChan chan ServerStatus
	ResponseChannel  chan string
	ShutdownChannel  chan bool
}

func init() {
	ll := log.InfoLevel
	if lvl, ok := os.LookupEnv("TC_LOG_LEVEL"); ok {
		if level, err := log.ParseLevel(lvl); err == nil {
			ll = level
		}
	}
	log.SetLevel(ll)
}

func NewTCClientWithConn(client pb.ConnectServiceClient) (*TCClient, error) {
	tc := TCClient{
		Client:           client,
		SecInfoUpdChan:   make(chan SecInfoUpd),
		ServerStatusChan: make(chan ServerStatus),
		ShutdownChannel:  make(chan bool),
		ResponseChannel:  make(chan string),
	}

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
		return nil, err
	}

	result := Result{}
	if err := xml.Unmarshal([]byte(response.GetMessage()), &result); err != nil {
		log.Error("Unmarshal(Result) ", err, response.GetMessage())
		return nil, err
	}
	if result.Success != "true" {
		log.Error("Result: ", result.Message)
		return nil, fmt.Errorf("Result not success: %s", result.Message)
	}

	stream, err := client.FetchResponseData(ctx, &pb.DataRequest{})
	if err != nil {
		log.Errorf("open stream error %v", err)
		return nil, err
	}
	go tc.LoopReadingFromStream(&stream)

	return &tc, nil
}

func NewTCClient() (*TCClient, error) {
	log.Infoln("Client running ...")
	conn, err := grpc.Dial(
		os.Getenv("TC_TARGET"),
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	)
	if err != nil {
		log.Error("grpc.Dial()", err)
		return nil, err
	}
	// Todo move Close
	// defer conn.Close()
	client := pb.NewConnectServiceClient(conn)
	return NewTCClientWithConn(client)
}

func (tc *TCClient) Disconnect() (*pb.SendCommandResponse, error) {
	return tc.SendCommand(Command{Id: "disconnect"})
}

func (tc *TCClient) SendCommand(cmd Command) (*pb.SendCommandResponse, error) {
	return tc.Client.SendCommand(context.Background(),
		&pb.SendCommandRequest{Message: EncodeRequest(cmd)},
	)
}

func (tc *TCClient) LoopReadingFromStream(stream *pb.ConnectService_FetchResponseDataClient) {
	for {
		resp, err := (*stream).Recv()
		if err == io.EOF {
			log.Debug("Resp received: %s", resp.Message)
			tc.ShutdownChannel <- true //means stream is finished
			return
		}
		if err != nil {
			//Todo reconnect
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
			if err := xml.Unmarshal(msgData, &tc.Data.SecInfoUpd); err != nil {
				log.Error(err)
			}
			// tc.SecInfoUpdChan <- tc.Data.SecInfoUpd
		case "server_status":
			if err := xml.Unmarshal(msgData, &tc.Data.ServerStatus); err != nil {
				log.Error("Decode serverStatus ", err, resp.Message)
			}
			tc.ServerStatusChan <- tc.Data.ServerStatus
		case "client":
			if err := xml.Unmarshal(msgData, &tc.Data.Client); err != nil {
				log.Error("Decode client ", err, resp.Message)
			}
		case "markets":
			if err := xml.Unmarshal(msgData, &tc.Data.Markets); err != nil {
				log.Error("Decode markets ", err, " msg:", resp.Message)
			}
		case "boards":
			if err := xml.Unmarshal(msgData, &tc.Data.Boards); err != nil {
				log.Error("Decode boards ", err, " msg:", resp.Message)
			}
		case "candlekinds":
			if err := xml.Unmarshal(msgData, &tc.Data.Candlekinds); err != nil {
				log.Error("Decode candlekinds ", err, " msg:", resp.Message)
			}
		case "securities":
			if err := xml.Unmarshal(msgData, &tc.Data.Securities); err != nil {
				log.Error("Decode securities ", err, " msg:", resp.Message)
			}
		case "pits":
			if err := xml.Unmarshal(msgData, &tc.Data.Pits); err != nil {
				log.Error("Decode pits ", err, " msg:", resp.Message)
			}
		case "positions":
			if err := xml.Unmarshal(msgData, &tc.Data.Positions); err != nil {
				log.Error("Decode positions ", err, " msg:", resp.Message)
			}
			log.Debug(resp.Message)
		case "united_portfolio":
			data := UnitedPortfolio{}
			if err := xml.Unmarshal(msgData, &data); err != nil {
				log.Error("Decode positions ", err, " msg:", resp.Message)
			} else {
				tc.Data.UnitedPortfolio = &data
				tc.ResponseChannel <- startElement.Name.Local
			}
		case "united_equity":
			if err := xml.Unmarshal(msgData, &tc.Data.UnitedEquity); err != nil {
				log.Error("Decode positions ", err, " msg:", resp.Message)
			} else {
				tc.ResponseChannel <- startElement.Name.Local
			}
		case "news_header":
			if err := xml.Unmarshal(msgData, &tc.Data.NewsHeader); err != nil {
				log.Error("Decode news_header ", err, " msg:", resp.Message)
			}
			log.Info(tc.Data.NewsHeader)
		case "messages":
			if err := xml.Unmarshal(msgData, &tc.Data.Messages); err != nil {
				log.Error("Decode messages ", err, " msg:", resp.Message)
			}
			log.Info(tc.Data.Messages)
		case "union":
			union := Union{}
			if err := xml.Unmarshal(msgData, &union); err != nil {
				log.Error("Decode union ", err, " msg:", resp.Message)
			}
			tc.Data.Unions = append(tc.Data.Unions, union)
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
