// Package model holds the typed TRANSAQ domain: exact prices and quantities,
// symbols, records and dictionaries shared by the binary decoder (records) and
// the XML layer (xmlcompat). It does not import other repository packages.
package model

import (
	"math"
	"strconv"
	"strings"
)

// Decimal is an exact price or quantity: Units × 10^-Scale.
type Decimal struct {
	Units int64
	Scale uint8
}

// String renders the decimal as the DLL does: trailing zeros after the point
// removed but at least one kept (305.12, 305.0, 0.098, -305.12, 7).
func (d Decimal) String() string {
	neg := d.Units < 0
	u := uint64(d.Units)
	if neg {
		u = -u
	}
	digits := strconv.FormatUint(u, 10)
	if d.Scale > 0 {
		decimals := int(d.Scale)
		if len(digits) <= decimals {
			digits = strings.Repeat("0", decimals-len(digits)+1) + digits
		}
		whole, frac := digits[:len(digits)-decimals], strings.TrimRight(digits[len(digits)-decimals:], "0")
		if frac == "" {
			frac = "0"
		}
		digits = whole + "." + frac
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// Float64 converts the decimal to a float; lossy for long fractions, so it is
// only for consumers that need a float (protocol fields encoded as D).
func (d Decimal) Float64() float64 {
	return float64(d.Units) / math.Pow10(int(d.Scale))
}
