package records

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// SecParam is the decoded R_SEC_PARAM (0x5e) body. The value layout follows RE-1
// (FUN_1803d78a0, docs/protocol/dictionaries.md) and was validated against the
// DLL callbacks of the E07 capture:
//
//	mask 0x1/0x2/0x4 -> a packed 64-bit value each (the OPT "bgo" block),
//	mask 0x8        -> two header bytes (meaning unknown) + a little-endian
//	                   double (point_cost),
//	mask 0x10/0x20  -> a packed 64-bit value each (minprice/maxprice on FUT).
//
// Only the value bits used by <sec_info_upd> are parsed; the bits above 0x20 do
// not affect the callback and stay unread. A record without mask 0x8 is muted
// (DLL: no callback, validated on 18 284 live records, S).
type SecParam struct {
	SecID uint16
	Mask  uint32

	BuyDeposit  int64
	SellDeposit int64
	BgoC        int64
	BgoNC       int64
	BgoBuy      int64

	// Board is the mode/board word that precedes the point_cost double
	// (0x2a/0x2c in the E07 fixture; the exact semantics beyond the value are I).
	Board        uint16
	PointCost    float64
	HasPointCost bool

	MinPrice int64
	MaxPrice int64
	HasMin   bool
	HasMax   bool
}

// ParseSecParam decodes an R_SEC_PARAM body: W secid, L mask, then the values of
// the set bits. Muted records (mask&0x8 == 0) stop after the mask.
func ParseSecParam(body []byte) (SecParam, error) {
	var p SecParam
	r := wire.NewReader(body)
	secid, err := r.Uint16()
	if err != nil {
		return p, fmt.Errorf("R_SEC_PARAM secid: %w", err)
	}
	mask, err := r.Int64()
	if err != nil {
		return p, fmt.Errorf("R_SEC_PARAM mask: %w", err)
	}
	p = SecParam{SecID: secid, Mask: uint32(mask)}
	if p.Mask&0x8 == 0 {
		return p, nil
	}
	if p.Mask&0x1 != 0 {
		v, err := r.Int64()
		if err != nil {
			return p, fmt.Errorf("R_SEC_PARAM bit 0x1: %w", err)
		}
		p.BgoC, p.BuyDeposit = v, v
	}
	if p.Mask&0x2 != 0 {
		v, err := r.Int64()
		if err != nil {
			return p, fmt.Errorf("R_SEC_PARAM bit 0x2: %w", err)
		}
		p.BgoNC, p.SellDeposit = v, v
	}
	if p.Mask&0x4 != 0 {
		v, err := r.Int64()
		if err != nil {
			return p, fmt.Errorf("R_SEC_PARAM bit 0x4: %w", err)
		}
		p.BgoBuy = v
	}
	if p.Mask&0x8 != 0 {
		if p.Board, err = r.Uint16(); err != nil {
			return p, fmt.Errorf("R_SEC_PARAM point cost board: %w", err)
		}
		raw, err := r.Raw(8)
		if err != nil {
			return p, fmt.Errorf("R_SEC_PARAM point cost: %w", err)
		}
		p.PointCost = math.Float64frombits(binary.LittleEndian.Uint64(raw))
		p.HasPointCost = true
	}
	if p.Mask&0x10 != 0 {
		v, err := r.Int64()
		if err != nil {
			return p, fmt.Errorf("R_SEC_PARAM bit 0x10: %w", err)
		}
		p.MinPrice, p.HasMin = v, true
	}
	if p.Mask&0x20 != 0 {
		v, err := r.Int64()
		if err != nil {
			return p, fmt.Errorf("R_SEC_PARAM bit 0x20: %w", err)
		}
		p.MaxPrice, p.HasMax = v, true
	}
	return p, nil
}

// SecInfoUpdate builds the typed event from the record and the catalog security:
// the DLL chooses the FUT form (buy_deposit/sell_deposit/minprice/maxprice) or the
// OPT form (bgo_c/bgo_nc/bgo_buy) by the security kind.
func (p SecParam) SecInfoUpdate(sec SecNew) model.SecInfoUpdate {
	u := model.SecInfoUpdate{
		Symbol: model.Symbol{SecCode: sec.SecCode},
		SecID:  p.SecID,
		Market: int(sec.Market),
		Kind:   sec.Kind,
	}
	// The DLL's type switch throws "Wrong sectype" for a kind other than 4/5.
	// The catalog always knows the kind on the wire; for a missing/unknown kind
	// the bit 0x4 (bgo_buy) presence selects the OPT form as a documented
	// fallback (I, unit-tested).
	opt := sec.Kind == 5 || (sec.Kind != 4 && p.Mask&0x4 != 0)
	if opt {
		if p.Mask&0x1 != 0 {
			v := moneyDecimal(p.BgoC)
			u.BgoC = &v
		}
		if p.Mask&0x2 != 0 {
			v := moneyDecimal(p.BgoNC)
			u.BgoNC = &v
		}
		if p.Mask&0x4 != 0 {
			v := moneyDecimal(p.BgoBuy)
			u.BgoBuy = &v
		}
	} else {
		if p.Mask&0x1 != 0 {
			v := moneyDecimal(p.BuyDeposit)
			u.BuyDeposit = &v
		}
		if p.Mask&0x2 != 0 {
			v := moneyDecimal(p.SellDeposit)
			u.SellDeposit = &v
		}
		decimals := defaultPitDecimals(sec)
		if p.HasMin {
			v := model.Decimal{Units: p.MinPrice, Scale: decimals}
			u.MinPrice = &v
		}
		if p.HasMax {
			v := model.Decimal{Units: p.MaxPrice, Scale: decimals}
			u.MaxPrice = &v
		}
	}
	if p.HasPointCost {
		v := pointCostDecimal(p.PointCost)
		u.PointCost = &v
	}
	return u
}

// defaultPitDecimals is the price scale of the default mode of a security.
func defaultPitDecimals(sec SecNew) uint8 {
	if pit, ok := sec.DefaultPit(); ok {
		return pit.Decimals
	}
	if len(sec.Pits) > 0 {
		return sec.Pits[0].Decimals
	}
	return 0
}

// moneyDecimal is the DLL money format: the wire value is hundredths.
func moneyDecimal(units int64) model.Decimal {
	return model.Decimal{Units: units, Scale: 2}
}

// pointCostDecimal renders the point_cost double as the DLL prints it: %g with
// six significant digits (for example 161.947), converted to an exact Decimal.
func pointCostDecimal(v float64) model.Decimal {
	s := fmt.Sprintf("%.6g", v)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	// %.6g uses the exponential form for |v| >= 1e6 and below 1e-4.
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa := s[:i]
		exp, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return model.Decimal{}
		}
		frac := 0
		if d := strings.IndexByte(mantissa, '.'); d >= 0 {
			frac = len(mantissa) - d - 1
			mantissa = mantissa[:d] + mantissa[d+1:]
		}
		units, err := strconv.ParseInt(mantissa, 10, 64)
		if err != nil {
			return model.Decimal{}
		}
		scale := frac - exp
		for scale < 0 {
			units *= 10
			scale++
		}
		if neg {
			units = -units
		}
		return model.Decimal{Units: units, Scale: uint8(scale)}
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		units, err := strconv.ParseInt(s[:i]+s[i+1:], 10, 64)
		if err == nil {
			if neg {
				units = -units
			}
			return model.Decimal{Units: units, Scale: uint8(len(s) - i - 1)}
		}
	}
	units, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return model.Decimal{}
	}
	if neg {
		units = -units
	}
	return model.Decimal{Units: units}
}
