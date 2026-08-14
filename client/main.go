package tcClient

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	. "github.com/kmlebedev/txmlconnector/client/commands"
	pb "github.com/kmlebedev/txmlconnector/proto"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultTarget = "localhost:50051"

var errUnknownMessage = errors.New("unknown TRANSAQ message")

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
	cancel           context.CancelFunc
	closeOnce        sync.Once
}

var timeNowLocation = moscowLocation()

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
	if client == nil {
		return nil, errors.New("gRPC client is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	tc := TCClient{
		Client:           client,
		SecInfoChan:      make(chan SecInfo, 16),
		SecInfoUpdChan:   make(chan SecInfoUpd, 16),
		ServerStatusChan: make(chan ServerStatus, 16),
		ShutdownChannel:  make(chan bool, 1),
		ResponseChannel:  make(chan string, 16),
		AllTradesChan:    make(chan AllTrades, 16),
		QuotesChan:       make(chan Quotes, 16),
		ctx:              ctx,
		cancel:           cancel,
		grpcConn:         conn,
	}

	stream, err := tc.Client.FetchResponseData(tc.ctx, &pb.DataRequest{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open response stream: %w", err)
	}
	streamReady := make(chan error, 1)
	go func() {
		_, err := stream.Header()
		streamReady <- err
	}()
	if err := tc.Connect(); err != nil {
		cancel()
		return nil, err
	}
	go func() {
		if err := <-streamReady; err != nil {
			if tc.ctx.Err() == nil {
				log.Errorf("response stream readiness: %v", err)
			}
			tc.signalShutdown()
			return
		}
		tc.loopReadingFromStream(stream)
	}()

	return &tc, nil
}

func NewTCClient() (*TCClient, error) {
	log.Infoln("gRPC client running ...")
	target := os.Getenv("TC_TARGET")
	if target == "" {
		target = defaultTarget
	}
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	)
	if err != nil {
		return nil, fmt.Errorf("create gRPC client for %s: %w", target, err)
	}
	client := pb.NewConnectServiceClient(conn)
	tc, err := NewTCClientWithConn(client, conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tc, nil
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
	return tc.send(tc.ctx, connectReq)
}

func (tc *TCClient) Disconnect() error {
	return tc.SendCommand(Command{Id: "disconnect"})
}

func (tc *TCClient) Close() {
	tc.closeOnce.Do(func() {
		if tc.cancel != nil {
			tc.cancel()
		}
		if tc.grpcConn != nil {
			_ = tc.grpcConn.Close()
		}
	})
}

func (tc *TCClient) SendCommand(cmd Command) error {
	return tc.send(tc.ctx, cmd)
}

func (tc *TCClient) send(ctx context.Context, command interface{}) error {
	message, err := EncodeRequestE(command)
	if err != nil {
		return fmt.Errorf("encode command: %w", err)
	}
	response, err := tc.Client.SendCommand(ctx, &pb.SendCommandRequest{Message: message})
	if err != nil {
		return fmt.Errorf("send command: %w", err)
	}
	result := Result{}
	if err := xml.Unmarshal([]byte(response.GetMessage()), &result); err != nil {
		return fmt.Errorf("decode command response %q: %w", response.GetMessage(), err)
	}
	if result.Success != "true" {
		return fmt.Errorf("TRANSAQ command failed: %s", result.Message)
	}
	return nil
}

// LoopReadingFromStream is kept for source compatibility. New code should let
// the client manage the stream created by NewTCClientWithConn.
func (tc *TCClient) LoopReadingFromStream(stream *pb.ConnectService_FetchResponseDataClient) {
	if stream == nil || *stream == nil {
		tc.signalShutdown()
		return
	}
	tc.loopReadingFromStream(*stream)
}

func (tc *TCClient) loopReadingFromStream(stream pb.ConnectService_FetchResponseDataClient) {
	defer tc.signalShutdown()
	queue := newResponseQueue()
	processed := make(chan struct{})
	go func() {
		defer close(processed)
		for {
			message, ok := queue.pop(tc.done())
			if !ok {
				return
			}
			if err := tc.handleMessage(message); err != nil {
				if errors.Is(err, errUnknownMessage) {
					log.Warnf("%v: %s", err, message)
				} else if !errors.Is(err, context.Canceled) {
					log.Errorf("handle response: %v", err)
				}
			}
		}
	}()
	defer func() {
		queue.close()
		<-processed
	}()

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			if tc.ctx == nil || tc.ctx.Err() == nil {
				log.Errorf("receive response stream: %v", err)
			}
			return
		}
		queue.push(resp.Message)
	}
}

