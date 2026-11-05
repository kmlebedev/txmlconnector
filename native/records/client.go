package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Record tags of the end of the load (task 7).
const (
	TagClientNew   byte = 0x02 // R_CLN_NEW, sized
	TagOvernight   byte = 0x18 // R_OVERNIGHT, one byte
	TagClientAsset byte = 0x3b // R_CLIENT_ASSET, sized
	TagUnion       byte = 0x68 // R_UNION
)

// ClientNew is one R_CLN_NEW record (tag 0x02, FUN_1803cbc40).
//
// Field order from RE-1 (docs/protocol/connect.md): G/L id, Z12 client code,
// Z32 login, B flag, B type, B market, Z7 union, B, B currency id, W, B
// ClPortSub, L, then optional fields while the record has bytes left.
type ClientNew struct {
	ID         int64
	Code       string
	Login      string
	Flag       byte
	Type       byte // 0 spot, 1 risk, 2 leverage, 3 margin_level, 4 mct
	Market     byte
	Union      string
	Byte1      byte
	CurrencyID byte
	Word       uint16
	ClPortSub  byte
	Int1       int32
	Extra      []byte
}

// ParseClientNew decodes an R_CLN_NEW body.
func ParseClientNew(body []byte) (ClientNew, error) {
	r := wire.NewReader(body)
	var c ClientNew
	var err error
	if c.ID, err = r.Int64(); err != nil {
		return c, fmt.Errorf("R_CLN_NEW id: %w", err)
	}
	if c.Code, err = r.String(12); err != nil {
		return c, fmt.Errorf("R_CLN_NEW code: %w", err)
	}
	if c.Login, err = r.String(32); err != nil {
		return c, fmt.Errorf("R_CLN_NEW login: %w", err)
	}
	fields := []*byte{&c.Flag, &c.Type, &c.Market}
	for _, field := range fields {
		if *field, err = r.Byte(); err != nil {
			return c, fmt.Errorf("R_CLN_NEW byte: %w", err)
		}
	}
	if c.Union, err = r.String(7); err != nil {
		return c, fmt.Errorf("R_CLN_NEW union: %w", err)
	}
	if c.Byte1, err = r.Byte(); err != nil {
		return c, fmt.Errorf("R_CLN_NEW byte1: %w", err)
	}
	if c.CurrencyID, err = r.Byte(); err != nil {
		return c, fmt.Errorf("R_CLN_NEW currency: %w", err)
	}
	if c.Word, err = r.Uint16(); err != nil {
		return c, fmt.Errorf("R_CLN_NEW word: %w", err)
	}
	if c.ClPortSub, err = r.Byte(); err != nil {
		return c, fmt.Errorf("R_CLN_NEW port sub: %w", err)
	}
	if c.Int1, err = r.Int32(); err != nil {
		return c, fmt.Errorf("R_CLN_NEW int: %w", err)
	}
	if r.Len() > 0 {
		c.Extra, _ = r.Raw(r.Len())
	}
	return c, nil
}

// ClientTypeName maps the R_CLN_NEW client type enum to the <type> literal.
func ClientTypeName(t byte) (string, bool) {
	switch t {
	case 0:
		return "spot", true
	case 1:
		return "risk", true
	case 2:
		return "leverage", true
	case 3:
		return "margin_level", true
	case 4:
		return "mct", true
	}
	return "", false
}

// CurrencyName maps the R_CLN_NEW currency id to the <currency> name. Only the
// demo value (RUB) is confirmed; the DLL throws on ids >= 9.
func CurrencyName(id byte) string {
	if id == 1 {
		return "RUB"
	}
	return ""
}

// ClientAssetReg is one register block of R_CLIENT_ASSET.
type ClientAssetReg struct {
	Reg         byte
	OpenBalance int64 // mask 0x01 -> <saldoin>
	Balance     int64 // mask 0x02, ignored by the modern server
	Bought      int64 // mask 0x04
	Sold        int64 // mask 0x08
	Blocked     int64 // mask 0x10
	Estimated   int64 // mask 0x20
	Commission  int64 // mask2 0x01
	WaposPrice  string
	Currency    string // mask2 0x04, e.g. RUR
}

