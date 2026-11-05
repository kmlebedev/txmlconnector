// Command terminal runs the AI trading terminal backend: an offline demo
// (embedded TRANSAQ snapshot) or a live read-only session, the paper broker,
// the AI agent and the HTTP/JSON/SSE API with the embedded SPA.
//
// The server listens on loopback by default and is meant to run locally only.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal"
	"github.com/kmlebedev/txmlconnector/terminal/agent"
	"github.com/kmlebedev/txmlconnector/terminal/api"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// version is the build version, overridable with -ldflags "-X main.version=...".
var version = "0.1.0"

// HTTP server timeouts: SSE needs an unbounded write timeout, so only the
// header read is bounded (a stalled client then cannot hold a connection open).
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func main() {
	logger := log.New(os.Stderr, "terminal: ", log.LstdFlags)
	if err := run(logger); err != nil {
		logger.Printf("ошибка: %v", err)
		os.Exit(1)
	}
}

// run parses the flags, wires the backend and serves until SIGINT/SIGTERM.
func run(logger *log.Logger) error {
	addr := flag.String("addr", "127.0.0.1:8090", "адрес HTTP-сервера (по умолчанию только loopback)")
	modeFlag := flag.String("mode", "auto", "режим данных: auto|offline|live")
	seedPath := flag.String("seed", "", "путь к seed.json.gz (по умолчанию встроенный снапшот)")
	paperCash := flag.Float64("paper-cash", 1_000_000, "стартовый кэш бумажного счёта, ₽")
	feeBps := flag.Float64("fee-bps", 5, "комиссия бумажных сделок, б.п.")
	llmFlag := flag.String("llm", "", "off — принудительно выключить LLM; иначе LLM из env")
	liveTrading := flag.Bool("live-trading", false, "разрешить реальные заявки (вместе с TERMINAL_LIVE_TRADING=1)")
	allowedOrigins := flag.String("allowed-origins", "", "дополнительные Origin для небезопасных методов API, через запятую (API также принимает свой Origin)")
	showVersion := flag.Bool("version", false, "печать версии и выход")
	flag.Parse()

	if *showVersion {
		fmt.Printf("AI Terminal %s\n", version)
		return nil
	}

	exchange := exchangeConfigFromEnv()
	mode, err := resolveMode(*modeFlag, exchange)
	if err != nil {
		return err
	}
	llmCfg, err := llmConfigFromEnv()
	if err != nil {
		return err
	}
	if *llmFlag == "off" {
		llmCfg.Enabled = false
		llmCfg.APIKey = ""
	} else if *llmFlag != "" {
		llmCfg.Model = *llmFlag
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	feedSvc, err := feed.New(feed.Config{
		Mode:     mode,
		Address:  exchange.Address,
		Login:    exchange.Login,
		Password: exchange.Password,
		SeedDir:  *seedPath,
	})
	if err != nil {
		return fmt.Errorf("инициализация рыночных данных: %w", err)
	}
	if err := feedSvc.Start(ctx); err != nil {
		_ = feedSvc.Close()
		return err
	}

	broker := paper.New(paper.Config{InitialCash: *paperCash, FeeBps: *feeBps})
	market := newFeedAdapter(feedSvc)
	portfolio := newPaperAdapter(broker, feedSvc)
	engine, err := agent.New(agent.Config{
		LLM:      llmCfg,
		Universe: feedSvc.Universe(),
		FeeBps:   *feeBps,
	}, market, portfolio)
	if err != nil {
		_ = feedSvc.Close()
		return fmt.Errorf("инициализация AI-агента: %w", err)
	}

	liveTradingAllowed := *liveTrading && os.Getenv("TERMINAL_LIVE_TRADING") == "1"
	// The API token and the instance nonce come only from the environment: the
	// DSH plugin generates both per launch and the token never touches disk.
	guardCfg := api.GuardConfig{
		AllowedOrigins: splitList(*allowedOrigins),
		Token:          os.Getenv("TERMINAL_API_TOKEN"),
	}
	apiSrv, err := api.New(api.Config{
		Version:            version,
		LiveTradingAllowed: liveTradingAllowed,
		PaperInitialCash:   *paperCash,
		SeedGeneratedAt:    feedSvc.Status().SeedGeneratedAt,
		Web:                terminal.WebFS(),
		Guard:              guardCfg,
		InstanceID:         os.Getenv("TERMINAL_INSTANCE_ID"),
	}, api.Deps{
		Feed:     feedSvc,
		Paper:    broker,
		Agent:    engine,
		Backtest: runBacktest,
	})
	if err != nil {
		_ = feedSvc.Close()
		return fmt.Errorf("инициализация HTTP API: %w", err)
	}

	// No global WriteTimeout: /api/events is a long-lived SSE stream. The API
	// bounds every SSE write with a per-write deadline instead (see api/sse.go).
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		WriteTimeout:      0,
	}
	if !isLoopbackAddr(*addr) {
		logger.Printf("внимание: сервер слушает не только loopback (%s)", *addr)
	}
	if guardCfg.Token == "" {
		logger.Printf("TERMINAL_API_TOKEN не задан: /api/* без токена (защита — Host/Origin/Content-Type)")
	}
	if liveTradingAllowed {
		logger.Printf("реальная торговля разрешена флагами, но маршрутизация на биржу в итерации 1 не реализована (paper only)")
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.ListenAndServe()
	}()
	logger.Printf("AI-терминал %s: режим=%s, %s, бумажный счёт %.0f ₽, LLM=%s",
		version, mode, "http://"+*addr, *paperCash, llmModeLabel(engine.Status().LLM))

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			apiSrv.Close()
			_ = feedSvc.Close()
			return fmt.Errorf("HTTP-сервер: %w", err)
		}
	case <-ctx.Done():
		logger.Printf("завершение работы")
	}
	// Close the API first: it drops the SSE clients, so Shutdown does not wait
	// for their long-lived responses.
	apiSrv.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("не удалось корректно остановить HTTP-сервер: %v", err)
	}
	if err := feedSvc.Close(); err != nil {
		logger.Printf("не удалось закрыть источник данных: %v", err)
	}
	return nil
}

// runBacktest is the HTTP backtest runner of the API: the handler fills the
// bars from the feed, the engine lives in terminal/backtest.
func runBacktest(req backtest.Request) (backtest.Result, error) {
	return backtest.Run(req)
}

// splitList parses a comma-separated flag value into trimmed non-empty items.
func splitList(raw string) []string {
	var items []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// llmModeLabel renders a secret-free LLM status for the startup log.
func llmModeLabel(status agent.LLMStatus) string {
	if status.Enabled {
		return "вкл (" + status.Model + ")"
	}
	return "локальный аналитик"
}

// isLoopbackAddr reports whether addr binds loopback only. An empty host
// (":8090"), "0.0.0.0" or "::" means all interfaces: the API is then
// reachable from the network and must warn like any public bind.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
