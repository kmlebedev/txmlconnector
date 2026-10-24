package tcClient

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/client/commands"
	pb "github.com/kmlebedev/txmlconnector/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type clientRPC struct {
	mu              sync.Mutex
	stream          grpc.ServerStreamingClient[pb.DataResponse]
	fetchErr        error
	fetchContext    context.Context
	commandResponse string
	commandErr      error
	commandBlock    bool
	requests        []string
	commandCalled   chan struct{}
	commandOnce     sync.Once
}

func (rpc *clientRPC) FetchResponseData(
	ctx context.Context,
	_ *pb.DataRequest,
	_ ...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.DataResponse], error) {
	rpc.mu.Lock()
	rpc.fetchContext = ctx
	rpc.mu.Unlock()
	if rpc.fetchErr != nil {
		return nil, rpc.fetchErr
	}
	return rpc.stream, nil
}

func (rpc *clientRPC) SendCommand(
	ctx context.Context,
	request *pb.SendCommandRequest,
	_ ...grpc.CallOption,
) (*pb.SendCommandResponse, error) {
	rpc.mu.Lock()
	rpc.requests = append(rpc.requests, request.GetMessage())
	rpc.mu.Unlock()
	if rpc.commandCalled != nil {
		rpc.commandOnce.Do(func() { close(rpc.commandCalled) })
	}
	if rpc.commandBlock {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if rpc.commandErr != nil {
		return nil, rpc.commandErr
	}
	return &pb.SendCommandResponse{Message: rpc.commandResponse}, nil
}

func (rpc *clientRPC) sentRequests() []string {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	return append([]string(nil), rpc.requests...)
}

func (rpc *clientRPC) responseContext() context.Context {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	return rpc.fetchContext
}

type responseStream struct {
	ctx           context.Context
	messages      []string
	mu            sync.Mutex
	next          int
	allReceived   chan struct{}
	received      sync.Once
	headerStarted chan struct{}
	headerGate    chan struct{}
	headerErr     error
	headerOnce    sync.Once
}

func (s *responseStream) Header() (metadata.MD, error) {
	if s.headerStarted != nil {
		s.headerOnce.Do(func() { close(s.headerStarted) })
	}
	if s.headerGate != nil {
		<-s.headerGate
	}
	return nil, s.headerErr
}
func (s *responseStream) Trailer() metadata.MD      { return nil }
func (s *responseStream) CloseSend() error          { return nil }
func (s *responseStream) Context() context.Context  { return s.ctx }
func (s *responseStream) SendMsg(interface{}) error { return nil }
func (s *responseStream) RecvMsg(interface{}) error { return nil }

func (s *responseStream) Recv() (*pb.DataResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next == len(s.messages) {
		return nil, io.EOF
	}
	message := s.messages[s.next]
	s.next++
	if s.next == len(s.messages) {
		s.received.Do(func() { close(s.allReceived) })
	}
	return &pb.DataResponse{Message: message}, nil
}

func TestNewTCClientWithConnStartsStreamAndConnects(t *testing.T) {
	t.Setenv("TC_LOGIN", "test-login")
	t.Setenv("TC_PASSWORD", "test-password")
	t.Setenv("TC_HOST", "test-host")
	t.Setenv("TC_PORT", "12345")

	rpc := &clientRPC{
		stream: &responseStream{
			ctx:         context.Background(),
			allReceived: make(chan struct{}),
		},
		commandResponse: `<result success="true"/>`,
	}
	client, err := NewTCClientWithConn(rpc, nil)
	if err != nil {
		t.Fatalf("NewTCClientWithConn error = %v", err)
	}
	defer client.Close()

	requests := rpc.sentRequests()
	if len(requests) != 1 {
		t.Fatalf("sent requests = %d, want 1", len(requests))
	}
	connect := commands.Connect{}
	if err := xml.Unmarshal([]byte(requests[0]), &connect); err != nil {
		t.Fatalf("decode connect request: %v", err)
	}
	if connect.Id != "connect" || connect.Login != "test-login" || connect.Password != "test-password" ||
		connect.Host != "test-host" || connect.Port != "12345" {
		t.Fatalf("connect request = %+v", connect)
	}

	select {
	case <-client.ShutdownChannel:
	case <-time.After(time.Second):
		t.Fatal("stream EOF did not signal client shutdown")
	}
}

func TestNewTCClientWithConnWaitsForStreamHeaderBeforeConnect(t *testing.T) {
	t.Setenv("TC_LOGIN", "test-login")
	t.Setenv("TC_PASSWORD", "test-password")
	t.Setenv("TC_HOST", "test-host")
	t.Setenv("TC_PORT", "12345")

	stream := &responseStream{
		ctx:           context.Background(),
		allReceived:   make(chan struct{}),
		headerStarted: make(chan struct{}),
		headerGate:    make(chan struct{}),
	}
	rpc := &clientRPC{
		stream:          stream,
		commandResponse: `<result success="true"/>`,
		commandCalled:   make(chan struct{}),
	}
	type result struct {
		client *TCClient
		err    error
	}
	done := make(chan result, 1)
	go func() {
		client, err := NewTCClientWithConn(rpc, nil)
		done <- result{client: client, err: err}
	}()

	select {
	case <-stream.headerStarted:
	case <-time.After(time.Second):
		t.Fatal("client did not wait for the response stream header")
	}
	select {
	case <-rpc.commandCalled:
		t.Fatal("connect was sent before the stream header")
	case <-time.After(100 * time.Millisecond):
	}

	close(stream.headerGate)
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("NewTCClientWithConn error = %v", res.err)
		}
		defer res.client.Close()
	case <-time.After(time.Second):
		t.Fatal("NewTCClientWithConn did not finish after the stream header")
	}
	if requests := rpc.sentRequests(); len(requests) != 1 {
		t.Fatalf("sent requests = %d, want 1", len(requests))
	}
}

