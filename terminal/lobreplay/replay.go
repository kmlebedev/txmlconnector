package lobreplay

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector-native/native/model"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// Event is one row of the replay stream: a md.book_events snapshot/level/gap row
// or a md.trades row (KindTrade with Trade set). Prices are model.Decimal; the
// replay never converts a price to float64. TS is the UTC event time.
type Event struct {
	TS    time.Time     `json:"ts"`
	Kind  string        `json:"kind"`
	Side  string        `json:"side"`
	Price model.Decimal `json:"price"`
	Qty   int64         `json:"qty"`
	Trade *Trade        `json:"trade,omitempty"`
}

// Trade is the md.trades payload of a KindTrade event: the fields that are not
// part of the generic Event. Event.Side is the aggressor side.
type Trade struct {
	TradeNo      int64 `json:"tradeNo"`
	OpenInterest int64 `json:"openInterest,omitempty"`
}

// Strategy decides on every replayed event. The book is the state after the
// event was applied; returned orders are submitted at the event time plus the
// fill model latency. OnEvent must be deterministic: it may not read the clock
// or the network, and it may only use the book and the event it is given.
type Strategy interface {
	OnEvent(b *Book, ev Event) []Order
}

// Source is the ordered event stream of one instrument. Next returns io.EOF at
// the end of the stream; any other error aborts the replay. Run does not close
// the source: the caller owns it.
type Source interface {
	Next() (Event, error)
	Close() error
}

// Result is one replay run. Metrics and Equity reuse the backtest types, so a
// replay result is consumed like a bar backtest result by the HTTP and agent
// layers; Metrics.Bars holds the number of replayed events, because an event
// stream has no bars.
type Result struct {
	Metrics    backtest.Metrics       `json:"metrics"`
	Equity     []backtest.EquityPoint `json:"equity"`
	Fills      []Fill                 `json:"fills"`
	RoundTrips []RoundTrip            `json:"roundTrips"`
	Warnings   []string               `json:"warnings"`
	// Events is the number of replayed rows, Orders the number of submitted
	// orders, Filled the fully executed ones, Open the still working limit
	// orders at the end of the stream and Cancelled the orders dropped by a
	// recording gap. UnfilledQty is the immediate-or-cancel remainder of market
	// orders that ran out of visible liquidity.
	Events      int   `json:"events"`
	Orders      int   `json:"orders"`
	Filled      int   `json:"filled"`
	Open        int   `json:"open"`
	Cancelled   int   `json:"cancelled"`
	UnfilledQty int64 `json:"unfilledQty"`
}

// profitFactorCap is reported when there are winning round trips but no losing
// ones: the ratio is infinite there, but the JSON API requires a finite number.
// It mirrors the cap of terminal/backtest.
const profitFactorCap = 1000.0

// Run replays src through s with the fill model fm until the source ends or the
// context is cancelled. The replay is deterministic and free of clock, network
// and random access: the same source, strategy and fill model produce the same
// Result (plan Task 14, TestReplayDeterministic).
func Run(ctx context.Context, src Source, s Strategy, fm FillModel) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if src == nil {
		return Result{}, errors.New("lobreplay: nil source")
	}
	if s == nil {
		return Result{}, errors.New("lobreplay: nil strategy")
	}
	if err := fm.validate(); err != nil {
		return Result{}, err
	}
	r := &replayer{
		book:       NewBook(),
		fill:       newFillEngine(fm),
		src:        src,
		strategy:   s,
		fm:         fm,
		seenTrades: make(map[int64]struct{}),
		times:      make([]time.Time, 0, 256),
		marks:      make([]int64, 0, 256),
		equity:     make([]int64, 0, 256),
	}
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("lobreplay: replay cancelled: %w", err)
		}
		ev, err := r.src.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("lobreplay: source: %w", err)
		}
		if err := r.step(ev); err != nil {
			return Result{}, err
		}
	}
	return r.result(), nil
}

// replayer is the state of one Run.
type replayer struct {
	book     *Book
	fill     *fillEngine
	src      Source
	strategy Strategy
	fm       FillModel

	events int
	gaps   int

	// inSnapshot marks a run of consecutive snapshot rows: the first row resets
	// the book, the rest fill it level by level.
	inSnapshot bool
	// seenTrades deduplicates md.trades rows by trade_no: the ReplacingMergeTree
	// may still return the same trade twice before a merge, and a replayed trade
	// must not fill an order twice.
	seenTrades map[int64]struct{}

	lastTradeUnits int64

	firstMarkUnits int64
	hasMark        bool
	exposureEvents int

	times  []time.Time
	marks  []int64
	equity []int64
}

