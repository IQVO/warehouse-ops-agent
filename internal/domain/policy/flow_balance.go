package policy

import "fmt"

// RebalanceAction mirrors wes-work-planning's get_rebalance_recommendation
// action enum (see that context's apis/openapi.yaml RebalanceAction schema).
// It is a closed set: any string outside these three values is untrusted
// input from an upstream tool response and must be rejected by
// ParseRebalanceAction, never silently defaulted (PROPOSAL §5 guardrail).
type RebalanceAction string

const (
	RebalanceNoActionNeeded   RebalanceAction = "NoActionNeeded"
	RebalanceThrottleUpstream RebalanceAction = "ThrottleUpstream"
	RebalanceReassignLabor    RebalanceAction = "ReassignLabor"
)

// ParseRebalanceAction validates a raw action string from wes-work-planning's
// get_rebalance_recommendation tool response against the closed enum. An
// unrecognized value is untrusted input and is rejected with an error —
// callers must never substitute a default action for it.
func ParseRebalanceAction(raw string) (RebalanceAction, error) {
	switch RebalanceAction(raw) {
	case RebalanceNoActionNeeded, RebalanceThrottleUpstream, RebalanceReassignLabor:
		return RebalanceAction(raw), nil
	default:
		return "", fmt.Errorf("policy: unrecognized RebalanceAction %q from upstream tool response", raw)
	}
}

// RecommendedAction is the closed set of corrective levers this exception's
// decision can rank: assign more heads to the path, release the next work
// unit into it, or hold for human judgement (no lever this agent can pull is
// supported by the evidence).
type RecommendedAction string

const (
	ActionAssignLabor     RecommendedAction = "assign_labor"
	ActionReleaseNextWork RecommendedAction = "release_next_work"
	FlowBalanceActionHold RecommendedAction = "hold"
)

// RebalanceSignal is the domain-owned mirror of the reading gathered from
// wes-work-planning's get_rebalance_recommendation tool. Source identifies
// which tool call produced it, for the decision's evidence trail; it carries
// no upstream Go type, keeping this package free of any adapter/ports
// dependency (see TestHexagonalDependencyRules: domain depends on domain
// only).
type RebalanceSignal struct {
	Source       string
	PathId       string
	Action       RebalanceAction
	BacklogDepth int
	WIP          int
}

// StaffingSignal is the domain-owned mirror of the reading gathered from
// workforce-management's get_staffing_gap tool.
type StaffingSignal struct {
	Source       string
	PathId       string
	PlannedHeads int
	ActiveHeads  int
	Understaffed bool
}

// StuckTasksSignal is the domain-owned mirror of the reading gathered from
// fulfillment-execution's diagnose_stuck_tasks tool. That tool is not
// path-scoped (see its published contract), so Count/Reasons are a
// system-wide stuck-task reading correlated alongside the path-scoped wes
// and wfm signals, not filtered to the path under decision.
type StuckTasksSignal struct {
	Source  string
	Count   int
	Reasons []string
}

// UtilizationSignal is the domain-owned mirror of the reading gathered
// from labor-performance's get_task_type_utilization tool (see ADR 0008).
// UtilizationPct is nil when nothing was observed in the window over
// which it was measured; CorrelateUtilization treats a nil pointer here
// exactly like an unavailable signal, never as 0%.
type UtilizationSignal struct {
	Source         string
	TaskType       string
	Associates     int
	WindowSeconds  int64
	TaskSeconds    int64
	IdleSeconds    int64
	OpenGapSeconds int64
	UtilizationPct *float64
}

// FlowBalanceEvidenceEntry is one line of the decision's evidence trail: which reading
// (identified by Source, e.g. "wes-work-planning.get_rebalance_recommendation")
// drove the decision, and what it showed.
type FlowBalanceEvidenceEntry struct {
	Source string
	Detail string
}

