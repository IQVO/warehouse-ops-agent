package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestDailyBrief_Execute_HealthyPath_NoExceptions(t *testing.T) {
	targets := []usecases.PathTarget{
		{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK", BuildingId: "wh1", ShiftId: "shift-1"},
	}
	facility := &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "Fulfilment Centre One"}}}}
	wes := &fakeWes{backlog: map[string]ports.BacklogTelemetry{
		"pick-zone-a": {PathId: "pick-zone-a", BacklogDepth: 4, WIP: 2, OverAlarmThreshold: false},
	}}
	fe := &fakeFe{
		queue: map[string]ports.QueueStatus{"PICK": {ProcessPath: "PICK", Depth: 3}},
		stuck: ports.StuckTasksResult{Count: 0},
	}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{
		"pick-zone-a": {PathId: "pick-zone-a", PlannedHeads: 3, ActiveHeads: 3, Understaffed: false},
	}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}
	brief := uc.Execute(context.Background())

	if len(brief.Sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(brief.Sites))
	}
	if got := brief.Sites[0].SiteName; got != "Fulfilment Centre One" {
		t.Errorf("site name = %q, want resolved from facility-layout list_sites", got)
	}
	if len(brief.Sites[0].Paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(brief.Sites[0].Paths))
	}
	if len(brief.OpenExceptions) != 0 {
		t.Errorf("expected no open exceptions for a healthy path, got %d", len(brief.OpenExceptions))
	}
}

func TestDailyBrief_Execute_ImbalancedPath_ProducesRankedException(t *testing.T) {
	targets := []usecases.PathTarget{
		{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK", BuildingId: "wh1", ShiftId: "shift-1"},
	}
	facility := &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "Fulfilment Centre One"}}}}
	wes := &fakeWes{backlog: map[string]ports.BacklogTelemetry{
		"pick-zone-a": {PathId: "pick-zone-a", BacklogDepth: 50, WIP: 10, OverAlarmThreshold: true},
	}}
	fe := &fakeFe{
		queue: map[string]ports.QueueStatus{"PICK": {ProcessPath: "PICK", Depth: 40}},
		stuck: ports.StuckTasksResult{Count: 2, Tasks: []ports.StuckTask{
			{TaskId: "t1", Type: "PICK", Reason: "lease expired"},
			{TaskId: "t2", Type: "PICK", Reason: "lease expired"},
		}},
	}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{
		"pick-zone-a": {PathId: "pick-zone-a", PlannedHeads: 5, ActiveHeads: 2, Understaffed: true},
	}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}
	brief := uc.Execute(context.Background())

	if len(brief.OpenExceptions) != 1 {
		t.Fatalf("expected exactly 1 open exception (3 correlated signals), got %d: %+v", len(brief.OpenExceptions), brief.OpenExceptions)
	}
	exc := brief.OpenExceptions[0]
	if exc.Severity != policy.SeverityCritical {
		t.Errorf("severity = %q, want critical (3 signals)", exc.Severity)
	}
	if exc.PathId != "pick-zone-a" || exc.SiteCode != "WH1" {
		t.Errorf("exception identity = (%q,%q), want (WH1,pick-zone-a)", exc.SiteCode, exc.PathId)
	}
	if len(exc.Evidence) == 0 {
		t.Errorf("expected a non-empty evidence trail on the exception")
	}
}

func TestDailyBrief_Execute_PartialUpstreamFailure_DegradesGracefully(t *testing.T) {
	targets := []usecases.PathTarget{
		{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK", BuildingId: "wh1", ShiftId: "shift-1"},
	}
	facility := &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "Fulfilment Centre One"}}}}
	// wes is down for this path.
	wes := &fakeWes{err: map[string]error{"pick-zone-a": errors.New("connect: connection refused")}}
	fe := &fakeFe{
		queue: map[string]ports.QueueStatus{"PICK": {ProcessPath: "PICK", Depth: 3}},
		stuck: ports.StuckTasksResult{Count: 0},
	}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{
		"pick-zone-a": {PathId: "pick-zone-a", PlannedHeads: 3, ActiveHeads: 3, Understaffed: false},
	}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}

	// Execute must not panic despite the upstream failure.
	brief := uc.Execute(context.Background())

	if len(brief.Sites) != 1 || len(brief.Sites[0].Paths) != 1 {
		t.Fatalf("expected a partial brief to still be produced, got %+v", brief)
	}
	pb := brief.Sites[0].Paths[0]
	if pb.Backlog != nil {
		t.Errorf("expected nil backlog fact for an unavailable source, got %+v", pb.Backlog)
	}
	if len(pb.Unavailable) != 1 {
		t.Fatalf("expected exactly 1 unavailable-source note, got %d: %v", len(pb.Unavailable), pb.Unavailable)
	}
	if pb.Queue == nil || pb.Staffing == nil {
		t.Errorf("expected the OTHER sources' facts to still be present: queue=%+v staffing=%+v", pb.Queue, pb.Staffing)
	}
}

