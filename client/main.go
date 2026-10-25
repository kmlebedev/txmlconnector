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

var (
	errUnknownMessage      = errors.New("unknown TRANSAQ message")
	clientHandshakeTimeout = 10 * time.Second
)

// Options configures the native client. The zero value keeps the historical
// environment-based behaviour and does not enable portfolio updates.
type Options struct {
	Target          string
	PortfolioBuffer int
	Connect         *ConnectConfig
}

// ConnectConfig overrides the TC_* environment variables for the connect
// command. Zero timeouts fall back to the historical defaults.
type ConnectConfig struct {
	Login          string
	Password       string
	Host           string
	Port           string
	SessionTimeout int
	RequestTimeout int
	PushUlimits    int
	PushPosEquity  int
}

type TCClient struct {
	Client pb.ConnectServiceClient
	// Data is kept for backward compatibility. The stream dispatcher writes it
	// without synchronization; concurrent readers must use the typed channels
	// (PortfolioUpdates, SecInfoChan, ...) instead.
	Data struct {
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
	SecInfoChan       chan SecInfo
	SecInfoUpdChan    chan SecInfoUpd
	ServerStatusChan  chan ServerStatus
	ResponseChannel   chan string
	ShutdownChannel   chan bool
	AllTradesChan     chan AllTrades
	QuotesChan        chan Quotes
	portfolioUpdates  chan PortfolioUpdate
	closeUpdatesOnce  sync.Once
	shutdown          chan struct{}
	closeShutdownOnce sync.Once
	options           Options
	grpcConn          *grpc.ClientConn
	ctx               context.Context
	cancel            context.CancelFunc
	closeOnce         sync.Once
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
	return NewTCClientWithConnAndOptions(client, conn, Options{})
}

// NewTCClientWithConnAndOptions is NewTCClientWithConn with an explicit
// configuration. PortfolioBuffer > 0 enables the PortfolioUpdates stream.
func NewTCClientWithConnAndOptions(client pb.ConnectServiceClient, conn *grpc.ClientConn, opts Options) (*TCClient, error) {
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
		shutdown:         make(chan struct{}),
		options:          opts,
		ctx:              ctx,
		cancel:           cancel,
		grpcConn:         conn,
	}
	if opts.PortfolioBuffer > 0 {
		tc.portfolioUpdates = make(chan PortfolioUpdate, opts.PortfolioBuffer)
	}

	stream, err := tc.Client.FetchResponseData(tc.ctx, &pb.DataRequest{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open response stream: %w", err)
	}
	// The server subscribes the stream before sending headers. Wait for that
	// readiness handshake before issuing connect so the initial client and
	// securities snapshot is not published to an empty broker and dropped.
	if err := tc.waitForStreamHeader(stream); err != nil {
		cancel()
		return nil, err
	}
	connectCtx, connectCancel := context.WithTimeout(tc.ctx, clientHandshakeTimeout)
	connectErr := tc.connect(connectCtx)
	connectCancel()
	if connectErr != nil {
		cancel()
		return nil, connectErr
	}
	go tc.loopReadingFromStream(stream)

	return &tc, nil
}

func (tc *TCClient) waitForStreamHeader(stream grpc.ServerStreamingClient[pb.DataResponse]) error {
	headerReady := make(chan error, 1)
	go func() {
		_, err := stream.Header()
		headerReady <- err
	}()
	select {
	case err := <-headerReady:
		if err != nil {
			return fmt.Errorf("response stream readiness: %w", err)
		}
		return nil
	case <-time.After(clientHandshakeTimeout):
		return fmt.Errorf("response stream readiness: timeout after %s", clientHandshakeTimeout)
	case <-tc.ctx.Done():
		return fmt.Errorf("response stream readiness: %w", tc.ctx.Err())
	}
}

func NewTCClient() (*TCClient, error) {
	return NewTCClientWithOptions(Options{})
}

// NewTCClientWithOptions creates a client from Options. Target overrides
// TC_TARGET; Connect overrides TC_LOGIN/TC_PASSWORD/TC_HOST/TC_PORT.
func NewTCClientWithOptions(opts Options) (*TCClient, error) {
	log.Infoln("gRPC client running ...")
	target := opts.Target
	if target == "" {
		target = os.Getenv("TC_TARGET")
	}
	if target == "" {
		target = defaultTarget
	}
	// Do not wait for readiness inside the factory: RunWithReconnect owns the
	// retry loop, and an unavailable server must surface as an error instead of
	// blocking the supervisor forever.
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create gRPC client for %s: %w", target, err)
	}
	client := pb.NewConnectServiceClient(conn)
	tc, err := NewTCClientWithConnAndOptions(client, conn, opts)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tc, nil
}

func (tc *TCClient) Connect() error {
	return tc.connect(tc.ctx)
}

func (tc *TCClient) connect(ctx context.Context) error {
	cfg := tc.connectConfig()
	switch {
	case cfg.Login == "":
		return errors.New("TC_LOGIN is required")
	case cfg.Password == "":
		return errors.New("TC_PASSWORD is required")
	case cfg.Host == "":
		return errors.New("TC_HOST is required")
	case cfg.Port == "":
		return errors.New("TC_PORT is required")
	}

	connectReq := Connect{
		Id:             "connect",
		Login:          cfg.Login,
		Password:       cfg.Password,
		Host:           cfg.Host,
		Port:           cfg.Port,
		SessionTimeout: cfg.SessionTimeout,
		RequestTimeout: cfg.RequestTimeout,
		Rqdelay:        1000,
		PushUlimits:    cfg.PushUlimits,
		PushPosEquity:  cfg.PushPosEquity,
		MicexRegisters: true,
	}
	return tc.send(ctx, connectReq)
}

