package tcClient

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/kmlebedev/txmlconnector/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

var errTestSessionClosed = errors.New("response stream closed")

type reconnectRPC struct {
	mu       sync.Mutex
	requests []string
}

func (rpc *reconnectRPC) FetchResponseData(
	context.Context,
	*pb.DataRequest,
	...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.DataResponse], error) {
	return nil, errors.New("not used by reconnect tests")
}

func (rpc *reconnectRPC) SendCommand(
	_ context.Context,
	request *pb.SendCommandRequest,
	_ ...grpc.CallOption,
) (*pb.SendCommandResponse, error) {
	rpc.mu.Lock()
	rpc.requests = append(rpc.requests, request.GetMessage())
	rpc.mu.Unlock()
	return &pb.SendCommandResponse{Message: `<result success="true"/>`}, nil
}

func (rpc *reconnectRPC) requestCount() int {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	return len(rpc.requests)
}

func newReconnectTestClient(rpc pb.ConnectServiceClient) *TCClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &TCClient{
		Client:          rpc,
		ShutdownChannel: make(chan bool, 1),
		ctx:             ctx,
		cancel:          cancel,
	}
}

func TestRunWithReconnectRecreatesClientAfterSessionCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rpc := &reconnectRPC{}
	created := 0
	factory := func() (*TCClient, error) {
		created++
		client := newReconnectTestClient(rpc)
		if created == 1 {
			client.ShutdownChannel <- true
		} else {
			cancel()
		}
		return client, nil
	}
	runSession := func(ctx context.Context, client *TCClient) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-client.ShutdownChannel:
			return errTestSessionClosed
		}
	}

	err := RunWithReconnect(ctx, factory, runSession, ReconnectConfig{
		RetryMin:           time.Millisecond,
		RetryMax:           2 * time.Millisecond,
		SessionStableAfter: time.Hour,
		DisconnectTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("RunWithReconnect error = %v", err)
	}
	if created != 2 {
		t.Fatalf("created clients = %d, want 2", created)
	}
	if got := rpc.requestCount(); got != 2 {
		t.Fatalf("disconnect commands = %d, want 2", got)
	}
}

func TestRunWithReconnectRetriesFactoryFailures(t *testing.T) {
	rpc := &reconnectRPC{}
	attempts := 0
	err := RunWithReconnect(
		context.Background(),
		func() (*TCClient, error) {
			attempts++
			switch attempts {
			case 1:
				return nil, errors.New("temporary factory failure")
			case 2:
				return nil, nil
			default:
				return newReconnectTestClient(rpc), nil
			}
		},
		func(context.Context, *TCClient) error { return nil },
		ReconnectConfig{
			RetryMin:           time.Millisecond,
			RetryMax:           2 * time.Millisecond,
			SessionStableAfter: time.Hour,
			DisconnectTimeout:  time.Second,
		},
	)
	if err != nil {
		t.Fatalf("RunWithReconnect error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("factory attempts = %d, want 3", attempts)
	}
	if got := rpc.requestCount(); got != 1 {
		t.Fatalf("disconnect commands = %d, want 1", got)
	}
}

func TestRunWithReconnectCancellationInterruptsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	factoryCalled := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RunWithReconnect(
			ctx,
			func() (*TCClient, error) {
				close(factoryCalled)
				return nil, errors.New("service unavailable")
			},
			func(context.Context, *TCClient) error { return nil },
			ReconnectConfig{RetryMin: time.Hour},
		)
	}()

	select {
	case <-factoryCalled:
	case <-time.After(time.Second):
		t.Fatal("client factory was not called")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithReconnect error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not interrupt reconnect delay")
	}
}

func TestRunWithReconnectClosesClientReturnedWithFactoryError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newReconnectTestClient(&reconnectRPC{})
	factoryCalled := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RunWithReconnect(
			ctx,
			func() (*TCClient, error) {
				close(factoryCalled)
				return client, errors.New("partially initialized client")
			},
			func(context.Context, *TCClient) error { return nil },
			ReconnectConfig{RetryMin: time.Hour},
		)
	}()

	select {
	case <-factoryCalled:
	case <-time.After(time.Second):
		t.Fatal("client factory was not called")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunWithReconnect error = %v", err)
	}
	if !errors.Is(client.ctx.Err(), context.Canceled) {
		t.Fatalf("partial client context error = %v", client.ctx.Err())
	}
}