func TestDailyBrief_Execute_FacilityUnavailable_ReturnsEmptyNotPanic(t *testing.T) {
	targets := []usecases.PathTarget{
		{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"},
	}
	facility := &fakeFacility{err: errors.New("connect: connection refused")}
	wes := &fakeWes{backlog: map[string]ports.BacklogTelemetry{"pick-zone-a": {PathId: "pick-zone-a"}}}
	fe := &fakeFe{queue: map[string]ports.QueueStatus{"PICK": {}}, stuck: ports.StuckTasksResult{}}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{"pick-zone-a": {}}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}
	brief := uc.Execute(context.Background())

	// Site name resolution failed, but the path is still synthesized under
	// its configured SiteCode with an empty name rather than dropped.
	if len(brief.Sites) != 1 {
		t.Fatalf("expected 1 site (grouped by configured SiteCode) even with facility-layout down, got %d", len(brief.Sites))
	}
	if brief.Sites[0].SiteName != "" {
		t.Errorf("expected empty site name when list_sites failed, got %q", brief.Sites[0].SiteName)
	}
}

func TestDailyBrief_Execute_MultiplePaths_GroupedBySite(t *testing.T) {
	targets := []usecases.PathTarget{
		{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"},
		{SiteCode: "WH1", PathId: "pack-station-1", ProcessPath: "PACK"},
		{SiteCode: "WH2", PathId: "pick-zone-b", ProcessPath: "PICK"},
	}
	facility := &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{
		{Code: "WH1", Name: "One"}, {Code: "WH2", Name: "Two"},
	}}}
	wes := &fakeWes{backlog: map[string]ports.BacklogTelemetry{}}
	fe := &fakeFe{queue: map[string]ports.QueueStatus{}, stuck: ports.StuckTasksResult{}}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}
	brief := uc.Execute(context.Background())

	if len(brief.Sites) != 2 {
		t.Fatalf("expected 2 distinct sites, got %d", len(brief.Sites))
	}
	var wh1 *policy.SiteBrief
	for i := range brief.Sites {
		if brief.Sites[i].SiteCode == "WH1" {
			wh1 = &brief.Sites[i]
		}
	}
	if wh1 == nil {
		t.Fatalf("WH1 site not found")
	}
	if len(wh1.Paths) != 2 {
		t.Errorf("expected 2 paths grouped under WH1, got %d", len(wh1.Paths))
	}
}

func TestDailyBrief_Execute_NoTargets_EmptyBrief(t *testing.T) {
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{},
		Wes:      &fakeWes{},
		Fe:       &fakeFe{},
		Wfm:      &fakeWfm{},
		Now:      fixedClock(time.Unix(0, 0)),
	}
	brief := uc.Execute(context.Background())

	if len(brief.Sites) != 0 || len(brief.OpenExceptions) != 0 {
		t.Fatalf("expected an empty brief with no configured targets, got %+v", brief)
	}
	if brief.GeneratedAt.IsZero() {
		t.Errorf("expected GeneratedAt to be stamped even for an empty brief")
	}
}

func TestDailyBrief_Execute_DefaultClock_UsesRealTime(t *testing.T) {
	// Now left nil: Execute must fall back to the real clock rather than
	// panicking or leaving GeneratedAt zero.
	before := time.Now()
	uc := &usecases.DailyBrief{Facility: &fakeFacility{}, Wes: &fakeWes{}, Fe: &fakeFe{}, Wfm: &fakeWfm{}}
	brief := uc.Execute(context.Background())
	after := time.Now()

	if brief.GeneratedAt.Before(before) || brief.GeneratedAt.After(after) {
		t.Errorf("GeneratedAt = %v, want between %v and %v", brief.GeneratedAt, before, after)
	}
}

