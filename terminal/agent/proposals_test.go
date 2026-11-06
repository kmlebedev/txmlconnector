package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// proposalOf extracts the proposal result of one tool call.
func proposalOf(t *testing.T, call ToolCall) Proposal {
	t.Helper()
	if call.Error != "" {
		t.Fatalf("tool %s error: %s", call.Name, call.Error)
	}
	res, ok := call.Result.(proposalResult)
	if !ok {
		t.Fatalf("tool %s result type = %T, want proposalResult", call.Name, call.Result)
	}
	return res.Proposal
}

// TestMutatingToolCreatesProposalInConfirmMode is the R5 regression test: in
// the default confirm mode a mutating tool must not reach the broker; it
// becomes a pending proposal that the human approves.
func TestMutatingToolCreatesProposalInConfirmMode(t *testing.T) {
	e, _, broker := newTestEngine(t)

	var events []Event
	cancel := e.Subscribe(func(ev Event) {
		if ev.Type == "proposal" {
			events = append(events, ev)
		}
	})
	defer cancel()

	call, artifact := e.executeToolCall(context.Background(), "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 10, "type": "market",
	})
	proposal := proposalOf(t, call)
	if proposal.ID == "" || proposal.Tool != "place_order" || proposal.Status != ProposalPending {
		t.Errorf("proposal = %+v, want a pending place_order proposal", proposal)
	}
	if !proposal.ExpiresAt.Equal(proposal.CreatedAt.Add(proposalTTL)) {
		t.Errorf("expiry = %v, want created + %v", proposal.ExpiresAt, proposalTTL)
	}
	if artifact != nil {
		t.Errorf("proposal must not create an artifact, got %+v", artifact)
	}
	if placed := broker.placedOrders(); len(placed) != 0 {
		t.Fatalf("broker received %d orders, want 0 before approval", len(placed))
	}
	if len(e.Proposals()) != 1 {
		t.Fatalf("proposals = %+v, want 1", e.Proposals())
	}
	if len(events) != 1 {
		t.Fatalf("proposal SSE events = %d, want 1", len(events))
	}

	// cancel_order and start_autonomy are gated as well. A cancel proposal
	// requires an existing order (a ghost cancel is an error, not a proposal).
	placed, err := broker.Place(PlaceOrder{Symbol: "TQBR:SBER", Side: "buy", Qty: 1, Type: "market"})
	if err != nil {
		t.Fatalf("seed order: %v", err)
	}
	cancelProposal := proposalOf(t, mustCall(t, e, "cancel_order", map[string]any{"orderId": placed.ID}))
	if cancelProposal.Tool != "cancel_order" {
		t.Errorf("cancel proposal = %+v", cancelProposal)
	}
	startProposal := proposalOf(t, mustCall(t, e, "start_autonomy", map[string]any{
		"symbol": "SBER", "strategy": "sma_cross",
	}))
	if startProposal.Tool != "start_autonomy" {
		t.Errorf("autonomy proposal = %+v", startProposal)
	}
	if e.Status().Autonomy.Running {
		t.Error("autonomy must not start before the proposal is approved")
	}

	// The kill-switch is the documented exception: stop_autonomy runs at once.
	stopCall := mustCall(t, e, "stop_autonomy", map[string]any{})
	if stopCall.Error != "" {
		t.Fatalf("stop_autonomy error: %s", stopCall.Error)
	}
	if _, isProposal := stopCall.Result.(proposalResult); isProposal {
		t.Error("stop_autonomy must not be gated: it is the kill-switch")
	}
}

// mustCall executes one tool and returns the ToolCall.
func mustCall(t *testing.T, e *Engine, name string, args map[string]any) ToolCall {
	t.Helper()
	call, _ := e.executeToolCall(context.Background(), name, args)
	return call
}

// TestApproveExecutesOnce checks that an approved proposal executes exactly
// once and a repeated approve is rejected.
func TestApproveExecutesOnce(t *testing.T) {
	e, _, broker := newTestEngine(t)
	call := mustCall(t, e, "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 10, "type": "market",
	})
	proposal := proposalOf(t, call)

	toolCall, err := e.Approve(context.Background(), proposal.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if toolCall.Name != "place_order" || toolCall.Error != "" {
		t.Fatalf("approved tool call = %+v", toolCall)
	}
	if placed := broker.placedOrders(); len(placed) != 1 {
		t.Fatalf("broker placements = %d, want exactly 1", len(placed))
	}
	props := e.Proposals()
	if len(props) != 1 || props[0].Status != ProposalExecuted {
		t.Fatalf("proposals after approve = %+v, want executed", props)
	}

	if _, err := e.Approve(context.Background(), proposal.ID); !errors.Is(err, ErrProposalHandled) {
		t.Fatalf("second Approve error = %v, want ErrProposalHandled", err)
	}
	if placed := broker.placedOrders(); len(placed) != 1 {
		t.Fatalf("broker placements after repeat approve = %d, want 1", len(placed))
	}
	if err := e.Reject(proposal.ID); !errors.Is(err, ErrProposalHandled) {
		t.Fatalf("Reject of an executed proposal = %v, want ErrProposalHandled", err)
	}
	if _, err := e.Approve(context.Background(), "prop_missing"); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("Approve of an unknown id = %v, want ErrProposalNotFound", err)
	}
}

