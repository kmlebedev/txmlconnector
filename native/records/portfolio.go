package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// ParseFondPortfolio decodes the preliminary RQ_FOND_PORTFOLIO (86) reply:
// 2 packed-G + 7 W (D, portfolios.md; the field meanings are I).
func ParseFondPortfolio(payload []byte) (model.PortfolioTPlus, error) {
	var out model.PortfolioTPlus
	r := wire.NewReader(payload)
	for i := 0; i < 2; i++ {
		value, err := r.Int64()
		if err != nil {
			return out, fmt.Errorf("RQ_FOND_PORTFOLIO G[%d]: %w", i, err)
		}
		out.RawG[i] = value
	}
	for i := 0; i < 7; i++ {
		value, err := r.Uint16()
		if err != nil {
			return out, fmt.Errorf("RQ_FOND_PORTFOLIO W[%d]: %w", i, err)
		}
		out.RawW[i] = value
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("RQ_FOND_PORTFOLIO: %d trailing bytes (layout is I)", r.Len())
	}
	return out, nil
}

// ParseMarginPosList decodes RQ_MARGIN_POS_LIST (85): 7 packed-G (D, I).
func ParseMarginPosList(payload []byte) ([]int64, error) {
	r := wire.NewReader(payload)
	out := make([]int64, 0, 7)
	for i := 0; i < 7; i++ {
		value, err := r.Int64()
		if err != nil {
			return nil, fmt.Errorf("RQ_MARGIN_POS_LIST G[%d]: %w", i, err)
		}
		out = append(out, value)
	}
	if r.Len() > 0 {
		return nil, fmt.Errorf("RQ_MARGIN_POS_LIST: %d trailing bytes (layout is I)", r.Len())
	}
	return out, nil
}

// ParseMarginPos decodes the preliminary RQ_MARGIN_POS (84) reply: the D field
// sequence (G,G,W,W,W,G,G,G,G,W,W,W,W,B,G,G,G,G + tail) whose meanings are I.
func ParseMarginPos(payload []byte) (model.MarginPos, error) {
	var out model.MarginPos
	r := wire.NewReader(payload)
	var err error
	if out.G1, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	if out.G2, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	words := []*uint16{&out.W1, &out.W2, &out.W3}
	for _, dst := range words {
		if *dst, err = r.Uint16(); err != nil {
			return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
		}
	}
	if out.G3, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	if out.G4, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	if out.G5, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	if out.G6, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	words2 := []*uint16{&out.W4, &out.W5, &out.W6, &out.W7}
	for _, dst := range words2 {
		if *dst, err = r.Uint16(); err != nil {
			return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
		}
	}
	if out.B1, err = r.Byte(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	if out.G7, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	if out.G8, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	if out.G9, err = r.Int64(); err != nil {
		return out, fmt.Errorf("RQ_MARGIN_POS: %w", err)
	}
	if r.Len() > 0 {
		out.RawTail, err = r.Raw(r.Len())
		if err != nil {
			return out, fmt.Errorf("RQ_MARGIN_POS tail: %w", err)
		}
	}
	return out, nil
}
