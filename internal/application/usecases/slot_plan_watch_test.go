package usecases_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

func slotWatchNow() time.Time {
	return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
}

func rfc3339(t *testing.T, ts time.Time) string {
	t.Helper()
	return ts.UTC().Format(time.RFC3339)
}

func approvedSummary(t *testing.T, planId, siteId string, approvedAt time.Time, moveCount int) ports.SlotPlanSummary {
	t.Helper()
	return ports.SlotPlanSummary{
		PlanId: planId, SiteId: siteId, State: "Approved", Policy: "velocity",
		WindowFrom: "2026-10-01T00:00:00Z", WindowTo: "2026-10-29T00:00:00Z",
		GeneratedAt: rfc3339(t, approvedAt.Add(-time.Hour)), ApprovedAt: rfc3339(t, approvedAt),
		MoveCount: moveCount, AssignmentCount: 10, UnassignedCount: 0, Version: 2,
	}
}

func TestSlotPlanWatch_NotConfigured_ReturnsNilSection(t *testing.T) {
	var uc *usecases.SlotPlanWatch
	if got := uc.Execute(context.Background(), slotWatchNow()); got != nil {
		t.Fatalf("a nil use case must produce no section, got %+v", got)
	}
	if got := (&usecases.SlotPlanWatch{}).Execute(context.Background(), slotWatchNow()); got != nil {
		t.Fatalf("a nil slotting port must produce no section, got %+v", got)
	}
}

func TestSlotPlanWatch_FlagsStaleApprovedPlanWithExecutedOverTotal(t *testing.T) {
	now := slotWatchNow()
	stale := now.Add(-30 * time.Hour)
	fresh := now.Add(-2 * time.Hour)
	slotting := &fakeSlotting{
		pages: map[string]ports.SlotPlanPage{"": {Items: []ports.SlotPlanSummary{
			approvedSummary(t, "plan-stale", "WH1", stale, 10),
			approvedSummary(t, "plan-fresh", "WH1", fresh, 10),
		}}},
		plans: map[string]ports.SlotPlan{
			"plan-stale": {PlanId: "plan-stale", SiteId: "WH1", State: "Approved", Execution: &ports.SlotPlanExecution{MovesTotal: 10, MovesExecuted: 3}},
		},
	}
	uc := &usecases.SlotPlanWatch{Slotting: slotting, StaleAfter: 24 * time.Hour}

	section := uc.Execute(context.Background(), now)
	if section == nil {
		t.Fatal("a configured watch must always produce a section")
	}
	if section.StaleAfter != 24*time.Hour {
		t.Fatalf("threshold = %v, want 24h", section.StaleAfter)
	}
	if section.Checked != 2 {
		t.Fatalf("checked = %d, want 2 (both Approved plans)", section.Checked)
	}
	if len(section.Unavailable) != 0 {
		t.Fatalf("a healthy scan must carry no unavailable lines, got %v", section.Unavailable)
	}
	if len(section.Stale) != 1 {
		t.Fatalf("only the 30h-old 3/10 plan must be flagged, got %+v", section.Stale)
	}
	flagged := section.Stale[0]
	if flagged.PlanId != "plan-stale" || flagged.SiteId != "WH1" || flagged.MovesExecuted != 3 || flagged.MovesTotal != 10 || flagged.Age != 30*time.Hour {
		t.Fatalf("flagged plan must carry plan id, site, age and executed/total: %+v", flagged)
	}
	if slotting.getCalls != 1 {
		t.Fatalf("get_slot_plan must be called only for the plan past the threshold (young plans need no counters), got %d calls", slotting.getCalls)
	}
	if slotting.lastQuery.State != "Approved" {
		t.Fatalf("the scan must filter to the Approved state, got %q", slotting.lastQuery.State)
	}
}

