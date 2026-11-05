package tcServer

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tcClient "github.com/kmlebedev/txmlconnector/client"
	tcCommands "github.com/kmlebedev/txmlconnector/client/commands"
	"github.com/kmlebedev/txmlconnector/connector"
	nativeconnector "github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/testserver"
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

// TestBackendSelection: the platform default is native outside Windows; an
// explicit dll backend keeps the platform error on non-Windows.
func TestBackendSelection(t *testing.T) {
	if got := defaultBackend(); runtime.GOOS != "windows" && got != "native" {
		t.Fatalf("defaultBackend = %q, want native on %s", got, runtime.GOOS)
	}
	if runtime.GOOS != "windows" {
		if _, err := newBackend(Config{Backend: "dll"}); !errors.Is(err, connector.ErrUnsupportedPlatform) {
			t.Fatalf("dll backend on %s err = %v, want ErrUnsupportedPlatform", runtime.GOOS, err)
		}
	}
	if _, err := newBackend(Config{Backend: "bogus"}); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

// TestServerRunsNativeBackend drives the gRPC API through the pure-Go native
// backend over the in-process testserver (no DLL/Wine): connect -> server_status.
func TestServerRunsNativeBackend(t *testing.T) {
	srv := testserver.New(t)
	raw, err := os.ReadFile("../native/records/testdata/startup-demo.hex")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Startup, err = hex.DecodeString(strings.TrimSpace(string(raw))); err != nil {
		t.Fatalf("startup fixture: %v", err)
	}
	cfg := Config{
		Backend:          "native",
		Native:           nativeconnector.Config{Dial: srv.Dial},
		SubscriberBuffer: 4,
		ShutdownTimeout:  time.Second,
	}
	backend, err := newBackend(cfg)
	if err != nil {
		t.Fatalf("newBackend(native): %v", err)
	}
	listener := bufconn.Listen(1024 * 1024)
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- Serve(ctx, listener, backend, cfg) }()
	conn, err := grpc.NewClient(
		"passthrough:///native-backend",
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
	streamCtx, streamCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer streamCancel()
	stream, err := client.FetchResponseData(streamCtx, &pb.DataRequest{})
	if err != nil {
		t.Fatalf("FetchResponseData: %v", err)
	}
	connect := "<command id=" + quoteServer + "connect" + quoteServer + "><login>" + testserver.Login +
		"</login><password>" + testserver.Password + "</password><host>127.0.0.1</host><port>1</port></command>"
	resp, err := client.SendCommand(context.Background(), &pb.SendCommandRequest{Message: connect})
	if err != nil {
		t.Fatalf("connect via native backend: %v", err)
	}
	if !strings.Contains(resp.Message, "success="+quoteServer+"true"+quoteServer) {
		t.Fatalf("connect response = %s", resp.Message)
	}
	// connect is asynchronous (DLL contract); wait for the connected status
	// before sending a command that needs the connection.
	for {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("wait connected status: %v", err)
		}
		if strings.Contains(msg.Message, "connected="+quoteServer+"true"+quoteServer) {
			break
		}
	}
	resp, err = client.SendCommand(context.Background(), &pb.SendCommandRequest{
		Message: "<command id=" + quoteServer + "server_status" + quoteServer + "/>",
	})
	if err != nil {
		t.Fatalf("server_status via native backend: %v", err)
	}
	if !strings.Contains(resp.Message, "success="+quoteServer+"true"+quoteServer) {
		t.Fatalf("server_status response = %s", resp.Message)
	}
	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("native backend server: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native backend server did not stop")
	}
}

const quoteServer = "\""

// TestNativeBackendTCClientRE4: the TCClient path (async connect + stream) must
// receive connected=true without the stream closing immediately (RE-4 demo EOF).
func TestNativeBackendTCClientRE4(t *testing.T) {
	srv := testserver.New(t)
	raw, err := os.ReadFile("../native/records/testdata/startup-demo.hex")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Startup, err = hex.DecodeString(strings.TrimSpace(string(raw))); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Backend: "native", Native: nativeconnector.Config{Dial: srv.Dial}, SubscriberBuffer: 4096, ShutdownTimeout: time.Second}
	backend, err := newBackend(cfg)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(4 * 1024 * 1024)
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- Serve(ctx, listener, backend, cfg) }()
	conn, err := grpc.NewClient("passthrough:///native-tcclient",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tc, err := tcClient.NewTCClientWithConnAndOptions(pb.NewConnectServiceClient(conn), conn, tcClient.Options{
		Connect: &tcClient.ConnectConfig{Login: testserver.Login, Password: testserver.Password, Host: "127.0.0.1", Port: "1"},
	})
	if err != nil {
		t.Fatalf("NewTCClient: %v", err)
	}
	defer tc.Close()
	select {
	case status := <-tc.ServerStatusChan:
		if status.Connected != "true" {
			t.Fatalf("status = %+v, want connected=true", status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no connected=true status")
	}
	if err := tc.SendCommand(tcCommands.Command{Id: "get_securities"}); err != nil {
		t.Fatalf("get_securities after connected: %v", err)
	}
	if err := tc.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}
