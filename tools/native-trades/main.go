// Command native-trades subscribes to the TRANSAQ <alltrades> feed with the
// native client (no DLL, no XML) and publishes every trade to NATS Core as a
// protobuf message (proto/marketdata.Trade) on
// <prefix>.<board>.<seccode>.
//
//	TC_LOGIN=... TC_PASSWORD=... native-trades -addr tr1.finam.ru:3900 \
//	    -nats nats://127.0.0.1:4222 -sec TQBR:SBER,TQBR:GAZP,FUT:SiZ6
//
// It exits on a lost server connection; run it under a supervisor that
// restarts it (reconnect inside the client is not implemented yet).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/natsout"
)

func main() {
	addr := flag.String("addr", "", "TRANSAQ server host:port")
	natsURL := flag.String("nats", nats.DefaultURL, "NATS server URL")
	prefix := flag.String("prefix", natsout.DefaultTradePrefix, "subject prefix")
	secs := flag.String("sec", "", "comma-separated BOARD:SECCODE list")
	delay := flag.Duration("rqdelay", 100*time.Millisecond, "RQ_MAIN polling interval")
	statsEvery := flag.Duration("stats", time.Minute, "log statistics every interval (0: never)")
	flag.Parse()
	login, password := os.Getenv("TC_LOGIN"), os.Getenv("TC_PASSWORD")
	symbols, err := parseSymbols(*secs)
	if *addr == "" || login == "" || password == "" || err != nil || len(symbols) == 0 {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		fmt.Fprintln(os.Stderr, "usage: TC_LOGIN=... TC_PASSWORD=... native-trades -addr host:port -sec TQBR:SBER[,FUT:SiZ6...] [-nats url]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *addr, login, password, *natsURL, *prefix, symbols, *delay, *statsEvery); err != nil {
		log.Fatal(err)
	}
}

func parseSymbols(list string) ([]model.Symbol, error) {
	var out []model.Symbol
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		board, code, ok := strings.Cut(item, ":")
		if !ok || board == "" || code == "" {
			return nil, fmt.Errorf("bad security %q, want BOARD:SECCODE", item)
		}
		out = append(out, model.Symbol{Board: board, SecCode: code})
	}
	return out, nil
}

func run(ctx context.Context, addr, login, password, natsURL, prefix string, symbols []model.Symbol, delay, statsEvery time.Duration) error {
	nc, err := nats.Connect(natsURL, nats.Name("txmlconnector native-trades"))
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer nc.Drain()
	sink := natsout.NewTradeSink(nc, prefix)

	start := time.Now()
	client, err := native.Dial(ctx, native.ClientConfig{Address: addr, Login: login, Password: password})
	if err != nil {
		return fmt.Errorf("transaq: %w", err)
	}
	defer func() {
		logoutCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = client.Logout(logoutCtx)
	}()
	log.Printf("connected in %v: %d securities, server tz %q", time.Since(start).Round(time.Millisecond),
		client.Catalog().Len(), client.ServerTZ())
	if err := client.SubscribeAllTrades(ctx, symbols...); err != nil {
		return err
	}
	log.Printf("subscribed to alltrades: %v; publishing to %s.<board>.<seccode>", symbols, prefix)

	var lastStats time.Time
	for {
		select {
		case <-ctx.Done():
			log.Printf("stopping: %+v", client.Stats())
			return nil
		case <-time.After(delay):
		}
		for more := true; more; {
			trades, m, err := client.Poll(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return fmt.Errorf("transaq: %w", err)
			}
			more = m
			if err := sink.Publish(trades); err != nil {
				return fmt.Errorf("nats: %w", err)
			}
		}
		if statsEvery > 0 && time.Since(lastStats) >= statsEvery {
			lastStats = time.Now()
			log.Printf("stats: %+v", client.Stats())
		}
	}
}
