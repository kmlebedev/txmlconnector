package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// ParseClientLimits decodes the RQ_CLIENT_LIMITS (43) reply (limits.md, D):
// L client_id, then the G×4 block when flags&2, then (flags&4) W coverage,
// L liquidity_c and G×15 money fields; the stream ends with L client_id == -1.
// The session flags and the newer-server tail are I until the oracle/S capture.
func ParseClientLimits(payload []byte, flags byte, newerServer bool) (model.ClientLimits, error) {
	r := wire.NewReader(payload)
	var out model.ClientLimits
	for {
		clientID, err := r.Int32()
		if err != nil {
			return out, fmt.Errorf("RQ_CLIENT_LIMITS client: %w", err)
		}
		if clientID == -1 {
			return out, nil
		}
		out.ClientID = uint64(uint32(clientID))
		if flags&2 != 0 {
			values := make([]*model.Decimal, 0, 4)
			fields := []struct {
				dst **model.Decimal
			}{}
			_ = fields
			var v [4]int64
			for i := range v {
				vv, err := r.Int64()
				if err != nil {
					return out, fmt.Errorf("RQ_CLIENT_LIMITS cbpl: %w", err)
				}
				v[i] = vv
			}
			dec := func(n int64) model.Decimal { return model.Decimal{Units: n, Scale: 2} }
			out.CBPLimit, out.CBPLUsed, out.CBPLPlanned, out.FobVarMargin = dec(v[0]), dec(v[1]), dec(v[2]), dec(v[3])
			_ = values
		}
		if flags&4 != 0 {
			coverage, err := r.Uint16()
			if err != nil {
				return out, fmt.Errorf("RQ_CLIENT_LIMITS coverage: %w", err)
			}
			out.Coverage = coverage
			liquidity, err := r.Int32()
			if err != nil {
				return out, fmt.Errorf("RQ_CLIENT_LIMITS liquidity: %w", err)
			}
			out.LiquidityC = model.Decimal{Units: int64(liquidity), Scale: 5}
			moneyFields := []*model.Decimal{
				&out.Profit, &out.MoneyCurrent, &out.MoneyReserve, &out.MoneyFree,
				&out.OptionsPremium, &out.ExchangeFee, &out.FortsVarMargin, &out.VarMargin,
				&out.PCLMargin, &out.OptionsVM, &out.SpotBuyLimit, &out.UsedSpotBuyLimit,
				&out.CollatCurrent, &out.CollatBlocked, &out.CollatFree,
			}
			for _, dst := range moneyFields {
				value, err := r.Int64()
				if err != nil {
					return out, fmt.Errorf("RQ_CLIENT_LIMITS money: %w", err)
				}
				*dst = model.Decimal{Units: value, Scale: 2}
			}
			if newerServer {
				for i := 0; i < 2; i++ {
					if _, err := r.Int64(); err != nil {
						return out, fmt.Errorf("RQ_CLIENT_LIMITS tail: %w", err)
					}
				}
			}
			return out, nil
		}
	}
}