func TestNewTCClientWithConnReportsStreamHeaderFailure(t *testing.T) {
	t.Setenv("TC_LOGIN", "test-login")
	t.Setenv("TC_PASSWORD", "test-password")
	t.Setenv("TC_HOST", "test-host")
	t.Setenv("TC_PORT", "12345")

	rpc := &clientRPC{
		stream: &responseStream{
			ctx:         context.Background(),
			allReceived: make(chan struct{}),
			headerErr:   errors.New("stream not ready"),
		},
		commandResponse: `<result success="true"/>`,
	}
	client, err := NewTCClientWithConn(rpc, nil)
	if err == nil || client != nil {
		t.Fatalf("NewTCClientWithConn = (%v, %v), want nil client and error", client, err)
	}
	if !strings.Contains(err.Error(), "response stream readiness") {
		t.Fatalf("error = %v, want response stream readiness", err)
	}
	if requests := rpc.sentRequests(); len(requests) != 0 {
		t.Fatalf("connect was sent after header failure: %d requests", len(requests))
	}

	responseCtx := rpc.responseContext()
	if responseCtx == nil {
		t.Fatal("response stream was not opened")
	}
	select {
	case <-responseCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("header failure did not cancel the client context")
	}
}

func setClientHandshakeTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	previous := clientHandshakeTimeout
	clientHandshakeTimeout = timeout
	t.Cleanup(func() { clientHandshakeTimeout = previous })
}

func TestNewTCClientWithConnTimesOutWaitingForStreamHeader(t *testing.T) {
	setClientHandshakeTimeout(t, 20*time.Millisecond)
	t.Setenv("TC_LOGIN", "test-login")
	t.Setenv("TC_PASSWORD", "test-password")
	t.Setenv("TC_HOST", "test-host")
	t.Setenv("TC_PORT", "12345")

	headerGate := make(chan struct{})
	rpc := &clientRPC{
		stream: &responseStream{
			ctx:           context.Background(),
			allReceived:   make(chan struct{}),
			headerStarted: make(chan struct{}),
			headerGate:    headerGate,
		},
		commandResponse: `<result success="true"/>`,
	}
	client, err := NewTCClientWithConn(rpc, nil)
	if err == nil || client != nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("NewTCClientWithConn = (%v, %v), want header timeout", client, err)
	}
	if requests := rpc.sentRequests(); len(requests) != 0 {
		t.Fatalf("connect was sent after header timeout: %d requests", len(requests))
	}
	close(headerGate)
}

