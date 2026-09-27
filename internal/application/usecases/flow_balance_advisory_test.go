package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// --- faked outbound MCP-client ports (no live servers) ----------------

type fbFakeWes struct {
	recommendation ports.RebalanceRecommendation
	err            error
	calledPathId   string
}

func (f *fbFakeWes) GetBacklogTelemetry(ctx context.Context, pathId string) (ports.BacklogTelemetry, error) {
	return ports.BacklogTelemetry{}, errors.New("not used by FlowBalanceAdvisory")
}

func (f *fbFakeWes) GetRebalanceRecommendation(ctx context.Context, pathId string) (ports.RebalanceRecommendation, error) {
	f.calledPathId = pathId
	if f.err != nil {
		return ports.RebalanceRecommendation{}, f.err
	}
	return f.recommendation, nil
}

type fbFakeWFM struct {
	gap                                           ports.StaffingGap
	err                                           error
	calledBuildingId, calledShiftId, calledPathId string
}

func (f *fbFakeWFM) GetStaffingGap(ctx context.Context, buildingId, shiftId, pathId string) (ports.StaffingGap, error) {
	f.calledBuildingId, f.calledShiftId, f.calledPathId = buildingId, shiftId, pathId
	if f.err != nil {
		return ports.StaffingGap{}, f.err
	}
	return f.gap, nil
}

func (f *fbFakeWFM) ProposePathHeads(ctx context.Context, buildingId, pathId string, charge, plannedRate float64) (ports.ProposedHeads, error) {
	return ports.ProposedHeads{}, errors.New("not used by FlowBalanceAdvisory")
}

type fbFakeFE struct {
	result        ports.StuckTasksResult
	err           error
	calledSeconds int
}

func (f *fbFakeFE) GetQueueStatus(ctx context.Context, processPath string) (ports.QueueStatus, error) {
	return ports.QueueStatus{}, errors.New("not used by FlowBalanceAdvisory")
}

func (f *fbFakeFE) FindClaimableWork(ctx context.Context, processPath string) (ports.ClaimableWorkResult, error) {
	return ports.ClaimableWorkResult{}, errors.New("not used by FlowBalanceAdvisory")
}

func (f *fbFakeFE) DiagnoseStuckTasks(ctx context.Context, withinSeconds int) (ports.StuckTasksResult, error) {
	f.calledSeconds = withinSeconds
	if f.err != nil {
		return ports.StuckTasksResult{}, f.err
	}
	return f.result, nil
}

type fbFakeLP struct {
	util                ports.TaskTypeUtilization
	err                 error
	calledTaskType      string
	calledWindowSeconds int64
}

func (f *fbFakeLP) GetAssociateScorecard(ctx context.Context, associateId string) (ports.AssociateScorecard, error) {
	return ports.AssociateScorecard{}, errors.New("not used by FlowBalanceAdvisory")
}

func (f *fbFakeLP) GetTaskTypePerformance(ctx context.Context, taskType string) (ports.TaskTypePerformance, error) {
	return ports.TaskTypePerformance{}, errors.New("not used by FlowBalanceAdvisory")
}

func (f *fbFakeLP) GetLaborStandard(ctx context.Context, taskType string) (ports.LaborStandard, error) {
	return ports.LaborStandard{}, errors.New("not used by FlowBalanceAdvisory")
}

func (f *fbFakeLP) GetTaskTypeUtilization(ctx context.Context, taskType string, windowSeconds int64) (ports.TaskTypeUtilization, error) {
	f.calledTaskType = taskType
	f.calledWindowSeconds = windowSeconds
	if f.err != nil {
		return ports.TaskTypeUtilization{}, f.err
	}
	return f.util, nil
}

var (
	_ ports.WesWorkPlanningClient      = (*fbFakeWes)(nil)
	_ ports.WorkforceManagementClient  = (*fbFakeWFM)(nil)
	_ ports.FulfillmentExecutionClient = (*fbFakeFE)(nil)
	_ ports.LaborPerformanceClient     = (*fbFakeLP)(nil)
)