// step processes one event: book update, order activation, limit fills from a
// trade, the strategy decision and the equity snapshot of the event.
func (r *replayer) step(ev Event) error {
	if err := validateEvent(ev); err != nil {
		return fmt.Errorf("lobreplay: event %d: %w", r.events+1, err)
	}
	keep, err := r.apply(ev)
	if err != nil {
		return fmt.Errorf("lobreplay: event %d: %w", r.events+1, err)
	}
	if !keep {
		return nil
	}
	r.fill.activate(r.book, ev.TS)
	if ev.Kind == KindTrade {
		r.fill.onTrade(ev)
	}
	orders := r.strategy.OnEvent(r.book, ev)
	if err := r.fill.submit(orders, ev.TS); err != nil {
		return fmt.Errorf("lobreplay: event %d (%s): %w", r.events+1, ev.TS.Format(time.RFC3339Nano), err)
	}
	r.fill.activate(r.book, ev.TS)
	r.record(ev)
	return nil
}

// apply rebuilds the book or marks the trade. It returns false when the event
// must be skipped (a duplicated trade number).
func (r *replayer) apply(ev Event) (bool, error) {
	switch ev.Kind {
	case KindSnapshot:
		if !r.inSnapshot {
			r.book.Reset()
			r.inSnapshot = true
		}
		r.book.setLevel(ev.Side, ev.Price, ev.Qty)
	case KindLevel:
		r.inSnapshot = false
		r.book.setLevel(ev.Side, ev.Price, ev.Qty)
	case KindGap:
		r.inSnapshot = false
		r.gaps++
		r.book.Reset()
		r.fill.cancelAll()
	case KindTrade:
		r.inSnapshot = false
		if ev.Trade != nil && ev.Trade.TradeNo != 0 {
			if _, ok := r.seenTrades[ev.Trade.TradeNo]; ok {
				return false, nil // the same trade row again: already counted
			}
			r.seenTrades[ev.Trade.TradeNo] = struct{}{}
		}
		if units := priceUnits(ev.Price); units > 0 {
			r.lastTradeUnits = units
		}
	default:
		return false, fmt.Errorf("unknown event kind %q", ev.Kind)
	}
	return true, nil
}

// record appends the equity point of the event and the exposure counter.
func (r *replayer) record(ev Event) {
	r.events++
	mark := r.markUnits()
	if mark > 0 && !r.hasMark {
		r.hasMark = true
		r.firstMarkUnits = mark
	}
	if r.fill.portfolio.position != 0 {
		r.exposureEvents++
	}
	r.times = append(r.times, ev.TS)
	r.marks = append(r.marks, mark)
	r.equity = append(r.equity, r.fill.portfolio.equityUnits(mark))
}

// markUnits returns the price the open position is marked at: the book mid when
// both sides are visible, the visible side otherwise, and the last trade price
// when the book is empty.
func (r *replayer) markUnits() int64 {
	bid, ask := r.book.Best()
	switch {
	case bid.Qty > 0 && ask.Qty > 0:
		return (priceUnits(bid.Price) + priceUnits(ask.Price)) / 2
	case bid.Qty > 0:
		return priceUnits(bid.Price)
	case ask.Qty > 0:
		return priceUnits(ask.Price)
	}
	return r.lastTradeUnits
}

// validateEvent rejects a malformed row before it can corrupt the book.
func validateEvent(ev Event) error {
	if ev.TS.IsZero() {
		return errors.New("zero event timestamp")
	}
	switch ev.Kind {
	case KindSnapshot, KindLevel:
		if ev.Side != SideBid && ev.Side != SideAsk {
			return fmt.Errorf("side %q of a %s event (want %q or %q)", ev.Side, ev.Kind, SideBid, SideAsk)
		}
		if ev.Qty < 0 {
			return fmt.Errorf("%s event qty must not be negative: %d", ev.Kind, ev.Qty)
		}
		if ev.Qty > 0 && priceUnits(ev.Price) <= 0 {
			return fmt.Errorf("%s event price must be positive: %s", ev.Kind, ev.Price)
		}
	case KindGap:
		// The gap marker carries no level fields.
	case KindTrade:
		if ev.Side != SideBuy && ev.Side != SideSell && ev.Side != SideNone {
			return fmt.Errorf("trade side %q (want %q, %q or %q)", ev.Side, SideBuy, SideSell, SideNone)
		}
		if ev.Qty < 0 {
			return fmt.Errorf("trade qty must not be negative: %d", ev.Qty)
		}
		if priceUnits(ev.Price) <= 0 {
			return fmt.Errorf("trade price must be positive: %s", ev.Price)
		}
	default:
		return fmt.Errorf("unknown event kind %q", ev.Kind)
	}
	return nil
}

