// Command terminal-seed captures the offline market-data snapshot of the AI
// terminal from a TRANSAQ demo/production server through the native client
// (no DLL, no Wine) and writes it as gzip JSON for terminal/seeddata.
//
// It is read-only: the tool logs in, fetches candle history for the universe,
// subscribes to quotations/order books/trades for a few seconds and writes only
// market data. No account, client, login or union data is captured; the login
// and password are taken from the environment and never printed.
//
//	TRANSAQ_ADDR=host:port TRANSAQ_LOGIN=... TRANSAQ_PASSWORD=... \
//	    go run ./tools/terminal-seed -out terminal/seeddata/data/seed.json.gz
//
// Fallbacks: TC_HOST/TC_PORT and TC_LOGIN/TC_PASSWORD.
package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
)

const defaultUniverse = "SBER,GAZP,LKOH,SMLT,PLZL,VTBR,GMKN,YDEX,T,OZON,SVCB,MOEX,ROSN,AFLT"

// Counts are the number of candles captured per period (seconds).
var candleCounts = []struct {
	periodSec int
	count     int
}{
	{60, 1200},
	{300, 700},
	{900, 400},
	{3600, 300},
	{86400, 260},
}

type barJSON struct {
	T int64   `json:"t"`
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V int64   `json:"v"`
}

type levelJSON struct {
	Price float64 `json:"price"`
	Qty   float64 `json:"qty"`
}

type tradeJSON struct {
	T     int64   `json:"t"`
	Price float64 `json:"price"`
	Qty   int64   `json:"qty"`
	Side  string  `json:"side,omitempty"`
}

type instrumentJSON struct {
	Symbol    string               `json:"symbol"`
	Board     string               `json:"board"`
	SecCode   string               `json:"seccode"`
	Name      string               `json:"name"`
	ShortName string               `json:"shortName"`
	Currency  string               `json:"currency"`
	Decimals  int                  `json:"decimals"`
	LotSize   int64                `json:"lotSize"`
	MinStep   float64              `json:"minStep"`
	PrevClose float64              `json:"prevClose"`
	Last      float64              `json:"last"`
	Change    float64              `json:"change"`
	ChangePct float64              `json:"changePct"`
	Open      float64              `json:"open"`
	High      float64              `json:"high"`
	Low       float64              `json:"low"`
	Volume    int64                `json:"volume"`
	Bid       float64              `json:"bid"`
	Ask       float64              `json:"ask"`
	OrderBook orderBookJSON        `json:"orderbook"`
	Spark     []float64            `json:"spark"`
	Candles   map[string][]barJSON `json:"candles"`
	Trades    []tradeJSON          `json:"trades,omitempty"`
}

type orderBookJSON struct {
	Bids []levelJSON `json:"bids"`
	Asks []levelJSON `json:"asks"`
}

type seedJSON struct {
	Schema      int              `json:"schema"`
	GeneratedAt string           `json:"generatedAt"`
	Source      string           `json:"source"`
	ExchangeTZ  string           `json:"exchangeTZ"`
	Universe    []string         `json:"universe"`
	Instruments []instrumentJSON `json:"instruments"`
}

// quoteState is the merged latest quotation of one symbol.
type quoteState struct {
	Last, Change, Bid, Ask              float64
	Open, High, Low                     float64
	Volume                              int64
	HasLast, HasChange                  bool
	HasBid, HasAsk                      bool
	HasOpen, HasHigh, HasLow, HasVolume bool
	Time                                string
}

type marketState struct {
	mu     sync.Mutex
	quotes map[string]*quoteState
	books  map[string][]model.Quote
	trades map[string][]tradeJSON
	status string
}

