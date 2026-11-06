package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// Gateway v1 is a read-only, versioned HTTP surface (/api/gw/v1/*) for the
// external dsh-trading connector. The contract is frozen in
// docs/ai-terminal/gateway-v1.md: prices are decimal strings, times are epoch
// milliseconds UTC. The Guard already protects every /api/* path.

const (
	gatewayVersion  = "1"
	gatewayMarket   = "ru"
	gatewayProvider = "finam"
)

// gatewayIntervals maps a candle period in seconds to its gateway name.
var gatewayIntervals = map[int]string{60: "1m", 300: "5m", 900: "15m", 3600: "1h", 86400: "1d"}

// registerGateway mounts the gateway v1 routes. GET only: other methods fall
// through to the 405/404 handling of the mux.
func (s *Server) registerGateway(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/gw/v1/info", s.handleGatewayInfo)
	mux.HandleFunc("GET /api/gw/v1/instruments", s.handleGatewayInstruments)
	mux.HandleFunc("GET /api/gw/v1/quotes", s.handleGatewayQuotes)
	mux.HandleFunc("GET /api/gw/v1/candles", s.handleGatewayCandles)
	mux.HandleFunc("GET /api/gw/v1/orderbook", s.handleGatewayOrderBook)
	mux.HandleFunc("GET /api/gw/v1/trades", s.handleGatewayTrades)
	mux.HandleFunc("GET /api/gw/v1/account", s.handleGatewayAccount)
}

// formatPrice renders v with exactly decimals fractional digits; a negative
// decimals falls back to 2.
// formatOptionalPrice is formatPrice, but "" when the value is absent (<= 0).
func formatOptionalPrice(v float64, decimals int) string {
	if v <= 0 {
		return ""
	}
	return formatPrice(v, decimals)
}

