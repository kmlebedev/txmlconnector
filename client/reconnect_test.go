package tcClient

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pb "github.com/kmlebedev/txmlconnector/proto"
	"google.golang.org/grpc"
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