func main() {
	addr := flag.String("addr", "", "TRANSAQ server host:port (default TRANSAQ_ADDR or TC_HOST:TC_PORT)")
	out := flag.String("out", "terminal/seeddata/data/seed.json.gz", "output .json.gz path")
	universe := flag.String("universe", defaultUniverse, "comma-separated seccodes on TQBR")
	wait := flag.Duration("wait", 8*time.Second, "how long to collect quotations/books/trades")
	flag.Parse()

	server := *addr
	if server == "" {
		server = os.Getenv("TRANSAQ_ADDR")
	}
	if server == "" {
		host, port := os.Getenv("TC_HOST"), os.Getenv("TC_PORT")
		if host != "" && port != "" {
			server = host + ":" + port
		}
	}
	login := firstNonEmpty(os.Getenv("TRANSAQ_LOGIN"), os.Getenv("TC_LOGIN"))
	password := firstNonEmpty(os.Getenv("TRANSAQ_PASSWORD"), os.Getenv("TC_PASSWORD"))
	if server == "" || login == "" || password == "" {
		fmt.Fprintln(os.Stderr, "usage: TRANSAQ_ADDR=host:port TRANSAQ_LOGIN=... TRANSAQ_PASSWORD=... terminal-seed -out seed.json.gz")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, server, login, password, *out, splitUniverse(*universe), *wait); err != nil {
		log.Fatalf("terminal-seed: %v", err)
	}
}

func run(ctx context.Context, server, login, password, out string, seccodes []string, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	start := time.Now()
	client, err := native.Dial(ctx, native.ClientConfig{
		Address:        server,
		Login:          login,
		Password:       password,
		RequestTimeout: 30 * time.Second,
		RqDelay:        100 * time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer client.Close()
	fmt.Printf("login ok in %v, catalog=%d, tz=%q\n", time.Since(start).Round(time.Millisecond), client.Catalog().Len(), client.ServerTZ())

	state := &marketState{
		quotes: map[string]*quoteState{},
		books:  map[string][]model.Quote{},
		trades: map[string][]tradeJSON{},
	}
	pollCtx, stopPoll := context.WithCancel(ctx)
	defer stopPoll()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		err := client.Run(pollCtx, native.RunOptions{RqDelay: 100 * time.Millisecond}, func(events []model.Event) error {
			state.apply(events)
			return nil
		})
		if err != nil && pollCtx.Err() == nil {
			fmt.Fprintf(os.Stderr, "run: %v\n", err)
		}
	}()

	// Resolve the universe through the catalog before requesting history.
	symbols := make([]model.Symbol, 0, len(seccodes))
	seen := map[string]bool{}
	for _, code := range seccodes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		sym := model.Symbol{Board: "TQBR", SecCode: code}
		if _, ok := client.SymbolByBoard(sym.Board, sym.SecCode); !ok {
			fmt.Printf("skip %s: not in the catalog\n", sym)
			continue
		}
		symbols = append(symbols, sym)
	}
	if len(symbols) == 0 {
		return fmt.Errorf("no instruments resolved from the catalog")
	}

	// Period kind ids by their period in seconds.
	kinds := map[int]byte{}
	if st := client.Catalog().Startup(); st != nil {
		for _, k := range st.CandleKinds {
			kinds[int(k.Period)] = k.ID
		}
	}

	seed := seedJSON{
		Schema:      1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Source:      "transaq-demo",
		ExchangeTZ:  "Europe/Moscow",
	}
	for _, sym := range symbols {
		inst, err := captureInstrument(ctx, client, kinds, sym, state, wait)
		if err != nil {
			fmt.Printf("instrument %s: %v\n", sym, err)
			continue
		}
		seed.Instruments = append(seed.Instruments, inst)
		seed.Universe = append(seed.Universe, inst.Symbol)
		fmt.Printf("captured %-12s bars: 1m=%d 5m=%d 15m=%d 1h=%d 1d=%d trades=%d book=%d/%d\n",
			inst.Symbol, len(inst.Candles["60"]), len(inst.Candles["300"]), len(inst.Candles["900"]),
			len(inst.Candles["3600"]), len(inst.Candles["86400"]), len(inst.Trades),
			len(inst.OrderBook.Bids), len(inst.OrderBook.Asks))
	}
	if len(seed.Instruments) == 0 {
		return fmt.Errorf("no instruments captured")
	}
	if err := writeSeed(out, seed); err != nil {
		return err
	}
	fmt.Printf("seed written: %s, %d instruments, %v\n", out, len(seed.Instruments), time.Since(start).Round(time.Millisecond))
	return nil
}

