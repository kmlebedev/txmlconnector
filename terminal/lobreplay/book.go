// Package lobreplay replays a recorded order book and trade feed event by event
// and applies a queue-aware fill model to the orders of a Strategy.
//
// The input is the recording of plan Task 11 (md.book_events and md.trades): a
// JSONL dump of the recorder rows (see testdata/README.md) or any other Source.
// The book is rebuilt as the stream is read:
//
//   - a run of consecutive snapshot events is one full snapshot: the first row
//     resets the book, the following rows fill it level by level;
//   - a level event replaces (qty > 0) or deletes (qty == 0) one price level;
//   - a gap event drops the book: the recording was interrupted, so the levels
//     before and after it are never joined (plan Review Focus 3). The resting
//     orders are cancelled as well: their queue position cannot be trusted
//     across a break.
//
// Prices are model.Decimal everywhere. The replay computes at the Decimal64(6)
// scale of the md schema and never puts a float price on the wire: orders, fills
// and round trips keep int64 scaled Decimals. Money becomes a float in exactly
// two places that do not affect the wire: the commission rate
// (FillModel.FeeBps) applied by the fill model, and the aggregate metric fields
// of backtest.Metrics at the result boundary. All timestamps are UTC.
//
// The replay is deterministic: it reads no clock, no network and no randomness,
// so the same Source, Strategy and FillModel produce the same Result.
package lobreplay

import (
	"sort"

	"github.com/kmlebedev/txmlconnector-native/native/model"
)

// Event kinds. The first three are the md.book_events.kind Enum8 values;
// KindTrade is one md.trades row.
const (
	KindSnapshot = "snapshot"
	KindLevel    = "level"
	KindGap      = "gap"
	KindTrade    = "trade"
)

// Sides. Book rows use the md.book_events names; trade rows use the md.trades
// aggressor names: a "buy" trade lifts the ask, a "sell" trade hits the bid.
const (
	SideNone = "none"
	SideBid  = "bid"
	SideAsk  = "ask"
	SideBuy  = "buy"
	SideSell = "sell"
)

// priceScale is Decimal64(6), the scale of md.book_events.price and
// md.trades.price. The replay compares and returns prices at this scale.
const priceScale = 6

// priceFactor is 10^priceScale: one whole price unit in scaled units.
const priceFactor int64 = 1_000_000

// Level is one order book level. Price is always returned at the Decimal64(6)
// scale of the md recording.
type Level struct {
	Price model.Decimal
	Qty   int64
}

// Book is the limit order book rebuilt from the recording. A quantity of 0
// deletes the price. The zero Book is ready to use.
//
// Book is not safe for concurrent use.
type Book struct {
	bids map[int64]int64 // price units -> quantity
	asks map[int64]int64
	// bidKeys and askKeys cache the best-first price order; dirty marks a level
	// change after the last rebuild.
	bidKeys []int64
	askKeys []int64
	dirty   bool
}

// NewBook returns an empty book.
func NewBook() *Book {
	return &Book{bids: make(map[int64]int64), asks: make(map[int64]int64)}
}

// Reset drops all levels: the book becomes empty.
func (b *Book) Reset() {
	b.bids = make(map[int64]int64)
	b.asks = make(map[int64]int64)
	b.bidKeys = nil
	b.askKeys = nil
	b.dirty = true
}

// Best returns the best bid and the best ask. An empty side yields the zero
// Level (Qty == 0).
func (b *Book) Best() (bid, ask Level) {
	b.sortedKeys()
	if len(b.bidKeys) > 0 {
		bid = Level{Price: decimalPrice(b.bidKeys[0]), Qty: b.bids[b.bidKeys[0]]}
	}
	if len(b.askKeys) > 0 {
		ask = Level{Price: decimalPrice(b.askKeys[0]), Qty: b.asks[b.askKeys[0]]}
	}
	return bid, ask
}

