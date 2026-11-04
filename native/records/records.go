// Package records decodes the binary TRANSAQ records carried by RQ_MAIN and
// the RQ_STARTUP reply into typed data. It is pure Go and does not depend on
// the XML layer: the DLL-compatible XML is rendered by native/xmlcompat, and
// the domain types live in native/model.
package records

import (
	"errors"
	"fmt"
	"math/bits"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// RQ_MAIN record tags (dispatcher FUN_18038d7d0; the switch value is tag-1).
const (
	TagAllTrade  byte = 0x08 // R_ALLTRADE ("NEW_MTRADE" in DLL messages)
	TagSecRate   byte = 0x0a // R_SEC_RATE: W secid, L value
	TagSecNew    byte = 0x28 // R_SEC_NEW
	TagDate      byte = 0x3d // R_DATE
	TagSecParam  byte = 0x5e // R_SEC_PARAM
	TagWrapper   byte = 0x5f // R_WRAPPER
	TagDoubleLeg byte = 0x66 // R_DOUBLELEG
)

// sizedTags carry a big-endian u16 body size right after the tag
// (FUN_1801bdf00), so they can be skipped without decoding.
var sizedTags = map[byte]string{
	0x02: "R_CLN_NEW", 0x03: "R_ORD_NEW", 0x04: "R_TRD", 0x08: "R_ALLTRADE",
	0x34: "R_BOARD", 0x7c: "ReadICA",
	0x10: "R_ORD_MODIFY", 0x28: "R_SEC_NEW", 0x3a: "R_ORD_RENEW", 0x3b: "R_CLIENT_ASSET",
	0x54: "R_STOP", 0x5e: "R_SEC_PARAM", 0x5f: "R_WRAPPER", 0x66: "R_DOUBLELEG",
}

// attrTags are security attribute updates handled by FUN_1803c1360: W secid
// followed by one byte (R_SEC_ZALOG, R_SEC_OBLIG, R_SEC_ONOFF, 0x0e).
var attrTags = map[byte]string{0x0b: "R_SEC_ZALOG", 0x0c: "R_SEC_OBLIG", 0x0d: "R_SEC_ONOFF", 0x0e: "R_SEC_0E"}

// Record is one RQ_MAIN event record.
type Record struct {
	Tag byte
	// Body is the sized record body, or for R_DATE the packed value bytes.
	Body []byte
}

// ErrUnknownRecord reports a record without a size prefix whose layout is not
// implemented; the rest of the payload cannot be split without it.
var ErrUnknownRecord = errors.New("records: record layout not implemented")

// UnknownRecordError is the error Records returns at such a record.
type UnknownRecordError struct {
	Tag    byte
	Offset int
}

func (e *UnknownRecordError) Error() string {
	return fmt.Sprintf("%v: tag %#02x at offset %d", ErrUnknownRecord, e.Tag, e.Offset)
}

func (e *UnknownRecordError) Is(target error) bool { return target == ErrUnknownRecord }

// Records splits an RQ_MAIN reply payload. R_WRAPPER bodies are read and
// skipped: their nested records (0x6e R_EDP_RATE) belong to task 12 and are not
// yielded yet. On a record whose layout is not implemented it returns the
// records before it and an error wrapping ErrUnknownRecord.
func Records(p []byte) ([]Record, error) {
	var out []Record
	r := wire.NewReader(p)
	for r.Len() > 0 {
		tag, _ := r.Byte()
		switch {
		case sizedTags[tag] != "":
			n, err := r.Uint16()
			if err != nil {
				return out, fmt.Errorf("%s size: %w", sizedTags[tag], err)
			}
			body, err := r.Raw(int(n))
			if err != nil {
				return out, fmt.Errorf("%s body: %w", sizedTags[tag], err)
			}
			if tag == TagWrapper {
				// The wrapper body carries nested records the DLL reads in
				// place (for example R_EDP_RATE 0x6e): parse them so their
				// lengths are verified and the splitter stays aligned.
				if nested, err := Records(body); err == nil {
					out = append(out, nested...)
				}
				// A wrapper body may carry records not mapped yet (the E07 tail
				// wraps an 0x74 record, task 12 I): the outer stream stays
				// aligned because the wrapper body length is known, so such a
				// body is skipped rather than aborting the split.

				continue
			}
			out = append(out, Record{Tag: tag, Body: body})
		case tag == TagDate:
			start := len(p) - r.Len()
			if _, err := r.Int32(); err != nil { // FUN_1801bd960: 32-bit, sign-extended
				return out, fmt.Errorf("R_DATE: %w", err)
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case tag == TagSecRate:
			start := len(p) - r.Len()
			if _, err := r.Uint16(); err != nil {
				return out, fmt.Errorf("R_SEC_RATE: %w", err)
			}
			if _, err := r.Int32(); err != nil {
				return out, fmt.Errorf("R_SEC_RATE: %w", err)
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case attrTags[tag] != "":
			body, err := r.Raw(3)
			if err != nil {
				return out, fmt.Errorf("%s: %w", attrTags[tag], err)
			}
			out = append(out, Record{Tag: tag, Body: body})
		case tag == TagOvernight:
			body, err := r.Raw(1)
			if err != nil {
				return out, fmt.Errorf("R_OVERNIGHT: %w", err)
			}
			out = append(out, Record{Tag: tag, Body: body})
		case tag == 0x62 || tag == 0x67:
			// ReadMarCat/ReadUnicat: two state bytes, no callbacks (RE-1).
			body, err := r.Raw(2)
			if err != nil {
				return out, fmt.Errorf("record %#02x: %w", tag, err)
			}
			out = append(out, Record{Tag: tag, Body: body})
		case tag == TagCandles:
			// R_HISTORY is not length-delimited: its last packed value is the
			// status, so the splitter expects one record at the start of the
			// payload (the DLL sends each history part in its own reply; several
			// records in one payload would be ambiguous — I).
			start := len(p) - r.Len()
			// Walk the record once to find its end: W,W,B,B, then candles until
			// only the packed status is left.
			c := wire.NewReader(p[start:])
			if _, err := c.Uint16(); err != nil {
				return out, fmt.Errorf("R_HISTORY: %w", err)
			}
			if _, err := c.Uint16(); err != nil {
				return out, fmt.Errorf("R_HISTORY: %w", err)
			}
			if _, err := c.Byte(); err != nil {
				return out, fmt.Errorf("R_HISTORY: %w", err)
			}
			flag, err := c.Byte()
			if err != nil {
				return out, fmt.Errorf("R_HISTORY: %w", err)
			}
			for c.Len() > 0 {
				if c.Len() <= 5 {
					if _, err := c.Int32(); err != nil {
						return out, fmt.Errorf("R_HISTORY status: %w", err)
					}
					break
				}
				for i := 0; i < 7; i++ {
					if _, err := c.Int32(); err != nil {
						return out, fmt.Errorf("R_HISTORY candle: %w", err)
					}
				}
				if flag != 0 {
					if _, err := c.Int32(); err != nil {
						return out, fmt.Errorf("R_HISTORY openinterest: %w", err)
					}
				}
			}
			n := len(p[start:]) - c.Len()
			r.Raw(n)
			out = append(out, Record{Tag: tag, Body: p[start : start+n]})
		case tag == TagFortsSession:
			start := len(p) - r.Len()
			if _, err := r.Int32(); err != nil {
				return out, fmt.Errorf("R_FORTS_SESSION: %w", err)
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case tag == TagUnion:
			start := len(p) - r.Len()
			if _, err := r.Int64(); err != nil {
				return out, fmt.Errorf("R_UNION id: %w", err)
			}
			if _, err := r.String(20); err != nil {
				return out, fmt.Errorf("R_UNION name: %w", err)
			}
			if _, err := r.String(32); err != nil {
				return out, fmt.Errorf("R_UNION category: %w", err)
			}
			if _, err := r.Byte(); err != nil {
				return out, fmt.Errorf("R_UNION unicat: %w", err)
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case tag == TagQuotes:
			start := len(p) - r.Len()
			if _, err := r.Uint16(); err != nil {
				return out, fmt.Errorf("R_QUOTES secid: %w", err)
			}
			if _, err := r.Uint16(); err != nil {
				return out, fmt.Errorf("R_QUOTES board: %w", err)
			}
			for {
				side, err := r.Byte()
				if err != nil {
					return out, fmt.Errorf("R_QUOTES side: %w", err)
				}
				if side == 0xff {
					break
				}
				if _, err := r.Int32(); err != nil {
					return out, fmt.Errorf("R_QUOTES price delta: %w", err)
				}
				if _, err := r.Int32(); err != nil {
					return out, fmt.Errorf("R_QUOTES quantity: %w", err)
				}
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case tag == TagFortsSession:
			start := len(p) - r.Len()
			if _, err := r.Int32(); err != nil {
				return out, fmt.Errorf("R_FORTS_SESSION: %w", err)
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case tag == TagEdpRate:
			start := len(p) - r.Len()
			if _, err := r.String(4); err != nil {
				return out, fmt.Errorf("R_EDP_RATE: %w", err)
			}
			if _, err := r.String(25); err != nil {
				return out, fmt.Errorf("R_EDP_RATE: %w", err)
			}
			if _, err := r.Byte(); err != nil {
				return out, fmt.Errorf("R_EDP_RATE: %w", err)
			}
			if _, err := r.Int32(); err != nil {
				return out, fmt.Errorf("R_EDP_RATE: %w", err)
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case tag == TagAmBook:
			start := len(p) - r.Len()
			if _, err := r.Uint16(); err != nil {
				return out, fmt.Errorf("AmBOOK: %w", err)
			}
			if _, err := r.Byte(); err != nil {
				return out, fmt.Errorf("AmBOOK: %w", err)
			}
			if _, err := r.Byte(); err != nil {
				return out, fmt.Errorf("AmBOOK: %w", err)
			}
			if _, err := r.Int32(); err != nil {
				return out, fmt.Errorf("AmBOOK: %w", err)
			}
			if _, err := r.Int32(); err != nil {
				return out, fmt.Errorf("AmBOOK: %w", err)
			}
			if _, err := r.String(4); err != nil {
				return out, fmt.Errorf("AmBOOK: %w", err)
			}
			if _, err := r.Int32(); err != nil {
				return out, fmt.Errorf("AmBOOK: %w", err)
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case tag == TagQuotation:
			start := len(p) - r.Len()
			if _, err := r.Uint16(); err != nil {
				return out, fmt.Errorf("R_QUOTATION secid: %w", err)
			}
			if _, err := r.Uint16(); err != nil {
				return out, fmt.Errorf("R_QUOTATION board: %w", err)
			}
			mask, err := r.Int64()
			if err != nil {
				return out, fmt.Errorf("R_QUOTATION mask: %w", err)
			}
			for i := 0; i < bits.OnesCount64(uint64(mask)); i++ {
				if _, err := r.Int32(); err != nil {
					return out, fmt.Errorf("R_QUOTATION value: %w", err)
				}
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		case tag == 0x69:
			// ReadURTUpdate: bytes up to and including 0xff (RE-1); state only.
			// Without the terminator the rest of the payload cannot be split, so
			// this is an unknown-layout record, not a silent swallow.
			start := len(p) - r.Len()
			terminated := false
			for r.Len() > 0 {
				b, err := r.Byte()
				if err != nil {
					return out, fmt.Errorf("record 0x69: %w", err)
				}
				if b == 0xff {
					terminated = true
					break
				}
			}
			if !terminated {
				return out, &UnknownRecordError{Tag: tag, Offset: start}
			}
			out = append(out, Record{Tag: tag, Body: p[start : len(p)-r.Len()]})
		default:
			return out, &UnknownRecordError{Tag: tag, Offset: len(p) - r.Len() - 1}
		}
	}
	return out, nil
}

// TradeDate decodes an R_DATE body: seconds since the Unix epoch at 00:00
// of the trading day (logged by the DLL as days, "R_DATE:20760").
func TradeDate(rec Record) (int64, error) {
	v, err := wire.NewReader(rec.Body).Int32()
	return int64(v), err
}