func captureInstrument(ctx context.Context, client *native.Client, kinds map[int]byte, sym model.Symbol, state *marketState, wait time.Duration) (instrumentJSON, error) {
	sec, board, ok := client.Catalog().Lookup(sym.Board, sym.SecCode)
	if !ok {
		return instrumentJSON{}, fmt.Errorf("unknown security")
	}
	decimals, lot, minStep := 2, int64(1), 0.01
	for _, pit := range sec.Pits {
		if pit.Board == board.Num || pit.Default {
			decimals = int(pit.Decimals)
			if pit.LotSize > 0 {
				lot = int64(pit.LotSize)
			}
			minStep = float64(pit.MinStep) / math.Pow10(decimals)
			break
		}
	}
	inst := instrumentJSON{
		Symbol:    sym.String(),
		Board:     sym.Board,
		SecCode:   sym.SecCode,
		Name:      sec.ShortName,
		ShortName: sec.ShortName,
		Currency:  sec.Currency,
		Decimals:  decimals,
		LotSize:   lot,
		MinStep:   minStep,
		Candles:   map[string][]barJSON{},
	}
	if inst.Currency == "" {
		inst.Currency = "RUB"
	}
	// Subscribe before history so quotations/books start arriving while the
	// history requests run; keep the subscription open until the wait ends.
	sub := native.Subscription{Quotations: []model.Symbol{sym}, Quotes: []model.Symbol{sym}, AllTrades: []model.Symbol{sym}}
	if err := client.Subscribe(ctx, sub); err != nil {
		return instrumentJSON{}, fmt.Errorf("subscribe: %w", err)
	}
	defer func() {
		unsub, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Unsubscribe(unsub, sub)
	}()

	for _, c := range candleCounts {
		kind, ok := kinds[c.periodSec]
		if !ok {
			continue
		}
		hctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		candles, err := client.History(hctx, native.HistoryRequest{Symbol: sym, Period: int(kind), Count: c.count, Reset: true})
		cancel()
		if err != nil {
			fmt.Printf("%s period %ds: %v\n", sym, c.periodSec, err)
			continue
		}
		bars := make([]barJSON, 0, len(candles.Items))
		for _, item := range candles.Items {
			bars = append(bars, barJSON{
				T: item.Time.Unix(), O: item.Open.Float64(), H: item.High.Float64(),
				L: item.Low.Float64(), C: item.Close.Float64(), V: item.Volume,
			})
		}
		if len(bars) > 0 {
			inst.Candles[fmt.Sprint(c.periodSec)] = bars
		}
		time.Sleep(80 * time.Millisecond)
	}
	// Sparkline: the last 60 closes of the 5-minute series (fallback 1-minute).
	sparkSrc := inst.Candles["300"]
	if len(sparkSrc) == 0 {
		sparkSrc = inst.Candles["60"]
	}
	start := len(sparkSrc) - 60
	if start < 0 {
		start = 0
	}
	inst.Spark = make([]float64, 0, len(sparkSrc)-start)
	for _, b := range sparkSrc[start:] {
		inst.Spark = append(inst.Spark, b.C)
	}

	// Collect the subscription snapshot for the requested window.
	time.Sleep(wait)
	quote, book, trades := state.snapshot(sym.String())
	if quote != nil {
		inst.Last = quote.Last
		inst.Change = quote.Change
		inst.Open, inst.High, inst.Low, inst.Volume = quote.Open, quote.High, quote.Low, quote.Volume
		inst.Bid, inst.Ask = quote.Bid, quote.Ask
		if quote.HasChange && quote.HasLast {
			inst.PrevClose = round(quote.Last-quote.Change, decimals)
		}
		if inst.PrevClose > 0 && inst.Change != 0 {
			inst.ChangePct = round(inst.Change*100/inst.PrevClose, 2)
		}
	}
	if inst.Last == 0 && len(sparkSrc) > 0 {
		inst.Last = sparkSrc[len(sparkSrc)-1].C
		if inst.PrevClose > 0 {
			inst.ChangePct = round((inst.Last-inst.PrevClose)*100/inst.PrevClose, 2)
		}
	}
	bids := make([]levelJSON, 0, len(book))
	asks := make([]levelJSON, 0, len(book))
	for _, lvl := range book {
		if lvl.Buy > 0 && lvl.Price.Units > 0 {
			bids = append(bids, levelJSON{Price: lvl.Price.Float64(), Qty: float64(lvl.Buy)})
		}
		if lvl.Sell > 0 && lvl.Price.Units > 0 {
			asks = append(asks, levelJSON{Price: lvl.Price.Float64(), Qty: float64(lvl.Sell)})
		}
	}
	sort.SliceStable(bids, func(i, j int) bool { return bids[i].Price > bids[j].Price })
	sort.SliceStable(asks, func(i, j int) bool { return asks[i].Price < asks[j].Price })
	if len(bids) > 20 {
		bids = bids[:20]
	}
	if len(asks) > 20 {
		asks = asks[:20]
	}
	inst.OrderBook = orderBookJSON{Bids: bids, Asks: asks}
	inst.Trades = trades
	if inst.Bid == 0 && len(bids) > 0 {
		inst.Bid = bids[0].Price
	}
	if inst.Ask == 0 && len(asks) > 0 {
		inst.Ask = asks[0].Price
	}
	return inst, nil
}

