package policy

import (
	"errors"
	"fmt"
)

// LLMMode selects how a model-backed Plan is combined with the
// deterministic Decision (ADR 0004). The deterministic path always runs;
// the mode only decides whether the Plan can replace its result.
type LLMMode string

const (
	// LLMOff never consults the model. Byte-for-byte pre-ADR-0004 behaviour.
	LLMOff LLMMode = "off"
	// LLMShadow consults the model, records agreement, returns the
	// deterministic Decision.
	LLMShadow LLMMode = "shadow"
	// LLMOn returns a valid Plan in place of the deterministic Decision and
	// falls back to it on any error, timeout or validation failure.
	LLMOn LLMMode = "on"
)

// ParseLLMMode is strict: unknown values are an error, never a default, per
// this service's "reject, never default" rule for untrusted input.
func ParseLLMMode(raw string) (LLMMode, error) {
	switch LLMMode(raw) {
	case LLMOff, LLMShadow, LLMOn:
		return LLMMode(raw), nil
	case "":
		return LLMOff, nil
	default:
		return "", fmt.Errorf("policy: unrecognized LLM_MODE %q (want off|shadow|on)", raw)
	}
}

// DecisionSource records which path produced the returned Decision.
type DecisionSource string

const (
	SourceDeterministic DecisionSource = "deterministic"
	SourceLLM           DecisionSource = "llm"
	SourceFallback      DecisionSource = "fallback"
)

// MaxProposedHeads bounds any head-count a Plan may propose. A model can
// never widen it; the deterministic proposedHeads() already respects it.
const MaxProposedHeads = 50

// ErrInvalidPlan is wrapped by every validation failure so callers can
// distinguish "the model produced garbage" from transport errors.
var ErrInvalidPlan = errors.New("policy: invalid plan")

// PlanProposal is the policy-level view of a Reasoner Plan: the enum-typed
// action plus bounded numbers. The application layer converts the port DTO
// into this before calling Arbitrate, so the domain never imports ports.
type PlanProposal struct {
	RecommendedAction RecommendedAction
	ProposedHeads     int
	Rationale         string
}

// ValidatePlan enforces the closed vocabulary and bounds. It is the single
// gate through which model output enters the domain.
func ValidatePlan(p PlanProposal) error {
	switch p.RecommendedAction {
	case ActionAssignLabor, ActionReleaseNextWork, FlowBalanceActionHold:
	default:
		return fmt.Errorf("%w: unrecognized action %q", ErrInvalidPlan, string(p.RecommendedAction))
	}
	// An assign_labor plan must assign at least one head: the deterministic
	// path floors its gap at 1 for the same reason (see proposedHeads), and
	// "assign labor" with zero heads is a no-op the advisory must never
	// present as an action.
	if p.RecommendedAction == ActionAssignLabor && (p.ProposedHeads < 1 || p.ProposedHeads > MaxProposedHeads) {
		return fmt.Errorf("%w: proposedHeads %d outside [1,%d] for %s", ErrInvalidPlan, p.ProposedHeads, MaxProposedHeads, ActionAssignLabor)
	}
	if p.ProposedHeads < 0 || p.ProposedHeads > MaxProposedHeads {
		return fmt.Errorf("%w: proposedHeads %d outside [0,%d]", ErrInvalidPlan, p.ProposedHeads, MaxProposedHeads)
	}
	if p.RecommendedAction != ActionAssignLabor && p.ProposedHeads != 0 {
		return fmt.Errorf("%w: proposedHeads only meaningful for %s", ErrInvalidPlan, ActionAssignLabor)
	}
	if p.Rationale == "" {
		return fmt.Errorf("%w: empty rationale", ErrInvalidPlan)
	}
	return nil
}

// Arbitration is the outcome of combining the two sources.
type Arbitration struct {
	Decision Decision
	Source   DecisionSource
	// Agree is set in shadow and on modes when a valid plan was available:
	// true when the plan's action matched the deterministic one.
	Agree *bool
	// Reason explains a fallback (planErr, invalid plan) or is empty.
	Reason string
}

// Arbitrate is the only place the deterministic Decision and a model Plan
// meet. planErr carries any transport/timeout error from the Reasoner; a
// nil plan with nil planErr means the mode never asked for one.
func Arbitrate(det Decision, plan *PlanProposal, planErr error, mode LLMMode) Arbitration {
	out := Arbitration{Decision: det, Source: SourceDeterministic}
	if mode == LLMOff {
		return out
	}
	if planErr != nil {
		out.Reason = planErr.Error()
		if mode == LLMOn {
			out.Source = SourceFallback
		}
		return out
	}
	if plan == nil {
		out.Reason = "no plan produced"
		if mode == LLMOn {
			out.Source = SourceFallback
		}
		return out
	}
	if err := ValidatePlan(*plan); err != nil {
		out.Reason = err.Error()
		if mode == LLMOn {
			out.Source = SourceFallback
		}
		return out
	}
	agree := plan.RecommendedAction == det.RecommendedAction
	out.Agree = &agree
	if mode == LLMShadow {
		return out
	}
	// LLMOn: the plan replaces the action, heads and rationale; evidence,
	// partiality and pathId stay exactly what the deterministic pass saw,
	// so a reader can always audit what the model was shown.
	d := det
	d.RecommendedAction = plan.RecommendedAction
	d.ProposedHeads = plan.ProposedHeads
	d.Rationale = plan.Rationale
	out.Decision = d
	out.Source = SourceLLM
	return out
}
