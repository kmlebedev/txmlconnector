package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TagSecMktPermit is R_SEC_MKT_PERMIT (0x0e): W secid, B val. The same record
// mutates the security flags (Catalog.ApplyMutations) and drives the
// <marketord> callback and the <marketable> permission (margin.md, D/O).
const TagSecMktPermit byte = 0x0e

// SecMktPermit is one R_SEC_MKT_PERMIT body (margin.md, D/O).
type SecMktPermit struct {
	SecID  uint16
	Permit bool
}

// ParseSecMktPermit decodes R_SEC_MKT_PERMIT: W secid, B val (val != 0 = yes).
func ParseSecMktPermit(body []byte) (SecMktPermit, error) {
	var out SecMktPermit
	r := wire.NewReader(body)
	secID, err := r.Uint16()
	if err != nil {
		return out, fmt.Errorf("R_SEC_MKT_PERMIT secid: %w", err)
	}
	val, err := r.Byte()
	if err != nil {
		return out, fmt.Errorf("R_SEC_MKT_PERMIT permit: %w", err)
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("R_SEC_MKT_PERMIT: %d trailing bytes", r.Len())
	}
	out.SecID, out.Permit = secID, val != 0
	return out, nil
}

// MarginPosReply is the reachable D branch of RQ_MARGIN_POS (84) (margin.md):
// G, G, W, W, W, G, G, G, G, W, W, W, W, B, then optional G values while the
// flag is set. The request is dead code in DLL 6.47 (O), so this parser exists
// only for completeness of the recovered format (I for the field meanings).
type MarginPosReply struct {
	G1, G2         int64
	W1, W2, W3     uint16
	G3, G4, G5, G6 int64
	W4, W5, W6, W7 uint16
	B1             byte
	Extra          []int64
}

// ParseMarginPosReply decodes the normal-client branch of RQ_MARGIN_POS.
func ParseMarginPosReply(body []byte) (MarginPosReply, error) {
	var out MarginPosReply
	r := wire.NewReader(body)
	readG := func(dst *int64) error {
		v, err := r.Int64()
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}
	readW := func(dst *uint16) error {
		v, err := r.Uint16()
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}
	for _, dst := range []*int64{&out.G1, &out.G2} {
		if err := readG(dst); err != nil {
			return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
		}
	}
	for _, dst := range []*uint16{&out.W1, &out.W2, &out.W3} {
		if err := readW(dst); err != nil {
			return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
		}
	}
	for _, dst := range []*int64{&out.G3, &out.G4, &out.G5, &out.G6} {
		if err := readG(dst); err != nil {
			return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
		}
	}
	for _, dst := range []*uint16{&out.W4, &out.W5, &out.W6, &out.W7} {
		if err := readW(dst); err != nil {
			return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
		}
	}
	flag, err := r.Byte()
	if err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS flag: %w", err)
	}
	out.B1 = flag
	if flag != 0 {
		for i := 0; i < 7; i++ {
			v, err := r.Int64()
			if err != nil {
				return out, fmt.Errorf("RQ_MARGIN_POS G[%d]: %w", i, err)
			}
			out.Extra = append(out.Extra, v)
		}
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("RQ_MARGIN_POS: %d trailing bytes (I)", r.Len())
	}
	return out, nil
}

// ParseMarginPosListReply decodes one RQ_MARGIN_POS_LIST (85) element: 7 packed
// G values (margin.md; the request is dead code in DLL 6.47).
func ParseMarginPosListReply(body []byte) ([]int64, error) {
	r := wire.NewReader(body)
	out := make([]int64, 0, 7)
	for i := 0; i < 7; i++ {
		v, err := r.Int64()
		if err != nil {
			return nil, fmt.Errorf("RQ_MARGIN_POS_LIST G[%d]: %w", i, err)
		}
		out = append(out, v)
	}
	if r.Len() > 0 {
		return nil, fmt.Errorf("RQ_MARGIN_POS_LIST: %d trailing bytes (I)", r.Len())
	}
	return out, nil
}

// ParseSecRate decodes R_SEC_RATE (0x0a): W secid then a packed L value, the
// cost of the lot in kopecks (SBER 291.46*1*100=29146; margin.md, O/D). The
// value is the price P of the max-buy/sell formula (task-52).
func ParseSecRate(body []byte) (SecRate, error) {
	var out SecRate
	r := wire.NewReader(body)
	secID, err := r.Uint16()
	if err != nil {
		return out, fmt.Errorf("R_SEC_RATE secid: %w", err)
	}
	value, err := r.Int64()
	if err != nil {
		return out, fmt.Errorf("R_SEC_RATE value: %w", err)
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("R_SEC_RATE: %d trailing bytes", r.Len())
	}
	out.SecID, out.Value = secID, value
	return out, nil
}

// SecRate is one R_SEC_RATE record: the lot price in kopecks.
type SecRate struct {
	SecID uint16
	Value int64
}