// --- tests --------------------------------------------------------------

func TestFlowBalanceAdvisory_Execute(t *testing.T) {
	t.Run("all three signals healthy => assign_labor recommendation with evidence", func(t *testing.T) {
		assertHealthyAdvisoryAssignsLabor(t)
	})
	t.Run("wes call fails => degrades to partial hold, no hard error", func(t *testing.T) {
		assertWesFailureDegradesToPartialHold(t)
	})
	t.Run("wfm and fe calls fail => partial hold, wes evidence still present", func(t *testing.T) {
		assertWfmAndFeFailuresKeepWesEvidence(t)
	})
	t.Run("unrecognized wes action enum is rejected outright, not defaulted", func(t *testing.T) {
		assertUnknownWesActionRejected(t)
	})
	t.Run("nil client ports treated as unavailable, not a panic", func(t *testing.T) {
		assertNilPortsTreatedAsUnavailable(t)
	})
	t.Run("NoActionNeeded + healthy signals => release_next_work, not a write call", func(t *testing.T) {
		assertHealthyBacklogReleasesNextWork(t)
	})
}

// assertHealthyAdvisoryAssignsLabor checks the fully-wired happy path:
// ReassignLabor + an understaffed gap yields assign_labor with the head
// gap, full evidence, and every port called with the right scoping
// arguments.
func assertHealthyAdvisoryAssignsLabor(t *testing.T) {
	t.Helper()
	wes := &fbFakeWes{recommendation: ports.RebalanceRecommendation{
		PathId: "pick-a", Action: "ReassignLabor", BacklogDepth: 120, WIP: 40,
	}}
	wfm := &fbFakeWFM{gap: ports.StaffingGap{
		PathId: "pick-a", PlannedHeads: 10, ActiveHeads: 6, Understaffed: true,
	}}
	fe := &fbFakeFE{result: ports.StuckTasksResult{Count: 0}}

	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe}
	got, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pick-a")
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}

	if got.RecommendedAction != policy.ActionAssignLabor {
		t.Errorf("RecommendedAction = %q, want %q", got.RecommendedAction, policy.ActionAssignLabor)
	}
	if got.ProposedHeads != 4 {
		t.Errorf("ProposedHeads = %d, want 4", got.ProposedHeads)
	}
	if got.Partial {
		t.Errorf("Partial = true, want false: %+v", got.MissingSignals)
	}
	if len(got.Evidence) != 3 {
		t.Errorf("len(Evidence) = %d, want 3: %+v", len(got.Evidence), got.Evidence)
	}

	// Every port was called with the right scoping arguments.
	if wes.calledPathId != "pick-a" {
		t.Errorf("wes called with pathId=%q, want pick-a", wes.calledPathId)
	}
	if wfm.calledBuildingId != "bldg-1" || wfm.calledShiftId != "shift-1" || wfm.calledPathId != "pick-a" {
		t.Errorf("wfm called with (%q,%q,%q), want (bldg-1,shift-1,pick-a)", wfm.calledBuildingId, wfm.calledShiftId, wfm.calledPathId)
	}
	if fe.calledSeconds <= 0 {
		t.Errorf("fe called with withinSeconds=%d, want > 0", fe.calledSeconds)
	}
}

// assertWesFailureDegradesToPartialHold checks that an unreachable wes
// yields a partial hold, not a hard error.
func assertWesFailureDegradesToPartialHold(t *testing.T) {
	t.Helper()
	wes := &fbFakeWes{err: errors.New("connection refused")}
	wfm := &fbFakeWFM{gap: ports.StaffingGap{Understaffed: true}}
	fe := &fbFakeFE{}

	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe}
	got, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pick-a")
	if err != nil {
		t.Fatalf("Execute: unexpected hard error on upstream failure: %v", err)
	}
	if !got.Partial {
		t.Error("expected a Partial decision when wes is unavailable")
	}
	if got.RecommendedAction != policy.FlowBalanceActionHold {
		t.Errorf("RecommendedAction = %q, want hold", got.RecommendedAction)
	}
}