// TestProposalExpires checks the 5-minute approval window measured by cfg.Now.
func TestProposalExpires(t *testing.T) {
	now := testNow
	e := newTestEngineWith(t, Config{
		DefaultSymbol: "TQBR:SBER",
		Now:           func() time.Time { return now },
	}, newFakeDataSource(), newFakeBroker(1_000_000))
	proposal := proposalOf(t, mustCall(t, e, "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 1, "type": "market",
	}))
	if !proposal.ExpiresAt.Equal(now.Add(proposalTTL)) {
		t.Fatalf("expiry = %v, want %v", proposal.ExpiresAt, now.Add(proposalTTL))
	}
	now = now.Add(proposalTTL + time.Minute)
	if _, err := e.Approve(context.Background(), proposal.ID); !errors.Is(err, ErrProposalExpired) {
		t.Fatalf("Approve after the window = %v, want ErrProposalExpired", err)
	}
	props := e.Proposals()
	if len(props) != 1 || props[0].Status != ProposalExpired {
		t.Fatalf("proposals after expiry = %+v, want expired", props)
	}
	if err := e.Reject(proposal.ID); !errors.Is(err, ErrProposalExpired) {
		t.Fatalf("Reject after the window = %v, want ErrProposalExpired", err)
	}
}

// TestEnvelopeModeAutoApprovesInsideLimits checks the explicit envelope mode:
// an order inside the envelope executes at once, outside/other mutating tools
// become proposals, and MaxOrdersPerRun bounds the auto-approvals.
func TestEnvelopeModeAutoApprovesInsideLimits(t *testing.T) {
	data := newFakeDataSource()
	broker := newFakeBroker(1_000_000)
	e := newTestEngineWith(t, Config{
		DefaultSymbol: "TQBR:SBER",
		Approval: ApprovalPolicy{
			Mode:            ApprovalEnvelope,
			Envelope:        RiskLimits{MaxOrderPct: 50, MaxPositionPct: 50},
			MaxOrdersPerRun: 1,
		},
	}, data, broker)

	// First small order is inside the envelope: executed immediately.
	call := mustCall(t, e, "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 10, "type": "market",
	})
	if call.Error != "" {
		t.Fatalf("inside-envelope order error: %s", call.Error)
	}
	if _, isProposal := call.Result.(proposalResult); isProposal {
		t.Fatal("inside-envelope order must execute, not become a proposal")
	}
	if _, ok := call.Result.(OrderView); !ok {
		t.Fatalf("result type = %T, want OrderView", call.Result)
	}
	if placed := broker.placedOrders(); len(placed) != 1 {
		t.Fatalf("broker placements = %d, want 1", len(placed))
	}

	// The per-run auto-approval quota is exhausted: the next one is a proposal.
	second := proposalOf(t, mustCall(t, e, "place_order", map[string]any{
		"symbol": "SBER", "side": "buy", "qty": 1, "type": "market",
	}))
	if second.Status != ProposalPending {
		t.Fatalf("second order proposal = %+v", second)
	}

	// start_autonomy is always a proposal in envelope mode.
	auto := proposalOf(t, mustCall(t, e, "start_autonomy", map[string]any{
		"symbol": "SBER", "strategy": "sma_cross",
	}))
	if auto.Status != ProposalPending {
		t.Fatalf("autonomy in envelope mode must be a proposal: %+v", auto)
	}
}

// TestProposalRejectAndSSE checks the reject transition and the SSE proposal
// stream (pending -> rejected).
func TestProposalRejectAndSSE(t *testing.T) {
	e, _, broker := newTestEngine(t)
	var statuses []string
	cancel := e.Subscribe(func(ev Event) {
		if ev.Type != "proposal" {
			return
		}
		if proposal, ok := ev.Data.(Proposal); ok {
			statuses = append(statuses, proposal.Status)
		}
	})
	defer cancel()
	proposal := proposalOf(t, mustCall(t, e, "start_autonomy", map[string]any{
		"symbol": "SBER", "strategy": "sma_cross",
	}))
	if err := e.Reject(proposal.ID); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	props := e.Proposals()
	if len(props) != 1 || props[0].Status != ProposalRejected {
		t.Fatalf("proposals after reject = %+v", props)
	}
	if len(statuses) != 2 || statuses[0] != ProposalPending || statuses[1] != ProposalRejected {
		t.Fatalf("proposal SSE statuses = %v", statuses)
	}
	if len(broker.placedOrders()) != 0 {
		t.Fatal("a rejected proposal must not execute")
	}
}
