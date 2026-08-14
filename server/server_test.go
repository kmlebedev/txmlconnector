package tcServer

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/connector"
	pb "github.com/kmlebedev/txmlconnector/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type fakeConnector struct {
	mu       sync.Mutex
	handler  connector.MessageHandler
	started  bool
	closed   bool
	commands []string
}

func (f *fakeConnector) Start(handler connector.MessageHandler) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = handler
	f.started = true
	return nil
}

func (f *fakeConnector) SendCommand(_ context.Context, message string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, message)
	return `<result success="true"/>`, nil
}

func (f *fakeConnector) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeConnector) emit(message string) {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	handler(message)
}

func TestServeBridgesConnectorAndGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	native := &fakeConnector{}
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- Serve(ctx, listener, native, Config{
			SubscriberBuffer: 4,
			ShutdownTimeout:  time.Second,
		})
	}()

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
	client := pb.NewConnectServiceClient(conn)
	stream, err := client.FetchResponseData(context.Background(), &pb.DataRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Header(); err != nil {
		t.Fatal(err)
	}

	command := `<command id="server_status"/>`
	response, err := client.SendCommand(context.Background(), &pb.SendCommandRequest{Message: command})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message != `<result success="true"/>` {
		t.Fatalf("response = %q", response.Message)
	}

	native.emit(`<server_status connected="true"/>`)
	received, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if received.Message != `<server_status connected="true"/>` {
		t.Fatalf("message = %q", received.Message)
	}

	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}

	native.mu.Lock()
	defer native.mu.Unlock()
	if !native.started || !native.closed {
		t.Fatalf("connector lifecycle: started=%v closed=%v", native.started, native.closed)
	}
	if len(native.commands) != 1 || native.commands[0] != command {
		t.Fatalf("commands = %q", native.commands)
	}
}

func TestConfigFromEnvReadsSubscriberBuffer(t *testing.T) {
	t.Setenv("TC_SUBSCRIBER_BUFFER", "16384")

	config := ConfigFromEnv()

	if config.SubscriberBuffer != 16384 {
		t.Fatalf("subscriber buffer = %d", config.SubscriberBuffer)
	}
}

func TestConfigFromEnvRejectsInvalidSubscriberBuffer(t *testing.T) {
	t.Setenv("TC_SUBSCRIBER_BUFFER", "0")

	config := ConfigFromEnv()

	if config.SubscriberBuffer != defaultSubscriberBuffer {
		t.Fatalf("subscriber buffer = %d", config.SubscriberBuffer)
	}
}
