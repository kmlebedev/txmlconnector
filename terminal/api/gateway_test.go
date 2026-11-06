package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/feed"
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
