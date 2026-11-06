package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/feed"
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
}

// formatPrice renders v with exactly decimals fractional digits; a negative
// decimals falls back to 2.
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
