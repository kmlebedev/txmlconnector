//go:build ignore

// Command generate writes testdata/sber-10m.jsonl.gz: a synthetic ten-minute
// SBER recording in the md.book_events/md.trades schema of Task 11.
//
// The data is generated, not captured: a deterministic LCG produces the price
// path, the level changes and the trades, so the file contains market data only
// — no live session, no credentials, no account or client identifiers of a real
// recording. Run it from the terminal/lobreplay directory to regenerate the
// committed fixture byte for byte:
//
//	go run testdata/generate.go
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

const (
	path         = "testdata/sber-10m.jsonl.gz"
	board        = "TQBR"
	secCode      = "SBER"
	sessionID    = "00000000-0000-4000-8000-000000000014" // placeholder, not a real session
	startLiteral = "2026-11-05 07:00:00.000"              // 10:00 MSK in UTC
	sessionSec   = 600                                    // ten minutes
	midCents     = 29150                                  // 291.50 in whole cents
)

// ClickHouse DateTime64 text layouts: milliseconds for ts, microseconds for
// recv_ts, both UTC; the loader parses them back without a float64.
const (
	millisLayout = "2006-01-02 15:04:05.000"
	microsLayout = "2006-01-02 15:04:05.000000"
	timeLayout   = "2006-01-02 15:04:05"
)

// row is one JSONL line: the md column names plus the "table" discriminator
// that lets a single file hold both feeds of the recording.
type row struct {
	Table        string `json:"table"`
	TS           string `json:"ts"`
	RecvTS       string `json:"recv_ts"`
	SessionID    string `json:"session_id,omitempty"`
	Seq          uint64 `json:"seq,omitempty"`
	Board        string `json:"board"`
	SecCode      string `json:"sec_code"`
	Kind         string `json:"kind,omitempty"`
	Side         string `json:"side"`
	Price        string `json:"price"`
	Qty          int64  `json:"qty"`
	TradeNo      int64  `json:"trade_no,omitempty"`
	OpenInterest int64  `json:"open_interest,omitempty"`
}

// lcg is the deterministic generator: no math/rand state and no clock, so the
// committed fixture is reproducible.
type lcg struct{ state uint64 }

func newLCG(seed uint64) *lcg { return &lcg{state: seed} }

func (l *lcg) next() uint64 {
	l.state = l.state*6364136223846793005 + 1442695040888963407
	return l.state
}

func (l *lcg) intn(n int) int { return int((l.next() >> 33) % uint64(n)) }

// bookSide is the generator's own view of one side: price in whole cents -> qty.
// It only keeps the file plausible; the replay rebuilds the book from the file.
type bookSide struct {
	levels map[int64]int64
}

func (s *bookSide) set(price, qty int64) { s.levels[price] = qty }

func (s *bookSide) delete(price int64) { delete(s.levels, price) }

// best returns the best price of the side (0 when empty).
func (s *bookSide) best(higherBetter bool) int64 {
	best, first := int64(0), true
	for price, qty := range s.levels {
		if qty <= 0 {
			continue
		}
		if first || (higherBetter && price > best) || (!higherBetter && price < best) {
			best = price
			first = false
		}
	}
	if first {
		return 0
	}
	return best
}

// prices returns the price levels best first (descending for bids, ascending for
// asks). Sorting removes the nondeterminism of the map iteration.
func (s *bookSide) prices(higherBetter bool) []int64 {
	out := make([]int64, 0, len(s.levels))
	for price, qty := range s.levels {
		if qty > 0 {
			out = append(out, price)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if higherBetter {
			return out[i] > out[j]
		}
		return out[i] < out[j]
	})
	return out
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "generate:", err)
		os.Exit(1)
	}
}