func TestRunWithReconnectValidatesRequiredCallbacks(t *testing.T) {
	validFactory := func() (*TCClient, error) { return newReconnectTestClient(&reconnectRPC{}), nil }
	validSession := func(context.Context, *TCClient) error { return nil }
	tests := []struct {
		name    string
		ctx     context.Context
		factory ClientFactory
		session SessionFunc
	}{
		{name: "context", factory: validFactory, session: validSession},
		{name: "factory", ctx: context.Background(), session: validSession},
		{name: "session", ctx: context.Background(), factory: validFactory},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := RunWithReconnect(test.ctx, test.factory, test.session, ReconnectConfig{}); err == nil {
				t.Fatal("RunWithReconnect accepted a missing dependency")
			}
		})
	}
}

func TestNextRetryDelayCapsAtMaximum(t *testing.T) {
	tests := []struct {
		current time.Duration
		maximum time.Duration
		want    time.Duration
	}{
		{current: time.Second, maximum: 8 * time.Second, want: 2 * time.Second},
		{current: 4 * time.Second, maximum: 8 * time.Second, want: 8 * time.Second},
		{current: 8 * time.Second, maximum: 8 * time.Second, want: 8 * time.Second},
	}
	for _, test := range tests {
		if got := nextRetryDelay(test.current, test.maximum); got != test.want {
			t.Fatalf("nextRetryDelay(%s, %s) = %s, want %s", test.current, test.maximum, got, test.want)
		}
	}
}

type streamRestartServer struct {
	pb.UnimplementedConnectServiceServer
	streams atomic.Int32
}

func (s *streamRestartServer) FetchResponseData(
	_ *pb.DataRequest,
	stream pb.ConnectService_FetchResponseDataServer,
) error {
	s.streams.Add(1)
	if err := stream.SendHeader(metadata.MD{}); err != nil {
		return err
	}
	if err := stream.Send(&pb.DataResponse{Message: `<server_status connected="true"/>`}); err != nil {
		return err
	}
	return status.Error(codes.Unavailable, "stream restart")
}

func (s *streamRestartServer) SendCommand(
	context.Context,
	*pb.SendCommandRequest,
) (*pb.SendCommandResponse, error) {
	return &pb.SendCommandResponse{Message: `<result success="true"/>`}, nil
}

func TestRunWithReconnectRecreatesClientAfterStreamFailure(t *testing.T) {
	t.Setenv("TC_LOGIN", "test-login")
	t.Setenv("TC_PASSWORD", "test-password")
	t.Setenv("TC_HOST", "test-host")
	t.Setenv("TC_PORT", "12345")

	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	fake := &streamRestartServer{}
	pb.RegisterConnectServiceServer(grpcServer, fake)
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Connect()
	readyCtx, readyCancel := context.WithTimeout(context.Background(), time.Second)
	defer readyCancel()
	for conn.GetState() != connectivity.Ready {
		if !conn.WaitForStateChange(readyCtx, conn.GetState()) {
			t.Fatal("gRPC connection did not become ready")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := pb.NewConnectServiceClient(conn)
	created := 0
	sessions := 0
	err = RunWithReconnect(
		ctx,
		func() (*TCClient, error) {
			created++
			if created > 2 {
				cancel()
				return nil, errors.New("stop reconnect test")
			}
			return NewTCClientWithConn(client, nil)
		},
		func(sessionCtx context.Context, client *TCClient) error {
			sessions++
			select {
			case <-sessionCtx.Done():
				return sessionCtx.Err()
			case <-client.ShutdownChannel:
				return errTestSessionClosed
			}
		},
		ReconnectConfig{
			RetryMin:           time.Millisecond,
			RetryMax:           2 * time.Millisecond,
			SessionStableAfter: time.Hour,
			DisconnectTimeout:  time.Second,
		},
	)
	if err != nil {
		t.Fatalf("RunWithReconnect error = %v", err)
	}
	if created != 3 {
		t.Fatalf("factory calls = %d, want 3", created)
	}
	if sessions != 2 {
		t.Fatalf("sessions = %d, want 2", sessions)
	}
	if got := fake.streams.Load(); got != 2 {
		t.Fatalf("streams = %d, want 2", got)
	}
}
