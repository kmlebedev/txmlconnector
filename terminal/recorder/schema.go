package recorder

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/terminal/chstore"
)

// Tables of the md database written by the recorder.
const (
	// Database is the ClickHouse database of the recorded market data.
	Database = "md"
	// BookEventsTable holds the order book snapshots, level changes and gaps.
	BookEventsTable = "md.book_events"
	// TradesTable holds the market trades; ReplacingMergeTree dedupes the
	// replayed trade numbers.
	TradesTable = "md.trades"
	// priceScale is the Decimal64 scale of both tables.
	priceScale = 6
)

// ClickHouse time layouts: DateTime64(3) keeps milliseconds, DateTime64(6)
// microseconds; both columns are declared with the UTC time zone.
const (
	timeMillisLayout = "2006-01-02 15:04:05.000"
	timeMicrosLayout = "2006-01-02 15:04:05.000000"
)

// schemaDDL is executed by EnsureSchema. The first statement creates the md
// database itself: on a clean ClickHouse the CREATE TABLE md.* would fail with
// "Database md does not exist". The columns, enums, ordering and partitioning
// are the plan Task 11 contract; data.md records the semantics.
var schemaDDL = []string{
	"CREATE DATABASE IF NOT EXISTS " + Database,
	`CREATE TABLE IF NOT EXISTS md.book_events (
	ts DateTime64(3, 'UTC'),
	recv_ts DateTime64(6, 'UTC'),
	session_id UUID,
	seq UInt64,
	board LowCardinality(String),
	sec_code LowCardinality(String),
	kind Enum8('snapshot' = 1, 'level' = 2, 'gap' = 3),
	side Enum8('bid' = 1, 'ask' = 2, 'none' = 0),
	price Decimal64(6),
	qty Int64
) ENGINE = MergeTree
ORDER BY (sec_code, ts, seq)
PARTITION BY toYYYYMMDD(ts)`,
	`CREATE TABLE IF NOT EXISTS md.trades (
	ts DateTime64(3, 'UTC'),
	recv_ts DateTime64(6, 'UTC'),
	board LowCardinality(String),
	sec_code LowCardinality(String),
	trade_no Int64,
	price Decimal64(6),
	qty Int64,
	side Enum8('buy' = 1, 'sell' = 2, 'none' = 0),
	open_interest Int64
) ENGINE = ReplacingMergeTree
ORDER BY (sec_code, trade_no)`,
}

// Schema returns the CREATE DATABASE / CREATE TABLE IF NOT EXISTS statements of
// the md database (a copy, for tools and tests).
func Schema() []string {
	return append([]string(nil), schemaDDL...)
}

// EnsureSchema creates the md database and its tables if they do not exist yet.
func EnsureSchema(ctx context.Context, cl *chstore.Client) error {
	if cl == nil {
		return fmt.Errorf("recorder: nil ClickHouse client")
	}
	for _, ddl := range schemaDDL {
		if err := cl.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("recorder: ensure schema: %w", err)
		}
	}
	return nil
}

// bookRow is one md.book_events JSONEachRow line. Prices and times are strings:
// the Decimal64 and DateTime64 values must stay exact, so no float64 is used
// anywhere on the way to ClickHouse.
type bookRow struct {
	TS        string `json:"ts"`
	RecvTS    string `json:"recv_ts"`
	SessionID string `json:"session_id"`
	Seq       uint64 `json:"seq"`
	Board     string `json:"board"`
	SecCode   string `json:"sec_code"`
	Kind      string `json:"kind"`
	Side      string `json:"side"`
	Price     string `json:"price"`
	Qty       int64  `json:"qty"`
}

// tradeRow is one md.trades JSONEachRow line.
type tradeRow struct {
	TS           string `json:"ts"`
	RecvTS       string `json:"recv_ts"`
	Board        string `json:"board"`
	SecCode      string `json:"sec_code"`
	TradeNo      int64  `json:"trade_no"`
	Price        string `json:"price"`
	Qty          int64  `json:"qty"`
	Side         string `json:"side"`
	OpenInterest int64  `json:"open_interest"`
}

// ClickHouseSink writes the recorder batches into md.book_events/md.trades. It
// is the recorder.Sink the md-recorder command wires to the chstore client; the
// context is the lifetime of the recording session.
type ClickHouseSink struct {
	ctx    context.Context
	client *chstore.Client
}