func formatPrice(v float64, decimals int) string {
	if decimals < 0 {
		decimals = 2
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

// epochMS converts t to Unix milliseconds; the zero time is 0.
func epochMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

type gwInterval struct {
	Interval  string `json:"interval"`
	PeriodSec int    `json:"periodSec"`
	MaxBars   int    `json:"maxBars"`
}

type gwTrading struct {
	Paper bool `json:"paper"`
	Live  bool `json:"live"`
}

type gwInfo struct {
	Version    string       `json:"version"`
	Market     string       `json:"market"`
	Provider   string       `json:"provider"`
	Mode       string       `json:"mode"`
	Connected  bool         `json:"connected"`
	Source     string       `json:"source"`
	ServerTime int64        `json:"serverTime"`
	Trading    gwTrading    `json:"trading"`
	Intervals  []gwInterval `json:"intervals"`
}

// handleGatewayInfo serves GET /api/gw/v1/info. Live trading is never offered
// through the gateway, whatever the terminal config allows.
func (s *Server) handleGatewayInfo(w http.ResponseWriter, r *http.Request) {
	st := s.feedStatus()
	source := "live"
	if st.Mode == feed.ModeOffline {
		source = "seed"
	}
	info := gwInfo{
		Version:    gatewayVersion,
		Market:     gatewayMarket,
		Provider:   gatewayProvider,
		Mode:       string(st.Mode),
		Connected:  st.Connected,
		Source:     source,
		ServerTime: epochMS(st.ServerTime),
		Trading:    gwTrading{Paper: true, Live: false},
		Intervals:  make([]gwInterval, 0, len(feed.Periods)),
	}
	for _, p := range feed.Periods {
		info.Intervals = append(info.Intervals, gwInterval{Interval: gatewayIntervals[p], PeriodSec: p, MaxBars: feed.MaxHistoryCount})
	}
	writeJSON(w, http.StatusOK, info)
}

type gwInstrument struct {
	Symbol    string `json:"symbol"`
	SecCode   string `json:"secCode"`
	Board     string `json:"board"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Currency  string `json:"currency"`
	Decimals  int    `json:"decimals"`
	LotSize   int64  `json:"lotSize"`
	MinStep   string `json:"minStep"`
}

// handleGatewayInstruments serves GET /api/gw/v1/instruments.
func (s *Server) handleGatewayInstruments(w http.ResponseWriter, r *http.Request) {
	list := s.deps.Feed.Instruments()
	out := struct {
		Instruments []gwInstrument `json:"instruments"`
	}{Instruments: make([]gwInstrument, 0, len(list))}
	for _, in := range list {
		out.Instruments = append(out.Instruments, gwInstrument{
			Symbol: in.Symbol, SecCode: in.SecCode, Board: in.Board,
			Name: in.Name, ShortName: in.ShortName, Currency: in.Currency,
			Decimals: in.Decimals, LotSize: in.LotSize,
			MinStep: formatPrice(in.MinStep, in.Decimals),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// gatewayMaxQuoteSymbols bounds one /quotes batch.
const gatewayMaxQuoteSymbols = 200

// gatewaySource is the data-origin label implied by the feed mode.
func (s *Server) gatewaySource() string {
	if s.feedStatus().Mode == feed.ModeOffline {
		return "seed"
	}
	return "live"
}

type gwQuote struct {
	Last       string `json:"last"`
	Bid        string `json:"bid"`
	Ask        string `json:"ask"`
	PrevClose  string `json:"prevClose"`
	ChangePct  string `json:"changePct"`
	VolumeLots int64  `json:"volumeLots"`
	Time       int64  `json:"time"`
	Source     string `json:"source"`
}

// handleGatewayQuotes serves GET /api/gw/v1/quotes?symbols=A,B. Unknown
// symbols are omitted from the result.
func (s *Server) handleGatewayQuotes(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("symbols"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указаны инструменты (symbols)")
		return
	}
	symbols := strings.Split(raw, ",")
	if len(symbols) > gatewayMaxQuoteSymbols {
		writeError(w, http.StatusBadRequest, codeBadRequest, "слишком много инструментов: максимум "+strconv.Itoa(gatewayMaxQuoteSymbols))
		return
	}
	source := s.gatewaySource()
	out := struct {
		Quotes map[string]gwQuote `json:"quotes"`
	}{Quotes: make(map[string]gwQuote, len(symbols))}
	for _, sym := range symbols {
		sym = strings.TrimSpace(sym)
		in, ok := s.deps.Feed.Instrument(sym)
		if !ok {
			continue
		}
		q, ok := s.deps.Feed.Quote(sym)
		if !ok {
			continue
		}
		out.Quotes[sym] = gwQuote{
			Last: formatPrice(q.Last, in.Decimals), Bid: formatOptionalPrice(q.Bid, in.Decimals),
			Ask: formatOptionalPrice(q.Ask, in.Decimals), PrevClose: formatOptionalPrice(in.PrevClose, in.Decimals),
			ChangePct: strconv.FormatFloat(q.ChangePct, 'f', 2, 64), VolumeLots: q.Volume,
			Time: epochMS(q.Time), Source: source,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type gwBar struct {
	OpenTime   int64  `json:"openTime"`
	CloseTime  int64  `json:"closeTime"`
	Open       string `json:"open"`
	High       string `json:"high"`
	Low        string `json:"low"`
	Close      string `json:"close"`
	VolumeLots int64  `json:"volumeLots"`
}

// handleGatewayCandles serves GET /api/gw/v1/candles.
func (s *Server) handleGatewayCandles(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
	if symbol == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент (symbol)")
		return
	}
	name := r.URL.Query().Get("interval")
	periodSec := 0
	for p, n := range gatewayIntervals {
		if n == name {
			periodSec = p
		}
	}
	if periodSec == 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest, "неподдерживаемый интервал свечей (допустимы 1m, 5m, 15m, 1h, 1d)")
		return
	}
	limit, ok := intParam(r, "limit", 500)
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректное количество свечей")
		return
	}
	in, found := s.deps.Feed.Instrument(symbol)
	if !found {
		writeError(w, http.StatusNotFound, codeNotFound, "инструмент не найден: "+symbol)
		return
	}
	ctx, cancel := requestContext(r, candlesRequestTimeout)
	defer cancel()
	h, err := s.deps.Feed.Candles(ctx, symbol, periodSec, limit)
	if err != nil {
		s.mapError(w, err)
		return
	}
	source := h.Source
	if source == "" {
		source = s.gatewaySource()
	}
	warnings := h.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	out := struct {
		Symbol   string   `json:"symbol"`
		Interval string   `json:"interval"`
		Source   string   `json:"source"`
		Warnings []string `json:"warnings"`
		Bars     []gwBar  `json:"bars"`
	}{Symbol: symbol, Interval: name, Source: source, Warnings: warnings, Bars: make([]gwBar, 0, len(h.Bars))}
	for _, b := range h.Bars {
		open := epochMS(b.Time)
		out.Bars = append(out.Bars, gwBar{
			OpenTime: open, CloseTime: open + int64(periodSec)*1000 - 1,
			Open: formatPrice(b.Open, in.Decimals), High: formatPrice(b.High, in.Decimals),
			Low: formatPrice(b.Low, in.Decimals), Close: formatPrice(b.Close, in.Decimals),
			VolumeLots: b.Volume,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type gwLevel struct {
	Price string `json:"price"`
	Lots  int64  `json:"lots"`
}

// gatewayLevels renders one side of the price ladder as the gateway v1
// contract requires: bids by descending price, asks by ascending. The feed
// order is not guaranteed (live <quotes> deltas are merged, not sorted), so the
// response is sorted defensively; the input slice is copied, never mutated.
func gatewayLevels(levels []feed.Level, decimals int, descending bool) []gwLevel {
	ordered := append([]feed.Level(nil), levels...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if descending {
			return ordered[i].Price > ordered[j].Price
		}
		return ordered[i].Price < ordered[j].Price
	})
	out := make([]gwLevel, 0, len(ordered))
	for _, l := range ordered {
		out = append(out, gwLevel{Price: formatPrice(l.Price, decimals), Lots: int64(l.Qty)})
	}
	return out
}

// handleGatewayOrderBook serves GET /api/gw/v1/orderbook.
func (s *Server) handleGatewayOrderBook(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
	if symbol == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент (symbol)")
		return
	}
	in, found := s.deps.Feed.Instrument(symbol)
	if !found {
		writeError(w, http.StatusNotFound, codeNotFound, "инструмент не найден: "+symbol)
		return
	}
	ctx, cancel := requestContext(r, candlesRequestTimeout)
	defer cancel()
	book, err := s.deps.Feed.OrderBook(ctx, symbol)
	if err != nil {
		s.mapError(w, err)
		return
	}
	source := book.Source
	if source == "" {
		source = s.gatewaySource()
	}
	writeJSON(w, http.StatusOK, struct {
		Symbol string    `json:"symbol"`
		Time   int64     `json:"time"`
		Source string    `json:"source"`
		Bids   []gwLevel `json:"bids"`
		Asks   []gwLevel `json:"asks"`
	}{symbol, epochMS(book.Time), source, gatewayLevels(book.Bids, in.Decimals, true), gatewayLevels(book.Asks, in.Decimals, false)})
}

type gwTrade struct {
	ID    string `json:"id"`
	Time  int64  `json:"time"`
	Price string `json:"price"`
	Lots  int64  `json:"lots"`
	Side  string `json:"side"`
}

// handleGatewayTrades serves GET /api/gw/v1/trades. The feed carries no trade
// id, so id is "<index>@<time>" (index within the returned page).
func (s *Server) handleGatewayTrades(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
	if symbol == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "не указан инструмент (symbol)")
		return
	}
	limit, ok := intParam(r, "limit", 50)
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest, "некорректный limit")
		return
	}
	in, found := s.deps.Feed.Instrument(symbol)
	if !found {
		writeError(w, http.StatusNotFound, codeNotFound, "инструмент не найден: "+symbol)
		return
	}
	ctx, cancel := requestContext(r, candlesRequestTimeout)
	defer cancel()
	trades, err := s.deps.Feed.Trades(ctx, symbol, limit)
	if err != nil {
		s.mapError(w, err)
		return
	}
	out := struct {
		Symbol string    `json:"symbol"`
		Trades []gwTrade `json:"trades"`
	}{Symbol: symbol, Trades: make([]gwTrade, 0, len(trades))}
	for i, t := range trades {
		side := t.Side
		if side != "buy" && side != "sell" {
			side = "unknown"
		}
		ms := epochMS(t.Time)
		out.Trades = append(out.Trades, gwTrade{
			ID: strconv.Itoa(i) + "@" + strconv.FormatInt(ms, 10), Time: ms,
			Price: formatPrice(t.Price, in.Decimals), Lots: t.Qty, Side: side,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// gatewayMaxFills bounds the fills list of /account.
const gatewayMaxFills = 50

// gatewayStatus maps a paper order status to its gateway name.
func gatewayStatus(st paper.Status) string {
	switch st {
	case paper.StatusPartial:
		return "partially_filled"
	case paper.StatusCancelled:
		return "canceled"
	}
	return string(st)
}

type gwPosition struct {
	Symbol      string `json:"symbol"`
	Qty         int64  `json:"qty"`
	AvgPrice    string `json:"avgPrice"`
	RealizedPnl string `json:"realizedPnl"`
}

type gwOpenOrder struct {
	ID     string `json:"id"`
	Symbol string `json:"symbol"`
	Side   string `json:"side"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Qty    int64  `json:"qty"`
	Filled int64  `json:"filled"`
	Price  string `json:"price"`
	Time   int64  `json:"time"`
}

type gwFill struct {
	ID      string `json:"id"`
	OrderID string `json:"orderId"`
	Symbol  string `json:"symbol"`
	Side    string `json:"side"`
	Qty     int64  `json:"qty"`
	Price   string `json:"price"`
	Fee     string `json:"fee"`
	Time    int64  `json:"time"`
}

// handleGatewayAccount serves GET /api/gw/v1/account: the default paper
// account. Quantities are shares (paper cash moves by qty*price).
func (s *Server) handleGatewayAccount(w http.ResponseWriter, r *http.Request) {
	if s.deps.Paper == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "бумажный счёт недоступен")
		return
	}
	snap := s.deps.Paper.Snapshot()
	decimals := func(symbol string) int {
		if in, ok := s.deps.Feed.Instrument(symbol); ok && in.Decimals >= 0 {
			return in.Decimals
		}
		return 2
	}
	out := struct {
		Environment string        `json:"environment"`
		Currency    string        `json:"currency"`
		Cash        string        `json:"cash"`
		Positions   []gwPosition  `json:"positions"`
		OpenOrders  []gwOpenOrder `json:"openOrders"`
		Fills       []gwFill      `json:"fills"`
	}{
		Environment: "paper", Currency: "RUB", Cash: formatPrice(snap.Cash, 2),
		Positions:  make([]gwPosition, 0, len(snap.Positions)),
		OpenOrders: []gwOpenOrder{}, Fills: []gwFill{},
	}
	for _, p := range snap.Positions {
		out.Positions = append(out.Positions, gwPosition{
			Symbol: p.Symbol, Qty: p.Qty,
			AvgPrice: formatPrice(p.AvgPrice, decimals(p.Symbol)), RealizedPnl: formatPrice(p.RealizedPnL, 2),
		})
	}
	for _, o := range snap.Orders {
		if o.Status != paper.StatusNew && o.Status != paper.StatusPartial {
			continue
		}
		out.OpenOrders = append(out.OpenOrders, gwOpenOrder{
			ID: o.ID, Symbol: o.Symbol, Side: string(o.Side), Type: string(o.Type),
			Status: gatewayStatus(o.Status), Qty: o.Qty, Filled: o.Filled,
			Price: formatPrice(o.Price, decimals(o.Symbol)), Time: epochMS(o.CreatedAt),
		})
	}
	trades := append([]paper.Trade(nil), snap.Trades...)
	sort.SliceStable(trades, func(i, j int) bool { return trades[i].Time.Before(trades[j].Time) })
	if len(trades) > gatewayMaxFills {
		trades = trades[len(trades)-gatewayMaxFills:]
	}
	for _, t := range trades {
		out.Fills = append(out.Fills, gwFill{
			ID: t.ID, OrderID: t.OrderID, Symbol: t.Symbol, Side: string(t.Side), Qty: t.Qty,
			Price: formatPrice(t.Price, decimals(t.Symbol)), Fee: formatPrice(t.Fee, 2), Time: epochMS(t.Time),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