// result derives the metrics, the equity curve and the warnings of the run.
func (r *replayer) result() Result {
	res := Result{
		Equity:      make([]backtest.EquityPoint, 0, len(r.times)),
		Fills:       append(make([]Fill, 0, len(r.fill.fills)), r.fill.fills...),
		RoundTrips:  append(make([]RoundTrip, 0, len(r.fill.portfolio.roundTrips)), r.fill.portfolio.roundTrips...),
		Warnings:    make([]string, 0, 4),
		Events:      len(r.times),
		Orders:      r.fill.orders,
		Filled:      r.fill.filled,
		Open:        len(r.fill.working),
		Cancelled:   r.fill.cancelled,
		UnfilledQty: r.fill.unfilledQty,
	}
	res.Metrics.InitialCash = initialCash
	res.Metrics.Bars = len(r.times)
	res.Metrics.FinalEquity = initialCash
	if len(r.times) > 0 {
		res.Metrics.From = r.times[0]
		res.Metrics.To = r.times[len(r.times)-1]
	}

	finalUnits := int64(initialCash) * priceFactor
	if len(r.equity) > 0 {
		finalUnits = r.equity[len(r.equity)-1]
	}
	res.Metrics.FinalEquity = equityValue(finalUnits)
	if initialCash > 0 {
		res.Metrics.TotalReturnPct = (res.Metrics.FinalEquity/initialCash - 1) * 100
	}

	last := len(r.marks) - 1
	if last >= 0 && r.marks[0] > 0 && r.marks[last] > 0 {
		res.Metrics.BenchmarkReturnPct = float64(r.marks[last]-r.marks[0]) / float64(r.marks[0]) * 100
	}

	peak := initialCash
	for i, units := range r.equity {
		value := equityValue(units)
		if value > peak {
			peak = value
		}
		drawdown := 0.0
		if peak > 0 {
			drawdown = (value/peak - 1) * 100
		}
		if drawdown < res.Metrics.MaxDrawdownPct {
			res.Metrics.MaxDrawdownPct = drawdown
		}
		benchmark := initialCash
		if r.hasMark && r.firstMarkUnits > 0 && r.marks[i] > 0 {
			benchmark = initialCash * float64(r.marks[i]) / float64(r.firstMarkUnits)
		}
		res.Equity = append(res.Equity, backtest.EquityPoint{
			Time:        r.times[i],
			Equity:      value,
			Benchmark:   benchmark,
			DrawdownPct: drawdown,
		})
	}
	if len(r.equity) > 0 {
		res.Metrics.ExposurePct = float64(r.exposureEvents) / float64(len(r.equity)) * 100
	}

	res.Metrics.Trades = len(res.RoundTrips)
	var wins, losses, pnlPctSum float64
	winners := 0
	for _, trip := range res.RoundTrips {
		net := float64(priceUnits(trip.PnL)-priceUnits(trip.Fee)) / float64(priceFactor)
		switch {
		case net > 0:
			winners++
			wins += net
		case net < 0:
			losses -= net
		}
		pnlPctSum += trip.PnLPct
	}
	if n := len(res.RoundTrips); n > 0 {
		res.Metrics.WinRatePct = float64(winners) / float64(n) * 100
		res.Metrics.AvgTradePct = pnlPctSum / float64(n)
	}
	switch {
	case losses > 0:
		res.Metrics.ProfitFactor = wins / losses
	case wins > 0:
		res.Metrics.ProfitFactor = profitFactorCap
	}

	if len(r.times) == 0 {
		res.Warnings = append(res.Warnings, "поток пуст: нет событий для replay")
		return res
	}
	if r.fm.FeeBps == 0 {
		res.Warnings = append(res.Warnings, "no_costs: FeeBps=0 — комиссия не взимается")
	}
	res.Warnings = append(res.Warnings,
		"sharpe не рассчитывается: у событийного потока нет фиксированного периода")
	if r.gaps > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"разрыв записи: книга сброшена %d раз, уровни до и после разрыва не склеиваются", r.gaps))
	}
	if r.fill.cancelled > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"разрыв записи: отменены незаполненные заявки: %d", r.fill.cancelled))
	}
	if r.fill.unfilledQty > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"рыночным заявкам не хватило ликвидности: %d ед.", r.fill.unfilledQty))
	}
	if position := r.fill.portfolio.position; position != 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"позиция открыта на конце потока: %d ед. оценены по последней цене", position))
	}
	if len(res.RoundTrips) == 0 {
		res.Warnings = append(res.Warnings, "сделок не было: стратегия не дала исполнений")
	}
	return res
}