// Depth returns up to n levels per side, best first: bids from the highest price
// down, asks from the lowest price up. A non-positive n returns nil slices, not
// all levels.
func (b *Book) Depth(n int) (bids, asks []Level) {
	if n <= 0 {
		return nil, nil
	}
	b.sortedKeys()
	bids = make([]Level, 0, min(n, len(b.bidKeys)))
	for _, units := range b.bidKeys[:min(n, len(b.bidKeys))] {
		bids = append(bids, Level{Price: decimalPrice(units), Qty: b.bids[units]})
	}
	asks = make([]Level, 0, min(n, len(b.askKeys)))
	for _, units := range b.askKeys[:min(n, len(b.askKeys))] {
		asks = append(asks, Level{Price: decimalPrice(units), Qty: b.asks[units]})
	}
	return bids, asks
}

// Imbalance returns (bid volume - ask volume) / (bid volume + ask volume) over
// the best n levels, in [-1, 1]; 0 for an empty book or n <= 0. It is a
// dimensionless indicator, so it is the only float64 the Book returns.
func (b *Book) Imbalance(n int) float64 {
	bids, asks := b.Depth(n)
	var bidQty, askQty int64
	for _, level := range bids {
		bidQty += level.Qty
	}
	for _, level := range asks {
		askQty += level.Qty
	}
	total := bidQty + askQty
	if total == 0 {
		return 0
	}
	return float64(bidQty-askQty) / float64(total)
}

// sortedKeys rebuilds the best-first price order if a level changed.
func (b *Book) sortedKeys() {
	if !b.dirty {
		return
	}
	b.bidKeys = b.bidKeys[:0]
	for units := range b.bids {
		b.bidKeys = append(b.bidKeys, units)
	}
	sort.Slice(b.bidKeys, func(i, j int) bool { return b.bidKeys[i] > b.bidKeys[j] })
	b.askKeys = b.askKeys[:0]
	for units := range b.asks {
		b.askKeys = append(b.askKeys, units)
	}
	sort.Slice(b.askKeys, func(i, j int) bool { return b.askKeys[i] < b.askKeys[j] })
	b.dirty = false
}

// levels returns the price map of one side, initializing it on first use.
func (b *Book) levels(side string) map[int64]int64 {
	if side == SideBid {
		if b.bids == nil {
			b.bids = make(map[int64]int64)
		}
		return b.bids
	}
	if b.asks == nil {
		b.asks = make(map[int64]int64)
	}
	return b.asks
}

// setLevel stores one level; a non-positive quantity deletes the price.
func (b *Book) setLevel(side string, price model.Decimal, qty int64) {
	units := priceUnits(price)
	levels := b.levels(side)
	_, exists := levels[units]
	if qty <= 0 {
		if !exists {
			return
		}
		delete(levels, units)
	} else {
		if exists && levels[units] == qty {
			return
		}
		levels[units] = qty
	}
	b.dirty = true
}

// qtyAt returns the quantity resting at a side/price (0 when the level is gone).
func (b *Book) qtyAt(side string, units int64) int64 {
	return b.levels(side)[units]
}

// sidePrices returns the best-first price units of one side: the walk order of a
// market order. The returned slice is owned by the book and must not be changed.
func (b *Book) sidePrices(side string) []int64 {
	b.sortedKeys()
	if side == SideBid {
		return b.bidKeys
	}
	return b.askKeys
}

// priceUnits converts a decimal price to the Decimal64(6) scaled units the
// replay computes with. A value with more than six decimals is truncated, as the
// md schema itself does; no float64 is involved.
func priceUnits(price model.Decimal) int64 {
	units := price.Units
	for scale := int(price.Scale); scale < priceScale; scale++ {
		units *= 10
	}
	for scale := priceScale; scale < int(price.Scale); scale++ {
		units /= 10
	}
	return units
}

// decimalPrice renders scaled units as a model.Decimal at the Decimal64(6) scale
// of the md recording.
func decimalPrice(units int64) model.Decimal {
	return model.Decimal{Units: units, Scale: priceScale}
}
