// Command native-soak runs a long read-only native session (M7): Dial, subscribe
// 20 securities to alltrades/quotations/quotes, Run, and log Stats/RSS/latency
// every five minutes. It is env-gated and does nothing live unless TC_SOAK=1.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	native "github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
)

var defaultSymbols = []model.Symbol{
	{Board: "TQBR", SecCode: "SBER"}, {Board: "TQBR", SecCode: "GAZP"},
	{Board: "TQBR", SecCode: "LKOH"}, {Board: "TQBR", SecCode: "ROSN"},
	{Board: "TQBR", SecCode: "NVTK"}, {Board: "TQBR", SecCode: "GMKN"},
	{Board: "TQBR", SecCode: "TATN"}, {Board: "TQBR", SecCode: "MTSS"},
	{Board: "TQBR", SecCode: "MGNT"}, {Board: "TQBR", SecCode: "SNGS"},
	{Board: "TQBR", SecCode: "SNGSP"}, {Board: "TQBR", SecCode: "VTBR"},
	{Board: "TQBR", SecCode: "ALRS"}, {Board: "TQBR", SecCode: "CHMF"},
	{Board: "TQBR", SecCode: "PLZL"}, {Board: "TQBR", SecCode: "POLY"},
	{Board: "TQBR", SecCode: "YNDX"}, {Board: "TQBR", SecCode: "MOEX"},
	{Board: "TQBR", SecCode: "AFKS"}, {Board: "TQBR", SecCode: "RUAL"},
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// parseSymbols parses TC_SOAK_SYMBOLS="TQBR:SBER,TQBR:GAZP,...".
func parseSymbols(s string) ([]model.Symbol, error) {
	if strings.TrimSpace(s) == "" {
		return defaultSymbols, nil
	}
	var out []model.Symbol
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		board, code, ok := strings.Cut(item, ":")
		if !ok || board == "" || code == "" {
			return nil, fmt.Errorf("bad symbol %q, want BOARD:SECCODE", item)
		}
		out = append(out, model.Symbol{Board: board, SecCode: code})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("TC_SOAK_SYMBOLS is empty")
	}
	return out, nil
}

// rssBytes returns process RSS on Linux; elsewhere runtime.MemStats.Sys is an
// upper-bound fallback (documented in the log label).
func rssBytes() uint64 {
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		if fields := strings.Fields(string(b)); len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return pages * uint64(os.Getpagesize())
			}
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys
}

func main() {
	host, port := os.Getenv("TC_HOST"), os.Getenv("TC_PORT")
	login, password := os.Getenv("TC_LOGIN"), os.Getenv("TC_PASSWORD")
	if host == "" || port == "" || login == "" || password == "" {
		log.Print("native-soak: TC_HOST/TC_PORT/TC_LOGIN/TC_PASSWORD not set; skipping live session")
		return
	}
	if os.Getenv("TC_SOAK") != "1" {
		log.Print("native-soak: set TC_SOAK=1 to run the live long session")
		return
	}
	symbols, err := parseSymbols(os.Getenv("TC_SOAK_SYMBOLS"))
	if err != nil {
		log.Fatalf("native-soak: %v", err)
	}
	duration := time.Duration(0)
	if raw := env("TC_SOAK_DURATION", ""); raw != "" {
		if duration, err = time.ParseDuration(raw); err != nil {
			log.Fatalf("native-soak: TC_SOAK_DURATION: %v", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, duration)
		defer cancel()
	}
	start := time.Now()
	client, err := native.Dial(ctx, native.ClientConfig{Address: host + ":" + port, Login: login, Password: password})
	if err != nil {
		log.Fatalf("native-soak: dial: %v", err)
	}
	defer client.Close()
	sub := native.Subscription{AllTrades: symbols, Quotations: symbols, Quotes: symbols}
	if err := client.Subscribe(ctx, sub); err != nil {
		log.Fatalf("native-soak: subscribe: %v", err)
	}
	log.Printf("native-soak: connected to %s:%s, %d symbols subscribed", host, port, len(symbols))
	var runs, events, trades, recoveries int64
	var maxGapNs int64
	lastEvent := time.Now()
	handler := func(items []model.Event) error {
		now := time.Now()
		if gap := now.Sub(lastEvent).Nanoseconds(); gap > atomic.LoadInt64(&maxGapNs) {
			atomic.StoreInt64(&maxGapNs, gap)
		}
		lastEvent = now
		atomic.AddInt64(&runs, 1)
		for _, ev := range items {
			atomic.AddInt64(&events, 1)
			switch e := ev.(type) {
			case *model.Trade:
				_ = e
				atomic.AddInt64(&trades, 1)
			case model.ServerStatus:
				if e.Recover {
					atomic.AddInt64(&recoveries, 1)
				}
			}
		}
		return nil
	}
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	runDone := make(chan error, 1)
	go func() { runDone <- client.Run(ctx, native.RunOptions{}, handler) }()
	for {
		select {
		case <-ticker.C:
			st := client.Stats()
			log.Printf("native-soak: uptime=%s runs=%d events=%d trades=%d recoveries=%d unknown=%d stats=%+v rss=%dMiB maxGap=%s",
				time.Since(start).Round(time.Second), atomic.LoadInt64(&runs), atomic.LoadInt64(&events),
				atomic.LoadInt64(&trades), atomic.LoadInt64(&recoveries), st.UnknownRecords,
				st, rssBytes()>>20, time.Duration(atomic.LoadInt64(&maxGapNs)))
		case err := <-runDone:
			st := client.Stats()
			log.Printf("native-soak: Run ended err=%v uptime=%s stats=%+v rss=%dMiB", err, time.Since(start).Round(time.Second), st, rssBytes()>>20)
			if st.UnknownRecords != 0 {
				log.Fatalf("native-soak: UnknownRecords=%d, want 0", st.UnknownRecords)
			}
			return
		case <-ctx.Done():
			st := client.Stats()
			log.Printf("native-soak: stopped uptime=%s stats=%+v rss=%dMiB unknown=%d", time.Since(start).Round(time.Second), st, rssBytes()>>20, st.UnknownRecords)
			if st.UnknownRecords != 0 {
				log.Fatalf("native-soak: UnknownRecords=%d, want 0", st.UnknownRecords)
			}
			return
		}
	}
}
