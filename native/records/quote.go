package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TagQuotes is R_QUOTES (0x07), the <quotes> order book feed of task 10.
const TagQuotes byte = 0x07

// QuoteLevel is one decoded level of an R_QUOTES record.
type QuoteLevel struct {
	Side  byte  // 'B' or 'S'
	Price int64 // accumulated absolute price in wire units
	Qty   int64
	// Deleted marks a level whose accumulated price stayed below 1: the DLL
	// looks the level up and marks it instead of adding one (I, not observed live).
	Deleted bool
}

// Quotes is one decoded R_QUOTES record: W secid, W board, then levels until
// 0xFF: B side, L price delta, L quantity; the absolute price is the running sum
// of the deltas and keeps accumulating across the B -> S transition (RE-1
// FUN_1803bdc40, validated against the 40/40 DLL levels of the r10 capture).
type Quotes struct {
	SecID  uint16
	Board  uint16
	Levels []QuoteLevel
}

// ParseQuotes decodes the body of an R_QUOTES record.
func ParseQuotes(body []byte) (Quotes, error) {
	var q Quotes
	r := wire.NewReader(body)
	secid, err := r.Uint16()
	if err != nil {
		return q, fmt.Errorf("R_QUOTES secid: %w", err)
	}
	board, err := r.Uint16()
	if err != nil {
		return q, fmt.Errorf("R_QUOTES board: %w", err)
	}
	q = Quotes{SecID: secid, Board: board}
	var price int64
	for {
		side, err := r.Byte()
		if err != nil {
			return q, fmt.Errorf("R_QUOTES side: %w", err)
		}
		if side == 0xff {
			return q, nil
		}
		if side != 'B' && side != 'S' {
			return q, fmt.Errorf("R_QUOTES side: unexpected %#02x", side)
		}
		delta, err := r.Int32()
		if err != nil {
			return q, fmt.Errorf("R_QUOTES price delta: %w", err)
		}
		qty, err := r.Int32()
		if err != nil {
			return q, fmt.Errorf("R_QUOTES quantity: %w", err)
		}
		price += int64(delta)
		level := QuoteLevel{Side: side, Price: price, Qty: int64(qty)}
		if price < 1 {
			level.Deleted = true
		}
		q.Levels = append(q.Levels, level)
	}
}

// Event builds the typed <quotes> event, resolving the security, the board code
// and the price decimals from the catalog.
func (q Quotes) Event(cat *Catalog) model.Quotes {
	ev := model.Quotes{SecID: q.SecID}
	var decimals uint8
	if cat != nil {
		if sec, ok := cat.Security(q.SecID); ok {
			ev.Symbol = model.Symbol{SecCode: sec.SecCode}
			if board, ok := cat.Board(q.Board); ok {
				ev.Symbol.Board = board.Code
			}
			ev.Board = ev.Symbol.Board
			decimals, _ = cat.Decimals(q.SecID, q.Board)
		}
	}
	for _, level := range q.Levels {
		quote := model.Quote{SecID: q.SecID, Symbol: ev.Symbol}
		if level.Deleted {
			// Deletion marker (PDF 4.9): the side quantity is -1, no price.
			if level.Side == 'B' {
				quote.Buy = -1
			} else {
				quote.Sell = -1
			}
			ev.Levels = append(ev.Levels, quote)
			continue
		}
		quote.Price = model.Decimal{Units: level.Price, Scale: decimals}
		if level.Side == 'B' {
			quote.Buy = level.Qty
		} else {
			quote.Sell = level.Qty
		}
		ev.Levels = append(ev.Levels, quote)
	}
	return ev
}
