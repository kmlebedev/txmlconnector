package records

import (
	"fmt"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TagCandles is R_HISTORY (0x09), the <candles> data of gethistorydata.
const TagCandles byte = 0x09

// Candle is one wire candle: 7 packed ints (time, open, close/high/low deltas to
// open, unknown field 6, volume), plus an extra value per candle when the record
// flag is set (openinterest candidate, I). The time is expressed in units of the
// candle period (see Candles.Event): for 1-minute data that is Unix minutes, for
// a 5-minute series it is 5-minute units, and so on.
type Candle struct {
	Time   int64 // absolute for the first candle, a same-unit delta afterwards
	Open   int64
	Close  int64
	High   int64
	Low    int64
	Field6 int64
	Volume int64
	HasOI  bool
	OI     int64
}

// Candles is one R_HISTORY record: W secid, W board, B period, B flag, candles
// newest-to-oldest, then one L status (0..3).
type Candles struct {
	SecID  uint16
	Board  uint16
	Period byte
	Flag   byte
	Items  []Candle
	Status int
}

// ParseCandles decodes one R_HISTORY body.
func ParseCandles(body []byte) (Candles, error) {
	var c Candles
	r := wire.NewReader(body)
	var err error
	if c.SecID, err = r.Uint16(); err != nil {
		return c, fmt.Errorf("R_HISTORY secid: %w", err)
	}
	if c.Board, err = r.Uint16(); err != nil {
		return c, fmt.Errorf("R_HISTORY board: %w", err)
	}
	if c.Period, err = r.Byte(); err != nil {
		return c, fmt.Errorf("R_HISTORY period: %w", err)
	}
	if c.Flag, err = r.Byte(); err != nil {
		return c, fmt.Errorf("R_HISTORY flag: %w", err)
	}
	for r.Len() > 0 {
		// The record ends with a single packed status; a candle needs at least
		// seven bytes (seven one-byte packed values).
		if r.Len() <= 5 {
			status, err := r.Int32()
			if err != nil {
				return c, fmt.Errorf("R_HISTORY status: %w", err)
			}
			if r.Len() != 0 {
				return c, fmt.Errorf("R_HISTORY: %d trailing bytes after status", r.Len())
			}
			c.Status = int(status)
			if c.Status < 0 || c.Status > 3 {
				return c, fmt.Errorf("R_HISTORY: status %d out of 0..3", c.Status)
			}
			return c, nil
		}
		var item Candle
		fields := []*int64{&item.Time, &item.Open, &item.Close, &item.High, &item.Low, &item.Field6, &item.Volume}
		for _, field := range fields {
			value, err := r.Int32()
			if err != nil {
				return c, fmt.Errorf("R_HISTORY candle: %w", err)
			}
			*field = int64(value)
		}
		if c.Flag != 0 {
			oi, err := r.Int32()
			if err != nil {
				return c, fmt.Errorf("R_HISTORY openinterest: %w", err)
			}
			item.HasOI, item.OI = true, int64(oi)
		}
		c.Items = append(c.Items, item)
	}
	return c, fmt.Errorf("R_HISTORY: missing status")
}

// Event builds the typed <candles> event: prices are absolute (open, open+deltas
// for close/high/low) with the mode decimals, time is the accumulated count of
// candle periods (period seconds from <candlekinds>; kind 1 is the 1-minute
// series, S) converted to a UTC instant, and the items are reversed to the DLL
// chronological order.
//
// When the period is not in <candlekinds> and is not kind 1, the event gets the
// "unknown candle period N" warning and every candle Time stays the zero
// instant: guessing 60 s dated 5-minute and daily series decades off (R8), so
// an undated series is better than a wrong one.
func (c Candles) Event(cat *Catalog, bias time.Duration) model.Candles {
	ev := model.Candles{SecID: c.SecID, Period: int(c.Period), Status: c.Status, ZoneBias: bias}
	var decimals uint8
	if cat != nil {
		if sec, ok := cat.Security(c.SecID); ok {
			ev.Symbol = model.Symbol{SecCode: sec.SecCode}
			if board, ok := cat.Board(c.Board); ok {
				ev.Symbol.Board = board.Code
			}
			decimals, _ = cat.Decimals(c.SecID, c.Board)
			if decimals == 0 {
				if pit, ok := quotationPit(sec, c.Board); ok {
					decimals = pit.Decimals
				}
			}
		}
	}
	// The wire time is in units of the candle period, not always minutes: the
	// 1-minute fixtures match minutes because their period is 60 s. The demo's
	// 5-minute history confirms the rule (raw*300 lands on the trade date) and
	// docs/protocol/history.md records the evidence. The multiplier comes from
	// the session dictionary only: without it the series stays undated.
	periodSeconds, dated := c.periodSeconds(cat)
	if !dated {
		ev.Warnings = append(ev.Warnings, fmt.Sprintf("unknown candle period %d", c.Period))
	}
	units := int64(0)
	for i, item := range c.Items {
		if i == 0 {
			units = item.Time
		} else {
			units += item.Time
		}
		var at time.Time
		if dated {
			at = time.Unix(units*periodSeconds, 0).UTC().Add(bias)
		}
		candle := model.Candle{
			Time:   at,
			Open:   model.Decimal{Units: item.Open, Scale: decimals},
			Close:  model.Decimal{Units: item.Open + item.Close, Scale: decimals},
			High:   model.Decimal{Units: item.Open + item.High, Scale: decimals},
			Low:    model.Decimal{Units: item.Open + item.Low, Scale: decimals},
			Volume: item.Volume,
		}
		if item.HasOI {
			oi := item.OI
			candle.OpenInterest = &oi
		}
		ev.Items = append(ev.Items, candle)
	}
	for i, j := 0, len(ev.Items)-1; i < j; i, j = i+1, j-1 {
		ev.Items[i], ev.Items[j] = ev.Items[j], ev.Items[i]
	}
	return ev
}

// periodSeconds resolves the wire time unit of the record from <candlekinds>.
// Kind 1 is the 1-minute series (S), so its 60-second unit is known even before
// the dictionary arrives; any other kind is unknown until then and the caller
// must not guess.
func (c Candles) periodSeconds(cat *Catalog) (int64, bool) {
	if cat != nil {
		if startup := cat.Startup(); startup != nil {
			for _, kind := range startup.CandleKinds {
				if int(kind.ID) == int(c.Period) && kind.Period > 0 {
					return int64(kind.Period), true
				}
			}
		}
	}
	if c.Period == 1 {
		return 60, true
	}
	return 0, false
}

// CombineCandles merges the multipart history replies (status 2 parts followed
// by the final status) into the single chronological <candles> callback of the
// DLL: items are concatenated in arrival order and then reversed.
func CombineCandles(parts []Candles, cat *Catalog, bias time.Duration) model.Candles {
	var out model.Candles
	if len(parts) == 0 {
		return out
	}
	seen := map[string]bool{}
	for _, part := range parts {
		ev := part.Event(cat, bias)
		out.Symbol = ev.Symbol
		out.SecID = ev.SecID
		out.Period = ev.Period
		out.Status = ev.Status
		out.ZoneBias = ev.ZoneBias
		// The parts share one series, so their warnings are the same: keep each
		// warning once and never tag the merged series with duplicates.
		for _, warning := range ev.Warnings {
			if !seen[warning] {
				seen[warning] = true
				out.Warnings = append(out.Warnings, warning)
			}
		}
		// Each Event is already chronological (oldest first) and the parts
		// arrive newest part first (status 2 parts before the final one), so
		// concatenate the parts in reverse arrival order and never reverse the
		// candles themselves.
		out.Items = append(ev.Items, out.Items...)
	}
	return out
}
