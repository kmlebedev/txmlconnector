package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

func TestGatewayInfo(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{LiveTradingAllowed: true})
	status, body := do(t, ts, http.MethodGet, "/api/gw/v1/info", nil)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	var got struct {
		Version   string `json:"version"`
		Market    string `json:"market"`
		Provider  string `json:"provider"`
		Mode      string `json:"mode"`
		Connected bool   `json:"connected"`
		Source    string `json:"source"`
		Trading   struct {
			Paper bool `json:"paper"`
			Live  bool `json:"live"`
		} `json:"trading"`
		Intervals []struct {
			Interval  string `json:"interval"`
			PeriodSec int    `json:"periodSec"`
			MaxBars   int    `json:"maxBars"`
		} `json:"intervals"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != "1" || got.Market != "ru" || got.Provider != "finam" {
		t.Fatalf("identity: %+v", got)
	}
	if got.Mode != "offline" || got.Source != "seed" || !got.Connected {
		t.Fatalf("mode/source: %+v", got)
	}
	if !got.Trading.Paper || got.Trading.Live {
		t.Fatalf("trading must be paper-only: %+v", got.Trading)
	}
	want := []struct {
		name string
		sec  int
	}{{"1m", 60}, {"5m", 300}, {"15m", 900}, {"1h", 3600}, {"1d", 86400}}
	if len(got.Intervals) != len(want) {
		t.Fatalf("intervals: %+v", got.Intervals)
	}
	for i, w := range want {
		g := got.Intervals[i]
		if g.Interval != w.name || g.PeriodSec != w.sec || g.MaxBars != feed.MaxHistoryCount {
			t.Fatalf("interval %d: %+v", i, g)
		}
	}
}

func TestGatewayInstrumentsDecimalStrings(t *testing.T) {
	deps, f, _, _ := defaultDeps()
	f.instruments = append(f.instruments,
		feed.Instrument{Symbol: "TQCB:BOND1", Board: "TQCB", SecCode: "BOND1", Decimals: 4, LotSize: 1, MinStep: 0.0001},
		feed.Instrument{Symbol: "TQBR:INT", Board: "TQBR", SecCode: "INT", Decimals: 0, LotSize: 1, MinStep: 1},
	)
	_, ts := newTestServer(t, deps, Config{})
	status, body := do(t, ts, http.MethodGet, "/api/gw/v1/instruments", nil)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	var got struct {
		Instruments []struct {
			Symbol   string `json:"symbol"`
			SecCode  string `json:"secCode"`
			Board    string `json:"board"`
			Decimals int    `json:"decimals"`
			LotSize  int    `json:"lotSize"`
			MinStep  string `json:"minStep"`
		} `json:"instruments"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	steps := map[string]string{}
	for _, in := range got.Instruments {
		steps[in.Symbol] = in.MinStep
	}
	for sym, want := range map[string]string{"TQBR:SBER": "0.01", "TQCB:BOND1": "0.0001", "TQBR:INT": "1"} {
		if steps[sym] != want {
			t.Errorf("%s minStep = %q, want %q", sym, steps[sym], want)
		}
	}
	if got.Instruments[0].LotSize != 10 || got.Instruments[0].SecCode != "SBER" || got.Instruments[0].Board != "TQBR" {
		t.Fatalf("first: %+v", got.Instruments[0])
	}
}

func TestGatewayRequiresToken(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{Guard: GuardConfig{Token: guardTestToken}})
	for _, path := range []string{"/api/gw/v1/info", "/api/gw/v1/instruments"} {
		status, body := do(t, ts, http.MethodGet, path, nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("%s: status %d: %s", path, status, body)
		}
		var e apiError
		if err := json.Unmarshal(body, &e); err != nil || e.Error.Code != "unauthorized" {
			t.Fatalf("%s: body %s", path, body)
		}
	}
}

