package policy

import (
	"testing"
	"time"
)

func slotWatchClock() time.Time {
	return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
}

func TestClassifyStaleSlotPlans_FlagsOldUnexecutedPlans(t *testing.T) {
	now := slotWatchClock()
	old := now.Add(-30 * time.Hour)
	facts := []SlotPlanFact{
		{PlanId: "plan-old", SiteId: "WH1", ApprovedAt: old, MovesTotal: 10, MovesExecuted: 3},
	}
	stale := ClassifyStaleSlotPlans(facts, now, 24*time.Hour)
	if len(stale) != 1 {
		t.Fatalf("a 30h-old plan with 3/10 moves executed must be flagged, got %+v", stale)
	}
	got := stale[0]
	if got.PlanId != "plan-old" || got.SiteId != "WH1" || got.MovesExecuted != 3 || got.MovesTotal != 10 {
		t.Fatalf("flagged plan must carry id, site and executed/total: %+v", got)
	}
	if got.Age != 30*time.Hour {
		t.Fatalf("age = %v, want 30h", got.Age)
	}
	if !got.ApprovedAt.Equal(old) {
		t.Fatalf("approvedAt = %v, want %v", got.ApprovedAt, old)
	}
}

func TestClassifyStaleSlotPlans_ThresholdBoundary(t *testing.T) {
	now := slotWatchClock()
	threshold := 24 * time.Hour
	facts := []SlotPlanFact{
		// Exactly at the threshold: "after 24h" includes the 24h mark.
		{PlanId: "at-boundary", ApprovedAt: now.Add(-threshold), MovesTotal: 4, MovesExecuted: 1},
		// One nanosecond short of the threshold: not yet.
		{PlanId: "just-young", ApprovedAt: now.Add(-(threshold - time.Nanosecond)), MovesTotal: 4, MovesExecuted: 1},
		{PlanId: "just-old", ApprovedAt: now.Add(-(threshold + time.Nanosecond)), MovesTotal: 4, MovesExecuted: 1},
	}
	stale := ClassifyStaleSlotPlans(facts, now, threshold)
	if len(stale) != 2 {
		t.Fatalf("exactly-at and just-past the threshold must both be flagged, got %+v", stale)
	}
	// Oldest first: just-old is one nanosecond older than at-boundary.
	if stale[0].PlanId != "just-old" || stale[1].PlanId != "at-boundary" {
		t.Fatalf("flagged plans = %+v, want [just-old at-boundary]", stale)
	}
}

func TestClassifyStaleSlotPlans_SkipsExecutedEmptyAndYoung(t *testing.T) {
	now := slotWatchClock()
	facts := []SlotPlanFact{
		{PlanId: "fully-executed", ApprovedAt: now.Add(-48 * time.Hour), MovesTotal: 8, MovesExecuted: 8},
		{PlanId: "no-moves", ApprovedAt: now.Add(-48 * time.Hour), MovesTotal: 0, MovesExecuted: 0},
		{PlanId: "young", ApprovedAt: now.Add(-2 * time.Hour), MovesTotal: 8, MovesExecuted: 0},
		{PlanId: "zero-time", ApprovedAt: time.Time{}, MovesTotal: 8, MovesExecuted: 0},
	}
	if stale := ClassifyStaleSlotPlans(facts, now, 24*time.Hour); len(stale) != 0 {
		t.Fatalf("fully-executed, move-less, young and zero-time plans must not be flagged, got %+v", stale)
	}
}

func TestClassifyStaleSlotPlans_OldestFirst(t *testing.T) {
	now := slotWatchClock()
	facts := []SlotPlanFact{
		{PlanId: "middle", ApprovedAt: now.Add(-36 * time.Hour), MovesTotal: 2, MovesExecuted: 0},
		{PlanId: "newest", ApprovedAt: now.Add(-25 * time.Hour), MovesTotal: 2, MovesExecuted: 0},
		{PlanId: "oldest", ApprovedAt: now.Add(-60 * time.Hour), MovesTotal: 2, MovesExecuted: 0},
	}
	stale := ClassifyStaleSlotPlans(facts, now, 24*time.Hour)
	if len(stale) != 3 {
		t.Fatalf("expected all three flagged, got %+v", stale)
	}
	for i, want := range []string{"oldest", "middle", "newest"} {
		if stale[i].PlanId != want {
			t.Fatalf("stale[%d] = %s, want %s (oldest first): %+v", i, stale[i].PlanId, want, stale)
		}
	}
}

func TestClassifyStaleSlotPlans_EmptyFactsYieldEmpty(t *testing.T) {
	if stale := ClassifyStaleSlotPlans(nil, slotWatchClock(), 24*time.Hour); len(stale) != 0 {
		t.Fatalf("no facts must yield no stale plans, got %+v", stale)
	}
}
