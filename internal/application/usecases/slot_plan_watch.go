// Package usecases: SlotPlanWatch (ADR 0024) -- the daily brief's
// slotting-optimization section: which Approved forward-pick slot plans have
// moves that are still not fully executed after the operator's staleness
// threshold (SLOT_PLAN_STALE_AGE, default 24h).
package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

const (
	// slotPlanPageSize is list_slot_plans' own maximum, so the scan of
	// Approved plans needs as few calls as possible.
	slotPlanPageSize = 500
	// slotPlanMaxPages bounds the Approved-plan scan to 10 pages (5,000
	// plans). Like the inbound outlook's page bound it is a work bound,
	// not a business input: when more plans remain the section says so.
	slotPlanMaxPages = 10
	// slotPlanDefaultStaleAfter is the watch's default threshold: an
	// Approved plan a day old with unexecuted moves is worth a line in
	// the daily brief. SLOT_PLAN_STALE_AGE overrides it.
	slotPlanDefaultStaleAfter = 24 * time.Hour
	// slotPlanApprovedState is slotting-optimization's own plan-state
	// literal for a plan the operator has approved.
	slotPlanApprovedState = "Approved"
)

// SlotPlanWatch is the ADR 0024 read-only use case over
// slotting-optimization's list_slot_plans and get_slot_plan tools. It
// invents nothing: plan ids, states, approval instants and move counts are
// slotting-optimization's published facts, "now" comes from the clock, and
// the only operator input is the staleness threshold.
//
// A nil *SlotPlanWatch, or one with a nil Slotting port, is "not
// configured" (SLOTTING_OPTIMIZATION_MCP_ENDPOINT unset): Execute returns
// nil and the daily brief omits the section entirely. When configured, the
// section NEVER fails the brief: an unreachable slotting-optimization, a
// failed call or a contract-breaking published fact degrades to an explicit
// unavailable line on the section (policy.SlotPlanWatchSection.Unavailable),
// never an error and never a silent "no plans".
type SlotPlanWatch struct {
	Slotting ports.SlottingOptimizationClient
	// StaleAfter is SLOT_PLAN_STALE_AGE. Non-positive means the default
	// (24h); unlike the inbound outlook's stale-receipt age there IS a
	// sensible default here, because "a day-old half-executed re-slot" is
	// the operational question this section exists to answer.
	StaleAfter time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Execute builds the slot-plan watch section, or returns nil when the use
// case is not configured (see the type's doc comment).
func (uc *SlotPlanWatch) Execute(ctx context.Context, now time.Time) *policy.SlotPlanWatchSection {
	if uc == nil || uc.Slotting == nil {
		return nil
	}
	threshold := uc.staleAfter()
	section := &policy.SlotPlanWatchSection{StaleAfter: threshold}

	summaries, complete, listErr := uc.scanApprovedPlans(ctx)
	switch {
	case listErr != nil:
		section.Unavailable = append(section.Unavailable, "slotting-optimization list_slot_plans: "+listErr.Error())
		return section
	case !complete:
		section.Unavailable = append(section.Unavailable, fmt.Sprintf("slotting-optimization list_slot_plans: page bound of %d pages reached with more Approved plans pending; the list below is a lower bound", slotPlanMaxPages))
	}
	section.Checked = len(summaries)
	if len(summaries) == 0 {
		// An explicit "no plans" line: an empty slotting-optimization is a
		// fact worth one line, never a silently absent section.
		section.NoApproved = true
		section.Stale = []policy.StaleSlotPlan{}
		return section
	}

	facts := make([]policy.SlotPlanFact, 0, len(summaries))
	for _, s := range summaries {
		fact, problem := uc.factOf(ctx, s, now, threshold)
		if problem != "" {
			section.Unavailable = append(section.Unavailable, problem)
			continue
		}
		facts = append(facts, fact)
	}
	section.Stale = policy.ClassifyStaleSlotPlans(facts, now, threshold)
	return section
}

// staleAfter resolves the threshold: the configured value when positive,
// the 24h default otherwise.
func (uc *SlotPlanWatch) staleAfter() time.Duration {
	if uc.StaleAfter > 0 {
		return uc.StaleAfter
	}
	return slotPlanDefaultStaleAfter
}

// scanApprovedPlans pages list_slot_plans filtered to the Approved state.
// complete is false when the page bound was reached with a cursor still
// pending.
func (uc *SlotPlanWatch) scanApprovedPlans(ctx context.Context) ([]ports.SlotPlanSummary, bool, error) {
	var all []ports.SlotPlanSummary
	cursor := ""
	for page := 0; page < slotPlanMaxPages; page++ {
		result, err := uc.Slotting.ListSlotPlans(ctx, ports.SlotPlanListQuery{
			Limit:  slotPlanPageSize,
			Cursor: cursor,
			State:  slotPlanApprovedState,
		})
		if err != nil {
			return nil, false, fmt.Errorf("list_slot_plans (Approved): %w", err)
		}
		all = append(all, result.Items...)
		if result.NextCursor == "" {
			return all, true, nil
		}
		cursor = result.NextCursor
	}
	return all, false, nil
}

// factOf builds the policy fact for one Approved summary, fetching its
// execution progress from get_slot_plan only when the plan is old enough
// that the counters could flag it. A returned problem string is the
// section's unavailable line for that plan; the watch then carries on with
// the next one.
func (uc *SlotPlanWatch) factOf(ctx context.Context, s ports.SlotPlanSummary, now time.Time, threshold time.Duration) (policy.SlotPlanFact, string) {
	approvedAt, err := parseSlotPlanInstant("approved_at of plan "+s.PlanId, s.ApprovedAt)
	if err != nil {
		return policy.SlotPlanFact{}, err.Error()
	}
	if s.MoveCount == 0 {
		// No moves planned: nothing to execute, nothing to watch.
		return policy.SlotPlanFact{PlanId: s.PlanId, SiteId: s.SiteId, ApprovedAt: approvedAt}, ""
	}
	if now.Sub(approvedAt) < threshold {
		// Younger than the threshold: the policy classifier drops it on
		// age alone, so its execution counters cannot change the verdict
		// and no get_slot_plan call is spent on it.
		return policy.SlotPlanFact{PlanId: s.PlanId, SiteId: s.SiteId, ApprovedAt: approvedAt, MovesTotal: s.MoveCount}, ""
	}
	plan, err := uc.Slotting.GetSlotPlan(ctx, s.PlanId)
	if err != nil {
		return policy.SlotPlanFact{}, fmt.Sprintf("slotting-optimization get_slot_plan (%s): %v", s.PlanId, err)
	}
	total, executed := slotPlanProgress(plan, s.MoveCount)
	return policy.SlotPlanFact{
		PlanId:        s.PlanId,
		SiteId:        s.SiteId,
		ApprovedAt:    approvedAt,
		MovesTotal:    total,
		MovesExecuted: executed,
	}, ""
}

// slotPlanProgress reads one plan's execution counters. When the execution
// view is present it is the authority (moves_total, moves_executed); when
// slotting-optimization has recorded no execution at all (a null
// execution), the summary's move count is the total and nothing has been
// executed.
func slotPlanProgress(plan ports.SlotPlan, summaryMoveCount int) (total, executed int) {
	if plan.Execution == nil {
		return summaryMoveCount, 0
	}
	return plan.Execution.MovesTotal, plan.Execution.MovesExecuted
}

// parseSlotPlanInstant parses an RFC 3339 instant
// slotting-optimization published; an empty or malformed value is a
// contract violation for an Approved plan (approved_at is its defining
// instant) and comes back as the unavailable line for that plan.
func parseSlotPlanInstant(what, raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("slotting-optimization published no %s", what)
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("slotting-optimization published an unparseable %s %q: %w", what, raw, err)
	}
	return t, nil
}
