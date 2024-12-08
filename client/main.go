package tcClient

import (
	"context"
	"encoding/xml"
	"fmt"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"io"
	"os"
	"strings"

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
		CandleKinds     CandleKinds
		Securities      Securities
		Candles         Candles
		Quotations      Quotations
		Pits            Pits
		Positions       *Positions
		UnitedPortfolio *UnitedPortfolio
		UnitedEquity    UnitedEquity
		NewsHeader      NewsHeader
		Messages        Messages
		Unions          []Union
	}
	SecInfoChan      chan SecInfo
	SecInfoUpdChan   chan SecInfoUpd
	ServerStatusChan chan ServerStatus
	ResponseChannel  chan string
	ShutdownChannel  chan bool
	AllTradesChan    chan AllTrades
	QuotesChan       chan Quotes
	grpcConn         *grpc.ClientConn
	ctx              context.Context
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

func NewTCClientWithConn(client pb.ConnectServiceClient, conn *grpc.ClientConn) (*TCClient, error) {
	tc := TCClient{
		Client:           client,
		SecInfoUpdChan:   make(chan SecInfoUpd),
		ServerStatusChan: make(chan ServerStatus),
		ShutdownChannel:  make(chan bool),
		ResponseChannel:  make(chan string),
		AllTradesChan:    make(chan AllTrades),
		ctx:              context.Background(),
		grpcConn:         conn,
	}

	if err := tc.Connect(); err != nil {
		return nil, err
	}
	stream, err := tc.Client.FetchResponseData(tc.ctx, &pb.DataRequest{})
	if err != nil {
		log.Errorf("open stream error %v", err)
		return nil, err
	}
	go tc.LoopReadingFromStream(&stream)

	return &tc, nil
}

func NewTCClient() (*TCClient, error) {
	log.Infoln("gRPC client running ...")
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
	client := pb.NewConnectServiceClient(conn)
	return NewTCClientWithConn(client, conn)
}

func (tc *TCClient) Connect() error {
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
		MicexRegisters: true,
	}
	request := &pb.SendCommandRequest{Message: EncodeRequest(connectReq)}

	response, err := tc.Client.SendCommand(tc.ctx, request)
	if err != nil {
		log.Error("SendCommand: ", err)
		return err
	}

	result := Result{}
	if err := xml.Unmarshal([]byte(response.GetMessage()), &result); err != nil {
		log.Error("Unmarshal(Result) ", err, response.GetMessage())
		return err
	}
	if result.Success != "true" {
		log.Error("Result: ", result.Message)
		return fmt.Errorf("Result not success: %s", result.Message)
	} else {
		log.Debugf("Result: %+v", result)
	}

	return nil
}

func (tc *TCClient) Disconnect() error {
	return tc.SendCommand(Command{Id: "disconnect"})
}

func (tc *TCClient) Close() {
	_ = tc.grpcConn.Close()
}

func (tc *TCClient) SendCommand(cmd Command) error {
	result := Result{}
	response, err := tc.Client.SendCommand(context.Background(),
		&pb.SendCommandRequest{Message: EncodeRequest(cmd)},
	)
	if err != nil {
		return fmt.Errorf("SendCommand ", err)
	}
	if err := xml.Unmarshal([]byte(response.GetMessage()), &result); err != nil {
		return fmt.Errorf("Unmarshal(Result) ", err, response.GetMessage())
	}
	if result.Success != "true" {
		return fmt.Errorf("Result: ", result.Message)
	}
	return nil
}

func (tc *TCClient) LoopReadingFromStream(stream *pb.ConnectService_FetchResponseDataClient) {
	for {
		resp, err := (*stream).Recv()
		if err == io.EOF {
			log.Debug("Resp received: %s", resp.Message)
			tc.ShutdownChannel <- true // means stream is finished
			return
		}
		if err != nil {
			// Todo reconnect
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
		case "alltrades":
			allTrades := AllTrades{}
			if err := xml.Unmarshal(msgData, &allTrades); err != nil {
				log.Error(err)
			} else {
				tc.AllTradesChan <- allTrades
			}
		case "quotes":
			quotes := Quotes{}
			if err := xml.Unmarshal(msgData, &quotes); err != nil {
				log.Error(err)
			} else {
				tc.QuotesChan <- quotes
			}
		case "sec_info_upd":
			secInfoUpd := SecInfoUpd{}
			if err := xml.Unmarshal(msgData, &secInfoUpd); err != nil {
				log.Error(err)
			} else {
				tc.SecInfoUpdChan <- secInfoUpd
			}
		case "sec_info":
			secInfo := SecInfo{}
			if err := xml.Unmarshal(msgData, &secInfo); err != nil {
				log.Error(err)
			} else {
				tc.SecInfoChan <- secInfo
			}
		case "server_status":
			if err := xml.Unmarshal(msgData, &tc.Data.ServerStatus); err != nil {
				log.Error("Decode serverStatus ", err, resp.Message)
			} else {
				tc.ServerStatusChan <- tc.Data.ServerStatus
			}
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
			if err := xml.Unmarshal(msgData, &tc.Data.CandleKinds); err != nil {
				log.Error("Decode candlekinds ", err, " msg:", resp.Message)
			}
		case "securities":
			if err := xml.Unmarshal(msgData, &tc.Data.Securities); err != nil {
				log.Error("Decode securities ", err, " msg:", resp.Message)
			}
		case "candles":
			date := Candles{}
			if err := xml.Unmarshal(msgData, &date); err != nil {
				log.Error("Decode candles ", err, " msg:", resp.Message)
			} else {
				tc.Data.Candles = date
				tc.ResponseChannel <- startElement.Name.Local
			}
		case "quotations":
			if err := xml.Unmarshal(msgData, &tc.Data.Quotations); err != nil {
				log.Error("Decode quotations ", err, " msg:", resp.Message)
			} else {
				tc.ResponseChannel <- startElement.Name.Local
			}
		case "pits":
			if err := xml.Unmarshal(msgData, &tc.Data.Pits); err != nil {
				log.Error("Decode pits ", err, " msg:", resp.Message)
			}
		case "positions":
			data := Positions{}
			if err := xml.Unmarshal(msgData, &data); err != nil {
				log.Error("Decode positions ", err, " msg:", resp.Message)
			} else {
				tc.Data.Positions = &data
				tc.ResponseChannel <- startElement.Name.Local
			}
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
