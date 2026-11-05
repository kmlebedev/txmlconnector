package feed

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/seeddata"
)

// seedFileName is the snapshot file name inside the seed directory.
const seedFileName = "seed.json.gz"

// seedSchema is the only supported snapshot schema version.
const seedSchema = 1

// seedFile is the offline market-data snapshot (docs/ai-terminal/ARCHITECTURE.md
// §5). It contains market data only: no accounts, clients or logins.
type seedFile struct {
	Schema      int              `json:"schema"`
	GeneratedAt time.Time        `json:"generatedAt"`
	Source      string           `json:"source"`
	ExchangeTZ  string           `json:"exchangeTZ"`
	Universe    []string         `json:"universe"`
	Instruments []seedInstrument `json:"instruments"`
}

// seedInstrument is one instrument of the snapshot.
type seedInstrument struct {
	Symbol    string  `json:"symbol"`
	Board     string  `json:"board"`
	SecCode   string  `json:"seccode"`
	Name      string  `json:"name"`
	ShortName string  `json:"shortName"`
	Currency  string  `json:"currency"`
	Decimals  int     `json:"decimals"`
	LotSize   int64   `json:"lotSize"`
	MinStep   float64 `json:"minStep"`

	PrevClose float64 `json:"prevClose"`
	Last      float64 `json:"last"`
	Change    float64 `json:"change"`
	ChangePct float64 `json:"changePct"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Volume    int64   `json:"volume"`
	Bid       float64 `json:"bid"`
	Ask       float64 `json:"ask"`

	OrderBook *seedOrderBook          `json:"orderbook"`
	Spark     []float64               `json:"spark"`
	Candles   map[string][]seedCandle `json:"candles"`
	Trades    []seedTrade             `json:"trades"`
}

// seedOrderBook is the snapshot price ladder.
type seedOrderBook struct {
	Bids []seedLevel `json:"bids"`
	Asks []seedLevel `json:"asks"`
}

// seedLevel is one snapshot level.
type seedLevel struct {
	Price float64 `json:"price"`
	Qty   float64 `json:"qty"`
}

// seedCandle is one snapshot candle; T is the bar instant in UTC seconds.
type seedCandle struct {
	T int64   `json:"t"`
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V int64   `json:"v"`
}

// seedTrade is one optional tick-tape entry of the snapshot.
type seedTrade struct {
	T     int64   `json:"t"`
	Price float64 `json:"price"`
	Qty   int64   `json:"qty"`
	Side  string  `json:"side"`
}

// symbolRef is one parsed "BOARD:SECCODE" universe entry.
type symbolRef struct {
	Board   string
	SecCode string
}

// loadSeed reads and validates the snapshot: the embedded seeddata.FS by
// default, or a *.json.gz file (or a directory containing seed.json.gz) from
// SeedDir. Broken data yields a Russian error instead of a panic.
func loadSeed(seedDir string) (*seedFile, error) {
	var (
		raw []byte
		err error
	)
	if seedDir == "" {
		raw, err = fs.ReadFile(seeddata.FS, seeddata.Dir+"/"+seedFileName)
		if err != nil {
			return nil, fmt.Errorf("feed: не удалось прочитать встроенный seed: %w", err)
		}
	} else {
		path := seedDir
		info, statErr := os.Stat(seedDir)
		if statErr != nil {
			return nil, fmt.Errorf("feed: не удалось открыть seed %q: %w", seedDir, statErr)
		}
		if info.IsDir() {
			path = filepath.Join(seedDir, seedFileName)
		}
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("feed: не удалось прочитать seed %q: %w", path, err)
		}
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("feed: повреждённый seed (не gzip): %w", err)
	}
	defer zr.Close()
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("feed: повреждённый seed (gzip): %w", err)
	}
	var seed seedFile
	if err := json.Unmarshal(data, &seed); err != nil {
		return nil, fmt.Errorf("feed: повреждённый seed (JSON): %w", err)
	}
	if seed.Schema != seedSchema {
		return nil, fmt.Errorf("feed: неподдерживаемая схема seed: %d (ожидается %d)", seed.Schema, seedSchema)
	}
	return &seed, nil
}

// loadOffline fills the caches from the snapshot and marks the source connected:
// the offline feed needs no network and Start is a no-op.
func (s *Service) loadOffline(seed *seedFile) error {
	lastEvent := seed.GeneratedAt
	for i := range seed.Instruments {
		si := &seed.Instruments[i]
		inst := instrumentFromSeed(si)
		s.instruments[inst.Symbol] = inst

		// The quote of the offline feed is the last 1m bar of the snapshot.
		update := inst.UpdatedAt
		if bars := si.Candles["60"]; len(bars) > 0 {
			last := bars[len(bars)-1]
			if inst.Last == 0 {
				inst.Last = last.C
			}
			if inst.Open == 0 {
				inst.Open = bars[0].O
			}
			update = time.Unix(last.T, 0).UTC()
		}
		inst.UpdatedAt = update
		s.quotes[inst.Symbol] = Quote{
			Symbol:    inst.Symbol,
			Last:      inst.Last,
			Change:    inst.Change,
			ChangePct: inst.ChangePct,
			Bid:       inst.Bid,
			Ask:       inst.Ask,
			Volume:    inst.Volume,
			Time:      update,
		}
		if update.After(lastEvent) {
			lastEvent = update
		}
		s.loadOfflineBook(si, inst)
		s.loadOfflineTrades(si)
	}
	if len(s.universe) == 0 {
		// A snapshot without a universe list still exposes its instruments.
		for i := range seed.Instruments {
			s.universe = append(s.universe, seed.Instruments[i].Symbol)
		}
	}
	s.status = Status{
		Mode:            ModeOffline,
		Connected:       true,
		Message:         "офлайн-снапшот",
		ServerTime:      lastEvent,
		ExchangeTZ:      s.exchangeTZ,
		SeedGeneratedAt: seed.GeneratedAt,
		LastEventAt:     lastEvent,
	}
	return nil
}

// loadLiveSkeleton prepares the instrument metadata of the live feed from the
// seed: names, boards and decimals are static, while quotes, books and trades
// arrive from the exchange.
func (s *Service) loadLiveSkeleton(seed *seedFile) {
	for i := range seed.Instruments {
		si := &seed.Instruments[i]
		inst := &Instrument{
			Symbol:    si.Symbol,
			Board:     si.Board,
			SecCode:   si.SecCode,
			Name:      si.Name,
			ShortName: si.ShortName,
			Currency:  normalizeCurrency(si.Currency),
			Decimals:  defaultDecimals(si.Decimals),
			LotSize:   defaultLotSize(si.LotSize),
			MinStep:   defaultMinStep(si.MinStep),
			PrevClose: si.PrevClose,
			Spark:     append([]float64(nil), si.Spark...),
		}
		s.instruments[inst.Symbol] = inst
	}
	s.status = Status{
		Mode:            ModeLive,
		Connected:       false,
		Message:         "нет соединения с сервером биржи",
		ExchangeTZ:      s.exchangeTZ,
		SeedGeneratedAt: seed.GeneratedAt,
	}
}

// loadOfflineBook stores the snapshot price ladder (or one synthesized by the
// snapshot bid/ask when the order book is absent).
func (s *Service) loadOfflineBook(si *seedInstrument, inst *Instrument) {
	book := OrderBook{Symbol: inst.Symbol, Last: inst.Last, Time: inst.UpdatedAt, Source: "seed"}
	if si.OrderBook != nil {
		for _, lvl := range si.OrderBook.Bids {
			book.Bids = append(book.Bids, Level{Price: lvl.Price, Qty: lvl.Qty})
		}
		for _, lvl := range si.OrderBook.Asks {
			book.Asks = append(book.Asks, Level{Price: lvl.Price, Qty: lvl.Qty})
		}
	} else {
		if inst.Bid > 0 {
			book.Bids = append(book.Bids, Level{Price: inst.Bid})
		}
		if inst.Ask > 0 {
			book.Asks = append(book.Asks, Level{Price: inst.Ask})
		}
	}
	book.Bids = limitLevels(book.Bids)
	book.Asks = limitLevels(book.Asks)
	s.books[inst.Symbol] = book
}

// loadOfflineTrades stores the snapshot tick tape, or synthesizes one from the
// last 1m bars (time = bar, price = close, qty = volume, side from open), as the
// snapshot tape is normally empty.
func (s *Service) loadOfflineTrades(si *seedInstrument) {
	if len(si.Trades) > 0 {
		trades := make([]Trade, 0, len(si.Trades))
		for _, t := range si.Trades {
			trades = append(trades, Trade{Time: time.Unix(t.T, 0).UTC(), Price: t.Price, Qty: t.Qty, Side: t.Side})
		}
		s.trades[si.Symbol] = trades
		return
	}
	bars := si.Candles["60"]
	const synthTrades = 50
	if len(bars) > synthTrades {
		bars = bars[len(bars)-synthTrades:]
	}
	trades := make([]Trade, 0, len(bars))
	for _, b := range bars {
		side := "sell"
		if b.C >= b.O {
			side = "buy"
		}
		trades = append(trades, Trade{Time: time.Unix(b.T, 0).UTC(), Price: b.C, Qty: b.V, Side: side})
	}
	if len(trades) > 0 {
		s.trades[si.Symbol] = trades
	}
}

// offlineCandles serves a candle series from the snapshot.
func (s *Service) offlineCandles(symbol string, periodSec, count int) (History, error) {
	si := s.seedInstrument(symbol)
	if si == nil {
		return History{}, notFound(symbol)
	}
	seedBars := si.Candles[strconv.Itoa(periodSec)]
	if len(seedBars) > count {
		seedBars = seedBars[len(seedBars)-count:]
	}
	bars := make([]Bar, 0, len(seedBars))
	for _, b := range seedBars {
		bars = append(bars, Bar{Time: time.Unix(b.T, 0).UTC(), Open: b.O, High: b.H, Low: b.L, Close: b.C, Volume: b.V})
	}
	return History{Symbol: symbol, PeriodSec: periodSec, Bars: bars, Source: "seed", Note: s.seedNote()}, nil
}

// offlineOrderBook serves a price ladder from the snapshot.
func (s *Service) offlineOrderBook(symbol string) (OrderBook, error) {
	if s.seedInstrument(symbol) == nil {
		return OrderBook{}, notFound(symbol)
	}
	s.mu.RLock()
	book, ok := s.books[symbol]
	s.mu.RUnlock()
	if !ok {
		return OrderBook{}, &Error{Code: CodeNotFound, Message: fmt.Sprintf("стакан недоступен: %s", symbol)}
	}
	return copyOrderBook(book), nil
}

// offlineTrades serves the snapshot tick tape (possibly synthesized).
func (s *Service) offlineTrades(symbol string, limit int) ([]Trade, error) {
	if s.seedInstrument(symbol) == nil {
		return nil, notFound(symbol)
	}
	s.mu.RLock()
	trades := s.trades[symbol]
	s.mu.RUnlock()
	if len(trades) > limit {
		trades = trades[len(trades)-limit:]
	}
	return append([]Trade(nil), trades...), nil
}

// seedInstrument returns the snapshot instrument (nil when unknown).
func (s *Service) seedInstrument(symbol string) *seedInstrument {
	for i := range s.seed.Instruments {
		if s.seed.Instruments[i].Symbol == symbol {
			return &s.seed.Instruments[i]
		}
	}
	return nil
}

// seedNote is the human-readable source label of offline data.
func (s *Service) seedNote() string {
	if s.seed.GeneratedAt.IsZero() {
		return "демо-снапшот"
	}
	at := s.seed.GeneratedAt
	if loc, err := time.LoadLocation(s.seed.ExchangeTZ); err == nil {
		at = at.In(loc)
	}
	return "демо-снапшот " + at.Format("02.01.2006")
}

// instrumentFromSeed maps a snapshot instrument to the feed type with the
// documented fallbacks.
func instrumentFromSeed(si *seedInstrument) *Instrument {
	symbol := si.Symbol
	if symbol == "" {
		symbol = si.Board + ":" + si.SecCode
	}
	name := si.Name
	if name == "" {
		name = si.ShortName
	}
	shortName := si.ShortName
	if shortName == "" {
		shortName = name
	}
	return &Instrument{
		Symbol:    symbol,
		Board:     si.Board,
		SecCode:   si.SecCode,
		Name:      name,
		ShortName: shortName,
		Currency:  normalizeCurrency(si.Currency),
		Decimals:  defaultDecimals(si.Decimals),
		LotSize:   defaultLotSize(si.LotSize),
		MinStep:   defaultMinStep(si.MinStep),
		PrevClose: si.PrevClose,
		Last:      si.Last,
		Change:    si.Change,
		ChangePct: si.ChangePct,
		Open:      si.Open,
		High:      si.High,
		Low:       si.Low,
		Volume:    si.Volume,
		Bid:       si.Bid,
		Ask:       si.Ask,
		Spark:     append([]float64(nil), si.Spark...),
	}
}

// normalizeCurrency renders the wire currency code as the UI expects (RUR is
// the TRANSAQ spelling of RUB).
func normalizeCurrency(code string) string {
	switch strings.ToUpper(code) {
	case "RUR", "RUB":
		return "RUB"
	}
	return code
}

func defaultDecimals(decimals int) int {
	if decimals <= 0 {
		return 2
	}
	return decimals
}

func defaultLotSize(lot int64) int64 {
	if lot <= 0 {
		return 1
	}
	return lot
}

func defaultMinStep(step float64) float64 {
	if step <= 0 {
		return 0.01
	}
	return step
}

// parseSymbols splits "BOARD:SECCODE" universe entries, skipping malformed ones.
func parseSymbols(universe []string) []symbolRef {
	out := make([]symbolRef, 0, len(universe))
	for _, raw := range universe {
		board, secCode, ok := strings.Cut(strings.TrimSpace(raw), ":")
		if !ok || board == "" || secCode == "" {
			continue
		}
		out = append(out, symbolRef{Board: board, SecCode: secCode})
	}
	return out
}

// copyOrderBook returns a deep copy of a price ladder.
func copyOrderBook(book OrderBook) OrderBook {
	out := book
	out.Bids = append([]Level(nil), book.Bids...)
	out.Asks = append([]Level(nil), book.Asks...)
	return out
}
