package xmlcompat

import (
	"math"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// SecurityFlags is the per-security derived state the DLL uses for
// <security>: the opmask byte, the tradability gates and quotestype.
// The model is recovered from FUN_180398640 (record parse and opmask seed),
// FUN_1801dc810/FUN_1801dcb20/FUN_1803035c0/FUN_1801dcf80 (trading checks),
// FUN_1801dfaa0/FUN_1801e0080 (quotestype) and FUN_18011f4a0 (output), and
// matches the DLL on all 499 demo-server instruments of the snapshot fixture.
type SecurityFlags struct {
	OpMask    byte // opmask byte: ioc 0x01, fok 0x02, bymarket 0x04, nosplit 0x08, usecredit 0x10
	Trading   bool // symbols&3==3, not an index, not MCT DCF, futures/options not blocked
	Active    bool // Trading and the security has a default pit
	ByMarket  bool // Trading and both opmask and the record byte carry the market-permit bit
	QuotesTyp byte // 0 - none, 1 - OrderBook, 2 - Level2
}

// SecurityFlags derives the DLL state of a record. DefaultPit and board
// lookups follow the DLL: the pit flagged default, else the first one; the
// quotestype gate reads the Byte2 of that pit's board from RQ_STARTUP.
func DeriveSecurityFlags(st *model.Startup, s records.SecNew) SecurityFlags {
	f := SecurityFlags{}
	if t, ok := st.SecTypeByID(s.Kind); ok && len(t.Raw) > 1 {
		f.OpMask = t.Raw[1]
	}
	if s.Props&2 != 0 {
		f.OpMask |= 1
	}
	if s.Props&4 != 0 {
		f.OpMask |= 2
	}
	pit, hasPit := s.DefaultPit()
	f.Trading = s.Flags&3 == 3 && s.Kind != 7 &&
		!(s.Market == 0x0e && s.Tail2 == 7) &&
		!((s.Kind == 4 || s.Kind == 5) && s.DerivFlags&4 != 0 && s.DerivFlags&8 == 0)
	f.Active = hasPit && f.Trading
	f.ByMarket = f.Trading && f.OpMask&4 != 0 && s.Flags&4 != 0
	// quotestype: the default board must allow quotes (RQ_STARTUP boards
	// Byte2&2), indices and swaps report 0, MCT takes the value from the
	// record tail, market 12 reports Level2, everything else OrderBook.
	if s.Kind != 0x11 && hasPit {
		if b, ok := st.BoardByNum(pit.Board); ok && b.Byte2&2 != 0 {
			switch {
			case s.Market == 0x0e:
				f.QuotesTyp = s.Tail1
			case s.Kind == 7:
				f.QuotesTyp = 0
			case s.Market == 12:
				f.QuotesTyp = 2
			default:
				f.QuotesTyp = 1
			}
		}
	}
	return f
}

var (
	sectypeNames = map[byte]string{0: "SHARE", 1: "BOND", 4: "FUT", 5: "OPT", 7: "IDX", 14: "MCT", 15: "ETS_CURRENCY", 16: "ETS_SWAP"}
	kindClass    = map[byte]byte{0: 'E', 1: 'B', 4: 'F', 5: 'O', 7: 'I', 15: 'C', 16: 'S'}
)

const (
	marketMICEX       = 1
	marketFORTS       = 4
	marketFORTSOption = 5
)

// Security converts an R_SEC_NEW record to the typed security model using the
// board dictionary from RQ_STARTUP. Fields verified against the DLL on all
// 17 176 demo-server instruments: secid, seccode, shortname, instrclass,
// board, market, decimals, minstep, lotsize, lotdivider, point_cost, sectype,
// currency, currencyid, sec_tz. active, opmask, quotestype, MIC and ticker
// are derived in SecurityFlags.
func Security(st *model.Startup, s records.SecNew) model.Security {
	out := model.Security{
		SecID:     int(s.SecID),
		SecCode:   s.SecCode,
		ShortName: s.ShortName,
		Market:    int(s.Market),
		SecType:   sectypeNames[s.Kind],
		Currency:  s.Currency,
	}
	if out.Market == marketFORTSOption {
		out.Market = marketFORTS
	}
	switch {
	case s.Class != 0:
		out.InstrClass = string(rune(s.Class))
	case kindClass[s.Kind] != 0:
		out.InstrClass = string(rune(kindClass[s.Kind]))
	}
	if out.Currency == "" && s.Market == marketMICEX {
		out.Currency = "RUR"
	}
	if s.HasTimeZone {
		out.SecTZ, _ = records.TimeZoneName(s.TimeZone)
	}
	f := DeriveSecurityFlags(st, s)
	out.Active = f.Active
	out.QuotesType = int(f.QuotesTyp)
	out.MIC, out.Ticker = s.MIC, s.Ticker
	if f.Active {
		out.OpMask = model.SecurityOpMask{
			UseCredit:     f.OpMask&0x10 != 0,
			ByMarket:      f.ByMarket,
			NoSplit:       f.OpMask&8 != 0,
			Fok:           f.OpMask&2 != 0,
			Ioc:           f.OpMask&1 != 0,
			ImmOrCancel:   f.OpMask&2 != 0,
			CancelBalance: f.OpMask&1 != 0,
		}
	}
	pit, ok := s.DefaultPit()
	if !ok {
		return out
	}
	out.Board = st.BoardCode(pit.Board)
	out.Decimals = int(pit.Decimals)
	out.MinStep = float64(pit.MinStep) / math.Pow10(int(pit.Decimals))
	out.LotSize = int(pit.LotSize)
	if s.Market == marketFORTS || s.Market == marketFORTSOption {
		out.LotSize = 1
	}
	out.LotDivider = int(pit.LotDivider)
	out.PointCost = pit.PointCost
	// Settlement currency of the default board, else the security currency
	// (already defaulted to RUR on MICEX); the DLL reports RUR as RUB.
	out.CurrencyID = pit.Currency
	if out.CurrencyID == "" {
		out.CurrencyID = out.Currency
	}
	if out.CurrencyID == "RUR" {
		out.CurrencyID = "RUB"
	}
	return out
}

func pow10(n int) float64 {
	v := 1.0
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
}

// FormatMinStep renders minstep as the DLL does: fixed to the price decimals
// with trailing zeros removed, keeping one digit after the point.
func FormatMinStep(v float64, decimals int) string {
	if decimals <= 0 {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	s := strings.TrimRight(strconv.FormatFloat(v, 'f', decimals, 64), "0")
	if strings.HasSuffix(s, ".") {
		s += "0"
	}
	return s
}

// FormatPointCost renders point_cost as the DLL does (C++ stream default,
// six significant digits).
func FormatPointCost(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}
