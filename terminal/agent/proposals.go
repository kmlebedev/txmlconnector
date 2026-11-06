package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Proposal lifecycle statuses (§9.5).
const (
	ProposalPending  = "pending"
	ProposalApproved = "approved"
	ProposalRejected = "rejected"
	ProposalExpired  = "expired"
	ProposalExecuted = "executed"
)

// Approval modes of the mutating tools.
const (
	// ApprovalConfirm requires an explicit human approve/reject for every
	// mutating tool call; it is the default mode (R5).
	ApprovalConfirm = "confirm"
	// ApprovalEnvelope auto-executes place_order inside ApprovalPolicy.Envelope
	// limits (up to MaxOrdersPerRun) and turns everything else into a proposal.
	// It must be enabled explicitly; it is the phase-F autonomy mode.
	ApprovalEnvelope = "envelope"
)

const (
	// proposalTTL is the approval window of a pending proposal.
	proposalTTL = 5 * time.Minute
	// maxProposals bounds the proposal ring kept by the engine.
	maxProposals = 100
)

// Proposal is one mutating tool call that waits for a human confirmation.
// Status is pending|approved|rejected|expired|executed.
type Proposal struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Summary   string         `json:"summary"`
	CreatedAt time.Time      `json:"createdAt"`
	ExpiresAt time.Time      `json:"expiresAt"`
	Status    string         `json:"status"`
}

// ApprovalPolicy controls when a mutating tool may execute without a human
// confirmation. Mode is confirm (default) or envelope; the envelope mode is for
// the phase-F autonomy and must be enabled explicitly. MaxOrdersPerRun limits
// the number of envelope auto-approvals of one engine session (0 = unlimited).
type ApprovalPolicy struct {
	Mode            string     `json:"mode"`
	Envelope        RiskLimits `json:"envelope"`
	MaxOrdersPerRun int        `json:"maxOrdersPerRun"`
}

// Errors of the proposal lifecycle, matched with errors.Is.
var (
	// ErrProposalNotFound is returned for an unknown proposal id.
	ErrProposalNotFound = errors.New("agent: proposal not found")
	// ErrProposalHandled is returned when a proposal was already approved,
	// rejected or executed.
	ErrProposalHandled = errors.New("agent: proposal already handled")
	// ErrProposalExpired is returned when the approval window has passed.
	ErrProposalExpired = errors.New("agent: proposal expired")
)

// proposalResult is the tool result of a call turned into a proposal; the
// model/UI sees {"proposal":{...}} instead of an executed result.
type proposalResult struct {
	Proposal Proposal `json:"proposal"`
}

// Proposals returns the proposal ring (newest last) with lazy expiry applied.
func (e *Engine) Proposals() []Proposal {
	e.proposalMu.Lock()
	defer e.proposalMu.Unlock()
	now := e.now()
	out := make([]Proposal, 0, len(e.proposalOrder))
	for _, id := range e.proposalOrder {
		p := e.proposals[id]
		if p == nil {
			continue
		}
		e.expireLocked(p, now)
		out = append(out, cloneProposal(*p))
	}
	return out
}

// Approve executes one pending, not expired proposal exactly once. The
// approved tool call bypasses the approval gate: the human already confirmed it.
func (e *Engine) Approve(ctx context.Context, id string) (ToolCall, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ToolCall{}, ErrProposalNotFound
	}
	e.proposalMu.Lock()
	p := e.proposals[id]
	if p == nil {
		e.proposalMu.Unlock()
		return ToolCall{}, ErrProposalNotFound
	}
	wasPending := p.Status == ProposalPending
	if e.expireLocked(p, e.now()) {
		expired := cloneProposal(*p)
		e.proposalMu.Unlock()
		if wasPending {
			e.publish(Event{Type: "proposal", Data: expired})
		}
		return ToolCall{}, ErrProposalExpired
	}
	if p.Status != ProposalPending {
		e.proposalMu.Unlock()
		return ToolCall{}, ErrProposalHandled
	}
	p.Status = ProposalApproved
	approved := cloneProposal(*p)
	tool, args := p.Tool, cloneArgs(p.Args)
	e.proposalMu.Unlock()
	e.publish(Event{Type: "proposal", Data: approved})

	call, _ := e.executeToolCallDirect(ctx, toolInvocation{}, tool, args)

	e.proposalMu.Lock()
	p.Status = ProposalExecuted
	e.proposalResults[id] = call
	executed := cloneProposal(*p)
	e.proposalMu.Unlock()
	e.publish(Event{Type: "proposal", Data: executed})
	return call, nil
}