func run() error {
	start, err := time.ParseInLocation(timeLayout, startLiteral, time.UTC)
	if err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	gz.Header.ModTime = time.Time{} // no wall clock in the archive header
	gz.Header.OS = 255
	buffered := bufio.NewWriterSize(gz, 64<<10)
	encoder := json.NewEncoder(buffered)
	encoder.SetEscapeHTML(false)

	rng := newLCG(0x5eed202611050014)
	bids := &bookSide{levels: make(map[int64]int64)}
	asks := &bookSide{levels: make(map[int64]int64)}
	for i := int64(1); i <= 10; i++ {
		bids.set(midCents-i, int64(100+rng.intn(900)))
		asks.set(midCents+i, int64(100+rng.intn(900)))
	}

	var seq uint64
	var tradeNo int64
	currentSession := sessionID

	recv := func(ts time.Time) string {
		return ts.Add(time.Duration(rng.intn(900)) * time.Microsecond).Format(microsLayout)
	}
	writeBook := func(ts time.Time, kind, side, price string, qty int64) error {
		seq++
		return encoder.Encode(row{
			Table: "book_events", TS: ts.Format(millisLayout), RecvTS: recv(ts),
			SessionID: currentSession, Seq: seq, Board: board, SecCode: secCode,
			Kind: kind, Side: side, Price: price, Qty: qty,
		})
	}
	writeTrade := func(ts time.Time, side string, cents, qty int64) error {
		tradeNo++
		return encoder.Encode(row{
			Table: "trades", TS: ts.Format(millisLayout), RecvTS: recv(ts),
			Board: board, SecCode: secCode, Side: side,
			Price: priceString(cents), Qty: qty, TradeNo: tradeNo,
		})
	}

	// writeSnapshot writes the full book after a (re)subscription the way the
	// recorder does it: bids highest first, asks lowest first.
	writeSnapshot := func(ts time.Time) error {
		for _, cents := range bids.prices(true) {
			if err := writeBook(ts, "snapshot", "bid", priceString(cents), bids.levels[cents]); err != nil {
				return err
			}
		}
		for _, cents := range asks.prices(false) {
			if err := writeBook(ts, "snapshot", "ask", priceString(cents), asks.levels[cents]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeSnapshot(start); err != nil {
		return err
	}

	updateLevel := func(ts time.Time, side *bookSide, name string, higherBetter bool) error {
		switch roll := rng.intn(100); {
		case roll < 55: // change an existing level
			prices := side.prices(higherBetter)
			if len(prices) == 0 {
				return nil
			}
			cents := prices[rng.intn(len(prices))]
			qty := int64(100 + rng.intn(1400))
			side.set(cents, qty)
			return writeBook(ts, "level", name, priceString(cents), qty)
		case roll < 80: // add a deeper level
			offset := int64(1 + rng.intn(8))
			cents := midCents + offset
			if name == "bid" {
				cents = midCents - offset
			}
			qty := int64(100 + rng.intn(1400))
			side.set(cents, qty)
			return writeBook(ts, "level", name, priceString(cents), qty)
		default: // delete a non-best level
			prices := side.prices(higherBetter)
			if len(prices) < 3 {
				return nil
			}
			cents := prices[2+rng.intn(len(prices)-2)]
			side.delete(cents)
			return writeBook(ts, "level", name, priceString(cents), 0)
		}
	}

	trade := func(ts time.Time) error {
		aggressorBuy := rng.intn(2) == 0
		var level *bookSide
		var cents int64
		name, bookName := "sell", "bid"
		if aggressorBuy {
			level, name, bookName = asks, "buy", "ask"
			cents = asks.best(false)
		} else {
			level, name, bookName = bids, "sell", "bid"
			cents = bids.best(true)
		}
		if cents == 0 {
			return nil
		}
		available := level.levels[cents]
		if available <= 0 {
			return nil
		}
		qty := int64(1 + rng.intn(300))
		if qty > available {
			qty = available
		}
		if err := writeTrade(ts, name, cents, qty); err != nil {
			return err
		}
		available -= qty
		if available == 0 {
			level.delete(cents)
			if err := writeBook(ts, "level", bookName, priceString(cents), 0); err != nil {
				return err
			}
			if len(level.levels) == 0 {
				// Never leave a side empty: quote one level around the mid so
				// the next trade has a price.
				refill := int64(midCents - 1)
				if bookName == "ask" {
					refill = int64(midCents + 1)
				}
				level.set(refill, 1000)
				if err := writeBook(ts, "level", bookName, priceString(refill), 1000); err != nil {
					return err
				}
			}
			return nil
		}
		level.set(cents, available)
		return nil
	}

	for sec := 0; sec < sessionSec; sec++ {
		ts := start.Add(time.Duration(sec) * time.Second)
		if rng.intn(100) < 60 {
			if err := updateLevel(ts, bids, "bid", true); err != nil {
				return err
			}
		}
		if rng.intn(100) < 20 {
			if err := updateLevel(ts, asks, "ask", false); err != nil {
				return err
			}
		}
		if rng.intn(100) < 85 {
			if err := trade(ts); err != nil {
				return err
			}
		}
		if rng.intn(100) < 15 {
			if err := trade(ts); err != nil {
				return err
			}
		}
		// One reconnect in the middle of the recording (Review Focus 3): the old
		// session ends with the gap marker, the new one starts with a fresh full
		// snapshot. The replay must not join the levels before and after it.
		if sec == sessionSec/2 {
			seq++
			if err := encoder.Encode(row{
				Table: "book_events", TS: ts.Format(millisLayout), RecvTS: recv(ts),
				SessionID: currentSession, Seq: seq, Board: board, SecCode: secCode,
				Kind: "gap", Side: "none", Price: priceString(0), Qty: 0,
			}); err != nil {
				return err
			}
			currentSession = "00000000-0000-4000-8000-000000000015" // second placeholder session
			seq = 0                                                 // the recorder's row sequence restarts with the session
			if err := writeSnapshot(ts); err != nil {
				return err
			}
		}
	}

	if err := buffered.Flush(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return file.Close()
}

// priceString renders whole cents as a Decimal64(6) string: 29150 -> "291.500000".
func priceString(cents int64) string {
	whole, fraction := cents/100, cents%100
	return fmt.Sprintf("%d.%06d", whole, fraction*10000)
}
