package skills

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Promotion policy defaults of plan Task 19 (Review Focus 4).
const (
	defaultMinOOSTrades      = 30
	defaultMinDeflatedSharpe = 0.95
	defaultMaxPBO            = 0.3
	defaultMinOOSMonths      = 3
)

// PromotionPolicy is the evidence policy of the draft -> validated transition.
// Zero fields fall back to the plan defaults.
type PromotionPolicy struct {
	MinOOSTrades      int
	MinDeflatedSharpe float64
	MaxPBO            float64
	MinOOSMonths      int
}

// DefaultPromotionPolicy returns the frozen plan defaults: at least 30 OOS
// trades, deflated Sharpe >= 0.95, PBO <= 0.3 and at least 3 months of OOS
// history.
func DefaultPromotionPolicy() PromotionPolicy {
	return PromotionPolicy{
		MinOOSTrades:      defaultMinOOSTrades,
		MinDeflatedSharpe: defaultMinDeflatedSharpe,
		MaxPBO:            defaultMaxPBO,
		MinOOSMonths:      defaultMinOOSMonths,
	}
}

// withDefaults fills the unset policy fields with the plan defaults.
func (p PromotionPolicy) withDefaults() PromotionPolicy {
	defaults := DefaultPromotionPolicy()
	if p.MinOOSTrades <= 0 {
		p.MinOOSTrades = defaults.MinOOSTrades
	}
	if p.MinDeflatedSharpe <= 0 {
		p.MinDeflatedSharpe = defaults.MinDeflatedSharpe
	}
	if p.MaxPBO <= 0 {
		p.MaxPBO = defaults.MaxPBO
	}
	if p.MinOOSMonths <= 0 {
		p.MinOOSMonths = defaults.MinOOSMonths
	}
	return p
}

// Promote validates a draft pattern against the evidence policy and moves it to
// the validated status when every criterion is met. A validated pattern is
// idempotent; a retired pattern can never be promoted again. Validated makes
// the skill visible to the agent — it never enables live trading.
func Promote(p *Pattern, policy PromotionPolicy) error {
	if p == nil {
		return errors.New("skills: nil pattern")
	}
	switch p.Status {
	case StatusValidated:
		return nil
	case StatusRetired:
		return fmt.Errorf("skills: pattern %q is retired and cannot be promoted", p.ID)
	case "", StatusDraft:
	default:
		return fmt.Errorf("skills: unknown pattern status %q", p.Status)
	}
	policy = policy.withDefaults()
	if unmet := promotionFailures(p.Evidence, policy); len(unmet) > 0 {
		return fmt.Errorf("skills: pattern %q не проходит promotion по evidence policy: %s",
			p.ID, strings.Join(unmet, "; "))
	}
	p.Status = StatusValidated
	return nil
}

// promotionFailures lists the unmet promotion criteria of the evidence.
func promotionFailures(evidence Evidence, policy PromotionPolicy) []string {
	problems := make([]string, 0, 4)
	switch evidence.Source {
	case SourceBars, SourceLOB:
	default:
		problems = append(problems, fmt.Sprintf("источник доказательств %q не является bars|lob", evidence.Source))
	}
	from, to := evidence.OOSWindow()
	if from.IsZero() || to.IsZero() || !to.After(from) {
		problems = append(problems, "нет out-of-sample окна (OOS.From/OOS.To обязательны)")
	} else if months := oosMonths(from, to); months < policy.MinOOSMonths {
		problems = append(problems, fmt.Sprintf("OOS-окно %d мес < %d мес", months, policy.MinOOSMonths))
	}
	if trades := evidence.OOSTradeCount(); trades < policy.MinOOSTrades {
		problems = append(problems, fmt.Sprintf("мало OOS-сделок: %d < %d", trades, policy.MinOOSTrades))
	}
	if evidence.DeflatedSharpe < policy.MinDeflatedSharpe {
		problems = append(problems, fmt.Sprintf("deflated Sharpe %.3f < %.3f", evidence.DeflatedSharpe, policy.MinDeflatedSharpe))
	}
	if evidence.PBO > policy.MaxPBO {
		problems = append(problems, fmt.Sprintf("PBO %.3f > %.3f", evidence.PBO, policy.MaxPBO))
	}
	if strings.TrimSpace(evidence.DataHash) == "" {
		problems = append(problems, "нет отпечатка данных (DataHash)")
	}
	return problems
}

// oosMonths is the out-of-sample window length in whole months (the average
// 30.4375-day month).
func oosMonths(from, to time.Time) int {
	if !to.After(from) {
		return 0
	}
	days := to.Sub(from).Hours() / 24
	return int(days / 30.4375)
}