// Decision is the FlowBalanceException correlation result: a ranked
// recommended action plus the full evidence trail that produced it. A
// Decision is always well-formed even when one or more upstream signals were
// unavailable — Partial and MissingSignals make that degradation explicit
// rather than the caller having to infer it.
type Decision struct {
	PathId            string
	RecommendedAction RecommendedAction
	ProposedHeads     int
	Rationale         string
	Partial           bool
	MissingSignals    []string
	Evidence          []FlowBalanceEvidenceEntry

	// Utilization is the additive labor-utilization correlation overlay
	// (ADR 0008): a separate advisory derived from queue depth and
	// labor-performance's observed task-type utilization, alongside
	// (never replacing) RecommendedAction above. Nil means either no
	// labor-performance signal was available (unreachable, or
	// UtilizationPct was null -- nothing observed in the window) or the
	// observed queue-depth/idle-share combination did not match any of
	// the three named outcomes; RecommendedAction and Rationale are
	// unaffected either way (ADR-0004 fallback discipline: a missing or
	// null utilization signal degrades to the pre-existing behavior,
	// never a crash or a guessed-at advisory).
	Utilization *UtilizationCorrelation

	// Source records which path produced this Decision (ADR 0004):
	// "deterministic" (LLM_MODE=off, or shadow/on with no Reasoner
	// wired), "llm" (LLM_MODE=on with a valid, timely Plan), or
	// "fallback" (LLM_MODE=on but the Reasoner errored, timed out, or
	// produced an invalid Plan — the deterministic Decision was used
	// instead). Empty only for a Decision built directly by tests that
	// bypass FlowBalanceAdvisory.arbitrate.
	Source DecisionSource
}

// Decide correlates the three upstream signals into one ranked
// FlowBalanceException recommendation. It is pure and side-effect-free: no
// I/O, no upstream calls — those are the application layer's job (see
// internal/application/usecases.FlowBalanceAdvisory). Each signal argument
// is nil when its upstream call failed or was skipped; Decide degrades to a
// Partial, conservative Hold whenever a signal it needs for the branch in
// play is missing, rather than guessing.
//
// wes is assumed to already carry a validated RebalanceAction — Decide never
// receives or has to reject an unrecognized enum value; that rejection
// happens once, at the untrusted-input boundary, via ParseRebalanceAction.
func Decide(pathId string, wes *RebalanceSignal, wfm *StaffingSignal, fe *StuckTasksSignal) Decision {
	missing := missingSignals(wes, wfm, fe)
	d := Decision{
		PathId:   pathId,
		Evidence: evidenceEntries(wes, wfm, fe),
	}

	// The wes signal is the anchor: without it there is nothing to
	// correlate against. Degrade immediately.
	if wes == nil {
		return degradeToHold(d, missing, "wes-work-planning's rebalance recommendation is unavailable; holding pending a retry — no lever can be safely ranked without the anchor signal.")
	}

	switch wes.Action {
	case RebalanceReassignLabor, RebalanceThrottleUpstream:
		// Both cases point at the same question: is the bottleneck a
		// confirmed labor gap? Only wfm can answer that.
		return decideLaborGap(d, wes, wfm, fe, missing)
	case RebalanceNoActionNeeded:
		return decideHealthyBacklog(d, wfm, fe, missing)
	default:
		// Unreachable when callers construct RebalanceSignal.Action via
		// ParseRebalanceAction, which is the only sanctioned path; kept as
		// an explicit, safe fallback rather than a panic.
		d.RecommendedAction = FlowBalanceActionHold
		d.Rationale = fmt.Sprintf("unrecognized wes action %q reached the policy layer; holding.", wes.Action)
		return d
	}
}

// missingSignals lists every upstream signal that was unavailable, in
// wes, wfm, fe order.
func missingSignals(wes *RebalanceSignal, wfm *StaffingSignal, fe *StuckTasksSignal) []string {
	var missing []string
	if wes == nil {
		missing = append(missing, "wes-work-planning.get_rebalance_recommendation")
	}
	if wfm == nil {
		missing = append(missing, "workforce-management.get_staffing_gap")
	}
	if fe == nil {
		missing = append(missing, "fulfillment-execution.diagnose_stuck_tasks")
	}
	return missing
}

// evidenceEntries appends one evidence-trail entry per available signal,
// in wes, wfm, fe order, so every decision shows the readings that drove
// it.
func evidenceEntries(wes *RebalanceSignal, wfm *StaffingSignal, fe *StuckTasksSignal) []FlowBalanceEvidenceEntry {
	var entries []FlowBalanceEvidenceEntry
	if wes != nil {
		entries = append(entries, FlowBalanceEvidenceEntry{
			Source: wes.Source,
			Detail: fmt.Sprintf("action=%s backlogDepth=%d wip=%d (pathId=%s)", wes.Action, wes.BacklogDepth, wes.WIP, wes.PathId),
		})
	}
	if wfm != nil {
		entries = append(entries, FlowBalanceEvidenceEntry{
			Source: wfm.Source,
			Detail: fmt.Sprintf("plannedHeads=%d activeHeads=%d understaffed=%t (pathId=%s)", wfm.PlannedHeads, wfm.ActiveHeads, wfm.Understaffed, wfm.PathId),
		})
	}
	if fe != nil {
		entries = append(entries, FlowBalanceEvidenceEntry{
			Source: fe.Source,
			Detail: fmt.Sprintf("stuckTaskCount=%d reasons=%v", fe.Count, fe.Reasons),
		})
	}
	return entries
}