func TestNewTCClientWithConnTimesOutConnecting(t *testing.T) {
	setClientHandshakeTimeout(t, 20*time.Millisecond)
	t.Setenv("TC_LOGIN", "test-login")
	t.Setenv("TC_PASSWORD", "test-password")
	t.Setenv("TC_HOST", "test-host")
	t.Setenv("TC_PORT", "12345")

	rpc := &clientRPC{
		stream: &responseStream{
			ctx:         context.Background(),
			allReceived: make(chan struct{}),
		},
		commandBlock:  true,
		commandCalled: make(chan struct{}),
	}
	client, err := NewTCClientWithConn(rpc, nil)
	if err == nil || client != nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("NewTCClientWithConn = (%v, %v), want connect timeout", client, err)
	}
	select {
	case <-rpc.commandCalled:
	default:
		t.Fatal("connect command was not attempted")
	}
}

func TestNewTCClientWithConnRejectsNilGRPCClient(t *testing.T) {
	if _, err := NewTCClientWithConn(nil, nil); err == nil {
		t.Fatal("NewTCClientWithConn accepted a nil gRPC client")
	}
}

func TestNewTCClientWithConnReportsStreamOpenFailure(t *testing.T) {
	rpc := &clientRPC{fetchErr: errors.New("stream unavailable")}
	client, err := NewTCClientWithConn(rpc, nil)
	if err == nil || client != nil || !strings.Contains(err.Error(), "open response stream") {
		t.Fatalf("NewTCClientWithConn = (%v, %v), want stream error", client, err)
	}

	responseCtx := rpc.responseContext()
	if responseCtx == nil {
		t.Fatal("response stream was not requested")
	}
	select {
	case <-responseCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("stream open failure did not cancel the client context")
	}
}

func TestNewTCClientWithConnCancelsStreamWhenConnectFails(t *testing.T) {
	rpc := &clientRPC{
		stream: &responseStream{
			ctx:         context.Background(),
			allReceived: make(chan struct{}),
		},
		commandErr: errors.New("send failed"),
	}
	if client, err := NewTCClientWithConn(rpc, nil); err == nil || client != nil {
		t.Fatalf("NewTCClientWithConn = (%v, %v), want nil client and error", client, err)
	}

	responseCtx := rpc.responseContext()
	if responseCtx == nil {
		t.Fatal("response stream was not opened")
	}
	select {
	case <-responseCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("failed client startup did not cancel the response stream")
	}
}