// equityValue converts scaled price units to currency units at the float64
// metric boundary. It is the only place a price becomes a float.
func equityValue(units int64) float64 {
	return float64(units) / float64(priceFactor)
}

// JSONLSource reads a JSONL dump of md.book_events and md.trades rows.
//
// One JSON object per line with the md column names plus a "table"
// discriminator ("book_events" or "trades"), so a single file can hold both
// feeds of one instrument. Prices are fixed-point strings at the Decimal64(6)
// scale, ts/recv_ts are UTC ClickHouse DateTime64 text. The stream must be
// ordered by non-decreasing ts and all rows must belong to one board/sec_code
// pair. The replay order is ts plus the file order of equal timestamps; recv_ts
// is accepted but not used, so the recorded cross-feed latency is not
// reconstructed. Plain and gzip data are both accepted: the reader peeks the
// gzip magic, so testdata/sber-10m.jsonl.gz loads without a special case.
type JSONLSource struct {
	reader io.Reader
	file   *os.File
	closer io.Closer

	scanner   *bufio.Scanner
	line      int
	lastTS    time.Time
	board     string
	secCode   string
	hasSymbol bool
}

// OpenJSONL opens a JSONL recording file for the replay.
func OpenJSONL(path string) (*JSONLSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("lobreplay: open %s: %w", path, err)
	}
	return &JSONLSource{reader: file, file: file}, nil
}

// NewJSONLSource reads a JSONL recording from an already opened reader. The
// reader is not closed by Close (only a gzip wrapper is).
func NewJSONLSource(reader io.Reader) *JSONLSource {
	return &JSONLSource{reader: reader}
}

// init wraps the reader on first use: buffered, transparently gunzipped.
func (s *JSONLSource) init() error {
	if s.scanner != nil {
		return nil
	}
	buffered := bufio.NewReader(s.reader)
	magic, err := buffered.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return fmt.Errorf("lobreplay: read source: %w", err)
	}
	reader := io.Reader(buffered)
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gzipReader, err := gzip.NewReader(buffered)
		if err != nil {
			return fmt.Errorf("lobreplay: open gzip source: %w", err)
		}
		s.closer = gzipReader
		reader = gzipReader
	}
	s.scanner = bufio.NewScanner(reader)
	s.scanner.Buffer(make([]byte, 64<<10), 1<<20)
	return nil
}