func TestSlotPlanWatch_NullExecutionMeansNothingExecuted(t *testing.T) {
	now := slotWatchNow()
	old := now.Add(-48 * time.Hour)
	slotting := &fakeSlotting{
		pages: map[string]ports.SlotPlanPage{"": {Items: []ports.SlotPlanSummary{
			approvedSummary(t, "plan-null-exec", "WH2", old, 7),
		}}},
		// get_slot_plan answers a plan with NO execution view at all.
		plans: map[string]ports.SlotPlan{"plan-null-exec": {PlanId: "plan-null-exec", SiteId: "WH2", State: "Approved"}},
	}
	uc := &usecases.SlotPlanWatch{Slotting: slotting, StaleAfter: 24 * time.Hour}

	section := uc.Execute(context.Background(), now)
	if len(section.Stale) != 1 {
		t.Fatalf("a plan with no execution recorded is 0/N executed and must be flagged, got %+v", section.Stale)
	}
	if section.Stale[0].MovesTotal != 7 || section.Stale[0].MovesExecuted != 0 {
		t.Fatalf("null execution must fall back to the summary's move count with nothing executed: %+v", section.Stale[0])
	}
}

func TestSlotPlanWatch_ThresholdBoundary(t *testing.T) {
	now := slotWatchNow()
	exactly := now.Add(-24 * time.Hour)
	slotting := &fakeSlotting{
		pages: map[string]ports.SlotPlanPage{"": {Items: []ports.SlotPlanSummary{
			approvedSummary(t, "plan-boundary", "WH1", exactly, 4),
		}}},
		plans: map[string]ports.SlotPlan{
			"plan-boundary": {Execution: &ports.SlotPlanExecution{MovesTotal: 4, MovesExecuted: 1}},
		},
	}
	uc := &usecases.SlotPlanWatch{Slotting: slotting, StaleAfter: 24 * time.Hour}

	section := uc.Execute(context.Background(), now)
	if len(section.Stale) != 1 {
		t.Fatalf("a plan approved exactly the threshold ago counts as after the threshold, got %+v", section.Stale)
	}
}

func TestSlotPlanWatch_DefaultThresholdIs24Hours(t *testing.T) {
	now := slotWatchNow()
	slotting := &fakeSlotting{
		pages: map[string]ports.SlotPlanPage{"": {Items: []ports.SlotPlanSummary{
			approvedSummary(t, "plan-day", "WH1", now.Add(-25*time.Hour), 4),
		}}},
		plans: map[string]ports.SlotPlan{"plan-day": {Execution: &ports.SlotPlanExecution{MovesTotal: 4, MovesExecuted: 1}}},
	}
	// StaleAfter left zero: the use case must apply the 24h default.
	uc := &usecases.SlotPlanWatch{Slotting: slotting}

	section := uc.Execute(context.Background(), now)
	if section.StaleAfter != 24*time.Hour {
		t.Fatalf("threshold = %v, want the 24h default", section.StaleAfter)
	}
	if len(section.Stale) != 1 {
		t.Fatalf("a 25h-old plan with unexecuted moves must be flagged under the default threshold, got %+v", section.Stale)
	}
}

func TestSlotPlanWatch_UnreachableUpstreamDegradesToUnavailableLine(t *testing.T) {
	slotting := &fakeSlotting{listErr: errors.New("connect: connection refused")}
	uc := &usecases.SlotPlanWatch{Slotting: slotting, StaleAfter: 24 * time.Hour}

	section := uc.Execute(context.Background(), slotWatchNow())
	if section == nil {
		t.Fatal("an unreachable upstream must still produce the section")
	}
	if len(section.Unavailable) != 1 || !strings.Contains(section.Unavailable[0], "list_slot_plans") || !strings.Contains(section.Unavailable[0], "connection refused") {
		t.Fatalf("an unreachable slotting-optimization must surface as exactly one explicit unavailable line, got %v", section.Unavailable)
	}
	if len(section.Stale) != 0 {
		t.Fatalf("no plans can be flagged when the list call failed, got %+v", section.Stale)
	}
}

func TestSlotPlanWatch_NoApprovedPlansIsAnExplicitLine(t *testing.T) {
	slotting := &fakeSlotting{pages: map[string]ports.SlotPlanPage{}}
	uc := &usecases.SlotPlanWatch{Slotting: slotting, StaleAfter: 24 * time.Hour}

	section := uc.Execute(context.Background(), slotWatchNow())
	if !section.NoApproved {
		t.Fatalf("an empty plan list must set NoApproved (an explicit 'no plans' state, never a silent section), got %+v", section)
	}
	if len(section.Stale) != 0 || len(section.Unavailable) != 0 {
		t.Fatalf("an empty plan list flags nothing and degrades nothing, got %+v", section)
	}
}