// degradeToHold marks d as a Partial, conservative Hold listing exactly
// which signals were missing — the shared degrade exit for every branch
// whose deciding signal was unavailable.
func degradeToHold(d Decision, missing []string, rationale string) Decision {
	d.Partial = true
	d.MissingSignals = missing
	d.RecommendedAction = FlowBalanceActionHold
	d.Rationale = rationale
	return d
}

// decideLaborGap resolves the ReassignLabor/ThrottleUpstream branch: wes
// sees a flow problem, and the question is whether a confirmed labor gap
// (only wfm can answer) or blocked claims (fe) explain it.
func decideLaborGap(d Decision, wes *RebalanceSignal, wfm *StaffingSignal, fe *StuckTasksSignal, missing []string) Decision {
	if wfm == nil {
		return degradeToHold(d, missing, fmt.Sprintf("wes recommends %s but workforce-management's staffing gap is unavailable; holding rather than guessing whether labor is the bottleneck.", wes.Action))
	}

	if wfm.Understaffed {
		d.RecommendedAction = ActionAssignLabor
		d.ProposedHeads = proposedHeads(wfm.PlannedHeads, wfm.ActiveHeads)
		d.Rationale = fmt.Sprintf("wes recommends %s and workforce-management confirms the path is understaffed; assigning %d head(s) is the in-vocabulary lever supported by both signals.", wes.Action, d.ProposedHeads)
		return d
	}

	// wfm says fully staffed, yet wes still sees a flow problem.
	// Assigning more labor is not supported by the evidence; check
	// whether stuck tasks explain the saturation instead.
	if fe == nil {
		return degradeToHold(d, missing, fmt.Sprintf("wes recommends %s but workforce-management reports adequate staffing and fulfillment-execution's stuck-task diagnostic is unavailable; holding for human review.", wes.Action))
	}

	d.RecommendedAction = FlowBalanceActionHold
	if fe.Count > 0 {
		d.Rationale = fmt.Sprintf("wes recommends %s, but workforce-management reports adequate staffing and fulfillment-execution reports %d stuck task(s) — the bottleneck looks like blocked claims, not a labor gap; holding for human review of the stuck leases.", wes.Action, fe.Count)
	} else {
		d.Rationale = fmt.Sprintf("wes recommends %s, but neither a staffing gap nor stuck tasks corroborate it; holding for human review rather than pulling an unsupported lever.", wes.Action)
	}
	return d
}

// decideHealthyBacklog resolves the NoActionNeeded branch: with a healthy
// backlog, a fully staffed path with no stuck tasks can safely take the
// next work unit; anything less degrades or holds.
func decideHealthyBacklog(d Decision, wfm *StaffingSignal, fe *StuckTasksSignal, missing []string) Decision {
	if fe == nil {
		return degradeToHold(d, missing, "wes reports no backlog action needed, but fulfillment-execution's stuck-task diagnostic is unavailable; holding rather than assuming the path is healthy.")
	}

	if fe.Count > 0 {
		d.RecommendedAction = FlowBalanceActionHold
		d.Rationale = fmt.Sprintf("wes reports no backlog action needed, but fulfillment-execution reports %d stuck task(s); holding for human review despite the healthy backlog signal.", fe.Count)
		return d
	}

	if wfm == nil {
		return degradeToHold(d, missing, "wes reports no backlog action needed and no tasks are stuck, but workforce-management's staffing gap is unavailable; holding rather than releasing work into a path of unknown staffing.")
	}

	if !wfm.Understaffed {
		d.RecommendedAction = ActionReleaseNextWork
		d.Rationale = "wes reports no backlog action needed, no tasks are stuck, and workforce-management confirms the path is fully staffed; releasing the next work unit is safe and keeps spare capacity utilized."
		return d
	}

	d.RecommendedAction = FlowBalanceActionHold
	d.Rationale = "wes reports no backlog action needed and no tasks are stuck, but workforce-management flags a staffing gap; holding — the gap is visible but not itself an exception this correlation should force an action on."
	return d
}

// proposedHeads is the labor gap, floored at 1 so an AssignLabor
// recommendation always proposes assigning at least one head.
func proposedHeads(planned, active int) int {
	gap := planned - active
	if gap < 1 {
		return 1
	}
	return gap
}