// assertWfmAndFeFailuresKeepWesEvidence checks that when only wes answers,
// the decision is a partial hold still carrying the wes evidence line.
func assertWfmAndFeFailuresKeepWesEvidence(t *testing.T) {
	t.Helper()
	wes := &fbFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pick-a", Action: "NoActionNeeded"}}
	wfm := &fbFakeWFM{err: errors.New("timeout")}
	fe := &fbFakeFE{err: errors.New("timeout")}

	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe}
	got, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pick-a")
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if !got.Partial {
		t.Error("expected Partial=true")
	}
	if len(got.Evidence) != 1 {
		t.Errorf("len(Evidence) = %d, want 1 (wes only)", len(got.Evidence))
	}
}

// assertUnknownWesActionRejected checks the untrusted-input guardrail: an
// unrecognized RebalanceAction enum value is rejected outright, never
// defaulted.
func assertUnknownWesActionRejected(t *testing.T) {
	t.Helper()
	wes := &fbFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pick-a", Action: "SomethingNewAndUnknown"}}
	wfm := &fbFakeWFM{}
	fe := &fbFakeFE{}

	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe}
	_, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pick-a")
	if err == nil {
		t.Fatal("expected an error for an unrecognized RebalanceAction enum value, got nil")
	}
}

// assertNilPortsTreatedAsUnavailable checks that an unwired use case
// degrades to a partial hold with no evidence, never a panic.
func assertNilPortsTreatedAsUnavailable(t *testing.T) {
	t.Helper()
	uc := &usecases.FlowBalanceAdvisory{}
	got, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pick-a")
	if err != nil {
		t.Fatalf("Execute: unexpected error with all-nil ports: %v", err)
	}
	if !got.Partial {
		t.Error("expected Partial=true when every port is nil")
	}
	if got.RecommendedAction != policy.FlowBalanceActionHold {
		t.Errorf("RecommendedAction = %q, want hold", got.RecommendedAction)
	}
	if len(got.Evidence) != 0 {
		t.Errorf("len(Evidence) = %d, want 0", len(got.Evidence))
	}
}

// assertHealthyBacklogReleasesNextWork checks the NoActionNeeded happy
// path: release_next_work is recommended, not any write call.
func assertHealthyBacklogReleasesNextWork(t *testing.T) {
	t.Helper()
	wes := &fbFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pick-a", Action: "NoActionNeeded"}}
	wfm := &fbFakeWFM{gap: ports.StaffingGap{PlannedHeads: 4, ActiveHeads: 4, Understaffed: false}}
	fe := &fbFakeFE{result: ports.StuckTasksResult{Count: 0}}

	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe}
	got, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pick-a")
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if got.RecommendedAction != policy.ActionReleaseNextWork {
		t.Errorf("RecommendedAction = %q, want release_next_work", got.RecommendedAction)
	}
}

// --- labor-utilization correlation overlay (ADR 0008, Phase 3) ---------