func TestGatewayFormatPrice(t *testing.T) {
	for _, c := range []struct {
		v    float64
		d    int
		want string
	}{{101, 2, "101.00"}, {0.0001, 4, "0.0001"}, {7, 0, "7"}, {1.5, -1, "1.50"}} {
		if got := formatPrice(c.v, c.d); got != c.want {
			t.Errorf("formatPrice(%v,%d)=%q want %q", c.v, c.d, got, c.want)
		}
	}
	if epochMS(timeZero()) != 0 {
		t.Fatal("zero time must be 0")
	}
}

func timeZero() (t time.Time) { return }

// periodSpyFeed records the candle period and count the gateway asks for.
type periodSpyFeed struct {
	*fakeFeed
	period, count int
}

func (p *periodSpyFeed) Candles(ctx context.Context, symbol string, periodSec, count int) (feed.History, error) {
	p.period, p.count = periodSec, count
	return p.fakeFeed.Candles(ctx, symbol, periodSec, count)
}

func gwGet(t *testing.T, ts *httptest.Server, path string, out any) int {
	t.Helper()
	status, body := do(t, ts, http.MethodGet, path, nil)
	if out != nil && status == http.StatusOK {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, body)
		}
	}
	return status
}

func TestGatewayQuotesBatchOmitsUnknown(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{})
	var got struct {
		Quotes map[string]struct {
			Last, Bid, Ask, PrevClose, ChangePct string
			VolumeLots                           int64
			Time                                 int64
			Source                               string
		} `json:"quotes"`
	}
	if st := gwGet(t, ts, "/api/gw/v1/quotes?symbols=TQBR:SBER,TQBR:NOPE,TQBR:GAZP", &got); st != http.StatusOK {
		t.Fatalf("status %d", st)
	}
	if len(got.Quotes) != 2 {
		t.Fatalf("quotes: %+v", got.Quotes)
	}
	q := got.Quotes["TQBR:SBER"]
	if q.Last != "101.00" || q.Bid != "100.90" || q.Ask != "101.10" || q.PrevClose != "100.00" || q.VolumeLots != 12345 || q.Source != "seed" || q.Time == 0 {
		t.Fatalf("SBER: %+v", q)
	}
	if q.ChangePct != "1.00" {
		t.Fatalf("changePct: %q", q.ChangePct)
	}
	if st := gwGet(t, ts, "/api/gw/v1/quotes", nil); st != http.StatusBadRequest {
		t.Fatalf("missing symbols: %d", st)
	}
	var syms []string
	for i := 0; i < 201; i++ {
		syms = append(syms, "TQBR:S"+strconv.Itoa(i))
	}
	if st := gwGet(t, ts, "/api/gw/v1/quotes?symbols="+strings.Join(syms, ","), nil); st != http.StatusBadRequest {
		t.Fatalf("201 symbols: %d", st)
	}
}

func TestGatewayQuotesEmptyWhenAbsent(t *testing.T) {
	deps, f, _, _ := defaultDeps()
	f.mu.Lock()
	for i := range f.instruments {
		if f.instruments[i].Symbol == "TQBR:SBER" {
			f.instruments[i].PrevClose = 0
		}
	}
	f.mu.Unlock()
	f.setQuote("TQBR:SBER", feed.Quote{Symbol: "TQBR:SBER", Last: 101, Time: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC)})
	_, ts := newTestServer(t, deps, Config{})
	var got struct {
		Quotes map[string]struct{ Last, Bid, Ask, PrevClose string } `json:"quotes"`
	}
	if st := gwGet(t, ts, "/api/gw/v1/quotes?symbols=TQBR:SBER", &got); st != http.StatusOK {
		t.Fatalf("status %d", st)
	}
	q := got.Quotes["TQBR:SBER"]
	if q.Last != "101.00" || q.Bid != "" || q.Ask != "" || q.PrevClose != "" {
		t.Fatalf("want empty bid/ask/prevClose, got %+v", q)
	}
}