func TestDailyBrief_Execute_MultiplePathsSameSite_RankedCriticalFirst(t *testing.T) {
	// Path A: only 1 signal (no exception). Path B: 3 signals (critical).
	// Path C: 2 signals (warning). OpenExceptions must come back
	// critical-first regardless of target order.
	targets := []usecases.PathTarget{
		{SiteCode: "WH1", PathId: "path-a", ProcessPath: "PICK"},
		{SiteCode: "WH1", PathId: "path-b", ProcessPath: "PACK"},
		{SiteCode: "WH1", PathId: "path-c", ProcessPath: "SLAM"},
	}
	facility := &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}}
	wes := &fakeWes{backlog: map[string]ports.BacklogTelemetry{
		"path-a": {OverAlarmThreshold: false},
		"path-b": {OverAlarmThreshold: true},
		"path-c": {OverAlarmThreshold: true},
	}}
	fe := &fakeFe{
		queue: map[string]ports.QueueStatus{"PICK": {}, "PACK": {}, "SLAM": {}},
		stuck: ports.StuckTasksResult{Count: 1, Tasks: []ports.StuckTask{{TaskId: "t1", Type: "PACK"}}},
	}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{
		"path-a": {Understaffed: false},
		"path-b": {Understaffed: true},
		"path-c": {Understaffed: true},
	}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}
	brief := uc.Execute(context.Background())

	if len(brief.OpenExceptions) != 2 {
		t.Fatalf("expected 2 open exceptions (path-b critical, path-c warning), got %d: %+v", len(brief.OpenExceptions), brief.OpenExceptions)
	}
	if brief.OpenExceptions[0].Severity != policy.SeverityCritical || brief.OpenExceptions[0].PathId != "path-b" {
		t.Errorf("first exception = %+v, want critical path-b first", brief.OpenExceptions[0])
	}
	if brief.OpenExceptions[1].Severity != policy.SeverityWarning || brief.OpenExceptions[1].PathId != "path-c" {
		t.Errorf("second exception = %+v, want warning path-c second", brief.OpenExceptions[1])
	}
}

func TestDailyBrief_Execute_NilClient_TreatedAsUnavailable(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	uc := &usecases.DailyBrief{
		// Facility and Wfm deliberately nil: not every deployment wires
		// every upstream client (e.g. a phased rollout).
		Wes:     &fakeWes{backlog: map[string]ports.BacklogTelemetry{"pick-zone-a": {PathId: "pick-zone-a"}}},
		Fe:      &fakeFe{queue: map[string]ports.QueueStatus{"PICK": {}}, stuck: ports.StuckTasksResult{}},
		Targets: targets,
		Now:     fixedClock(time.Unix(0, 0)),
	}

	brief := uc.Execute(context.Background())
	if len(brief.Sites) != 1 || len(brief.Sites[0].Paths) != 1 {
		t.Fatalf("expected a degraded but non-panicking brief, got %+v", brief)
	}
	pb := brief.Sites[0].Paths[0]
	if pb.Staffing != nil {
		t.Errorf("expected nil staffing fact when Wfm client is nil, got %+v", pb.Staffing)
	}
	found := false
	for _, u := range pb.Unavailable {
		if u == "workforce-management: client not configured" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an explicit 'client not configured' note for the nil Wfm client, got %v", pb.Unavailable)
	}
}

func TestDailyBrief_Execute_StuckTaskCount_FilteredByProcessPath(t *testing.T) {
	// diagnose_stuck_tasks is queue-wide; the use case must filter its
	// flat task list down to the tasks matching THIS path's process-path
	// queue name before counting.
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	facility := &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}}
	wes := &fakeWes{backlog: map[string]ports.BacklogTelemetry{"pick-zone-a": {}}}
	fe := &fakeFe{
		queue: map[string]ports.QueueStatus{"PICK": {}},
		stuck: ports.StuckTasksResult{Count: 3, Tasks: []ports.StuckTask{
			{TaskId: "t1", Type: "PICK"},
			{TaskId: "t2", Type: "PACK"}, // different queue: must not count against pick-zone-a.
			{TaskId: "t3", Type: "PICK"},
		}},
	}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{"pick-zone-a": {}}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}
	brief := uc.Execute(context.Background())

	pb := brief.Sites[0].Paths[0]
	if pb.Stuck == nil || pb.Stuck.Count != 2 {
		t.Fatalf("expected stuck count filtered to PICK-type tasks only (2), got %+v", pb.Stuck)
	}
}