// TestFlowBalanceAdvisory_UtilizationCorrelation drives the utilization
// overlay with an anchored fixture: wes always reports a healthy queue at
// pathId "pick-a", with BacklogDepth varying per subtest to drive the
// queue-depth-high/low branch of the correlation. wfm/fe are set so the
// base RecommendedAction is stable and each subtest can assert it is
// UNCHANGED by the utilization overlay.
func TestFlowBalanceAdvisory_UtilizationCorrelation(t *testing.T) {
	t.Run("queue depth HIGH + idle share HIGH => claim/flow problem advisory, base decision unchanged", func(t *testing.T) {
		// 30% utilization, idle share = 1300/(700+1300) = 65%
		pct := 30.0
		lp := &fbFakeLP{util: ports.TaskTypeUtilization{TaskType: "PICK", WindowSeconds: 3600, TaskSeconds: 700, IdleSeconds: 1300, UtilizationPct: &pct}}
		assertUtilizationKind(t, 120, policy.ActionReleaseNextWork, policy.UtilizationCorrelationClaimFlowProblem, lp)
		if lp.calledTaskType != "PICK" {
			t.Errorf("LP called with taskType=%q, want PICK", lp.calledTaskType)
		}
	})
	t.Run("queue depth LOW + idle share HIGH => starvation advisory", func(t *testing.T) {
		pct := 30.0
		assertUtilizationKind(t, 5, "", policy.UtilizationCorrelationStarvation,
			&fbFakeLP{util: ports.TaskTypeUtilization{TaskType: "PICK", WindowSeconds: 3600, TaskSeconds: 700, IdleSeconds: 1300, UtilizationPct: &pct}})
	})
	t.Run("queue depth HIGH + idle share LOW => staffing gap confirmed, evidence carries the utilization reading", func(t *testing.T) {
		pct := 90.0
		assertStaffingGapUtilization(t,
			&fbFakeLP{util: ports.TaskTypeUtilization{TaskType: "PICK", WindowSeconds: 3600, TaskSeconds: 900, IdleSeconds: 100, UtilizationPct: &pct}})
	})
	t.Run("nil LP client => Utilization stays nil, base decision unaffected (ADR-0004 fallback)", func(t *testing.T) {
		assertNoUtilizationCorrelation(t, nil, policy.ActionReleaseNextWork)
	})
	t.Run("LP call error => Utilization stays nil, no hard failure", func(t *testing.T) {
		assertNoUtilizationCorrelation(t, &fbFakeLP{err: errors.New("connection refused")}, "")
	})
	t.Run("UtilizationPct nil (nothing observed) => Utilization stays nil, never coerced to 0%", func(t *testing.T) {
		assertNoUtilizationCorrelation(t, &fbFakeLP{util: ports.TaskTypeUtilization{TaskType: "PICK", WindowSeconds: 3600}}, "")
	})
	t.Run("pathId not bound to a task type => LP never called, Utilization stays nil", func(t *testing.T) {
		pct := 30.0
		assertUnboundPathSkipsLP(t, &fbFakeLP{util: ports.TaskTypeUtilization{TaskType: "PICK", UtilizationPct: &pct}})
	})
}

// executeUtilization wires the anchored NoActionNeeded fixture (wes at
// "pick-a" with the given backlog depth, wfm/fe healthy so the base
// action is stable) plus the labor-performance fake, and executes one
// advisory.
func executeUtilization(t *testing.T, backlogDepth int, lp ports.LaborPerformanceClient) policy.Decision {
	t.Helper()
	wes := &fbFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pick-a", Action: "NoActionNeeded", BacklogDepth: backlogDepth}}
	wfm := &fbFakeWFM{gap: ports.StaffingGap{PlannedHeads: 4, ActiveHeads: 4, Understaffed: false}}
	fe := &fbFakeFE{result: ports.StuckTasksResult{Count: 0}}
	pathTaskTypes := map[string]string{"pick-a": "PICK"}

	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, LP: lp, PathTaskTypes: pathTaskTypes}
	got, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pick-a")
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	return got
}

