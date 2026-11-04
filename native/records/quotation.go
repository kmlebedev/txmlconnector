package records

import (
	"fmt"
	"strconv"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TagQuotation is R_QUOTATION (0x06), the <quotations> feed of task 9.
const TagQuotation byte = 0x06

// Quotation is one decoded R_QUOTATION record: W secid, W board, G mask and one
// packed int value per set mask bit, in ascending bit order (RE-1
// FUN_1803bc2f0, validated against the 16 DLL callbacks of the E07/r9 capture).
type Quotation struct {
	SecID uint16
	Board uint16
	Mask  uint64
	// Values holds the value of every set mask bit, keyed by the bit number.
	Values map[int]int64
}

// QuotationMaskBits maps the mask bits the demo server uses to <quotation>
// field names (docs/protocol/alltrades.md, validated 99 fields / 0 mismatches).
// Bits 17, 39, 40, 41 are read by the DLL but not printed (I).
var quotationPriceBits = map[int]string{
	6: "bid", 7: "offer", 14: "last", 15: "change", 16: "priceminusprevwaprice",
	20: "waprice", 26: "highbid", 27: "lowoffer", 28: "high", 29: "low", 30: "open",
	31: "closeprice", 42: "lcurrentprice",
}

var quotationIntBits = map[int]string{
	0: "biddeptht", 1: "numbids", 2: "offerdeptht", 3: "numoffers",
	4: "biddepth", 5: "offerdepth", 8: "numtrades", 9: "voltoday",
	10: "openpositions", 12: "quantity",
}

var quotationOtherBits = map[int]string{
	13: "time", 18: "valtoday", 33: "status", 34: "tradingstatus",
	54: "buydeposit", 55: "selldeposit",
}

// ParseQuotation decodes the body of one R_QUOTATION record.
func ParseQuotation(body []byte) (Quotation, error) {
	var q Quotation
	r := wire.NewReader(body)
	secid, err := r.Uint16()
	if err != nil {
		return q, fmt.Errorf("R_QUOTATION secid: %w", err)
	}
	board, err := r.Uint16()
	if err != nil {
		return q, fmt.Errorf("R_QUOTATION board: %w", err)
	}
	mask, err := r.Int64()
	if err != nil {
		return q, fmt.Errorf("R_QUOTATION mask: %w", err)
	}
	q = Quotation{SecID: secid, Board: board, Mask: uint64(mask), Values: map[int]int64{}}
	for bit := 0; bit < 64; bit++ {
		if q.Mask&(1<<uint(bit)) == 0 {
			continue
		}
		value, err := r.Int32()
		if err != nil {
			return q, fmt.Errorf("R_QUOTATION bit %d value: %w", bit, err)
		}
		q.Values[bit] = int64(value)
	}
	return q, nil
}

// Event builds the typed <quotation> event, resolving the security, the board
// code and the price decimals from the catalog. The time is formatted as UTC
// (S: the raw Unix second is printed as-is; the general rule is I).
func (q Quotation) Event(cat *Catalog) model.Quotation {
	ev := model.Quotation{SecID: q.SecID}
	if cat == nil {
		return ev
	}
	sec, ok := cat.Security(q.SecID)
	if !ok {
		// Unknown security: the price scale is unknown, so the price bits are
		// skipped (the integer/status fields still go out).
		for bit, raw := range q.Values {
			q.apply(&ev.Fields, bit, raw, 0, false)
		}
		return ev
	}
	ev.Symbol = model.Symbol{SecCode: sec.SecCode}
	if board, ok := cat.Board(q.Board); ok {
		ev.Symbol.Board = board.Code
	}
	ev.Board = ev.Symbol.Board
	decimals, haveDecimals := cat.Decimals(q.SecID, q.Board)
	if !haveDecimals {
		if pit, ok := quotationPit(sec, q.Board); ok {
			decimals, haveDecimals = pit.Decimals, true
		}
	}
	if pit, ok := quotationPit(sec, q.Board); ok {
		ev.Fields.PointCost = pointCostPtr(pit.PointCost)
	}
	for bit, raw := range q.Values {
		q.apply(&ev.Fields, bit, raw, decimals, haveDecimals)
	}
	return ev
}

// apply fills the typed field of one mask bit.
func (q Quotation) apply(fields *model.QuotationFields, bit int, raw int64, decimals uint8, haveDecimals bool) {
	switch {
	case quotationIntBits[bit] != "":
		v := raw
		switch quotationIntBits[bit] {
		case "biddeptht":
			fields.BidDepthT = &v
		case "numbids":
			fields.NumBids = &v
		case "offerdeptht":
			fields.OfferDepthT = &v
		case "numoffers":
			fields.NumOffers = &v
		case "biddepth":
			fields.BidDepth = &v
		case "offerdepth":
			fields.OfferDepth = &v
		case "numtrades":
			fields.NumTrades = &v
		case "voltoday":
			fields.VolToday = &v
		case "openpositions":
			fields.OpenPositions = &v
		case "quantity":
			fields.Quantity = &v
		}
	case quotationPriceBits[bit] != "":
		if !haveDecimals {
			return
		}
		v := model.Decimal{Units: raw, Scale: decimals}
		switch quotationPriceBits[bit] {
		case "bid":
			fields.Bid = &v
		case "offer":
			fields.Offer = &v
		case "last":
			fields.Last = &v
		case "change":
			fields.Change = &v
		case "priceminusprevwaprice":
			fields.PriceMinusPrevWaPrice = &v
		case "waprice":
			fields.WaPrice = &v
		case "highbid":
			fields.HighBid = &v
		case "lowoffer":
			fields.LowOffer = &v
		case "high":
			fields.High = &v
		case "low":
			fields.Low = &v
		case "open":
			fields.Open = &v
		case "closeprice":
			fields.ClosePrice = &v
		case "lcurrentprice":
			fields.LCurrentPrice = &v
		}
	case quotationOtherBits[bit] != "":
		switch quotationOtherBits[bit] {
		case "time":
			fields.Time = quotationTimePtr(raw)
		case "valtoday":
			v := model.Decimal{Units: raw, Scale: 3}
			fields.ValToday = &v
		case "status":
			v := string(rune(byte(raw)))
			fields.Status = &v
		case "tradingstatus":
			v := string(rune(byte(raw)))
			fields.TradingStatus = &v
		case "buydeposit":
			v := model.Decimal{Units: raw, Scale: 2}
			fields.BuyDeposit = &v
		case "selldeposit":
			v := model.Decimal{Units: raw, Scale: 2}
			fields.SellDeposit = &v
		}
	}
}

// quotationTimePtr formats the DLL <time>: below a day it is the seconds of the
// trading day (HH:MM:SS), otherwise the server wall clock of the Unix second.
func quotationTimePtr(raw int64) *string {
	var s string
	if raw >= 0 && raw < 86400 {
		s = formatSecondsOfDay(raw)
	} else {
		// The DLL prints the wire Unix second as UTC without a zone shift (S:
		// the HYDR/MCT callback shows 17:04:32 for the raw second).
		s = time.Unix(raw, 0).UTC().Format("02.01.2006 15:04:05")
	}
	return &s
}

func formatSecondsOfDay(sec int64) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	return two(h) + ":" + two(m) + ":" + two(s)
}

func two(v int64) string {
	if v < 10 {
		return "0" + strconv.FormatInt(v, 10)
	}
	return strconv.FormatInt(v, 10)
}

// pointCostPtr is the catalog point cost as an exact Decimal (%g, six
// significant digits, as the DLL prints it).
func pointCostPtr(v float64) *model.Decimal {
	if v == 0 {
		return nil
	}
	d := pointCostDecimal(v)
	return &d
}

// quotationPit returns the R_SEC_NEW pit of the board the quotation belongs to.
func quotationPit(sec SecNew, board uint16) (Pit, bool) {
	for _, pit := range sec.Pits {
		if pit.Board == board {
			return pit, true
		}
	}
	return Pit{}, false
}