func TestGatewayCandlesIntervalMapping(t *testing.T) {
	deps, f, _, _ := defaultDeps()
	spy := &periodSpyFeed{fakeFeed: f}
	deps.Feed = spy
	_, ts := newTestServer(t, deps, Config{})
	var got struct {
		Symbol, Interval, Source string
		Warnings                 []string
		Bars                     []struct {
			OpenTime, CloseTime    int64
			Open, High, Low, Close string
			VolumeLots             int64
		} `json:"bars"`
	}
	if st := gwGet(t, ts, "/api/gw/v1/candles?symbol=TQBR:SBER&interval=1h&limit=2", &got); st != http.StatusOK {
		t.Fatalf("status %d", st)
	}
	if spy.period != 3600 || spy.count != 2 {
		t.Fatalf("feed called with %d/%d", spy.period, spy.count)
	}
	if got.Symbol != "TQBR:SBER" || got.Interval != "1h" || got.Source != "seed" || got.Warnings == nil || len(got.Bars) != 2 {
		t.Fatalf("resp: %+v", got)
	}
	b := got.Bars[0]
	if b.CloseTime-b.OpenTime != 3_599_999 || b.Open != "100.50" || b.VolumeLots != 20 {
		t.Fatalf("bar: %+v", b)
	}
	if st := gwGet(t, ts, "/api/gw/v1/candles?symbol=TQBR:SBER&interval=2h", nil); st != http.StatusBadRequest {
		t.Fatalf("2h: %d", st)
	}
	if st := gwGet(t, ts, "/api/gw/v1/candles?symbol=TQBR:NOPE&interval=1m", nil); st != http.StatusNotFound {
		t.Fatalf("unknown symbol: %d", st)
	}
}

