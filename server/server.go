package tcServer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kmlebedev/txmlconnector/connector"
	pb "github.com/kmlebedev/txmlconnector/proto"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

const defaultAddress = ":50051"

type Config struct {
	Address          string
	SubscriberBuffer int
	ShutdownTimeout  time.Duration
	Connector        connector.Config
}

func ConfigFromEnv() Config {
	address := os.Getenv("TC_LISTEN_ADDR")
	if address == "" {
		address = defaultAddress
	}
	return Config{
		Address:          address,
		SubscriberBuffer: subscriberBufferFromEnv(),
		ShutdownTimeout:  10 * time.Second,
		Connector:        connector.ConfigFromEnv(),
	}
}

func subscriberBufferFromEnv() int {
	value := os.Getenv("TC_SUBSCRIBER_BUFFER")
	if value == "" {
		return defaultSubscriberBuffer
	}
	buffer, err := strconv.Atoi(value)
	if err != nil || buffer < 1 {
		log.Warnf("invalid TC_SUBSCRIBER_BUFFER %q; using %d", value, defaultSubscriberBuffer)
		return defaultSubscriberBuffer
	}
	return buffer
}

// Run starts the process-level server and handles SIGINT/SIGTERM gracefully.
func Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunContext(ctx, ConfigFromEnv())
}

func RunContext(ctx context.Context, config Config) error {
	configureLogging()
	if config.Address == "" {
		config.Address = defaultAddress
	}
	native, err := connector.New(config.Connector)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", config.Address, err)
	}
	defer listener.Close()
	return Serve(ctx, listener, native, config)
}

// Serve wires the native adapter to the transport. The injected connector and
// listener make the application lifecycle testable without a DLL or real port.
func Serve(ctx context.Context, listener net.Listener, native connector.Connector, config Config) error {
	if listener == nil {
		return errors.New("listener is required")
	}
	if native == nil {
		return errors.New("connector is required")
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 10 * time.Second
	}

	service := newService(native, config.SubscriberBuffer)
	if err := native.Start(service.handleMessage); err != nil {
		return fmt.Errorf("start connector: %w", err)
	}
	defer func() {
		if err := native.Close(); err != nil {
			log.Errorf("close connector: %v", err)
		}
	}()

	grpcServer := grpc.NewServer()
	pb.RegisterConnectServiceServer(grpcServer, service)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- grpcServer.Serve(listener)
	}()

	log.Infof("server listening on %s", listener.Addr())
	select {
	case err := <-serveErr:
		service.messages.close()
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve gRPC: %w", err)
		}
		return nil
	case <-ctx.Done():
		service.messages.close()
		gracefulStop(grpcServer, config.ShutdownTimeout)
		select {
		case <-serveErr:
		case <-time.After(config.ShutdownTimeout):
			return errors.New("gRPC server did not stop")
		}
		return nil
	}
}

func gracefulStop(server *grpc.Server, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		server.Stop()
	}
}

func configureLogging() {
	level := log.InfoLevel
	if value := os.Getenv("TC_LOG_LEVEL"); value != "" {
		if parsed, err := log.ParseLevel(value); err == nil {
			level = parsed
		}
	}
	log.SetLevel(level)
}
