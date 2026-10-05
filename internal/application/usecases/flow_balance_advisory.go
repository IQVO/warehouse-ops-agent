// Package usecases is the application layer of warehouse-ops-agent.
//
// FlowBalanceAdvisory (this T2 slice) is the first concrete use case: it
// orchestrates the three outbound MCP-client ports built in T1 — wes
// (get_rebalance_recommendation), workforce-management (get_staffing_gap),
// and fulfillment-execution (diagnose_stuck_tasks) — gathers their readings,
// and hands them to the internal/domain/policy correlation rule to produce
// one ranked FlowBalanceException recommendation. It never writes directly
// to a bounded context's storage — the only mutation path available to this
// module at all is calling one of the five contexts' own published write
// tools, and that path is deliberately not wired up here (recommendations
// only, per the T2 card body).
package usecases

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

const (
	wesSource = "wes-work-planning.get_rebalance_recommendation"
	wfmSource = "workforce-management.get_staffing_gap"
	feSource  = "fulfillment-execution.diagnose_stuck_tasks"
	lpSource  = "labor-performance.get_task_type_utilization"

	// defaultStuckTaskWindow bounds how far back diagnose_stuck_tasks looks
	// for a lease past its expiry, when the caller does not override it.
	defaultStuckTaskWindow = 15 * time.Minute
)

// FlowBalanceAdvisory is the E1 correlation use case: given a process path
// (scoped to a building/shift for the staffing lookup), it gathers the three
// upstream signals and returns the resulting policy.Decision. A missing or
// erroring upstream call degrades the decision to a partial, conservative
// result (see policy.Decide) instead of failing the whole use case — the
// one exception is an unrecognized RebalanceAction enum value, which is
// untrusted input and is rejected outright (never defaulted).
type FlowBalanceAdvisory struct {
	Wes ports.WesWorkPlanningClient
	WFM ports.WorkforceManagementClient
	FE  ports.FulfillmentExecutionClient

	// LP is labor-performance's outbound MCP-client port (ADR 0008,
	// Phase 3). Nil is a valid value: the labor-utilization correlation
	// overlay (Decision.Utilization) simply stays nil, and every other
	// field of Decision is produced exactly as before LP existed --
	// this is the deterministic fallback ADR-0004 requires when a
	// signal/tool is unavailable.
	LP ports.LaborPerformanceClient

	// PathTaskTypes maps a wes-work-planning pathId to the
	// fulfillment-execution process-path/task-type name it corresponds
	// to (PICK/PACK/SLAM) -- the SAME PathId->ProcessPath binding
	// DailyBrief already consumes via config.PathTarget.ProcessPath
	// (see cmd/agent's toUseCaseTargets). This use case never infers
	// that binding itself; a pathId with no entry here (or a nil map)
	// simply skips the labor-utilization correlation, exactly like a
	// nil LP client.
	PathTaskTypes map[string]string

	// UtilizationWindowSeconds bounds labor-performance's
	// get_task_type_utilization window. Non-positive (including the
	// zero value) means "use the tool's own default" (1h); forwarded
	// through unchanged, never defaulted here.
	UtilizationWindowSeconds int64

	// StuckTaskWindow bounds the diagnose_stuck_tasks lookback. Defaults to
	// defaultStuckTaskWindow when zero.
	StuckTaskWindow time.Duration

	// Reasoner, LLMMode, ReasonerTools and Metrics are the ADR 0004
	// additions. All optional: a nil Reasoner or LLMMode off (the zero
	// value) keeps the pre-ADR behaviour byte-for-byte.
	Reasoner      ports.Reasoner
	LLMMode       policy.LLMMode
	ReasonerTools []ports.ToolSpec
	Metrics       ports.ArbitrationMetrics

	// Logger receives a structured warning for each upstream call that
	// failed, so a degraded/partial decision is observable without forcing
	// the caller to inspect Decision.MissingSignals. Defaults to
	// slog.Default() when nil.
	Logger *slog.Logger
}