// NewClickHouseSink builds the ClickHouse sink for the recording session. A nil
// ctx means context.Background().
func NewClickHouseSink(ctx context.Context, cl *chstore.Client) *ClickHouseSink {
	if ctx == nil {
		ctx = context.Background()
	}
	return &ClickHouseSink{ctx: ctx, client: cl}
}

// Book inserts the md.book_events rows of one recorder batch.
func (s *ClickHouseSink) Book(events []BookEvent) error {
	if len(events) == 0 {
		return nil
	}
	if s == nil || s.client == nil {
		return fmt.Errorf("recorder: no ClickHouse client")
	}
	rows := make([]bookRow, 0, len(events))
	for _, event := range events {
		row, err := bookRowOf(event)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	return s.client.Insert(s.ctx, BookEventsTable, rows)
}

// Trades inserts the md.trades rows of one recorder batch.
func (s *ClickHouseSink) Trades(events []TradeEvent) error {
	if len(events) == 0 {
		return nil
	}
	if s == nil || s.client == nil {
		return fmt.Errorf("recorder: no ClickHouse client")
	}
	rows := make([]tradeRow, 0, len(events))
	for _, event := range events {
		row, err := tradeRowOf(event)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	return s.client.Insert(s.ctx, TradesTable, rows)
}

// bookRowOf maps one typed book event to its ClickHouse row.
func bookRowOf(ev BookEvent) (bookRow, error) {
	kind, side := ev.Kind.String(), ev.Side.String()
	if kind == "" {
		return bookRow{}, fmt.Errorf("recorder: unknown book kind %d", ev.Kind)
	}
	if side == "" {
		return bookRow{}, fmt.Errorf("recorder: unknown book side %d", ev.Side)
	}
	return bookRow{
		TS:        chTime(ev.TS, timeMillisLayout),
		RecvTS:    chTime(ev.RecvTS, timeMicrosLayout),
		SessionID: ev.SessionID,
		Seq:       ev.Seq,
		Board:     ev.Board,
		SecCode:   ev.SecCode,
		Kind:      kind,
		Side:      side,
		Price:     decimalString(ev.Price, priceScale),
		Qty:       ev.Qty,
	}, nil
}

// tradeRowOf maps one typed trade event to its ClickHouse row.
func tradeRowOf(ev TradeEvent) (tradeRow, error) {
	side := tradeSideName(ev.Side)
	if side == "" {
		return tradeRow{}, fmt.Errorf("recorder: unknown trade side %d", ev.Side)
	}
	return tradeRow{
		TS:           chTime(ev.TS, timeMillisLayout),
		RecvTS:       chTime(ev.RecvTS, timeMicrosLayout),
		Board:        ev.Board,
		SecCode:      ev.SecCode,
		TradeNo:      ev.TradeNo,
		Price:        decimalString(ev.Price, priceScale),
		Qty:          ev.Qty,
		Side:         side,
		OpenInterest: ev.OpenInterest,
	}, nil
}

// tradeSideName is the md.trades.side Enum8 name of the shared side value.
func tradeSideName(side Side) string {
	switch side {
	case SideNone:
		return "none"
	case SideBid:
		return "buy"
	case SideAsk:
		return "sell"
	}
	return ""
}

// chTime renders a UTC DateTime64 value the way ClickHouse parses a timezone-less
// string for a column declared with the UTC zone.
func chTime(t time.Time, layout string) string {
	return t.UTC().Format(layout)
}

// decimalString renders a model.Decimal as the fixed priceScale Decimal64 string
// (29146 with scale 2 -> "291.460000"), so no float64 ever touches a price. A
// value with more than priceScale decimals is truncated: no MOEX board uses more.
func decimalString(d model.Decimal, scale int) string {
	units := d.Units
	shift := scale - int(d.Scale)
	for ; shift > 0; shift-- {
		units *= 10
	}
	for ; shift < 0; shift++ {
		units /= 10
	}
	return fixedDecimal(units, scale)
}

// fixedDecimal renders units as a decimal string with exactly scale decimals.
func fixedDecimal(units int64, scale int) string {
	negative := units < 0
	magnitude := uint64(units)
	if negative {
		magnitude = -magnitude
	}
	digits := strconv.FormatUint(magnitude, 10)
	if scale > 0 {
		for len(digits) <= scale {
			digits = "0" + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if negative {
		return "-" + digits
	}
	return digits
}