// Reject marks one pending proposal rejected. A rejected proposal never runs.
func (e *Engine) Reject(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrProposalNotFound
	}
	e.proposalMu.Lock()
	p := e.proposals[id]
	if p == nil {
		e.proposalMu.Unlock()
		return ErrProposalNotFound
	}
	wasPending := p.Status == ProposalPending
	if e.expireLocked(p, e.now()) {
		expired := cloneProposal(*p)
		e.proposalMu.Unlock()
		if wasPending {
			e.publish(Event{Type: "proposal", Data: expired})
		}
		return ErrProposalExpired
	}
	if p.Status != ProposalPending {
		e.proposalMu.Unlock()
		return ErrProposalHandled
	}
	p.Status = ProposalRejected
	rejected := cloneProposal(*p)
	e.proposalMu.Unlock()
	e.publish(Event{Type: "proposal", Data: rejected})
	return nil
}

// Propose creates one pending proposal for a mutating action initiated outside
// the tool loop (the bot runtime promotion, plan Task 22). It is the
// bot.Approver implementation of the engine: the proposal is published via the
// normal SSE "proposal" channel and approved/rejected exactly like a
// tool-generated one. The returned id is used by the caller in its journal.
func (e *Engine) Propose(tool string, args map[string]any, summary string) (string, error) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return "", errors.New("agent: proposal tool is required")
	}
	if !isMutatingTool(tool) {
		return "", fmt.Errorf("agent: proposal for a non-mutating tool %q", tool)
	}
	e.proposalMu.Lock()
	proposal := e.newProposalLocked(tool, args)
	if custom := strings.TrimSpace(summary); custom != "" {
		if stored := e.proposals[proposal.ID]; stored != nil {
			stored.Summary = custom
			proposal = cloneProposal(*stored)
		}
	}
	e.proposalMu.Unlock()
	e.publish(Event{Type: "proposal", Data: proposal})
	return proposal.ID, nil
}

// ProposalResult returns the executed tool call of an approved proposal, if it
// has been executed already.
func (e *Engine) ProposalResult(id string) (ToolCall, bool) {
	e.proposalMu.Lock()
	defer e.proposalMu.Unlock()
	call, ok := e.proposalResults[strings.TrimSpace(id)]
	return call, ok
}

// propose creates, registers and publishes a pending proposal.
func (e *Engine) propose(tool string, args map[string]any) proposalResult {
	e.proposalMu.Lock()
	p := e.newProposalLocked(tool, args)
	e.proposalMu.Unlock()
	e.publish(Event{Type: "proposal", Data: p})
	return proposalResult{Proposal: p}
}

// newProposalLocked registers a new pending proposal in the bounded ring. The
// caller holds proposalMu.
func (e *Engine) newProposalLocked(tool string, args map[string]any) Proposal {
	e.proposalSeq++
	now := e.now()
	p := &Proposal{
		ID:        fmt.Sprintf("prop_%06d", e.proposalSeq),
		Tool:      tool,
		Args:      cloneArgs(args),
		Summary:   summarizeTool(tool, args),
		CreatedAt: now,
		ExpiresAt: now.Add(proposalTTL),
		Status:    ProposalPending,
	}
	e.proposals[p.ID] = p
	e.proposalOrder = append(e.proposalOrder, p.ID)
	for len(e.proposalOrder) > maxProposals {
		oldest := e.proposalOrder[0]
		e.proposalOrder = e.proposalOrder[1:]
		delete(e.proposals, oldest)
		delete(e.proposalResults, oldest)
	}
	return cloneProposal(*p)
}

// gateMutatingTool turns one validated mutating tool call (cancel_order,
// start_autonomy) into a pending proposal. It returns the proposal result and
// handled=true: the tool never executes before a human approves it.
//
// stop_autonomy is the documented exception: the kill-switch is never delayed
// by a confirmation, so callers invoke it directly and never reach this gate.
func (e *Engine) gateMutatingTool(tool string, args map[string]any) (result any, handled bool) {
	return e.propose(tool, args), true
}

// gatePlaceOrder decides whether a validated place_order must wait for a human.
// In the explicitly enabled envelope mode an order inside the envelope limits
// (and inside MaxOrdersPerRun) executes at once; everything else becomes a
// proposal.
func (e *Engine) gatePlaceOrder(args map[string]any, req PlaceOrder) (result any, handled bool) {
	if e.cfg.Approval.Mode == ApprovalEnvelope && e.envelopeAllowsOrder(req) && e.takeEnvelopeSlot() {
		return nil, false
	}
	return e.propose("place_order", args), true
}

// takeEnvelopeSlot reserves one envelope auto-approval of the session.
func (e *Engine) takeEnvelopeSlot() bool {
	limit := e.cfg.Approval.MaxOrdersPerRun
	if limit <= 0 {
		return true
	}
	e.proposalMu.Lock()
	defer e.proposalMu.Unlock()
	if e.autoApprovals >= limit {
		return false
	}
	e.autoApprovals++
	return true
}