func (tc *TCClient) done() <-chan struct{} {
	if tc.ctx == nil {
		return nil
	}
	return tc.ctx.Done()
}

func (tc *TCClient) signalShutdown() {
	select {
	case tc.ShutdownChannel <- true:
	default:
	}
}

func (tc *TCClient) handleMessage(message string) error {
	name, err := rootElement(message)
	if err != nil {
		return err
	}
	data := []byte(message)
	unmarshal := func(target interface{}) error {
		if err := xml.Unmarshal(data, target); err != nil {
			return fmt.Errorf("decode %s: %w", name, err)
		}
		return nil
	}
	notify := func() {
		select {
		case tc.ResponseChannel <- name:
		default:
			log.Warnf("response notification channel is full; dropped %s notification", name)
		}
	}

	switch name {
	case "alltrades":
		allTrades := AllTrades{}
		if err := unmarshal(&allTrades); err != nil {
			return err
		}
		select {
		case tc.AllTradesChan <- allTrades:
		case <-tc.done():
			return tc.ctx.Err()
		}
	case "quotes":
		quotes := Quotes{}
		if err := unmarshal(&quotes); err != nil {
			return err
		}
		quotes.Time = time.Now().In(timeNowLocation)
		select {
		case tc.QuotesChan <- quotes:
		case <-tc.done():
			return tc.ctx.Err()
		}
	case "sec_info_upd":
		secInfoUpd := SecInfoUpd{}
		if err := unmarshal(&secInfoUpd); err != nil {
			return err
		}
		select {
		case tc.SecInfoUpdChan <- secInfoUpd:
		case <-tc.done():
			return tc.ctx.Err()
		}
	case "sec_info":
		secInfo := SecInfo{}
		if err := unmarshal(&secInfo); err != nil {
			return err
		}
		select {
		case tc.SecInfoChan <- secInfo:
		case <-tc.done():
			return tc.ctx.Err()
		}
	case "server_status":
		if err := unmarshal(&tc.Data.ServerStatus); err != nil {
			return err
		}
		select {
		case tc.ServerStatusChan <- tc.Data.ServerStatus:
		case <-tc.done():
			return tc.ctx.Err()
		}
	case "client":
		return unmarshal(&tc.Data.Client)
	case "markets":
		return unmarshal(&tc.Data.Markets)
	case "boards":
		return unmarshal(&tc.Data.Boards)
	case "candlekinds":
		return unmarshal(&tc.Data.CandleKinds)
	case "securities":
		return unmarshal(&tc.Data.Securities)
	case "candles":
		if err := unmarshal(&tc.Data.Candles); err != nil {
			return err
		}
		notify()
	case "quotations":
		if err := unmarshal(&tc.Data.Quotations); err != nil {
			return err
		}
		notify()
	case "pits":
		return unmarshal(&tc.Data.Pits)
	case "positions":
		data := Positions{}
		if err := unmarshal(&data); err != nil {
			return err
		}
		tc.Data.Positions = &data
		notify()
	case "united_portfolio":
		data := UnitedPortfolio{}
		if err := unmarshal(&data); err != nil {
			return err
		}
		tc.Data.UnitedPortfolio = &data
		notify()
	case "united_equity":
		if err := unmarshal(&tc.Data.UnitedEquity); err != nil {
			return err
		}
		notify()
	case "news_header":
		if err := unmarshal(&tc.Data.NewsHeader); err != nil {
			return err
		}
		log.Info(tc.Data.NewsHeader)
	case "messages":
		if err := unmarshal(&tc.Data.Messages); err != nil {
			return err
		}
		log.Info(tc.Data.Messages)
	case "union":
		union := Union{}
		if err := unmarshal(&union); err != nil {
			return err
		}
		tc.Data.Unions = append(tc.Data.Unions, union)
	case "overnight":
		overnight := Overnight{}
		return unmarshal(&overnight)
	default:
		return fmt.Errorf("%w: %s", errUnknownMessage, name)
	}
	return nil
}

func rootElement(message string) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(message))
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("decode XML root: %w", err)
		}
		if start, ok := token.(xml.StartElement); ok {
			return start.Name.Local, nil
		}
	}
}

func moscowLocation() *time.Location {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return location
}