func TestDailyBrief_Execute_EmptyProcessPath_CountsUnfiltered(t *testing.T) {
	// An empty ProcessPath (misconfigured target) must not panic or silently
	// drop every stuck task; it falls back to the tool's own total count.
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: ""}}
	facility := &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}}
	wes := &fakeWes{backlog: map[string]ports.BacklogTelemetry{"pick-zone-a": {}}}
	fe := &fakeFe{
		queue: map[string]ports.QueueStatus{"": {}},
		stuck: ports.StuckTasksResult{Count: 2, Tasks: []ports.StuckTask{{TaskId: "t1", Type: "PICK"}, {TaskId: "t2", Type: "PACK"}}},
	}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{"pick-zone-a": {}}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}
	brief := uc.Execute(context.Background())

	pb := brief.Sites[0].Paths[0]
	if pb.Stuck == nil || pb.Stuck.Count != 2 {
		t.Fatalf("expected unfiltered stuck count (2) when ProcessPath is empty, got %+v", pb.Stuck)
	}
}

func TestDailyBrief_Execute_StuckTasksError_Unavailable(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	facility := &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}}
	wes := &fakeWes{backlog: map[string]ports.BacklogTelemetry{"pick-zone-a": {}}}
	fe := &fakeFe{
		queue:    map[string]ports.QueueStatus{"PICK": {}},
		stuckErr: errors.New("connect: connection refused"),
	}
	wfm := &fakeWfm{gaps: map[string]ports.StaffingGap{"pick-zone-a": {}}}

	uc := &usecases.DailyBrief{Facility: facility, Wes: wes, Fe: fe, Wfm: wfm, Targets: targets, Now: fixedClock(time.Unix(0, 0))}
	brief := uc.Execute(context.Background())

	pb := brief.Sites[0].Paths[0]
	if pb.Stuck != nil {
		t.Errorf("expected nil stuck fact when diagnose_stuck_tasks fails, got %+v", pb.Stuck)
	}
	found := false
	for _, u := range pb.Unavailable {
		if u == "fulfillment-execution diagnose_stuck_tasks: connect: connection refused" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the stuck-tasks failure noted as unavailable, got %v", pb.Unavailable)
	}
}

func TestDailyBrief_Execute_NilFeClient_QueueAndStuckBothUnavailable(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}},
		Wes:      &fakeWes{backlog: map[string]ports.BacklogTelemetry{"pick-zone-a": {}}},
		Wfm:      &fakeWfm{gaps: map[string]ports.StaffingGap{"pick-zone-a": {}}},
		Targets:  targets,
		Now:      fixedClock(time.Unix(0, 0)),
	}
	brief := uc.Execute(context.Background())

	pb := brief.Sites[0].Paths[0]
	if pb.Queue != nil || pb.Stuck != nil {
		t.Errorf("expected nil queue and stuck facts when Fe client is nil, got queue=%+v stuck=%+v", pb.Queue, pb.Stuck)
	}
	if len(pb.Unavailable) != 1 {
		t.Errorf("expected exactly 1 unavailable note (fe covers both queue+stuck when nil), got %v", pb.Unavailable)
	}
}

func TestDailyBrief_Execute_SlotPlanWatchSectionRidesTheBrief(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	slotting := &fakeSlotting{
		pages: map[string]ports.SlotPlanPage{"": {Items: []ports.SlotPlanSummary{{
			PlanId: "plan-stale", SiteId: "WH1", State: "Approved",
			ApprovedAt: now.Add(-30 * time.Hour).UTC().Format(time.RFC3339), MoveCount: 6,
		}}}},
		plans: map[string]ports.SlotPlan{
			"plan-stale": {Execution: &ports.SlotPlanExecution{MovesTotal: 6, MovesExecuted: 2}},
		},
	}
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}},
		Wes:      &fakeWes{backlog: map[string]ports.BacklogTelemetry{"pick-zone-a": {}}},
		Fe:       &fakeFe{queue: map[string]ports.QueueStatus{"PICK": {}}, stuck: ports.StuckTasksResult{}},
		Wfm:      &fakeWfm{gaps: map[string]ports.StaffingGap{"pick-zone-a": {}}},
		Targets:  targets,
		Now:      fixedClock(now),
		SlotPlanWatch: &usecases.SlotPlanWatch{
			Slotting:   slotting,
			StaleAfter: 24 * time.Hour,
		},
	}
	brief := uc.Execute(context.Background())

	if brief.SlotPlanWatch == nil {
		t.Fatal("a configured slot-plan watch must attach its section to the brief")
	}
	if len(brief.SlotPlanWatch.Stale) != 1 || brief.SlotPlanWatch.Stale[0].PlanId != "plan-stale" {
		t.Fatalf("the brief's slot-plan section must carry the flagged plan, got %+v", brief.SlotPlanWatch)
	}
	// The section must not leak into the per-path facts or exceptions.
	if len(brief.OpenExceptions) != 0 {
		t.Fatalf("the slot-plan watch is informational and must not raise path exceptions, got %v", brief.OpenExceptions)
	}
}