// envelopeAllowsOrder reports whether one validated place_order is inside the
// approval envelope: the order notional and the resulting position are within
// the envelope risk limits. The engine risk limits were already checked by
// checkOrderRisk; this gate is only about the autonomy auto-approval envelope.
func (e *Engine) envelopeAllowsOrder(req PlaceOrder) bool {
	if e.broker == nil || req.Symbol == "" || req.Qty <= 0 {
		return false
	}
	symbol := req.Symbol
	side := strings.ToLower(strings.TrimSpace(req.Side))
	if side != "buy" && side != "sell" {
		return false
	}
	refPrice := req.Price
	if !strings.EqualFold(req.Type, "limit") {
		refPrice = e.referencePrice(symbol, side)
	}
	if refPrice <= 0 {
		return false
	}
	notional := refPrice * float64(req.Qty)
	limits := e.envelopeLimits()
	portfolio := e.broker.Portfolio()
	if side == "buy" {
		cash := e.broker.Cash()
		if cash <= 0 {
			return false
		}
		if limits.MaxOrderPct > 0 && notional > cash*limits.MaxOrderPct/100 {
			return false
		}
		if limits.MaxPositionPct > 0 {
			maxPos := portfolio.TotalValue * limits.MaxPositionPct / 100
			if maxPos > 0 && positionValue(portfolio, symbol)+notional > maxPos {
				return false
			}
		}
		return true
	}
	return positionQty(portfolio, symbol) >= req.Qty
}

// envelopeLimits merges the policy envelope with the engine risk limits: an
// unset (<= 0) envelope field falls back to the engine limit.
func (e *Engine) envelopeLimits() RiskLimits {
	env := e.cfg.Approval.Envelope
	risk := e.cfg.Risk
	if env.MaxPositionPct <= 0 {
		env.MaxPositionPct = risk.MaxPositionPct
	}
	if env.MaxOrderPct <= 0 {
		env.MaxOrderPct = risk.MaxOrderPct
	}
	if env.StopLossPct <= 0 {
		env.StopLossPct = risk.StopLossPct
	}
	if env.TakeProfitPct <= 0 {
		env.TakeProfitPct = risk.TakeProfitPct
	}
	if env.DailyLossPct <= 0 {
		env.DailyLossPct = risk.DailyLossPct
	}
	if env.MaxTradesPerHour <= 0 {
		env.MaxTradesPerHour = risk.MaxTradesPerHour
	}
	return env
}

// expireLocked lazily marks a pending proposal expired. The caller holds
// proposalMu. It reports whether the proposal is expired now.
func (e *Engine) expireLocked(p *Proposal, now time.Time) bool {
	if p.Status == ProposalExpired {
		return true
	}
	if p.Status == ProposalPending && !now.Before(p.ExpiresAt) {
		p.Status = ProposalExpired
		return true
	}
	return false
}

// summarizeTool renders the short Russian summary of the approval card.
func summarizeTool(tool string, args map[string]any) string {
	switch tool {
	case "place_order":
		action := "Купить"
		if strings.EqualFold(strings.TrimSpace(argString(args, "side")), "sell") {
			action = "Продать"
		}
		symbol := strings.ToUpper(secCode(argString(args, "symbol")))
		if symbol == "" {
			symbol = "инструмент"
		}
		qty := int64(argFloat(args, "qty", 0))
		summary := fmt.Sprintf("%s %d %s", action, qty, symbol)
		if strings.EqualFold(strings.TrimSpace(argString(args, "type")), "limit") {
			summary += fmt.Sprintf(" по лимитной цене %.2f ₽", argFloat(args, "price", 0))
		} else {
			summary += " по рынку"
		}
		return summary
	case "cancel_order":
		return "Отменить заявку " + argString(args, "orderId")
	case "start_autonomy":
		symbol := strings.ToUpper(secCode(argString(args, "symbol")))
		if symbol == "" {
			symbol = "инструмент"
		}
		summary := "Запустить автономию: " + symbol
		if strategy := argString(args, "strategy"); strategy != "" {
			summary += ", стратегия " + strategy
		}
		return summary
	case "stop_autonomy":
		return "Остановить автономию (kill-switch)"
	case "bot_start":
		return fmt.Sprintf("Запустить бота %s в стадии %s", argString(args, "id"), argString(args, "stage"))
	case "bot_promote":
		return "Перевести бота " + argString(args, "id") + " на бумажный счёт"
	case "bot_stop":
		return "Остановить бота " + argString(args, "id")
	default:
		return tool
	}
}

// cloneProposal returns a copy of the proposal safe to hand to callers.
func cloneProposal(p Proposal) Proposal {
	p.Args = cloneArgs(p.Args)
	return p
}

// cloneArgs copies one tool argument map.
func cloneArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}
