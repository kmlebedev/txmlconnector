package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// SecPositionFromAsset builds the <sec_position> of a R_CLIENT_ASSET record with
// a paper sym_id (O: position-sec). The register money fields are summed; the
// exact bought/sold/ordbuy sources are I (docs/protocol/positions.md).
func SecPositionFromAsset(asset ClientAsset, client, union string, sec SecNew) model.SecPosition {
	pos := model.SecPosition{
		Client: client, Union: union, SecID: asset.SymID,
		SecCode: sec.SecCode, ShortName: sec.ShortName, Market: int(sec.Market),
	}
	for _, reg := range asset.Regs {
		pos.SaldoIn += reg.OpenBalance
		pos.Bought += reg.Bought
		pos.Sold += reg.Sold
	}
	pos.Saldo = pos.SaldoIn + pos.Bought - pos.Sold
	return pos
}

// ClientHolding is one record of the RQ_CLIENT_HOLDINGS (42) reply: L client_id,
// W secid, G startnet/todaybuy/todaysell/totalnet/openbuys/opensells/varmargin,
// B mask (+ optional blocks, I) and a L -1 terminator (D + O).
type ClientHolding struct {
	ClientID  int64
	SecID     uint16
	StartNet  int64
	TodayBuy  int64
	TodaySell int64
	TotalNet  int64
	OpenBuys  int64
	OpenSells int64
	VarMargin int64
	Mask      byte
}

// ParseClientHoldings decodes the RQ_CLIENT_HOLDINGS reply. The reply starts
// with eight G values (the headers; a newer server reads two more, I).
func ParseClientHoldings(payload []byte) ([]ClientHolding, error) {
	r := wire.NewReader(payload)
	for i := 0; i < 8; i++ {
		if _, err := r.Int64(); err != nil {
			return nil, fmt.Errorf("RQ_CLIENT_HOLDINGS header: %w", err)
		}
	}
	var out []ClientHolding
	for {
		clientID, err := r.Int32()
		if err != nil {
			return out, fmt.Errorf("RQ_CLIENT_HOLDINGS client: %w", err)
		}
		if clientID == -1 {
			return out, nil
		}
		var h ClientHolding
		h.ClientID = int64(clientID)
		if h.SecID, err = r.Uint16(); err != nil {
			return out, fmt.Errorf("RQ_CLIENT_HOLDINGS secid: %w", err)
		}
		fields := []*int64{&h.StartNet, &h.TodayBuy, &h.TodaySell, &h.TotalNet, &h.OpenBuys, &h.OpenSells, &h.VarMargin}
		for _, field := range fields {
			value, err := r.Int64()
			if err != nil {
				return out, fmt.Errorf("RQ_CLIENT_HOLDINGS field: %w", err)
			}
			*field = value
		}
		if h.Mask, err = r.Byte(); err != nil {
			return out, fmt.Errorf("RQ_CLIENT_HOLDINGS mask: %w", err)
		}
		// Mask-selected blocks are not recovered (I); the oracle fixture uses 0.
		if h.Mask != 0 {
			return out, fmt.Errorf("RQ_CLIENT_HOLDINGS: mask %#x fields are not recovered (I)", h.Mask)
		}
		out = append(out, h)
	}
}
