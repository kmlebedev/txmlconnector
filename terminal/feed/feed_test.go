package feed

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native/model"
)

const testBase = int64(1793893440) // 2026-11-05T16:24:00Z, the seed's last bar

// testSeed is a small synthetic snapshot: two instruments, candle series for
// 60s and 300s, one order book and a sparkline.
func testSeed() *seedFile {
	return &seedFile{
		Schema:      1,
		GeneratedAt: time.Date(2026, 11, 5, 16, 21, 36, 0, time.UTC),
		Source:      "test",
		ExchangeTZ:  "Europe/Moscow",
		Universe:    []string{"TQBR:SBER", "TQBR:GAZP"},
		Instruments: []seedInstrument{
			{
				Symbol: "TQBR:SBER", Board: "TQBR", SecCode: "SBER",
				Name: "Сбербанк", ShortName: "Сбербанк", Currency: "RUR",
				Decimals: 2, LotSize: 10, MinStep: 0.01,
				PrevClose: 100, Last: 101, Change: 1, ChangePct: 1,
				Open: 100.5, High: 102, Low: 99, Volume: 12345,
				Bid: 100.9, Ask: 101.1,
				OrderBook: &seedOrderBook{
					Bids: []seedLevel{{Price: 100.9, Qty: 10}, {Price: 100.8, Qty: 5}},
					Asks: []seedLevel{{Price: 101.1, Qty: 20}, {Price: 101.2, Qty: 7}},
				},
				Spark: []float64{99.5, 100, 101},
				Candles: map[string][]seedCandle{
					"60": {
						{T: testBase - 60, O: 100.5, H: 101, L: 100, C: 101, V: 1000},
						{T: testBase, O: 101, H: 101.5, L: 100.8, C: 100.9, V: 500},
					},
					"300": {
						{T: testBase - 300, O: 99, H: 102, L: 99, C: 101, V: 5000},
					},
				},
			},
			{
				// No order book, no candles, no spark: the fallbacks are tested.
				Symbol: "TQBR:GAZP", Board: "TQBR", SecCode: "GAZP",
				Name: "Газпром", ShortName: "Газпром", Currency: "RUB",
				Decimals: 2, LotSize: 10, MinStep: 0.01,
				PrevClose: 150, Last: 151, Change: 1, ChangePct: 0.67,
				Bid: 150.9, Ask: 151.1, Volume: 42,
			},
		},
	}
}

// writeSeedGzip writes the snapshot as a temp-dir seed.json.gz.
func writeSeedGzip(t *testing.T, seed any) string {
	t.Helper()
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	return writeSeedRaw(t, gzipBytes(t, raw))
}

// writeSeedRaw writes raw file bytes as seed.json.gz in a temp dir.
func writeSeedRaw(t *testing.T, raw []byte) string {
	t.Helper()
	dir := t.TempDir()
	writeSeedFile(t, filepath.Join(dir, seedFileName), raw)
	return dir
}