func TestGatewayOrderBookLots(t *testing.T) {
	deps, f, _, _ := defaultDeps()
	f.books["TQBR:SBER"] = feed.OrderBook{
		Symbol: "TQBR:SBER", Time: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC), Source: "live",
		Bids: []feed.Level{{Price: 306.25, Qty: 10}},
		Asks: []feed.Level{{Price: 306.3, Qty: 7}},
	}
	_, ts := newTestServer(t, deps, Config{})
	status, body := do(t, ts, http.MethodGet, "/api/gw/v1/orderbook?symbol=TQBR:SBER", nil)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	s := string(body)
	for _, want := range []string{`"price":"306.25"`, `"lots":10`, `"price":"306.30"`, `"lots":7`, `"source":"live"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if st := gwGet(t, ts, "/api/gw/v1/orderbook?symbol=TQBR:NOPE", nil); st != http.StatusNotFound {
		t.Fatalf("unknown: %d", st)
	}
}

func TestGatewayTradesSideUnknown(t *testing.T) {
	deps, f, _, _ := defaultDeps()
	f.tapes["TQBR:SBER"] = append(f.tapes["TQBR:SBER"], feed.Trade{Time: time.Date(2026, 11, 5, 16, 25, 0, 0, time.UTC), Price: 101.5, Qty: 3})
	_, ts := newTestServer(t, deps, Config{})
	var got struct {
		Symbol string
		Trades []struct {
			ID    string
			Time  int64
			Price string
			Lots  int64
			Side  string
		}
	}
	if st := gwGet(t, ts, "/api/gw/v1/trades?symbol=TQBR:SBER", &got); st != http.StatusOK {
		t.Fatalf("status %d", st)
	}
	if len(got.Trades) != 3 {
		t.Fatalf("trades: %+v", got.Trades)
	}
	if got.Trades[0].Side != "buy" || got.Trades[1].Side != "sell" || got.Trades[2].Side != "unknown" {
		t.Fatalf("sides: %+v", got.Trades)
	}
	if got.Trades[2].Price != "101.50" || got.Trades[2].Lots != 3 || got.Trades[0].Time >= got.Trades[2].Time {
		t.Fatalf("trade: %+v", got.Trades[2])
	}
	if got.Trades[2].ID != "2@"+strconv.FormatInt(got.Trades[2].Time, 10) {
		t.Fatalf("id: %q", got.Trades[2].ID)
	}
}

func TestGatewaySeedSource(t *testing.T) {
	deps, _, _, _ := defaultDeps() // offline fake
	_, ts := newTestServer(t, deps, Config{})
	var q struct {
		Quotes map[string]struct{ Source string } `json:"quotes"`
	}
	gwGet(t, ts, "/api/gw/v1/quotes?symbols=TQBR:SBER", &q)
	var c struct{ Source string }
	gwGet(t, ts, "/api/gw/v1/candles?symbol=TQBR:SBER&interval=1m", &c)
	if q.Quotes["TQBR:SBER"].Source != "seed" || c.Source != "seed" {
		t.Fatalf("quote %+v candles %+v", q, c)
	}
}

func TestGatewayAccountMapsStatuses(t *testing.T) {
	deps, _, broker, _ := defaultDeps()
	q := paper.QuoteView{Symbol: "TQBR:SBER", Last: 101, Bid: 100.9, Ask: 101.1, Time: time.Date(2026, 11, 5, 16, 24, 0, 0, time.UTC)}
	if _, err := broker.Place(paper.OrderRequest{Symbol: "TQBR:SBER", Side: paper.Buy, Qty: 10, Type: paper.Market}, q); err != nil {
		t.Fatalf("market: %v", err)
	}
	if _, err := broker.Place(paper.OrderRequest{Symbol: "TQBR:SBER", Side: paper.Buy, Qty: 5, Type: paper.Limit, Price: 50}, q); err != nil {
		t.Fatalf("limit: %v", err)
	}
	_, ts := newTestServer(t, deps, Config{})
	var got struct {
		Environment string `json:"environment"`
		Currency    string `json:"currency"`
		Cash        string `json:"cash"`
		Positions   []struct {
			Symbol, Qty, AvgPrice, RealizedPnl any
		} `json:"positions"`
		OpenOrders []struct {
			Symbol string `json:"symbol"`
			Side   string `json:"side"`
			Type   string `json:"type"`
			Status string `json:"status"`
			Qty    int64  `json:"qty"`
			Price  string `json:"price"`
			Time   int64  `json:"time"`
		} `json:"openOrders"`
		Fills []struct {
			Symbol string `json:"symbol"`
			Qty    int64  `json:"qty"`
			Price  string `json:"price"`
			Fee    string `json:"fee"`
		} `json:"fills"`
	}
	if st := gwGet(t, ts, "/api/gw/v1/account", &got); st != http.StatusOK {
		t.Fatalf("status %d", st)
	}
	if got.Environment != "paper" || got.Currency != "RUB" || got.Cash == "" || !strings.Contains(got.Cash, ".") {
		t.Fatalf("header %+v", got)
	}
	if len(got.OpenOrders) != 1 || got.OpenOrders[0].Status != "new" || got.OpenOrders[0].Qty != 5 || got.OpenOrders[0].Price != "50.00" {
		t.Fatalf("open orders %+v", got.OpenOrders)
	}
	if len(got.Positions) != 1 || len(got.Fills) != 1 || got.Fills[0].Qty != 10 || got.Fills[0].Price != "101.10" {
		t.Fatalf("positions %+v fills %+v", got.Positions, got.Fills)
	}
}

func TestGatewayAccountNoPaper(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	deps.Paper = nil
	_, ts := newTestServer(t, deps, Config{})
	if st := gwGet(t, ts, "/api/gw/v1/account", nil); st != http.StatusServiceUnavailable {
		t.Fatalf("status %d", st)
	}
}

func TestGatewayStatusMapping(t *testing.T) {
	for in, want := range map[paper.Status]string{paper.StatusPartial: "partially_filled", paper.StatusCancelled: "canceled", paper.StatusNew: "new", paper.StatusFilled: "filled", paper.StatusRejected: "rejected"} {
		if got := gatewayStatus(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}