func (s *marketState) apply(events []model.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range events {
		switch e := ev.(type) {
		case *model.Quotation:
			q := s.quotes[e.Symbol.String()]
			if q == nil {
				q = &quoteState{}
				s.quotes[e.Symbol.String()] = q
			}
			f := e.Fields
			if f.Last != nil {
				q.Last, q.HasLast = f.Last.Float64(), true
			}
			if f.Change != nil {
				q.Change, q.HasChange = f.Change.Float64(), true
			}
			if f.Bid != nil {
				q.Bid, q.HasBid = f.Bid.Float64(), true
			}
			if f.Offer != nil {
				q.Ask, q.HasAsk = f.Offer.Float64(), true
			}
			if f.Open != nil {
				q.Open, q.HasOpen = f.Open.Float64(), true
			}
			if f.High != nil {
				q.High, q.HasHigh = f.High.Float64(), true
			}
			if f.Low != nil {
				q.Low, q.HasLow = f.Low.Float64(), true
			}
			if f.VolToday != nil {
				q.Volume, q.HasVolume = *f.VolToday, true
			}
			if f.Time != nil {
				q.Time = *f.Time
			}
		case *model.Quotes:
			s.books[e.Symbol.String()] = append([]model.Quote(nil), e.Levels...)
		case *model.Trade:
			side := ""
			switch e.BuySell {
			case 'B':
				side = "buy"
			case 'S':
				side = "sell"
			}
			sym := e.Board + ":" + e.SecCode
			list := append(s.trades[sym], tradeJSON{
				T: e.Time.Unix(), Price: e.Price.Float64(), Qty: e.Quantity, Side: side,
			})
			if len(list) > 200 {
				list = list[len(list)-200:]
			}
			s.trades[sym] = list
		case model.ServerStatus:
			s.status = fmt.Sprintf("connected=%v", e.Connected)
		}
	}
}

func (s *marketState) snapshot(symbol string) (*quoteState, []model.Quote, []tradeJSON) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.quotes[symbol]
	var quote quoteState
	if q != nil {
		quote = *q
		q = &quote
	}
	book := append([]model.Quote(nil), s.books[symbol]...)
	trades := append([]tradeJSON(nil), s.trades[symbol]...)
	return q, book, trades
}

func writeSeed(path string, seed seedJSON) error {
	data, err := json.Marshal(seed)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	if _, err := zw.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	fmt.Printf("seed JSON %d bytes, gzip %d bytes\n", len(data), gzipSize(path))
	return nil
}

func gzipSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

func splitUniverse(list string) []string {
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func round(v float64, decimals int) float64 {
	p := math.Pow10(decimals)
	return math.Round(v*p) / p
}