// ClientAsset is one R_CLIENT_ASSET record (tag 0x3b, FUN_1803cde40).
type ClientAsset struct {
	ClientID int64
	SymID    uint16 // 0xffff is the money asset on the demo
	Regs     []ClientAssetReg
}

// ParseClientAsset decodes an R_CLIENT_ASSET body.
func ParseClientAsset(body []byte) (ClientAsset, error) {
	r := wire.NewReader(body)
	var a ClientAsset
	var err error
	if a.ClientID, err = r.Int64(); err != nil {
		return a, fmt.Errorf("R_CLIENT_ASSET client: %w", err)
	}
	if a.SymID, err = r.Uint16(); err != nil {
		return a, fmt.Errorf("R_CLIENT_ASSET sym: %w", err)
	}
	var mask2 byte
	for {
		reg, err := r.Byte()
		if err != nil {
			return a, fmt.Errorf("R_CLIENT_ASSET reg: %w", err)
		}
		if reg == 0xff {
			break
		}
		mask, err := r.Byte()
		if err != nil {
			return a, fmt.Errorf("R_CLIENT_ASSET mask: %w", err)
		}
		item := ClientAssetReg{Reg: reg}
		if mask&0x01 != 0 {
			if item.OpenBalance, err = r.Int64(); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET open balance: %w", err)
			}
		}
		if mask&0x02 != 0 {
			if item.Balance, err = r.Int64(); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET balance: %w", err)
			}
		}
		if mask&0x04 != 0 {
			if item.Bought, err = r.Int64(); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET bought: %w", err)
			}
		}
		if mask&0x08 != 0 {
			if item.Sold, err = r.Int64(); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET sold: %w", err)
			}
		}
		if mask&0x10 != 0 {
			if item.Blocked, err = r.Int64(); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET blocked: %w", err)
			}
		}
		if mask&0x20 != 0 {
			if item.Estimated, err = r.Int64(); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET estimated: %w", err)
			}
		}
		a.Regs = append(a.Regs, item)
	}
	if mask2, err = r.Byte(); err != nil {
		return a, fmt.Errorf("R_CLIENT_ASSET mask2: %w", err)
	}
	_ = mask2
	if len(a.Regs) > 0 {
		last := &a.Regs[len(a.Regs)-1]
		if mask2&0x01 != 0 {
			if last.Commission, err = r.Int64(); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET commission: %w", err)
			}
		}
		if mask2&0x02 != 0 {
			if last.WaposPrice, err = r.String(15); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET wapos: %w", err)
			}
		}
		if mask2&0x04 != 0 {
			if last.Currency, err = r.String(4); err != nil {
				return a, fmt.Errorf("R_CLIENT_ASSET currency: %w", err)
			}
		}
	}
	return a, nil
}

// ParseOvernight decodes R_OVERNIGHT (tag 0x18): one flag byte.
func ParseOvernight(body []byte) (bool, error) {
	if len(body) < 1 {
		return false, fmt.Errorf("R_OVERNIGHT: %w", wire.ErrShortBuffer)
	}
	return body[0] != 0, nil
}

// ParseUnion decodes R_UNION (tag 0x68): L id, Z20 name, Z32 category, B unicat.
func ParseUnion(body []byte) (name string, err error) {
	name, _, err = ParseUnionCategory(body)
	return name, err
}

// ParseUnionCategory also returns unicat, the margin template category the
// DLL requests with RQ_INQUIRE_UT (S: 0x12 on production).
func ParseUnionCategory(body []byte) (name string, unicat byte, err error) {
	r := wire.NewReader(body)
	if _, err = r.Int64(); err != nil {
		return "", 0, fmt.Errorf("R_UNION id: %w", err)
	}
	if name, err = r.String(20); err != nil {
		return "", 0, fmt.Errorf("R_UNION name: %w", err)
	}
	if _, err = r.String(32); err != nil {
		return "", 0, fmt.Errorf("R_UNION category: %w", err)
	}
	if unicat, err = r.Byte(); err != nil {
		return "", 0, fmt.Errorf("R_UNION unicat: %w", err)
	}
	return name, unicat, nil
}