// assertUtilizationKind checks that the overlay produced the wanted
// correlation kind; wantBaseAction (non-empty) additionally asserts the
// base RecommendedAction is unaffected by the overlay.
func assertUtilizationKind(t *testing.T, backlogDepth int, wantBaseAction policy.RecommendedAction, wantKind policy.UtilizationCorrelationKind, lp *fbFakeLP) {
	t.Helper()
	got := executeUtilization(t, backlogDepth, lp)
	if wantBaseAction != "" && got.RecommendedAction != wantBaseAction {
		t.Errorf("base RecommendedAction must be unaffected by the utilization overlay, got %q", got.RecommendedAction)
	}
	if got.Utilization == nil {
		t.Fatal("expected a non-nil Utilization correlation")
	}
	if got.Utilization.Kind != wantKind {
		t.Errorf("Utilization.Kind = %q, want %q", got.Utilization.Kind, wantKind)
	}
}

// assertStaffingGapUtilization checks the confirmed-staffing-gap overlay
// on a ReassignLabor/understaffed base decision, including the
// utilization reading's presence in the evidence trail.
func assertStaffingGapUtilization(t *testing.T, lp *fbFakeLP) {
	t.Helper()
	wes := &fbFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pick-a", Action: "ReassignLabor", BacklogDepth: 120, WIP: 40}}
	wfm := &fbFakeWFM{gap: ports.StaffingGap{PathId: "pick-a", PlannedHeads: 10, ActiveHeads: 6, Understaffed: true}}
	fe := &fbFakeFE{result: ports.StuckTasksResult{Count: 0}}
	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, LP: lp, PathTaskTypes: map[string]string{"pick-a": "PICK"}}

	got, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pick-a")
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if got.RecommendedAction != policy.ActionAssignLabor {
		t.Errorf("base RecommendedAction must be unaffected by the utilization overlay, got %q", got.RecommendedAction)
	}
	if got.Utilization == nil {
		t.Fatal("expected a non-nil Utilization correlation")
	}
	if got.Utilization.Kind != policy.UtilizationCorrelationStaffingGapConfirmed {
		t.Errorf("Utilization.Kind = %q, want %q", got.Utilization.Kind, policy.UtilizationCorrelationStaffingGapConfirmed)
	}
	found := false
	for _, e := range got.Evidence {
		if e.Source == "labor-performance.get_task_type_utilization" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the utilization reading in the evidence trail: %+v", got.Evidence)
	}
}

// assertNoUtilizationCorrelation checks a degrade path: Utilization stays
// nil and (when wantBaseAction is non-empty) the base decision is
// unaffected. lp takes the interface type so the nil-LP case passes an
// untyped nil (a typed *fbFakeLP nil would defeat the ADR-0004 guard).
func assertNoUtilizationCorrelation(t *testing.T, lp ports.LaborPerformanceClient, wantBaseAction policy.RecommendedAction) {
	t.Helper()
	got := executeUtilization(t, 120, lp)
	if got.Utilization != nil {
		t.Errorf("expected nil Utilization, got %+v", got.Utilization)
	}
	if wantBaseAction != "" && got.RecommendedAction != wantBaseAction {
		t.Errorf("base decision must be unaffected, got %q", got.RecommendedAction)
	}
}

// assertUnboundPathSkipsLP checks that a pathId with no task-type binding
// never reaches the labor-performance client.
func assertUnboundPathSkipsLP(t *testing.T, lp *fbFakeLP) {
	t.Helper()
	wes := &fbFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pack-b", Action: "NoActionNeeded", BacklogDepth: 120}}
	wfm := &fbFakeWFM{gap: ports.StaffingGap{PlannedHeads: 4, ActiveHeads: 4, Understaffed: false}}
	fe := &fbFakeFE{result: ports.StuckTasksResult{Count: 0}}
	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, LP: lp, PathTaskTypes: map[string]string{"pick-a": "PICK"}}

	got, err := uc.Execute(context.Background(), "bldg-1", "shift-1", "pack-b")
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if got.Utilization != nil {
		t.Errorf("expected nil Utilization for an unbound pathId, got %+v", got.Utilization)
	}
	if lp.calledTaskType != "" {
		t.Errorf("LP must never be called for an unbound pathId, got calledTaskType=%q", lp.calledTaskType)
	}
}