func TestSlotPlanWatch_PlanContractViolationsDegradePerPlan(t *testing.T) {
	now := slotWatchNow()
	old := now.Add(-48 * time.Hour)
	broken := approvedSummary(t, "plan-broken", "WH1", old, 5)
	broken.ApprovedAt = "" // an Approved plan without approved_at breaks the contract
	slotting := &fakeSlotting{
		pages: map[string]ports.SlotPlanPage{"": {Items: []ports.SlotPlanSummary{
			broken,
			approvedSummary(t, "plan-ok", "WH1", old, 5),
		}}},
		plans: map[string]ports.SlotPlan{
			"plan-ok":   {Execution: &ports.SlotPlanExecution{MovesTotal: 5, MovesExecuted: 1}},
			"plan-dead": {},
		},
		getErr: map[string]error{"plan-dead": errors.New("boom")},
	}
	uc := &usecases.SlotPlanWatch{Slotting: slotting, StaleAfter: 24 * time.Hour}

	section := uc.Execute(context.Background(), now)
	if len(section.Unavailable) != 1 || !strings.Contains(section.Unavailable[0], "approved_at of plan plan-broken") {
		t.Fatalf("a plan without approved_at must degrade to one unavailable line naming it, got %v", section.Unavailable)
	}
	if len(section.Stale) != 1 || section.Stale[0].PlanId != "plan-ok" {
		t.Fatalf("the healthy plan must still be flagged, got %+v", section.Stale)
	}
}

func TestSlotPlanWatch_PageBoundIsReportedAsLowerBound(t *testing.T) {
	now := slotWatchNow()
	// Pages that always hand back another cursor: the 10-page bound is hit.
	endless := func(cursor string) ports.SlotPlanPage {
		return ports.SlotPlanPage{Items: []ports.SlotPlanSummary{approvedSummary(t, "plan-"+cursor, "WH1", now.Add(-48*time.Hour), 1)}, NextCursor: cursor + "x"}
	}
	slotting := &fakeSlotting{pages: map[string]ports.SlotPlanPage{
		"":          endless(""),
		"x":         endless("x"),
		"xx":        endless("xx"),
		"xxx":       endless("xxx"),
		"xxxx":      endless("xxxx"),
		"xxxxx":     endless("xxxxx"),
		"xxxxxx":    endless("xxxxxx"),
		"xxxxxxx":   endless("xxxxxxx"),
		"xxxxxxxx":  endless("xxxxxxxx"),
		"xxxxxxxxx": endless("xxxxxxxxx"),
	}}
	uc := &usecases.SlotPlanWatch{Slotting: slotting, StaleAfter: 24 * time.Hour}

	section := uc.Execute(context.Background(), now)
	found := false
	for _, line := range section.Unavailable {
		if strings.Contains(line, "lower bound") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reaching the page bound must add an explicit lower-bound line, got %v", section.Unavailable)
	}
	if section.Checked != 10 {
		t.Fatalf("checked = %d, want the 10 plans of the 10 bounded pages", section.Checked)
	}
}

func TestSlotPlanWatch_ZeroMovePlanNeedsNoGetSlotPlan(t *testing.T) {
	now := slotWatchNow()
	slotting := &fakeSlotting{
		pages: map[string]ports.SlotPlanPage{"": {Items: []ports.SlotPlanSummary{
			approvedSummary(t, "plan-no-moves", "WH1", now.Add(-48*time.Hour), 0),
		}}},
	}
	uc := &usecases.SlotPlanWatch{Slotting: slotting, StaleAfter: 24 * time.Hour}

	section := uc.Execute(context.Background(), now)
	if len(section.Stale) != 0 {
		t.Fatalf("a plan with no moves cannot be unexecuted, got %+v", section.Stale)
	}
	if slotting.getCalls != 0 {
		t.Fatalf("a move-less plan must not cost a get_slot_plan call, got %d", slotting.getCalls)
	}
}
