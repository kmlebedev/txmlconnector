package api

// Tests of POST /api/agent/tools/{name}/invoke (plan Task 9): the shared tool
// invocation path of the DSH bridge and the MCP server.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// invokeDataSource is the agent.DataSource fake of the tool-invoke tests.
type invokeDataSource struct{}

const invokeSymbol = "TQBR:SBER"

func (invokeDataSource) Instruments() []agent.InstrumentRef {
	return []agent.InstrumentRef{{Symbol: invokeSymbol, Name: "Сбербанк", Last: 293.22, ChangePct: 0.36}}
}

func (invokeDataSource) Quote(symbol string) (agent.Quote, bool) {
	if symbol != invokeSymbol {
		return agent.Quote{}, false
	}
	return agent.Quote{
		Symbol: symbol, Last: 293.22, Bid: 293.2, Ask: 293.26, ChangePct: 0.36,
		Time: time.Date(2026, 11, 5, 16, 0, 0, 0, time.UTC),
	}, true
}

func (invokeDataSource) OrderBook(symbol string) (agent.OrderBook, error) {
	if symbol != invokeSymbol {
		return agent.OrderBook{}, errors.New("no order book for " + symbol)
	}
	return agent.OrderBook{
		Symbol: symbol, Last: 293.22,
		Bids: []agent.Level{{Price: 293.2, Qty: 10}},
		Asks: []agent.Level{{Price: 293.26, Qty: 20}},
	}, nil
}

func (invokeDataSource) History(context.Context, string, int, int) ([]backtest.Bar, error) {
	return nil, errors.New("no history in the invoke fixture")
}

// invokeBroker is the agent.Broker fake: it holds enough cash for the risk
// checks so a valid place_order reaches the approval gate.
type invokeBroker struct{}

func (invokeBroker) Place(req agent.PlaceOrder) (agent.OrderView, error) {
	return agent.OrderView{ID: "ord_1", Symbol: req.Symbol, Side: req.Side, Qty: req.Qty, Status: "filled"}, nil
}

func (invokeBroker) PlaceAt(req agent.PlaceOrder, fill agent.Quote, _ time.Time, _ string) (agent.OrderView, error) {
	return invokeBroker{}.Place(req)
}

func (invokeBroker) Allocate(string, float64) error { return nil }

func (invokeBroker) Cancel(string) (agent.OrderView, error) {
	return agent.OrderView{}, errors.New("order not found")
}

func (invokeBroker) Orders() []agent.OrderView { return nil }

func (invokeBroker) Portfolio() agent.PortfolioView {
	return agent.PortfolioView{TotalValue: 1_000_000, Cash: 1_000_000}
}

func (invokeBroker) Cash() float64 { return 1_000_000 }

// newInvokeDeps backs the API with a real agent engine over fake market data,
// so the endpoint test exercises the real tool dispatch and the proposal gate.
func newInvokeDeps(t *testing.T) Deps {
	t.Helper()
	deps, _, _, _ := defaultDeps()
	engine, err := agent.New(agent.Config{DefaultSymbol: invokeSymbol}, invokeDataSource{}, invokeBroker{})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	deps.Agent = engine
	return deps
}

// invokeResponse is the endpoint reply shape.
type invokeResponse struct {
	Result   map[string]any  `json:"result"`
	Error    string          `json:"error"`
	Artifact *agent.Artifact `json:"artifact"`
	Proposal *agent.Proposal `json:"proposal"`
}

// TestInvokeToolEndpoint checks the shared invoke path: a read-only tool
// returns its result, an unknown name is a JSON 404, a mutating tool in the
// default confirm mode returns a pending proposal instead of executing, and a
// tool-level error is reported in the error field (not as an HTTP error).
func TestInvokeToolEndpoint(t *testing.T) {
	_, ts := newTestServer(t, newInvokeDeps(t), Config{})

	t.Run("read-only tool", func(t *testing.T) {
		status, data := do(t, ts, http.MethodPost, "/api/agent/tools/get_quote/invoke", map[string]any{
			"args": map[string]any{"symbol": invokeSymbol},
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, data)
		}
		var resp invokeResponse
		decode(t, data, &resp)
		if resp.Error != "" {
			t.Fatalf("error = %q, want a result", resp.Error)
		}
		if resp.Result == nil || resp.Result["symbol"] != invokeSymbol {
			t.Fatalf("result = %+v, want symbol %s", resp.Result, invokeSymbol)
		}
		if resp.Proposal != nil {
			t.Fatalf("proposal = %+v, want nil for a read-only tool", resp.Proposal)
		}
	})

	t.Run("unknown tool is 404", func(t *testing.T) {
		status, data := do(t, ts, http.MethodPost, "/api/agent/tools/no_such_tool/invoke", map[string]any{
			"args": map[string]any{},
		})
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, body %s", status, data)
		}
		if code := errorCode(t, data); code != codeNotFound {
			t.Fatalf("error code = %q, want %q", code, codeNotFound)
		}
	})

	t.Run("mutating tool proposes", func(t *testing.T) {
		status, data := do(t, ts, http.MethodPost, "/api/agent/tools/place_order/invoke", map[string]any{
			"args": map[string]any{"symbol": invokeSymbol, "side": "buy", "qty": 1, "type": "market"},
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, data)
		}
		var resp invokeResponse
		decode(t, data, &resp)
		if resp.Error != "" {
			t.Fatalf("error = %q, want a proposal", resp.Error)
		}
		if resp.Result != nil {
			t.Fatalf("result = %+v, want nil: the order must not execute", resp.Result)
		}
		if resp.Proposal == nil {
			t.Fatal("proposal = nil, want a pending proposal")
		}
		if resp.Proposal.Status != agent.ProposalPending {
			t.Fatalf("proposal status = %q, want %q", resp.Proposal.Status, agent.ProposalPending)
		}
		if resp.Proposal.Tool != "place_order" {
			t.Fatalf("proposal tool = %q, want place_order", resp.Proposal.Tool)
		}
	})

	t.Run("tool error is a field", func(t *testing.T) {
		status, data := do(t, ts, http.MethodPost, "/api/agent/tools/get_quote/invoke", map[string]any{
			"args": map[string]any{"symbol": "TQBR:NOPE"},
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, data)
		}
		var resp invokeResponse
		decode(t, data, &resp)
		if resp.Error == "" {
			t.Fatalf("error is empty, want the tool failure: %s", data)
		}
		if resp.Result != nil {
			t.Fatalf("result = %+v, want nil on a tool error", resp.Result)
		}
	})
}
