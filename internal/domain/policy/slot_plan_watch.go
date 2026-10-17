// Package policy: slot-plan watch rules (ADR 0024's A14 section).
//
// Pure, side-effect-free classification of slotting-optimization's published
// slot-plan facts: which Approved plans have moves that are still not fully
// executed after the operator's staleness threshold. The facts are gathered
// one layer out (internal/application/usecases); this file only judges them.
package policy

import "time"

// SlotPlanFact is one Approved slot plan as the watch judges it: its
// identity, when it was approved, and how many of its moves have been
// executed. MovesTotal is the plan's own move count (the summary's
// move_count, or the execution view's moves_total when execution has
// started); MovesExecuted is 0 until inventory-storage records relocations.
type SlotPlanFact struct {
	PlanId        string
	SiteId        string
	ApprovedAt    time.Time
	MovesTotal    int
	MovesExecuted int
}

// StaleSlotPlan is one Approved plan flagged by the watch: approved more
// than the threshold ago with moves still not fully executed.
type StaleSlotPlan struct {
	PlanId        string
	SiteId        string
	ApprovedAt    time.Time
	Age           time.Duration
	MovesTotal    int
	MovesExecuted int
}

// SlotPlanWatchSection is the daily brief's slotting-optimization section.
// Stale is the flagged plans, oldest first. Checked counts every Approved
// plan the watch examined. Unavailable carries the section's explicit
// degradation lines: a failed or unreachable slotting-optimization call (or
// one plan whose published facts broke the contract) lands here, never as
// an error and never as a silent "no plans". A non-empty Unavailable MUST
// be read as "this section is incomplete", not "nothing to report".
type SlotPlanWatchSection struct {
	StaleAfter  time.Duration
	Stale       []StaleSlotPlan
	Checked     int
	NoApproved  bool // true when the plan list came back empty: slotting has no Approved plan at all
	Unavailable []string
}

// ClassifyStaleSlotPlans returns the Approved plans whose moves are not
// fully executed and whose approval age has reached the threshold, oldest
// first. A plan approved exactly the threshold ago counts as stale ("after
// 24h" includes the 24h mark); a plan with all moves executed, none to
// execute, or one younger than the threshold does not. A fact whose
// ApprovedAt is the zero time (an Approved plan slotting-optimization
// published without approved_at) is never classified — the use case drops
// it with an explicit unavailable line before building the fact.
func ClassifyStaleSlotPlans(facts []SlotPlanFact, now time.Time, threshold time.Duration) []StaleSlotPlan {
	var stale []StaleSlotPlan
	for _, f := range facts {
		if f.ApprovedAt.IsZero() {
			continue
		}
		age := now.Sub(f.ApprovedAt)
		if age < threshold {
			continue
		}
		if f.MovesExecuted >= f.MovesTotal {
			continue
		}
		stale = append(stale, StaleSlotPlan{
			PlanId:        f.PlanId,
			SiteId:        f.SiteId,
			ApprovedAt:    f.ApprovedAt,
			Age:           age,
			MovesTotal:    f.MovesTotal,
			MovesExecuted: f.MovesExecuted,
		})
	}
	sortStaleSlotPlansOldestFirst(stale)
	return stale
}

// sortStaleSlotPlansOldestFirst orders stale plans by approval instant
// (oldest — the one rotting longest — first), keeping the upstream's
// relative order for ties.
func sortStaleSlotPlansOldestFirst(plans []StaleSlotPlan) {
	for i := 1; i < len(plans); i++ {
		j := i
		for j > 0 && plans[j-1].ApprovedAt.After(plans[j].ApprovedAt) {
			plans[j-1], plans[j] = plans[j], plans[j-1]
			j--
		}
	}
}
