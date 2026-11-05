package agent

import (
	"errors"
	"fmt"
	"strings"
)

// checkOrderRisk normalizes a paper order request and enforces the frozen risk
// limits of §9.4 before it reaches the broker:
//
//   - MaxOrderPct  — max share of cash spent by one order,
//   - MaxPositionPct — max share of the portfolio value in one position,
//   - sells may not exceed the current position.
func (e *Engine) checkOrderRisk(symbol, side string, qty int64, typ string, price float64) (PlaceOrder, error) {
	if e.broker == nil {
		return PlaceOrder{}, errors.New("agent: paper broker is not configured")
	}
	symbol = e.resolveSymbol(symbol)
	if symbol == "" {
		return PlaceOrder{}, errors.New("agent: symbol is required")
	}
	side = strings.ToLower(strings.TrimSpace(side))
	if side != "buy" && side != "sell" {
		return PlaceOrder{}, fmt.Errorf("agent: side must be buy or sell, got %q", side)
	}
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "" {
		typ = "market"
	}
	if typ != "market" && typ != "limit" {
		return PlaceOrder{}, fmt.Errorf("agent: type must be market or limit, got %q", typ)
	}
	if qty <= 0 {
		return PlaceOrder{}, fmt.Errorf("agent: qty must be positive, got %d", qty)
	}

	risk := e.cfg.Risk
	refPrice := price
	if typ == "limit" {
		if refPrice <= 0 {
			return PlaceOrder{}, errors.New("agent: limit order price must be positive")
		}
	} else {
		refPrice = e.referencePrice(symbol, side)
		if refPrice <= 0 {
			return PlaceOrder{}, fmt.Errorf("agent: no market price for %s", symbol)
		}
		// A market order is sent to the paper broker with a zero price: the
		// broker fills at the current quote.
		price = 0
	}
	notional := refPrice * float64(qty)

	if side == "buy" {
		cash := e.broker.Cash()
		if cash <= 0 {
			return PlaceOrder{}, errors.New("risk: no free cash for a buy order")
		}
		fee := notional * e.feeBps() / 10000
		if notional+fee > cash {
			return PlaceOrder{}, fmt.Errorf("risk: order costs %.2f ₽, available cash %.2f ₽", notional+fee, cash)
		}
		if maxOrder := cash * risk.MaxOrderPct / 100; maxOrder > 0 && notional > maxOrder {
			return PlaceOrder{}, fmt.Errorf(
				"risk: order notional %.2f ₽ exceeds max order size %.2f ₽ (maxOrderPct %.0f%% of cash)",
				notional, maxOrder, risk.MaxOrderPct)
		}
		portfolio := e.broker.Portfolio()
		if maxPos := portfolio.TotalValue * risk.MaxPositionPct / 100; maxPos > 0 {
			current := positionValue(portfolio, symbol)
			if current+notional > maxPos {
				return PlaceOrder{}, fmt.Errorf(
					"risk: position value %.2f ₽ exceeds max position %.2f ₽ (maxPositionPct %.0f%%)",
					current+notional, maxPos, risk.MaxPositionPct)
			}
		}
	} else {
		portfolio := e.broker.Portfolio()
		position := positionQty(portfolio, symbol)
		if position < qty {
			return PlaceOrder{}, fmt.Errorf("risk: sell %d exceeds position %d for %s", qty, position, symbol)
		}
	}

	return PlaceOrder{Symbol: symbol, Side: side, Qty: qty, Type: typ, Price: price}, nil
}

// referencePrice returns the price used for risk checks of a market order:
// ask for buys, bid for sells, falling back to last and to the previous close.
func (e *Engine) referencePrice(symbol, side string) float64 {
	q, ok := e.data.Quote(symbol)
	if ok {
		if side == "buy" && q.Ask > 0 {
			return q.Ask
		}
		if side == "sell" && q.Bid > 0 {
			return q.Bid
		}
		if q.Last > 0 {
			return q.Last
		}
	}
	if book, err := e.data.OrderBook(symbol); err == nil && book.Last > 0 {
		return book.Last
	}
	for _, ins := range e.data.Instruments() {
		if strings.EqualFold(ins.Symbol, symbol) && ins.Last > 0 {
			return ins.Last
		}
	}
	return 0
}

// positionValue returns the market value of one position in the portfolio.
func positionValue(p PortfolioView, symbol string) float64 {
	for _, pos := range p.Positions {
		if strings.EqualFold(pos.Symbol, symbol) {
			return pos.Value
		}
	}
	return 0
}

// positionQty returns the quantity held for one symbol in the portfolio.
func positionQty(p PortfolioView, symbol string) int64 {
	for _, pos := range p.Positions {
		if strings.EqualFold(pos.Symbol, symbol) {
			return pos.Qty
		}
	}
	return 0
}