func (tc *TCClient) connectConfig() ConnectConfig {
	defaults := ConnectConfig{
		SessionTimeout: 60,
		RequestTimeout: 10,
		PushUlimits:    30,
		PushPosEquity:  30,
	}
	if tc.options.Connect == nil {
		defaults.Login = os.Getenv("TC_LOGIN")
		defaults.Password = os.Getenv("TC_PASSWORD")
		defaults.Host = os.Getenv("TC_HOST")
		defaults.Port = os.Getenv("TC_PORT")
		return defaults
	}
	cfg := *tc.options.Connect
	if cfg.SessionTimeout <= 0 {
		cfg.SessionTimeout = defaults.SessionTimeout
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaults.RequestTimeout
	}
	if cfg.PushUlimits <= 0 {
		cfg.PushUlimits = defaults.PushUlimits
	}
	if cfg.PushPosEquity <= 0 {
		cfg.PushPosEquity = defaults.PushPosEquity
	}
	return cfg
}

func (tc *TCClient) Disconnect() error {
	return tc.SendCommand(Command{Id: "disconnect"})
}

func (tc *TCClient) Close() {
	tc.closeOnce.Do(func() {
		tc.cancelContext()
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
		tc.closeShutdown()
		tc.closePortfolioUpdates()
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
				} else if !errors.Is(err, context.Canceled) && !errors.Is(err, errStreamClosed) {
					log.Errorf("handle response: %v", err)
				}
			}
		}
	}()
	defer func() {
		queue.close()
		// Unblock typed sends without cancelling the client context: queued
		// messages must still be drained before the stream is declared closed.
		tc.closeShutdown()
		<-processed
		tc.closePortfolioUpdates()
		// The session is over; fail future commands fast. Cancellation happens
		// only after the queue is drained so no received snapshot is dropped.
		tc.cancelContext()
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

func (tc *TCClient) cancelContext() {
	if tc.cancel != nil {
		tc.cancel()
	}
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
		return sendTyped(tc, tc.AllTradesChan, allTrades)
	case "quotes":
		quotes := Quotes{}
		if err := unmarshal(&quotes); err != nil {
			return err
		}
		quotes.Time = time.Now().In(timeNowLocation)
		return sendTyped(tc, tc.QuotesChan, quotes)
	case "sec_info_upd":
		secInfoUpd := SecInfoUpd{}
		if err := unmarshal(&secInfoUpd); err != nil {
			return err
		}
		return sendTyped(tc, tc.SecInfoUpdChan, secInfoUpd)
	case "sec_info":
		secInfo := SecInfo{}
		if err := unmarshal(&secInfo); err != nil {
			return err
		}
		return sendTyped(tc, tc.SecInfoChan, secInfo)
	case "server_status":
		if err := unmarshal(&tc.Data.ServerStatus); err != nil {
			return err
		}
		return sendTyped(tc, tc.ServerStatusChan, tc.Data.ServerStatus)
	case "client":
		if err := unmarshal(&tc.Data.Client); err != nil {
			return err
		}
		client := tc.Data.Client
		return tc.sendPortfolioUpdate(PortfolioUpdate{
			Kind:       UpdateClient,
			ReceivedAt: time.Now().UTC(),
			Raw:        message,
			Client:     &client,
		})
	case "markets":
		return unmarshal(&tc.Data.Markets)
	case "boards":
		return unmarshal(&tc.Data.Boards)
	case "candlekinds":
		return unmarshal(&tc.Data.CandleKinds)
	case "securities":
		securities := Securities{}
		if err := unmarshal(&securities); err != nil {
			return err
		}
		tc.Data.Securities = securities
		return tc.sendPortfolioUpdate(PortfolioUpdate{
			Kind:       UpdateSecurities,
			ReceivedAt: time.Now().UTC(),
			Raw:        message,
			Securities: &securities,
		})
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
		now := time.Now().UTC()
		if err := tc.sendPortfolioUpdate(PortfolioUpdate{
			Kind:       UpdatePositions,
			ReceivedAt: now,
			Raw:        message,
			Positions:  &data,
		}); err != nil {
			return err
		}
		for i := range data.UnitedLimits {
			limits := data.UnitedLimits[i]
			if err := tc.sendPortfolioUpdate(PortfolioUpdate{
				Kind:         UpdateUnitedLimits,
				ReceivedAt:   now,
				Raw:          message,
				UnitedLimits: &limits,
			}); err != nil {
				return err
			}
		}
		return nil
	case "united_portfolio":
		data := UnitedPortfolio{}
		if err := unmarshal(&data); err != nil {
			return err
		}
		tc.Data.UnitedPortfolio = &data
		notify()
		return tc.sendPortfolioUpdate(PortfolioUpdate{
			Kind:            UpdateUnitedPortfolio,
			ReceivedAt:      time.Now().UTC(),
			Raw:             message,
			UnitedPortfolio: &data,
		})
	case "united_limits":
		limits := UnitedLimits{}
		if err := unmarshal(&limits); err != nil {
			return err
		}
		return tc.sendPortfolioUpdate(PortfolioUpdate{
			Kind:         UpdateUnitedLimits,
			ReceivedAt:   time.Now().UTC(),
			Raw:          message,
			UnitedLimits: &limits,
		})
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
