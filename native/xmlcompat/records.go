package xmlcompat

import (
	"errors"
	"fmt"

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
var ErrUnknownRecord = errors.New("xmlcompat: record layout not implemented")

// UnknownRecordError is the error Records returns at such a record.
type UnknownRecordError struct {
	Tag    byte
	Offset int
}

func (e *UnknownRecordError) Error() string {
	return fmt.Sprintf("%v: tag %#02x at offset %d", ErrUnknownRecord, e.Tag, e.Offset)
}

func (e *UnknownRecordError) Is(target error) bool { return target == ErrUnknownRecord }

// Records splits an RQ_MAIN reply payload. R_WRAPPER bodies are not
// returned: the wrapped records follow it and are yielded themselves. On a
// record whose layout is not implemented it returns the records before it
// and an error wrapping ErrUnknownRecord.
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
			if tag == TagWrapper {
				continue
			}
			body, err := r.Raw(int(n))
			if err != nil {
				return out, fmt.Errorf("%s body: %w", sizedTags[tag], err)
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