// Execute runs the E1 correlation for one process path. buildingId and
// shiftId scope the workforce-management staffing-gap lookup (that tool's
// published contract requires them); pathId scopes both the wes rebalance
// recommendation and the staffing gap.
func (uc *FlowBalanceAdvisory) Execute(ctx context.Context, buildingId, shiftId, pathId string) (policy.Decision, error) {
	logger := uc.Logger
	if logger == nil {
		logger = slog.Default()
	}
	window := uc.StuckTaskWindow
	if window <= 0 {
		window = defaultStuckTaskWindow
	}

	wesSignal, err := uc.gatherRebalanceSignal(ctx, pathId)
	if err != nil {
		// Unrecognized enum value: untrusted input, reject outright rather
		// than let the policy layer guess at a default.
		return policy.Decision{}, err
	}
	if wesSignal == nil {
		logger.Warn("flow_balance_advisory: wes-work-planning unavailable", "pathId", sanitizeForLog(pathId))
	}

	wfmSignal, err := uc.gatherStaffingSignal(ctx, buildingId, shiftId, pathId)
	if err != nil {
		logger.Warn("flow_balance_advisory: workforce-management unavailable", "pathId", sanitizeForLog(pathId), "error", sanitizeForLog(err.Error()))
	}

	feSignal, err := uc.gatherStuckTasksSignal(ctx, window)
	if err != nil {
		logger.Warn("flow_balance_advisory: fulfillment-execution unavailable", "error", sanitizeForLog(err.Error()))
	}

	decision := policy.Decide(pathId, wesSignal, wfmSignal, feSignal)
	if decision.Partial {
		logger.Warn("flow_balance_advisory: partial decision", "pathId", sanitizeForLog(pathId), "missingSignals", decision.MissingSignals)
	}

	// Labor-utilization correlation overlay (ADR 0008, Phase 3): additive,
	// never changes RecommendedAction/ProposedHeads/Rationale above. A
	// missing wes signal means there is no queue-depth reading to
	// correlate against either, so this is skipped in that case too --
	// the same anchor-signal reasoning Decide() already applies.
	if wesSignal != nil {
		utilSignal, err := uc.gatherUtilizationSignal(ctx, pathId, uc.UtilizationWindowSeconds)
		if err != nil {
			logger.Warn("flow_balance_advisory: labor-performance unavailable", "pathId", sanitizeForLog(pathId), "error", sanitizeForLog(err.Error()))
		}
		if utilSignal != nil {
			decision.Evidence = append(decision.Evidence, policy.FlowBalanceEvidenceEntry{
				Source: utilSignal.Source,
				Detail: fmt.Sprintf("taskType=%s utilizationPct=%s idleSeconds=%d taskSeconds=%d windowSeconds=%d", utilSignal.TaskType, formatUtilizationPct(utilSignal.UtilizationPct), utilSignal.IdleSeconds, utilSignal.TaskSeconds, utilSignal.WindowSeconds),
			})
		}
		decision.Utilization = policy.CorrelateUtilization(wesSignal.BacklogDepth, utilSignal)
	}

	return uc.arbitrate(ctx, logger, pathId, decision, wesSignal, wfmSignal, feSignal), nil
}