func TestDailyBrief_Execute_NilSlotPlanWatchLeavesBriefUnchanged(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{},
		Wes:      &fakeWes{},
		Fe:       &fakeFe{},
		Wfm:      &fakeWfm{},
		Targets:  targets,
		Now:      fixedClock(time.Unix(0, 0)),
	}
	brief := uc.Execute(context.Background())
	if brief.SlotPlanWatch != nil {
		t.Fatalf("no slot-plan watch configured => no section, got %+v", brief.SlotPlanWatch)
	}
}

func TestDailyBrief_Execute_UnreachableSlottingDegradesInsideTheSection(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{},
		Wes:      &fakeWes{},
		Fe:       &fakeFe{},
		Wfm:      &fakeWfm{},
		Targets:  targets,
		Now:      fixedClock(time.Unix(0, 0)),
		SlotPlanWatch: &usecases.SlotPlanWatch{
			Slotting:   &fakeSlotting{listErr: errors.New("connect: connection refused")},
			StaleAfter: 24 * time.Hour,
		},
	}
	brief := uc.Execute(context.Background())
	if brief.SlotPlanWatch == nil || len(brief.SlotPlanWatch.Unavailable) != 1 {
		t.Fatalf("an unreachable slotting-optimization must degrade to an unavailable line on an attached section, got %+v", brief.SlotPlanWatch)
	}
	if len(brief.Sites) != 1 || len(brief.Sites[0].Paths) != 1 {
		t.Fatalf("the rest of the brief must still be produced, got %+v", brief.Sites)
	}
}

func TestDailyBrief_Execute_PromiseHealthSectionRidesTheBrief(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{},
		Wes:      &fakeWes{},
		Fe:       &fakeFe{},
		Wfm:      &fakeWfm{},
		Targets:  targets,
		Now:      fixedClock(now),
		PromiseHealth: &usecases.PromiseHealth{
			OrderMgmt: &fakeOrderMgmtMCP{health: ports.PromiseHealth{OrdersAllocatedTotal: 40, RepromiseRate: 0.05}},
		},
	}
	brief := uc.Execute(context.Background())
	if brief.PromiseHealth == nil {
		t.Fatal("a configured promise-health use case must attach its section to the brief")
	}
	if brief.PromiseHealth.OrdersAllocatedTotal != 40 || brief.PromiseHealth.RepromiseRate != 0.05 {
		t.Fatalf("the section must carry the tool's KPIs, got %+v", brief.PromiseHealth)
	}
	if len(brief.OpenExceptions) != 0 {
		t.Fatalf("the promise-health section is informational and must not raise path exceptions, got %v", brief.OpenExceptions)
	}
}

func TestDailyBrief_Execute_NilPromiseHealthLeavesBriefUnchanged(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{},
		Wes:      &fakeWes{},
		Fe:       &fakeFe{},
		Wfm:      &fakeWfm{},
		Targets:  targets,
		Now:      fixedClock(time.Unix(0, 0)),
	}
	brief := uc.Execute(context.Background())
	if brief.PromiseHealth != nil {
		t.Fatalf("no promise-health configured => no section, got %+v", brief.PromiseHealth)
	}
}

func TestDailyBrief_Execute_UnreachableOrderManagementDegradesInsideTheSection(t *testing.T) {
	targets := []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}}
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{},
		Wes:      &fakeWes{},
		Fe:       &fakeFe{},
		Wfm:      &fakeWfm{},
		Targets:  targets,
		Now:      fixedClock(time.Unix(0, 0)),
		PromiseHealth: &usecases.PromiseHealth{
			OrderMgmt: &fakeOrderMgmtMCP{err: errors.New("connect: connection refused")},
		},
	}
	brief := uc.Execute(context.Background())
	if brief.PromiseHealth == nil || len(brief.PromiseHealth.Unavailable) != 1 {
		t.Fatalf("an unreachable order-management must degrade to an unavailable line on an attached section, got %+v", brief.PromiseHealth)
	}
	if len(brief.Sites) != 1 || len(brief.Sites[0].Paths) != 1 {
		t.Fatalf("the rest of the brief must still be produced, got %+v", brief.Sites)
	}
}