// writeSeedFile writes raw bytes to path.
func writeSeedFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// newOffline builds an offline service from the synthetic seed.
func newOffline(t *testing.T) *Service {
	t.Helper()
	svc, err := New(Config{Mode: ModeOffline, SeedDir: writeSeedGzip(t, testSeed())})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

// TestOfflineSeedServes checks the offline read path: status, universe,
// instruments, quote from the last 1m bar, candles, order book and tape.
func TestOfflineSeedServes(t *testing.T) {
	svc := newOffline(t)

	st := svc.Status()
	if st.Mode != ModeOffline || !st.Connected || st.Message != "офлайн-снапшот" {
		t.Fatalf("status = %+v", st)
	}
	if st.ExchangeTZ != "Europe/Moscow" || st.SeedGeneratedAt.IsZero() {
		t.Fatalf("status metadata = %+v", st)
	}
	if got, want := svc.Universe(), []string{"TQBR:SBER", "TQBR:GAZP"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("universe = %v", got)
	}
	if got := svc.Instruments(); len(got) != 2 || got[0].Symbol != "TQBR:SBER" || got[0].Currency != "RUB" {
		t.Fatalf("instruments = %+v", got)
	}
	inst, ok := svc.Instrument("TQBR:SBER")
	if !ok || inst.Last != 101 || inst.Spark[0] != 99.5 || inst.UpdatedAt.Unix() != testBase {
		t.Fatalf("instrument = %+v (ok=%v)", inst, ok)
	}
	if _, ok := svc.Instrument("TQBR:UNKNOWN"); ok {
		t.Fatal("unknown instrument found")
	}

	q, ok := svc.Quote("TQBR:SBER")
	if !ok || q.Last != 101 || q.Bid != 100.9 || q.Ask != 101.1 || q.Volume != 12345 {
		t.Fatalf("quote = %+v (ok=%v)", q, ok)
	}
	if q.Time.Unix() != testBase {
		t.Fatalf("quote time = %v, want last 1m bar %v", q.Time, time.Unix(testBase, 0).UTC())
	}

	ctx := context.Background()
	h, err := svc.Candles(ctx, "TQBR:SBER", 60, 10)
	if err != nil {
		t.Fatalf("candles: %v", err)
	}
	if h.Source != "seed" || h.PeriodSec != 60 || len(h.Bars) != 2 || h.Bars[1].Close != 100.9 {
		t.Fatalf("history = %+v", h)
	}
	if h.Note == "" || h.Note == "демо-снапшот" {
		t.Fatalf("history note = %q", h.Note)
	}
	// The count keeps the most recent bars.
	h, err = svc.Candles(ctx, "TQBR:SBER", 60, 1)
	if err != nil || len(h.Bars) != 1 || h.Bars[0].Close != 100.9 {
		t.Fatalf("trimmed history = %+v, err %v", h, err)
	}
	// A known instrument without this period returns an empty series, not an error.
	h, err = svc.Candles(ctx, "TQBR:GAZP", 60, 10)
	if err != nil || len(h.Bars) != 0 {
		t.Fatalf("empty history = %+v, err %v", h, err)
	}

	book, err := svc.OrderBook(ctx, "TQBR:SBER")
	if err != nil {
		t.Fatalf("order book: %v", err)
	}
	if book.Source != "seed" || len(book.Bids) != 2 || book.Bids[0].Price != 100.9 || len(book.Asks) != 2 {
		t.Fatalf("order book = %+v", book)
	}
	// The GAZP fallback book comes from the snapshot bid/ask.
	book, err = svc.OrderBook(ctx, "TQBR:GAZP")
	if err != nil || len(book.Bids) != 1 || book.Bids[0].Price != 150.9 || len(book.Asks) != 1 {
		t.Fatalf("fallback book = %+v, err %v", book, err)
	}

	trades, err := svc.Trades(ctx, "TQBR:SBER", 50)
	if err != nil {
		t.Fatalf("trades: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("trades = %+v, want 2 synthesized from 1m bars", trades)
	}
	if trades[0].Price != 101 || trades[0].Side != "buy" || trades[0].Qty != 1000 {
		t.Fatalf("first trade = %+v", trades[0])
	}
	if trades[1].Price != 100.9 || trades[1].Side != "sell" {
		t.Fatalf("second trade = %+v", trades[1])
	}
	if got, _ := svc.Trades(ctx, "TQBR:SBER", 1); len(got) != 1 || got[0].Price != 100.9 {
		t.Fatalf("limited trades = %+v", got)
	}
}

// TestOfflineErrors checks the typed feed errors.
func TestOfflineErrors(t *testing.T) {
	svc := newOffline(t)
	ctx := context.Background()

	_, err := svc.Candles(ctx, "TQBR:SBER", 77, 10)
	var ferr *Error
	if !errors.As(err, &ferr) || ferr.Code != CodeBadPeriod {
		t.Fatalf("bad period err = %v", err)
	}
	var wantErr *Error
	wantErr = &Error{}
	if !errors.As(err, &wantErr) {
		t.Fatal("typed error expected")
	}

	_, err = svc.Candles(ctx, "TQBR:UNKNOWN", 60, 10)
	if !errors.As(err, &ferr) || ferr.Code != CodeNotFound {
		t.Fatalf("unknown symbol err = %v", err)
	}
	_, err = svc.OrderBook(ctx, "TQBR:UNKNOWN")
	if !errors.As(err, &ferr) || ferr.Code != CodeNotFound {
		t.Fatalf("unknown book err = %v", err)
	}
	_, err = svc.Trades(ctx, "TQBR:UNKNOWN", 10)
	if !errors.As(err, &ferr) || ferr.Code != CodeNotFound {
		t.Fatalf("unknown trades err = %v", err)
	}
}

// TestOfflineStartClose checks that offline Start is a no-op and Close is
// idempotent.
func TestOfflineStartClose(t *testing.T) {
	svc := newOffline(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Data stays readable after Close: the snapshot is static.
	if _, err := svc.Candles(ctx, "TQBR:SBER", 60, 1); err != nil {
		t.Fatalf("candles after close: %v", err)
	}
}

// TestOfflineEmptyUniverse checks that a snapshot without instruments starts
// cleanly and serves empty lists.
func TestOfflineEmptyUniverse(t *testing.T) {
	dir := writeSeedGzip(t, &seedFile{Schema: 1, Universe: []string{}, Instruments: []seedInstrument{}})
	svc, err := New(Config{Mode: ModeOffline, SeedDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	if !svc.Status().Connected {
		t.Fatal("offline feed must be connected")
	}
	if len(svc.Universe()) != 0 || len(svc.Instruments()) != 0 {
		t.Fatalf("universe/instruments = %v / %v", svc.Universe(), svc.Instruments())
	}
	if _, ok := svc.Quote("TQBR:SBER"); ok {
		t.Fatal("quote of empty snapshot")
	}
	// Universes listed in the snapshot but not in "universe" are still exposed.
	if len(svc.Instruments()) != 0 {
		t.Fatal("instruments must stay empty")
	}
}

// TestEmbeddedSeedLoads checks the production embed path (the real seed file).
func TestEmbeddedSeedLoads(t *testing.T) {
	svc, err := New(Config{Mode: ModeOffline})
	if err != nil {
		t.Fatalf("New embedded: %v", err)
	}
	defer svc.Close()
	if !svc.Status().Connected {
		t.Fatal("embedded offline feed must be connected")
	}
	// The real seed always has an exchange time zone.
	if svc.Status().ExchangeTZ == "" {
		t.Fatal("embedded seed has no exchangeTZ")
	}
}

// TestSeedDirFileArg checks that -seed may point to the file itself.
func TestSeedDirFileArg(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, seedFileName)
	raw, err := json.Marshal(testSeed())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeSeedFile(t, path, gzipBytes(t, raw))
	svc, err := New(Config{Mode: ModeOffline, SeedDir: path})
	if err != nil {
		t.Fatalf("New with file seed: %v", err)
	}
	defer svc.Close()
	if len(svc.Universe()) != 2 {
		t.Fatalf("universe = %v", svc.Universe())
	}
}

// TestBrokenSeed checks that broken snapshots yield a clear Russian error.
func TestBrokenSeed(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"not gzip", []byte("not a gzip file"), "повреждённый seed"},
		{"bad json", gzipBytes(t, []byte("{not json")), "повреждённый seed (JSON)"},
		{"bad schema", gzipBytes(t, []byte(`{"schema":2}`)), "неподдерживаемая схема seed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeSeedRaw(t, tc.raw)
			_, err := New(Config{Mode: ModeOffline, SeedDir: dir})
			if err == nil {
				t.Fatal("want error")
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

// TestSubscribe checks the pub-sub lifecycle.
func TestSubscribe(t *testing.T) {
	svc := newOffline(t)
	var got []string
	cancel := svc.Subscribe(func(ev Event) { got = append(got, ev.Type) })
	svc.publish(Event{Type: "status"})
	svc.publish(Event{Type: "quote"})
	cancel()
	svc.publish(Event{Type: "orderbook"})
	if len(got) != 2 || got[0] != "status" || got[1] != "quote" {
		t.Fatalf("events = %v", got)
	}
	// A nil sink is ignored.
	if cancel := svc.Subscribe(nil); cancel == nil {
		t.Fatal("nil sink cancel is nil")
	}
}

// TestUniverseOverride checks the configured universe wins over the snapshot.
func TestUniverseOverride(t *testing.T) {
	dir := writeSeedGzip(t, testSeed())
	svc, err := New(Config{Mode: ModeOffline, SeedDir: dir, Universe: []string{"TQBR:SBER"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	if got := svc.Universe(); len(got) != 1 || got[0] != "TQBR:SBER" {
		t.Fatalf("universe = %v", got)
	}
	if len(svc.Instruments()) != 1 {
		t.Fatalf("instruments = %+v", svc.Instruments())
	}
}

// TestParseSymbols checks universe parsing.
func TestParseSymbols(t *testing.T) {
	refs := parseSymbols([]string{"TQBR:SBER", "bad", "", " TQBR:GAZP "})
	if len(refs) != 2 || refs[0].Board != "TQBR" || refs[1].SecCode != "GAZP" {
		t.Fatalf("refs = %+v", refs)
	}
}

// TestHelpers checks the small pure helpers.
func TestHelpers(t *testing.T) {
	if !ValidPeriod(60) || !ValidPeriod(86400) || ValidPeriod(61) {
		t.Fatal("ValidPeriod")
	}
	if got := historyCount(0); got != defaultHistoryCount {
		t.Fatalf("historyCount(0) = %d", got)
	}
	if got := historyCount(10000); got != maxHistoryCount {
		t.Fatalf("historyCount(10000) = %d", got)
	}
	if got := tradeLimit(0); got != defaultTradeLimit {
		t.Fatalf("tradeLimit(0) = %d", got)
	}
	if got := tradeLimit(1000); got != maxTradeLimit {
		t.Fatalf("tradeLimit(1000) = %d", got)
	}
	if got := changePct(102.5, 2.5); got != 2.5 {
		t.Fatalf("changePct = %v", got)
	}
	if got := normalizeCurrency("RUR"); got != "RUB" {
		t.Fatalf("normalizeCurrency = %q", got)
	}
}

// contains is a tiny strings.Contains wrapper to keep the test imports tight.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestNewUnknownMode checks mode validation.
func TestNewUnknownMode(t *testing.T) {
	dir := writeSeedGzip(t, testSeed())
	if _, err := New(Config{Mode: "weird", SeedDir: dir}); err == nil {
		t.Fatal("want unknown mode error")
	}
}

// TestNewLiveRequiresCredentials checks live config validation (never dials).
func TestNewLiveRequiresCredentials(t *testing.T) {
	dir := writeSeedGzip(t, testSeed())
	if _, err := New(Config{Mode: ModeLive, SeedDir: dir}); err == nil {
		t.Fatal("want missing credentials error")
	}
}

// TestOfflineInstrumentWithoutShortSeries is the P3 regression: an instrument
// without a 1m candle series still gets non-zero timestamps (the snapshot
// timestamp) in Instrument.UpdatedAt, Quote.Time and OrderBook.Time instead of
// year 0001 in the UI.
func TestOfflineInstrumentWithoutShortSeries(t *testing.T) {
	generated := time.Date(2026, 11, 5, 16, 21, 36, 0, time.UTC)
	seed := &seedFile{
		Schema:      1,
		GeneratedAt: generated,
		Source:      "test",
		ExchangeTZ:  "Europe/Moscow",
		Universe:    []string{"TQBR:SBER"},
		Instruments: []seedInstrument{{
			Symbol: "TQBR:SBER", Board: "TQBR", SecCode: "SBER",
			Name: "Сбербанк", ShortName: "Сбербанк", Currency: "RUB",
			Decimals: 2, LotSize: 1, MinStep: 0.01,
			Last: 101, Bid: 100.9, Ask: 101.1,
			Candles: map[string][]seedCandle{
				"300": {{T: generated.Unix() - 300, O: 100, H: 101, L: 99, C: 100, V: 5}},
			},
		}},
	}
	svc, err := New(Config{Mode: ModeOffline, SeedDir: writeSeedGzip(t, seed)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	inst, ok := svc.Instrument("TQBR:SBER")
	if !ok {
		t.Fatal("instrument missing")
	}
	if !inst.UpdatedAt.Equal(generated) {
		t.Fatalf("instrument UpdatedAt = %v, want snapshot %v", inst.UpdatedAt, generated)
	}
	quote, ok := svc.Quote("TQBR:SBER")
	if !ok {
		t.Fatal("quote missing")
	}
	if !quote.Time.Equal(generated) {
		t.Fatalf("quote Time = %v, want snapshot %v", quote.Time, generated)
	}
	book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
	if err != nil {
		t.Fatalf("OrderBook: %v", err)
	}
	if !book.Time.Equal(generated) {
		t.Fatalf("order book Time = %v, want snapshot %v", book.Time, generated)
	}
	// No 1m series and no snapshot tape: the synthesized tape stays empty.
	if trades, err := svc.Trades(context.Background(), "TQBR:SBER", 10); err != nil || len(trades) != 0 {
		t.Fatalf("trades = %+v, err %v", trades, err)
	}
}

// TestLiveOrderBookAppliesIncrementalQuotes is the PR 67 regression: the demo
// server sends one full <quotes> snapshot and then incremental deltas (a
// positive quantity upserts a level; a -1 deletion marker is not applied yet —
// see mergeQuotes and the pinned deletion encoding). The feed must merge the
// deltas into a persistent per-symbol ladder instead of replacing the book with
// every callback, which left it one-sided in live acceptance.
func TestLiveOrderBookAppliesIncrementalQuotes(t *testing.T) {
	sym := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	client := &fakeClient{}
	client.runFn = func(ctx context.Context, h func([]model.Event) error) error {
		if err := h([]model.Event{
			// Full snapshot: levels deliberately out of order.
			&model.Quotes{Symbol: sym, Levels: []model.Quote{
				{Price: *dec(98.5), Buy: 4},
				{Price: *dec(100.0), Buy: 10},
				{Price: *dec(99.5), Buy: 5},
				{Price: *dec(103.0), Sell: 2},
				{Price: *dec(101.0), Sell: 20},
				{Price: *dec(101.5), Sell: 7},
			}},
			// Incremental delta: upsert and add on both sides. Deletions use
			// the pinned wire encoding (accumulated price < 1, price 0) and
			// are not applied (see mergeQuotes): the level stays until RE
			// resolves the deletion encoding.
			&model.Quotes{Symbol: sym, Levels: []model.Quote{
				{Price: *dec(100.0), Buy: 15},  // upsert bid quantity
				{Price: *dec(0), Buy: -1},      // deletion marker, price not recoverable
				{Price: *dec(99.0), Buy: 3},    // new bid
				{Price: *dec(101.0), Sell: 25}, // upsert ask quantity
				{Price: *dec(0), Sell: -1},     // deletion marker, price not recoverable
				{Price: *dec(102.0), Sell: 2},  // new ask
			}},
		}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	svc := newLive(t, client)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	waitFor(t, "the merged order book", func() bool {
		book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
		return err == nil && len(book.Bids) == 4 && len(book.Asks) == 4
	})
	book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
	if err != nil {
		t.Fatalf("OrderBook: %v", err)
	}
	// Deletion markers are ignored (pinned encoding, see mergeQuotes): the
	// 99.5 bid and the 101.5 ask stay in the book.
	wantBids := []Level{{Price: 100.0, Qty: 15}, {Price: 99.5, Qty: 5}, {Price: 99.0, Qty: 3}, {Price: 98.5, Qty: 4}}
	wantAsks := []Level{{Price: 101.0, Qty: 25}, {Price: 101.5, Qty: 7}, {Price: 102.0, Qty: 2}, {Price: 103.0, Qty: 2}}
	if len(book.Bids) != len(wantBids) {
		t.Fatalf("bids = %+v, want %+v", book.Bids, wantBids)
	}
	for i := range wantBids {
		if book.Bids[i] != wantBids[i] {
			t.Errorf("bid %d = %+v, want %+v (bids %+v)", i, book.Bids[i], wantBids[i], book.Bids)
		}
	}
	for i := range wantAsks {
		if book.Asks[i] != wantAsks[i] {
			t.Errorf("ask %d = %+v, want %+v (asks %+v)", i, book.Asks[i], wantAsks[i], book.Asks)
		}
	}
	if book.Source != "live" {
		t.Fatalf("source = %q, want live", book.Source)
	}
}

// TestLiveOrderBookResetOnNewSession checks that a reconnect starts from a clean
// ladder: deltas of the new session must not be applied to the stale book of the
// previous one (a level that vanished with the old session must not survive).
func TestLiveOrderBookResetOnNewSession(t *testing.T) {
	sym := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	var mu sync.Mutex
	runs := 0
	client := &fakeClient{}
	client.runFn = func(ctx context.Context, h func([]model.Event) error) error {
		mu.Lock()
		runs++
		n := runs
		mu.Unlock()
		switch n {
		case 1:
			if err := h([]model.Event{&model.Quotes{Symbol: sym, Levels: []model.Quote{
				{Price: *dec(100.0), Buy: 10},
			}}}); err != nil {
				return err
			}
			return errors.New("session lost")
		case 2:
			// A delta-only session: the new ladder must not contain the stale bid.
			if err := h([]model.Event{&model.Quotes{Symbol: sym, Levels: []model.Quote{
				{Price: *dec(101.0), Buy: 10},
			}}}); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	svc := newLive(t, client)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	waitFor(t, "a book of the fresh session", func() bool {
		book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
		return err == nil && len(book.Bids) > 0 && book.Bids[0].Price == 101.0
	})
	book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
	if err != nil {
		t.Fatalf("OrderBook: %v", err)
	}
	if len(book.Bids) != 1 || book.Bids[0] != (Level{Price: 101.0, Qty: 10}) {
		t.Fatalf("bids after reconnect = %+v, want the fresh session only", book.Bids)
	}
}

// TestLiveOrderBookOutputDepth checks that the persistent ladder is bounded: a
// deep snapshot is clamped on output to the best maxBookLevels levels.
func TestLiveOrderBookOutputDepth(t *testing.T) {
	sym := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	levels := make([]model.Quote, 0, 2*maxBookLevels+10)
	for i := 0; i < maxBookLevels+10; i++ {
		price := 100.0 - float64(i)*0.1
		levels = append(levels, model.Quote{Price: *dec(price), Buy: int64(i + 1)})
	}
	for i := 0; i < maxBookLevels+10; i++ {
		price := 101.0 + float64(i)*0.1
		levels = append(levels, model.Quote{Price: *dec(price), Sell: int64(i + 1)})
	}
	client := &fakeClient{}
	client.runFn = func(ctx context.Context, h func([]model.Event) error) error {
		if err := h([]model.Event{&model.Quotes{Symbol: sym, Levels: levels}}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	svc := newLive(t, client)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Close()
	waitFor(t, "the deep order book", func() bool {
		book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
		return err == nil && len(book.Bids) > 0
	})
	book, err := svc.OrderBook(context.Background(), "TQBR:SBER")
	if err != nil {
		t.Fatalf("OrderBook: %v", err)
	}
	if len(book.Bids) != maxBookLevels || len(book.Asks) != maxBookLevels {
		t.Fatalf("depth = %d/%d, want %d", len(book.Bids), len(book.Asks), maxBookLevels)
	}
	if book.Bids[0].Price != 100.0 || book.Asks[0].Price != 101.0 {
		t.Fatalf("best levels = %+v / %+v", book.Bids[0], book.Asks[0])
	}
	// The internal ladder is bounded as well (no unbounded memory growth).
	svc.mu.RLock()
	stored := svc.books["TQBR:SBER"]
	svc.mu.RUnlock()
	if len(stored.Bids) > 4*maxBookLevels || len(stored.Asks) > 4*maxBookLevels {
		t.Fatalf("stored depth = %d/%d, want <= %d", len(stored.Bids), len(stored.Asks), 4*maxBookLevels)
	}
}

func mergeTestBook(t *testing.T, levels ...model.Quote) OrderBook {
	t.Helper()
	sym := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	return mergeQuotes(OrderBook{}, &model.Quotes{Symbol: sym, Levels: levels}, 0, time.Now())
}

func mergeTestDelta(prev OrderBook, levels ...model.Quote) OrderBook {
	sym := model.Symbol{Board: "TQBR", SecCode: "SBER"}
	return mergeQuotes(prev, &model.Quotes{Symbol: sym, Levels: levels}, 0, time.Now())
}

// TestLiveOrderBookPrunesCrossedLevels: when the market moves up, stale bids at
// or above the new best ask are removed (deletions are not decoded yet).
func TestLiveOrderBookPrunesCrossedLevels(t *testing.T) {
	book := mergeTestBook(t,
		model.Quote{Price: *dec(100.0), Buy: 1}, model.Quote{Price: *dec(99.0), Buy: 1},
		model.Quote{Price: *dec(101.0), Sell: 1}, model.Quote{Price: *dec(102.0), Sell: 1})
	book = mergeTestDelta(book,
		model.Quote{Price: *dec(100.5), Buy: 3}, model.Quote{Price: *dec(101.5), Sell: 2},
		model.Quote{Price: *dec(103.0), Sell: 2})
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		t.Fatalf("book = %+v", book)
	}
	if book.Bids[0].Price >= book.Asks[0].Price {
		t.Fatalf("crossed book: bids %+v asks %+v", book.Bids, book.Asks)
	}
	// 101.0 ask is below the newest bid 100.5? No: asks <= max bid (100.5) are
	// pruned; 101.0 stays. Bids >= min delta ask (101.5) are pruned: none.
	if book.Asks[0].Price != 101.0 || book.Bids[0].Price != 100.5 {
		t.Fatalf("best = %+v / %+v", book.Bids[0], book.Asks[0])
	}
	// Market moves up through the old asks: a bid at 102.5 prunes asks <= 102.5.
	book = mergeTestDelta(book, model.Quote{Price: *dec(102.5), Buy: 4})
	if book.Asks[0].Price != 103.0 || book.Bids[0].Price != 102.5 {
		t.Fatalf("after move up: bids %+v asks %+v", book.Bids, book.Asks)
	}
	// Market moves down: new ask 99.5 prunes bids >= 99.5.
	book = mergeTestDelta(book, model.Quote{Price: *dec(99.5), Sell: 1})
	if book.Asks[0].Price != 99.5 || book.Bids[0].Price != 99.0 {
		t.Fatalf("after move down: bids %+v asks %+v", book.Bids, book.Asks)
	}
}

// TestLiveOrderBookPriceMigratesBetweenSides: a price updated on one side is
// removed from the other.
func TestLiveOrderBookPriceMigratesBetweenSides(t *testing.T) {
	book := mergeTestBook(t,
		model.Quote{Price: *dec(100.0), Buy: 5}, model.Quote{Price: *dec(99.0), Buy: 5},
		model.Quote{Price: *dec(101.0), Sell: 5})
	book = mergeTestDelta(book, model.Quote{Price: *dec(100.0), Sell: 2})
	for _, l := range book.Bids {
		if l.Price == 100.0 {
			t.Fatalf("100.0 still in bids: %+v", book.Bids)
		}
	}
	if len(book.Asks) != 2 || book.Asks[0] != (Level{Price: 100.0, Qty: 2}) {
		t.Fatalf("asks = %+v", book.Asks)
	}
}

// TestLiveOrderBookDepthCapKeepsFreshLevels: after pruning, the depth cap keeps
// the best remaining levels, not stale ones.
func TestLiveOrderBookDepthCapKeepsFreshLevels(t *testing.T) {
	var snap []model.Quote
	for i := 0; i < maxBookDepth+50; i++ {
		snap = append(snap, model.Quote{Price: *dec(1000.0 - float64(i)*0.5), Buy: 1})
	}
	book := mergeTestBook(t, snap...)
	ask := 1000.0 - 10*0.5 // prunes the ten best (stale) bids at or above it
	book = mergeTestDelta(book, model.Quote{Price: *dec(ask), Sell: 1})
	if len(book.Bids) == 0 || book.Bids[0].Price >= ask {
		t.Fatalf("stale bids kept: first %+v", book.Bids[:1])
	}
	if book.Asks[0].Price != ask || len(book.Bids) > maxBookDepth {
		t.Fatalf("book = bids %d asks %+v", len(book.Bids), book.Asks)
	}
}