// arbitrate runs the model-backed Reasoner (ADR 0004) when LLMMode is not
// off and lets policy.Arbitrate decide whether its Plan is used. The
// deterministic Decision is always computed first and is always the
// fallback; this method can never return an error.
func (uc *FlowBalanceAdvisory) arbitrate(ctx context.Context, logger *slog.Logger, pathId string, det policy.Decision, wes *policy.RebalanceSignal, wfm *policy.StaffingSignal, fe *policy.StuckTasksSignal) policy.Decision {
	mode := uc.LLMMode
	if mode == "" {
		mode = policy.LLMOff
	}
	if mode == policy.LLMOff || uc.Reasoner == nil {
		det.Source = policy.SourceDeterministic
		return det
	}

	brief := ports.Brief{
		UseCase:        "flow_balance_advisory",
		Question:       fmt.Sprintf("Backlog on process path %q is being reviewed. Which single lever should the operator pull now: assign_labor (with how many heads), release_next_work, or hold?", pathId),
		Facts:          flowBalanceFacts(wes, wfm, fe),
		AllowedActions: []string{string(policy.ActionAssignLabor), string(policy.ActionReleaseNextWork), string(policy.FlowBalanceActionHold)},
		Tools:          uc.ReasonerTools,
	}
	started := time.Now()
	plan, err := uc.Reasoner.Reason(ctx, brief)
	var proposal *policy.PlanProposal
	if err == nil {
		proposal = &policy.PlanProposal{
			RecommendedAction: policy.RecommendedAction(plan.RecommendedAction),
			ProposedHeads:     plan.ProposedHeads,
			Rationale:         plan.Rationale,
		}
	}
	arb := policy.Arbitrate(det, proposal, err, mode)

	attrs := []any{
		"pathId", sanitizeForLog(pathId),
		"mode", string(mode),
		"source", string(arb.Source),
		"model", sanitizeForLog(plan.Model),
		"latency_ms", time.Since(started).Milliseconds(),
		"tool_calls", len(plan.ToolCalls),
		"deterministic_action", string(det.RecommendedAction),
	}
	if proposal != nil {
		attrs = append(attrs, "llm_action", string(proposal.RecommendedAction), "llm_heads", proposal.ProposedHeads)
	}
	if arb.Agree != nil {
		attrs = append(attrs, "agree", *arb.Agree)
	}
	if arb.Reason != "" {
		attrs = append(attrs, "reason", sanitizeForLog(arb.Reason))
	}
	logger.Info("flow_balance_advisory: llm arbitration", attrs...)
	if uc.Metrics != nil {
		uc.Metrics.RecordArbitration(ctx, "flow_balance_advisory", string(mode), string(arb.Source), arb.Agree)
	}
	result := arb.Decision
	result.Source = arb.Source
	return result
}

// flowBalanceFacts renders the gathered signals as the Reasoner's facts,
// keyed by source so the model's rationale can cite them. Absent signals
// are stated as absent rather than omitted, so the model knows what it
// does not know.
func flowBalanceFacts(wes *policy.RebalanceSignal, wfm *policy.StaffingSignal, fe *policy.StuckTasksSignal) map[string]any {
	facts := map[string]any{}
	if wes != nil {
		facts[wes.Source] = map[string]any{"pathId": wes.PathId, "action": string(wes.Action), "backlogDepth": wes.BacklogDepth, "wip": wes.WIP}
	} else {
		facts[wesSource] = "unavailable"
	}
	if wfm != nil {
		facts[wfm.Source] = map[string]any{"pathId": wfm.PathId, "plannedHeads": wfm.PlannedHeads, "activeHeads": wfm.ActiveHeads, "understaffed": wfm.Understaffed}
	} else {
		facts[wfmSource] = "unavailable"
	}
	if fe != nil {
		facts[fe.Source] = map[string]any{"stuckTaskCount": fe.Count, "reasons": fe.Reasons}
	} else {
		facts[feSource] = "unavailable"
	}
	return facts
}

