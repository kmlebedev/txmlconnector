package records

import (
	"fmt"
	"math/bits"
	"strconv"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// specField is the wire type of one RQ_SEC_SPEC mask bit.
type specField struct {
	kind byte // 'S' string, 'L' packed 32, 'G' packed wide, 'B' byte
	max  int
}

// specFields is the RQ_SEC_SPEC value table (RE-1 FUN_1803bac90; docs/protocol/
// dictionaries.md). Bits not listed here are not recovered (I) and fail the
// parse instead of desynchronising the stream.
var specFields = map[int]specField{
	0: {'S', 75}, 1: {'S', 9}, 2: {'S', 4}, 3: {'G', 0}, 4: {'L', 0}, 5: {'L', 0},
	6: {'L', 0}, 7: {'L', 0}, 8: {'L', 0}, 9: {'L', 0}, 10: {'L', 0}, 11: {'L', 0},
	12: {'G', 0}, 13: {'L', 0}, 14: {'L', 0}, 15: {'L', 0}, 16: {'L', 0}, 17: {'L', 0},
	18: {'L', 0}, 19: {'L', 0}, 20: {'L', 0}, 21: {'B', 0}, 22: {'B', 0}, 23: {'B', 0},
	24: {'G', 0}, 25: {'G', 0}, 26: {'G', 0}, 27: {'G', 0}, 28: {'B', 0}, 29: {'B', 0},
	30: {'S', 14}, 31: {'S', 21}, 32: {'S', 3}, 33: {'L', 0}, 34: {'B', 0}, 35: {'G', 0},
	36: {'S', 12}, 37: {'S', 20}, 38: {'L', 0}, 39: {'L', 0}, 40: {'S', 160}, 41: {'S', 100},
}

// SecSpec is the decoded RQ_SEC_SPEC (15) record of one security: W secid, G
// mask, values of the set bits in ascending bit order. Only the fields the
// <sec_info> renderer uses are typed; the rest is collected in Values (I).
type SecSpec struct {
	SecID uint16
	Mask  uint64

	SecName       string
	PName         string
	Currency      string // bit 2: the mode currency (RUR on the demo)
	FaceValue     int64  // bit 3
	ClearingPrice int64  // bit 5 (FUT)
	MatDate       int64  // bit 8: Unix days
	CouponValue   int64  // bit 9: hundredths
	CouponPeriod  int64  // bit 10: days
	CouponDate    int64  // bit 11: Unix days
	MinPrice      int64  // bit 19 (FUT)
	MaxPrice      int64  // bit 20 (FUT)
	BuyDeposit    int64  // bit 24: hundredths
	SellDeposit   int64  // bit 25: hundredths
	CurrencyID    string // bit 32: RUR in the spec, RUB in the XML
	ISIN          string // bit 36

	// Values holds the raw value of every recovered bit (diagnostics/tests).
	Values map[int]int64
}

// ParseSecSpec decodes a stream of RQ_SEC_SPEC records (until 0xffff or the end
// of the payload).
func ParseSecSpec(payload []byte) ([]SecSpec, error) {
	var out []SecSpec
	r := wire.NewReader(payload)
	for r.Len() > 0 {
		if r.Len() == 2 && payload[len(payload)-2] == 0xff && payload[len(payload)-1] == 0xff {
			break
		}
		secid, err := r.Uint16()
		if err != nil {
			return out, fmt.Errorf("RQ_SEC_SPEC secid: %w", err)
		}
		if secid == 0xffff {
			break
		}
		mask, err := r.Int64()
		if err != nil {
			return out, fmt.Errorf("RQ_SEC_SPEC mask: %w", err)
		}
		spec := SecSpec{SecID: secid, Mask: uint64(mask), Values: map[int]int64{}}
		for bit := 0; bit < bits.Len64(uint64(mask)); bit++ {
			if spec.Mask&(1<<uint(bit)) == 0 {
				continue
			}
			field, ok := specFields[bit]
			if !ok {
				return out, fmt.Errorf("RQ_SEC_SPEC secid %d: unrecovered mask bit %d", secid, bit)
			}
			switch field.kind {
			case 'S':
				value, err := r.String(field.max)
				if err != nil {
					return out, fmt.Errorf("RQ_SEC_SPEC bit %d: %w", bit, err)
				}
				spec.setString(bit, value)
			case 'L':
				value, err := r.Int32()
				if err != nil {
					return out, fmt.Errorf("RQ_SEC_SPEC bit %d: %w", bit, err)
				}
				spec.setInt(bit, int64(value))
			case 'G':
				value, err := r.Int64()
				if err != nil {
					return out, fmt.Errorf("RQ_SEC_SPEC bit %d: %w", bit, err)
				}
				spec.setInt(bit, value)
			case 'B':
				value, err := r.Byte()
				if err != nil {
					return out, fmt.Errorf("RQ_SEC_SPEC bit %d: %w", bit, err)
				}
				spec.setInt(bit, int64(value))
			}
		}
		out = append(out, spec)
	}
	return out, nil
}

func (s *SecSpec) setString(bit int, value string) {
	value = DecodeCP1251(value)
	switch bit {
	case 0:
		s.SecName = value
	case 1:
		s.PName = value
	case 2:
		s.Currency = value
	case 32:
		s.CurrencyID = value
	case 36:
		s.ISIN = value
	}
}

func (s *SecSpec) setInt(bit int, value int64) {
	s.Values[bit] = value
	switch bit {
	case 3:
		s.FaceValue = value
	case 5:
		s.ClearingPrice = value
	case 8:
		s.MatDate = value
	case 9:
		s.CouponValue = value
	case 10:
		s.CouponPeriod = value
	case 11:
		s.CouponDate = value
	case 19:
		s.MinPrice = value
	case 20:
		s.MaxPrice = value
	case 24:
		s.BuyDeposit = value
	case 25:
		s.SellDeposit = value
	}
}

// Info builds the typed <sec_info> from the spec and the catalog security: the
// price scale, point cost and lot volume come from the default pit; the fields
// the spec does not carry (accruedint, put_call, opt_type, ...) stay nil until
// their mask bits are recovered (I).
func (s SecSpec) Info(sec SecNew) model.SecInfo {
	info := model.SecInfo{
		Symbol:  model.Symbol{SecCode: sec.SecCode},
		SecID:   s.SecID,
		SecName: s.SecName,
		SecCode: sec.SecCode,
		Market:  int(sec.Market),
		PName:   s.PName,
		ISIN:    s.ISIN,
	}
	decimals := defaultPitDecimals(sec)
	pit, hasPit := sec.DefaultPit()
	if hasPit {
		if pc := pointCostPtr(pit.PointCost); pc != nil {
			info.PointCost = pc
		}
		if sec.Kind == 4 || sec.Kind == 5 {
			if pit.LotSize > 0 {
				lot := int64(pit.LotSize)
				info.LotVolume = &lot
			}
		}
	}
	if s.Mask&(1<<3) != 0 {
		v := model.Decimal{Units: s.FaceValue, Scale: decimals}
		info.FaceValue = &v
	}
	if s.Mask&(1<<5) != 0 && (sec.Kind == 4 || sec.Kind == 5) {
		v := model.Decimal{Units: s.ClearingPrice, Scale: decimals}
		info.ClearingPrice = &v
	}
	if s.Mask&(1<<19) != 0 {
		v := model.Decimal{Units: s.MinPrice, Scale: decimals}
		info.MinPrice = &v
	}
	if s.Mask&(1<<20) != 0 {
		v := model.Decimal{Units: s.MaxPrice, Scale: decimals}
		info.MaxPrice = &v
	}
	if s.Mask&(1<<24) != 0 {
		v := model.Decimal{Units: s.BuyDeposit, Scale: 2}
		info.BuyDeposit = &v
	}
	if s.Mask&(1<<25) != 0 {
		v := model.Decimal{Units: s.SellDeposit, Scale: 2}
		info.SellDeposit = &v
	}
	if s.Mask&(1<<8) != 0 {
		info.MatDate = specDate(s.MatDate)
	}
	if s.Mask&(1<<9) != 0 {
		v := model.Decimal{Units: s.CouponValue, Scale: 2}
		info.CouponValue = &v
	}
	if s.Mask&(1<<10) != 0 {
		v := s.CouponPeriod
		info.CouponPeriod = &v
	}
	if s.Mask&(1<<11) != 0 {
		info.CouponDate = specDate(s.CouponDate)
	}
	if s.CurrencyID != "" {
		info.CurrencyID = xmlCurrency(s.CurrencyID)
	}
	return info
}

func specDate(days int64) *time.Time {
	t := time.Unix(days*86400, 0).UTC()
	return &t
}

// xmlCurrency maps the spec currency id to the <currencyid> literal.
func xmlCurrency(id string) string {
	if id == "RUR" {
		return "RUB"
	}
	return id
}

// specDecimalFixed renders a decimal with exactly scale digits (the DLL facevalue
// and money fields keep their trailing zeros, e.g. 1000.0000).
func specDecimalFixed(d model.Decimal, scale int) string {
	units := d.Units
	if int(d.Scale) < scale {
		units *= pow10(scale - int(d.Scale))
	} else if int(d.Scale) > scale {
		units /= pow10(int(d.Scale) - scale)
	}
	neg := units < 0
	if neg {
		units = -units
	}
	whole := strconv.FormatInt(units/pow10(scale), 10)
	frac := strconv.FormatInt(units%pow10(scale), 10)
	for len(frac) < scale {
		frac = "0" + frac
	}
	text := whole
	if scale > 0 {
		text += "." + frac
	}
	if neg {
		return "-" + text
	}
	return text
}

func pow10(n int) int64 {
	v := int64(1)
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
}