func TestSendCommandReportsTransportAndResponseErrors(t *testing.T) {
	tests := []struct {
		name     string
		response string
		rpcErr   error
		want     string
	}{
		{name: "transport", rpcErr: errors.New("connection lost"), want: "send command"},
		{name: "malformed XML", response: `<result`, want: "decode command response"},
		{name: "TRANSAQ rejection", response: `<result success="false"><message>denied</message></result>`, want: "TRANSAQ command failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rpc := &clientRPC{commandResponse: test.response, commandErr: test.rpcErr}
			client := &TCClient{Client: rpc, ctx: context.Background()}
			err := client.SendCommand(commands.Command{Id: "test"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("SendCommand error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestConnectRequiresCredentials(t *testing.T) {
	values := map[string]string{
		"TC_LOGIN":    "test-login",
		"TC_PASSWORD": "test-password",
		"TC_HOST":     "test-host",
		"TC_PORT":     "12345",
	}
	for _, missing := range []string{"TC_LOGIN", "TC_PASSWORD", "TC_HOST", "TC_PORT"} {
		t.Run(missing, func(t *testing.T) {
			for key, value := range values {
				if key == missing {
					t.Setenv(key, "")
					continue
				}
				t.Setenv(key, value)
			}
			client := &TCClient{ctx: context.Background()}
			err := client.Connect()
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("Connect error = %v, want %s required", err, missing)
			}
		})
	}
}

func TestCloseIsIdempotentAndCancelsClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &TCClient{ctx: ctx, cancel: cancel}
	client.Close()
	client.Close()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("client context error = %v", ctx.Err())
	}
}

func TestHandleMessageRoutesQuotes(t *testing.T) {
	tc := TCClient{QuotesChan: make(chan commands.Quotes, 1)}
	if err := tc.handleMessage(`<quotes><quote secid="42"><price>123.45</price></quote></quotes>`); err != nil {
		t.Fatal(err)
	}
	quotes := <-tc.QuotesChan
	if len(quotes.Items) != 1 || quotes.Items[0].SecId != 42 || quotes.Items[0].Price != 123.45 {
		t.Fatalf("quotes = %+v", quotes)
	}
	if quotes.Time.IsZero() {
		t.Fatal("quotes timestamp was not set")
	}
}

func TestHandleMessageStoresPositionsAndNotifies(t *testing.T) {
	tc := TCClient{ResponseChannel: make(chan string, 1)}
	if err := tc.handleMessage(`<positions></positions>`); err != nil {
		t.Fatal(err)
	}
	if tc.Data.Positions == nil {
		t.Fatal("positions were not stored")
	}
	if got := <-tc.ResponseChannel; got != "positions" {
		t.Fatalf("notification = %q", got)
	}
}

func TestHandleMessageRejectsMalformedAndUnknownXML(t *testing.T) {
	tc := TCClient{}
	if err := tc.handleMessage(`<quotes>`); err == nil {
		t.Fatal("malformed XML was accepted")
	}
	if err := tc.handleMessage(`<new_message/>`); !errors.Is(err, errUnknownMessage) {
		t.Fatalf("unknown message error = %v", err)
	}
}

func TestLoopReadingFromStreamKeepsReceivingWhileDispatchIsBlocked(t *testing.T) {
	const messageCount = 256
	messages := make([]string, messageCount)
	for i := range messages {
		messages[i] = fmt.Sprintf(`<sec_info_upd><secid>%d</secid></sec_info_upd>`, i)
	}
	stream := &responseStream{
		ctx:         context.Background(),
		messages:    messages,
		allReceived: make(chan struct{}),
	}
	tc := TCClient{
		SecInfoUpdChan:  make(chan commands.SecInfoUpd, 1),
		ShutdownChannel: make(chan bool, 1),
		ctx:             context.Background(),
	}
	done := make(chan struct{})
	go func() {
		tc.loopReadingFromStream(stream)
		close(done)
	}()

	receivedWithoutDraining := true
	select {
	case <-stream.allReceived:
	case <-time.After(time.Second):
		receivedWithoutDraining = false
	}

	for i := range messageCount {
		select {
		case event := <-tc.SecInfoUpdChan:
			if event.SecId != i {
				t.Fatalf("event %d has secid %d", i, event.SecId)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out draining dispatched messages")
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream reader did not stop after EOF")
	}
	if !receivedWithoutDraining {
		t.Fatal("gRPC receive loop was blocked by the typed event consumer")
	}
}

func TestLoopReadingFromStreamCancellationUnblocksDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &responseStream{
		ctx:         ctx,
		messages:    []string{`<sec_info_upd><secid>1</secid></sec_info_upd>`},
		allReceived: make(chan struct{}),
	}
	tc := TCClient{
		SecInfoUpdChan:  make(chan commands.SecInfoUpd),
		ShutdownChannel: make(chan bool, 1),
		ctx:             ctx,
		cancel:          cancel,
	}
	done := make(chan struct{})
	go func() {
		tc.loopReadingFromStream(stream)
		close(done)
	}()

	select {
	case <-stream.allReceived:
	case <-time.After(time.Second):
		t.Fatal("stream message was not received")
	}
	tc.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock typed event dispatch")
	}
	select {
	case <-tc.ShutdownChannel:
	default:
		t.Fatal("client shutdown was not signaled")
	}
}