// gatherRebalanceSignal calls wes-work-planning's get_rebalance_recommendation
// and validates its action enum. A transport/call error degrades to (nil,
// nil) — signal unavailable, no hard failure. An unrecognized enum value is
// untrusted input and is returned as an error, per the reject-never-default
// guardrail.
func (uc *FlowBalanceAdvisory) gatherRebalanceSignal(ctx context.Context, pathId string) (*policy.RebalanceSignal, error) {
	if uc.Wes == nil {
		return nil, nil
	}

	raw, err := uc.Wes.GetRebalanceRecommendation(ctx, pathId)
	if err != nil {
		return nil, nil
	}

	action, err := policy.ParseRebalanceAction(raw.Action)
	if err != nil {
		return nil, fmt.Errorf("flow_balance_advisory: %w", err)
	}

	return &policy.RebalanceSignal{
		Source:       wesSource,
		PathId:       raw.PathId,
		Action:       action,
		BacklogDepth: raw.BacklogDepth,
		WIP:          raw.WIP,
	}, nil
}

// gatherStaffingSignal calls workforce-management's get_staffing_gap. A
// call error degrades to a nil signal, surfaced to the caller for logging.
func (uc *FlowBalanceAdvisory) gatherStaffingSignal(ctx context.Context, buildingId, shiftId, pathId string) (*policy.StaffingSignal, error) {
	if uc.WFM == nil {
		return nil, nil
	}

	raw, err := uc.WFM.GetStaffingGap(ctx, buildingId, shiftId, pathId)
	if err != nil {
		return nil, err
	}

	return &policy.StaffingSignal{
		Source:       wfmSource,
		PathId:       raw.PathId,
		PlannedHeads: raw.PlannedHeads,
		ActiveHeads:  raw.ActiveHeads,
		Understaffed: raw.Understaffed,
	}, nil
}

// gatherStuckTasksSignal calls fulfillment-execution's diagnose_stuck_tasks.
// A call error degrades to a nil signal, surfaced to the caller for
// logging.
func (uc *FlowBalanceAdvisory) gatherStuckTasksSignal(ctx context.Context, window time.Duration) (*policy.StuckTasksSignal, error) {
	if uc.FE == nil {
		return nil, nil
	}

	raw, err := uc.FE.DiagnoseStuckTasks(ctx, int(window.Seconds()))
	if err != nil {
		return nil, err
	}

	reasons := make([]string, 0, len(raw.Tasks))
	for _, t := range raw.Tasks {
		reasons = append(reasons, t.Reason)
	}

	return &policy.StuckTasksSignal{
		Source:  feSource,
		Count:   raw.Count,
		Reasons: reasons,
	}, nil
}

// gatherUtilizationSignal calls labor-performance's
// get_task_type_utilization for the task type bound to pathId, per
// uc.PathTaskTypes (the same PathId->ProcessPath binding DailyBrief
// consumes -- this method never invents its own resolution). A nil LP
// client, an unbound pathId, or a call error all degrade to (nil, err)
// with err carrying the reason for logging; a nil result is never treated
// by the caller as a hard failure (ADR-0004 fallback discipline).
func (uc *FlowBalanceAdvisory) gatherUtilizationSignal(ctx context.Context, pathId string, windowSeconds int64) (*policy.UtilizationSignal, error) {
	if uc.LP == nil {
		return nil, nil
	}
	taskType, ok := uc.PathTaskTypes[pathId]
	if !ok || taskType == "" {
		return nil, nil
	}

	raw, err := uc.LP.GetTaskTypeUtilization(ctx, taskType, windowSeconds)
	if err != nil {
		return nil, err
	}

	return &policy.UtilizationSignal{
		Source:         lpSource,
		TaskType:       raw.TaskType,
		Associates:     raw.Associates,
		WindowSeconds:  raw.WindowSeconds,
		TaskSeconds:    raw.TaskSeconds,
		IdleSeconds:    raw.IdleSeconds,
		OpenGapSeconds: raw.OpenGapSeconds,
		UtilizationPct: raw.UtilizationPct,
	}, nil
}

// formatUtilizationPct renders a nullable utilization percentage for the
// evidence trail: "null" when nothing was observed in the window, never a
// fabricated "0.0".
func formatUtilizationPct(pct *float64) string {
	if pct == nil {
		return "null"
	}
	return fmt.Sprintf("%.1f", *pct)
}
