// Command md-recorder writes the live order book and trade feed of the exchange
// into ClickHouse (md.book_events, md.trades) for the AI-terminal research
// pipeline (replay, backtests). It is read-only on the exchange side: it
// subscribes the <quotes> and <alltrades> feeds and never sends trading
// commands.
//
// Environment:
//
//	TC_HOST, TC_PORT        exchange address (required)
//	TC_LOGIN, TC_PASSWORD   exchange credentials (required, never logged)
//	MD_CLICKHOUSE_URL       ClickHouse HTTP endpoint (required)
//	MD_CLICKHOUSE_USER      ClickHouse user (fallback CLICKHOUSE_USER)
//	MD_CLICKHOUSE_PASSWORD  ClickHouse password (fallback CLICKHOUSE_PASSWORD)
//	MD_SYMBOLS              comma list of BOARD:SECCODE (default TQBR:SBER)
//
// The command creates the md tables if needed, then records session after
// session until SIGINT/SIGTERM. The live check itself needs the user's consent
// and the demo-server trading window; see docs/ai-terminal/data.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native"
	"github.com/kmlebedev/txmlconnector/terminal/chstore"
	"github.com/kmlebedev/txmlconnector/terminal/recorder"
)

// Reconnect tuning of the recorder command.
const (
	reconnectMin = 5 * time.Second
	reconnectMax = time.Minute
	// sessionStable is how long a session must survive to reset the backoff.
	sessionStable = 5 * time.Minute
)

// config is the resolved command configuration: all values come from env.
type config struct {
	addr       string
	login      string
	password   string
	clickhouse string
	chUser     string
	chPassword string
	symbols    []string
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := configFromEnv()
	if err != nil {
		log.Fatalf("md-recorder: %v", err)
	}
	client := chstore.New(cfg.clickhouse, cfg.chUser, cfg.chPassword, nil)
	if err := recorder.EnsureSchema(ctx, client); err != nil {
		log.Fatalf("md-recorder: %v", err)
	}
	sink := recorder.NewClickHouseSink(ctx, client)
	log.Printf("md-recorder: writing %s and %s, symbols=%s", recorder.BookEventsTable, recorder.TradesTable, strings.Join(cfg.symbols, ","))
	backoff := reconnectMin
	for ctx.Err() == nil {
		started := time.Now()
		err := recordSession(ctx, cfg, sink)
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			break
		}
		log.Printf("md-recorder: session ended: %v", err)
		if time.Since(started) >= sessionStable {
			backoff = reconnectMin
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
	log.Print("md-recorder: stopped")
}

// recordSession runs one exchange session end to end.
func recordSession(ctx context.Context, cfg config, sink recorder.Sink) error {
	client, err := native.Dial(ctx, native.ClientConfig{Address: cfg.addr, Login: cfg.login, Password: cfg.password})
	if err != nil {
		// The error carries the transport/login failure, never the credentials.
		return fmt.Errorf("dial: %w", err)
	}
	defer client.Close()
	log.Printf("md-recorder: connected to %s, recording %d symbols", cfg.addr, len(cfg.symbols))
	return recorder.Run(ctx, client, sink, cfg.symbols)
}

// configFromEnv reads and validates the command environment. Error messages name
// the missing variable but never echo a value.
func configFromEnv() (config, error) {
	var cfg config
	host, port := os.Getenv("TC_HOST"), os.Getenv("TC_PORT")
	if host == "" || port == "" {
		return cfg, errors.New("TC_HOST and TC_PORT are required")
	}
	cfg.addr = net.JoinHostPort(host, port)
	cfg.login, cfg.password = os.Getenv("TC_LOGIN"), os.Getenv("TC_PASSWORD")
	if cfg.login == "" || cfg.password == "" {
		return cfg, errors.New("TC_LOGIN and TC_PASSWORD are required")
	}
	cfg.clickhouse = os.Getenv("MD_CLICKHOUSE_URL")
	if cfg.clickhouse == "" {
		return cfg, errors.New("MD_CLICKHOUSE_URL is required")
	}
	cfg.chUser = envOr("MD_CLICKHOUSE_USER", os.Getenv("CLICKHOUSE_USER"))
	cfg.chPassword = envOr("MD_CLICKHOUSE_PASSWORD", os.Getenv("CLICKHOUSE_PASSWORD"))
	raw := envOr("MD_SYMBOLS", "TQBR:SBER")
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		board, secCode, ok := strings.Cut(item, ":")
		if !ok || strings.TrimSpace(board) == "" || strings.TrimSpace(secCode) == "" {
			return cfg, fmt.Errorf("MD_SYMBOLS: bad symbol %q (want BOARD:SECCODE)", item)
		}
		cfg.symbols = append(cfg.symbols, item)
	}
	if len(cfg.symbols) == 0 {
		return cfg, errors.New("MD_SYMBOLS is empty")
	}
	return cfg, nil
}

// envOr returns the environment value or the fallback.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