// Next returns the next event or io.EOF at the end of the recording.
func (s *JSONLSource) Next() (Event, error) {
	if err := s.init(); err != nil {
		return Event{}, err
	}
	for s.scanner.Scan() {
		s.line++
		line := bytes.TrimSpace(s.scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		ev, err := s.parse(line)
		if err != nil {
			return Event{}, fmt.Errorf("lobreplay: line %d: %w", s.line, err)
		}
		if !s.lastTS.IsZero() && ev.TS.Before(s.lastTS) {
			return Event{}, fmt.Errorf(
				"lobreplay: line %d: timestamp %v goes back before %v (the file must be ordered by ts)",
				s.line, ev.TS, s.lastTS)
		}
		s.lastTS = ev.TS
		return ev, nil
	}
	if err := s.scanner.Err(); err != nil {
		return Event{}, fmt.Errorf("lobreplay: read source: %w", err)
	}
	return Event{}, io.EOF
}

// Close releases the file and the gzip wrapper opened by OpenJSONL.
func (s *JSONLSource) Close() error {
	var err error
	if s.closer != nil {
		err = s.closer.Close()
		s.closer = nil
	}
	if s.file != nil {
		if closeErr := s.file.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		s.file = nil
	}
	return err
}

// jsonRow is one JSONL line: the md column names plus the table discriminator.
type jsonRow struct {
	Table        string `json:"table"`
	TS           string `json:"ts"`
	RecvTS       string `json:"recv_ts"`
	SessionID    string `json:"session_id"`
	Seq          uint64 `json:"seq"`
	Board        string `json:"board"`
	SecCode      string `json:"sec_code"`
	Kind         string `json:"kind"`
	Side         string `json:"side"`
	Price        string `json:"price"`
	Qty          int64  `json:"qty"`
	TradeNo      int64  `json:"trade_no"`
	OpenInterest int64  `json:"open_interest"`
}

// parse converts one JSONL line into an event.
func (s *JSONLSource) parse(line []byte) (Event, error) {
	var row jsonRow
	if err := json.Unmarshal(line, &row); err != nil {
		return Event{}, fmt.Errorf("decode row: %w", err)
	}
	if row.TS == "" {
		return Event{}, errors.New("row has no ts")
	}
	ts, err := parseTime(row.TS)
	if err != nil {
		return Event{}, fmt.Errorf("ts %q: %w", row.TS, err)
	}
	if row.Board == "" || row.SecCode == "" {
		return Event{}, errors.New("row has no board/sec_code")
	}
	if !s.hasSymbol {
		s.hasSymbol = true
		s.board, s.secCode = row.Board, row.SecCode
	} else if row.Board != s.board || row.SecCode != s.secCode {
		return Event{}, fmt.Errorf(
			"row is %s:%s, but the file started with %s:%s (one instrument per file)",
			row.Board, row.SecCode, s.board, s.secCode)
	}
	price, err := parsePrice(row.Price)
	if err != nil {
		return Event{}, fmt.Errorf("price %q: %w", row.Price, err)
	}
	switch row.Table {
	case "book_events":
		switch row.Kind {
		case KindSnapshot, KindLevel, KindGap:
		default:
			return Event{}, fmt.Errorf("unknown book kind %q", row.Kind)
		}
		side, err := bookSideName(row.Side)
		if err != nil {
			return Event{}, err
		}
		if row.Kind != KindGap && side == SideNone {
			return Event{}, fmt.Errorf("%s row with side %q (want %q or %q)", row.Kind, row.Side, SideBid, SideAsk)
		}
		return Event{TS: ts, Kind: row.Kind, Side: side, Price: price, Qty: row.Qty}, nil
	case "trades":
		side, err := tradeSideName(row.Side)
		if err != nil {
			return Event{}, err
		}
		return Event{
			TS: ts, Kind: KindTrade, Side: side, Price: price, Qty: row.Qty,
			Trade: &Trade{TradeNo: row.TradeNo, OpenInterest: row.OpenInterest},
		}, nil
	default:
		return Event{}, fmt.Errorf("unknown table %q (want book_events or trades)", row.Table)
	}
}

// bookSideName maps the md.book_events.side Enum8 name to the event side.
func bookSideName(name string) (string, error) {
	switch name {
	case SideBid, SideAsk:
		return name, nil
	case SideNone, "":
		return SideNone, nil
	}
	return "", fmt.Errorf("unknown book side %q", name)
}

// tradeSideName maps the md.trades.side Enum8 name to the event side.
func tradeSideName(name string) (string, error) {
	switch name {
	case SideBuy, SideSell:
		return name, nil
	case SideNone, "":
		return SideNone, nil
	}
	return "", fmt.Errorf("unknown trade side %q", name)
}

// timeLayouts are the accepted timestamp forms, most precise first: the
// ClickHouse DateTime64 text layouts of the md tables, then RFC3339 for hand
// written sources. All of them are parsed as UTC.
var timeLayouts = []string{
	"2006-01-02 15:04:05.000000",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
}

// parseTime parses a timestamp in UTC.
func parseTime(value string) (time.Time, error) {
	for _, layout := range timeLayouts {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time (want ClickHouse DateTime64 text or RFC3339)")
}

// parsePrice parses a fixed-point decimal string ("291.460000") exactly: the md
// tables store Decimal64(6) prices as strings, so no float64 touches a price.
func parsePrice(value string) (model.Decimal, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return model.Decimal{}, errors.New("empty decimal")
	}
	negative := false
	switch text[0] {
	case '-':
		negative = true
		text = text[1:]
	case '+':
		text = text[1:]
	}
	whole, fraction, _ := strings.Cut(text, ".")
	digits := whole + fraction
	if digits == "" {
		return model.Decimal{}, fmt.Errorf("no digits")
	}
	if len(fraction) > 255 {
		return model.Decimal{}, fmt.Errorf("more than 255 decimals")
	}
	units, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return model.Decimal{}, fmt.Errorf("not an int64 fixed-point value: %w", err)
	}
	if negative {
		units = -units
	}
	return model.Decimal{Units: units, Scale: uint8(len(fraction))}, nil
}
