package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeFacility struct {
	sites  ports.SitesResult
	travel ports.TravelDistance
	err    error
}

func (f *fakeFacility) ListSites(ctx context.Context) (ports.SitesResult, error) { return f.sites, nil }
func (f *fakeFacility) GetSiteLayout(ctx context.Context, siteCode string) (ports.SiteLayout, error) {
	return ports.SiteLayout{}, nil
}
func (f *fakeFacility) GetZoneGrid(ctx context.Context, zoneId string) (ports.ZoneGrid, error) {
	return ports.ZoneGrid{}, nil
}
func (f *fakeFacility) EstimateTravelDistance(ctx context.Context, from, to string) (ports.TravelDistance, error) {
	if f.err != nil {
		return ports.TravelDistance{}, f.err
	}
	return f.travel, nil
}

type fakeWes struct{ backlog ports.BacklogTelemetry }

func (f *fakeWes) GetBacklogTelemetry(ctx context.Context, pathId string) (ports.BacklogTelemetry, error) {
	return f.backlog, nil
}
func (f *fakeWes) GetRebalanceRecommendation(ctx context.Context, pathId string) (ports.RebalanceRecommendation, error) {
	return ports.RebalanceRecommendation{}, nil
}

type fakeFe struct {
	queue ports.QueueStatus
	stuck ports.StuckTasksResult
}

func (f *fakeFe) GetQueueStatus(ctx context.Context, processPath string) (ports.QueueStatus, error) {
	return f.queue, nil
}
func (f *fakeFe) FindClaimableWork(ctx context.Context, processPath string) (ports.ClaimableWorkResult, error) {
	return ports.ClaimableWorkResult{}, nil
}
func (f *fakeFe) DiagnoseStuckTasks(ctx context.Context, withinSeconds int) (ports.StuckTasksResult, error) {
	return f.stuck, nil
}

type fakeWfm struct{ gap ports.StaffingGap }

func (f *fakeWfm) GetStaffingGap(ctx context.Context, buildingId, shiftId, pathId string) (ports.StaffingGap, error) {
	return f.gap, nil
}
func (f *fakeWfm) ProposePathHeads(ctx context.Context, buildingId, pathId string, charge, plannedRate float64) (ports.ProposedHeads, error) {
	return ports.ProposedHeads{}, nil
}

func newTestDeps(imbalanced bool) Deps {
	if imbalanced {
		return Deps{DailyBrief: &usecases.DailyBrief{
			Facility: &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}},
			Wes:      &fakeWes{backlog: ports.BacklogTelemetry{PathId: "pick-zone-a", BacklogDepth: 50, WIP: 10, OverAlarmThreshold: true}},
			Fe: &fakeFe{
				queue: ports.QueueStatus{ProcessPath: "PICK", Depth: 40},
				stuck: ports.StuckTasksResult{Count: 1, Tasks: []ports.StuckTask{{TaskId: "t1", Type: "PICK"}}},
			},
			Wfm:     &fakeWfm{gap: ports.StaffingGap{PathId: "pick-zone-a", PlannedHeads: 5, ActiveHeads: 2, Understaffed: true}},
			Targets: []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}},
			Now:     func() time.Time { return time.Unix(0, 0) },
		}}
	}
	return Deps{DailyBrief: &usecases.DailyBrief{
		Facility: &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}},
		Wes:      &fakeWes{backlog: ports.BacklogTelemetry{PathId: "pick-zone-a", BacklogDepth: 4, WIP: 2}},
		Fe: &fakeFe{
			queue: ports.QueueStatus{ProcessPath: "PICK", Depth: 3},
			stuck: ports.StuckTasksResult{Count: 0},
		},
		Wfm:     &fakeWfm{gap: ports.StaffingGap{PathId: "pick-zone-a", PlannedHeads: 3, ActiveHeads: 3}},
		Targets: []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}},
		Now:     func() time.Time { return time.Unix(0, 0) },
	}}
}

func TestGetDailyBrief(t *testing.T) {
	deps := newTestDeps(true)
	out, err := deps.getDailyBrief(context.Background(), dailyBriefInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Sites) != 1 || out.Sites[0].SiteCode != "WH1" {
		t.Fatalf("unexpected sites: %+v", out.Sites)
	}
	if len(out.OpenExceptions) != 1 {
		t.Fatalf("expected 1 open exception, got %d", len(out.OpenExceptions))
	}
}

func TestListOpenExceptions_NoFilter_ReturnsAll(t *testing.T) {
	deps := newTestDeps(true)
	out, err := deps.listOpenExceptions(context.Background(), listOpenExceptionsInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("count = %d, want 1", out.Count)
	}
}

func TestListOpenExceptions_HealthyBrief_ReturnsEmpty(t *testing.T) {
	deps := newTestDeps(false)
	out, err := deps.listOpenExceptions(context.Background(), listOpenExceptionsInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 0 {
		t.Fatalf("count = %d, want 0 for a healthy brief", out.Count)
	}
}

func TestListOpenExceptions_SeverityFilter(t *testing.T) {
	deps := newTestDeps(true) // produces exactly one CRITICAL exception (3 signals).

	tests := []struct {
		name     string
		severity string
		wantN    int
	}{
		{"critical filter includes the critical exception", "critical", 1},
		{"warning filter (>=warning severity) includes critical too", "warning", 1},
		{"info filter includes everything", "info", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := deps.listOpenExceptions(context.Background(), listOpenExceptionsInput{Severity: tc.severity})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Count != tc.wantN {
				t.Fatalf("count = %d, want %d", out.Count, tc.wantN)
			}
		})
	}
}

func TestListOpenExceptions_UnknownSeverityRejected(t *testing.T) {
	deps := newTestDeps(true)
	if _, err := deps.listOpenExceptions(context.Background(), listOpenExceptionsInput{Severity: "apocalyptic"}); err == nil {
		t.Fatal("expected an error for an unknown severity value, model input must be validated")
	}
}

// --- get_flow_balance_exception ------------------------------------------

type fbToolFakeWes struct {
	recommendation ports.RebalanceRecommendation
}

func (f *fbToolFakeWes) GetBacklogTelemetry(ctx context.Context, pathId string) (ports.BacklogTelemetry, error) {
	return ports.BacklogTelemetry{}, nil
}
func (f *fbToolFakeWes) GetRebalanceRecommendation(ctx context.Context, pathId string) (ports.RebalanceRecommendation, error) {
	return f.recommendation, nil
}

type fbToolFakeWfm struct {
	gap ports.StaffingGap
}

func (f *fbToolFakeWfm) GetStaffingGap(ctx context.Context, buildingId, shiftId, pathId string) (ports.StaffingGap, error) {
	return f.gap, nil
}
func (f *fbToolFakeWfm) ProposePathHeads(ctx context.Context, buildingId, pathId string, charge, plannedRate float64) (ports.ProposedHeads, error) {
	return ports.ProposedHeads{}, nil
}

type fbToolFakeFe struct {
	result ports.StuckTasksResult
}

func (f *fbToolFakeFe) GetQueueStatus(ctx context.Context, processPath string) (ports.QueueStatus, error) {
	return ports.QueueStatus{}, nil
}
func (f *fbToolFakeFe) FindClaimableWork(ctx context.Context, processPath string) (ports.ClaimableWorkResult, error) {
	return ports.ClaimableWorkResult{}, nil
}
func (f *fbToolFakeFe) DiagnoseStuckTasks(ctx context.Context, withinSeconds int) (ports.StuckTasksResult, error) {
	return f.result, nil
}

func TestGetFlowBalanceException_AllSignalsHealthy_AssignsLabor(t *testing.T) {
	deps := Deps{
		DailyBrief: &usecases.DailyBrief{Now: func() time.Time { return time.Unix(0, 0) }},
		FlowBalanceAdvisory: &usecases.FlowBalanceAdvisory{
			Wes: &fbToolFakeWes{recommendation: ports.RebalanceRecommendation{
				PathId: "pick-a", Action: "ReassignLabor", BacklogDepth: 90, WIP: 30,
			}},
			WFM: &fbToolFakeWfm{gap: ports.StaffingGap{PathId: "pick-a", PlannedHeads: 10, ActiveHeads: 6, Understaffed: true}},
			FE:  &fbToolFakeFe{result: ports.StuckTasksResult{Count: 0}},
		},
	}

	out, err := deps.getFlowBalanceException(context.Background(), flowBalanceExceptionInput{
		BuildingId: "bldg-1", ShiftId: "shift-1", PathId: "pick-a",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.RecommendedAction != "assign_labor" {
		t.Errorf("RecommendedAction = %q, want assign_labor", out.RecommendedAction)
	}
	if out.ProposedHeads != 4 {
		t.Errorf("ProposedHeads = %d, want 4", out.ProposedHeads)
	}
	if out.Partial {
		t.Errorf("Partial = true, want false: %+v", out.MissingSignals)
	}
	if len(out.Evidence) != 3 {
		t.Errorf("len(Evidence) = %d, want 3: %+v", len(out.Evidence), out.Evidence)
	}
}

// fbToolFakeLP is a minimal ports.LaborPerformanceClient fake that only
// implements GetTaskTypeUtilization, mirroring the usecases package's own
// fbFakeLP, to drive the labor-utilization correlation overlay through the
// MCP tool boundary.
type fbToolFakeLP struct {
	util ports.TaskTypeUtilization
}

func (f *fbToolFakeLP) GetAssociateScorecard(ctx context.Context, associateId string) (ports.AssociateScorecard, error) {
	return ports.AssociateScorecard{}, nil
}
func (f *fbToolFakeLP) GetTaskTypePerformance(ctx context.Context, taskType string) (ports.TaskTypePerformance, error) {
	return ports.TaskTypePerformance{}, nil
}
func (f *fbToolFakeLP) GetLaborStandard(ctx context.Context, taskType string) (ports.LaborStandard, error) {
	return ports.LaborStandard{}, nil
}
func (f *fbToolFakeLP) GetTaskTypeUtilization(ctx context.Context, taskType string, windowSeconds int64) (ports.TaskTypeUtilization, error) {
	return f.util, nil
}

// TestGetFlowBalanceException_UtilizationRoundTrips is the ADR-0008
// regression test for the BUG this fixes: the MCP tool's output must
// surface Decision.Utilization exactly like the REST handler already does
// (router.go toFlowBalanceExceptionDTO), never silently drop it.
func TestGetFlowBalanceException_UtilizationRoundTrips(t *testing.T) {
	pct := 30.0
	deps := Deps{
		FlowBalanceAdvisory: &usecases.FlowBalanceAdvisory{
			Wes: &fbToolFakeWes{recommendation: ports.RebalanceRecommendation{
				PathId: "pick-a", Action: "NoActionNeeded", BacklogDepth: 120, WIP: 10,
			}},
			WFM: &fbToolFakeWfm{gap: ports.StaffingGap{PathId: "pick-a", PlannedHeads: 4, ActiveHeads: 4}},
			FE:  &fbToolFakeFe{result: ports.StuckTasksResult{Count: 0}},
			LP: &fbToolFakeLP{util: ports.TaskTypeUtilization{
				TaskType: "PICK", WindowSeconds: 3600, TaskSeconds: 700, IdleSeconds: 1300, UtilizationPct: &pct,
			}},
			PathTaskTypes: map[string]string{"pick-a": "PICK"},
		},
	}

	out, err := deps.getFlowBalanceException(context.Background(), flowBalanceExceptionInput{
		BuildingId: "bldg-1", ShiftId: "shift-1", PathId: "pick-a",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Utilization == nil {
		t.Fatal("expected a non-nil Utilization correlation on the MCP tool output")
	}
	if out.Utilization.Kind != string(policy.UtilizationCorrelationClaimFlowProblem) {
		t.Errorf("Utilization.Kind = %q, want %q", out.Utilization.Kind, policy.UtilizationCorrelationClaimFlowProblem)
	}
	if out.Utilization.Rationale == "" {
		t.Error("expected a non-empty Utilization.Rationale")
	}
}

func TestGetFlowBalanceException_NoLP_UtilizationStaysNil(t *testing.T) {
	deps := Deps{
		FlowBalanceAdvisory: &usecases.FlowBalanceAdvisory{
			Wes: &fbToolFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pick-a", Action: "NoActionNeeded"}},
			WFM: &fbToolFakeWfm{gap: ports.StaffingGap{PathId: "pick-a", PlannedHeads: 4, ActiveHeads: 4}},
			FE:  &fbToolFakeFe{result: ports.StuckTasksResult{Count: 0}},
		},
	}
	out, err := deps.getFlowBalanceException(context.Background(), flowBalanceExceptionInput{
		BuildingId: "bldg-1", ShiftId: "shift-1", PathId: "pick-a",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Utilization != nil {
		t.Errorf("expected nil Utilization with no LP client wired, got %+v", out.Utilization)
	}
}

func TestGetFlowBalanceException_UnrecognizedActionEnumRejected(t *testing.T) {
	deps := Deps{
		FlowBalanceAdvisory: &usecases.FlowBalanceAdvisory{
			Wes: &fbToolFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pick-a", Action: "SomethingUnknown"}},
			WFM: &fbToolFakeWfm{},
			FE:  &fbToolFakeFe{},
		},
	}
	if _, err := deps.getFlowBalanceException(context.Background(), flowBalanceExceptionInput{
		BuildingId: "bldg-1", ShiftId: "shift-1", PathId: "pick-a",
	}); err == nil {
		t.Fatal("expected an error for an unrecognized RebalanceAction enum value, got nil")
	}
}

// --- detect_stranded_reservation ------------------------------------------

type srToolFakeFe struct {
	stuck    ports.StuckTasksResult
	stuckErr error
}

func (f *srToolFakeFe) GetQueueStatus(ctx context.Context, processPath string) (ports.QueueStatus, error) {
	return ports.QueueStatus{}, nil
}
func (f *srToolFakeFe) FindClaimableWork(ctx context.Context, processPath string) (ports.ClaimableWorkResult, error) {
	return ports.ClaimableWorkResult{}, nil
}
func (f *srToolFakeFe) DiagnoseStuckTasks(ctx context.Context, withinSeconds int) (ports.StuckTasksResult, error) {
	if f.stuckErr != nil {
		return ports.StuckTasksResult{}, f.stuckErr
	}
	return f.stuck, nil
}

type srToolFakeInv struct {
	availability ports.Availability
	occupancy    ports.BinOccupancy
}

func (f *srToolFakeInv) CheckAvailability(ctx context.Context, sku string) (ports.Availability, error) {
	return f.availability, nil
}
func (f *srToolFakeInv) GetBinOccupancy(ctx context.Context, binId string) (ports.BinOccupancy, error) {
	return f.occupancy, nil
}

// TestDetectStrandedReservation_CallsUseCase_FullyCorrelated proves the
// adapter maps its input DTO into usecases.StrandedReservationRequest,
// calls DetectStrandedReservation.Execute, and maps the result back --
// the wiring contract this test guards against regressing.
func TestDetectStrandedReservation_CallsUseCase_FullyCorrelated(t *testing.T) {
	deps := Deps{
		StrandedReservation: &usecases.DetectStrandedReservation{
			FulfillmentExecution: &srToolFakeFe{stuck: ports.StuckTasksResult{
				Count: 1,
				Tasks: []ports.StuckTask{{TaskId: "t-1", Type: "PICK", Reason: "lease already expired"}},
			}},
			InventoryStorage: &srToolFakeInv{
				availability: ports.Availability{SKU: "SKU-1", Usable: 2},
				occupancy: ports.BinOccupancy{
					BinId:    "BIN-A1",
					Reserved: 12,
					Lines: []ports.BinOccupancyLine{
						{StockUnitId: "SU-1", SKU: "SKU-1", OnHand: 12, Reserved: 12, Usable: 0, State: "RESERVED"},
					},
				},
			},
		},
	}

	out, err := deps.detectStrandedReservation(context.Background(), strandedReservationInput{
		TaskType:           "PICK",
		SKU:                "SKU-1",
		MinUsableThreshold: 5,
		ReservationId:      "R-1",
		BinId:              "BIN-A1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Detected {
		t.Fatal("Detected = false, want true")
	}
	if out.Action != "revoke_reservation" {
		t.Fatalf("Action = %q, want revoke_reservation", out.Action)
	}
	if out.ReservationId != "R-1" {
		t.Fatalf("ReservationId = %q, want R-1", out.ReservationId)
	}
	if out.BlastRadius == nil {
		t.Fatal("expected a non-nil BlastRadius on a revoke recommendation")
	}
	if out.BlastRadius.BinId != "BIN-A1" || out.BlastRadius.QuantityFreed != 12 {
		t.Fatalf("BlastRadius = %+v, want bin BIN-A1 freeing 12 units", out.BlastRadius)
	}
	if len(out.Evidence) == 0 {
		t.Fatal("expected a non-empty evidence trail")
	}
}

// TestDetectStrandedReservation_UnknownTaskTypeRejected proves the adapter
// surfaces the use case's untrusted-input validation error rather than
// swallowing or panicking on it.
func TestDetectStrandedReservation_UnknownTaskTypeRejected(t *testing.T) {
	deps := Deps{
		StrandedReservation: &usecases.DetectStrandedReservation{
			FulfillmentExecution: &srToolFakeFe{},
			InventoryStorage:     &srToolFakeInv{},
		},
	}
	if _, err := deps.detectStrandedReservation(context.Background(), strandedReservationInput{
		TaskType: "BOGUS",
	}); err == nil {
		t.Fatal("expected an error for an unknown task type, model input must be validated")
	}
}

// TestDetectStrandedReservation_DegradesToHold_NoBlastRadius proves a
// missing candidate reservation/bin degrades to a typed hold with a nil
// BlastRadius, rather than the adapter fabricating one.
func TestDetectStrandedReservation_DegradesToHold_NoBlastRadius(t *testing.T) {
	deps := Deps{
		StrandedReservation: &usecases.DetectStrandedReservation{
			FulfillmentExecution: &srToolFakeFe{stuck: ports.StuckTasksResult{
				Count: 1,
				Tasks: []ports.StuckTask{{TaskId: "t-1", Type: "PICK", Reason: "lease already expired"}},
			}},
			InventoryStorage: &srToolFakeInv{availability: ports.Availability{SKU: "SKU-1", Usable: 0}},
		},
	}

	out, err := deps.detectStrandedReservation(context.Background(), strandedReservationInput{
		TaskType:           "PICK",
		SKU:                "SKU-1",
		MinUsableThreshold: 5,
		// ReservationId and BinId intentionally empty.
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Action != "hold" {
		t.Fatalf("Action = %q, want hold", out.Action)
	}
	if out.BlastRadius != nil {
		t.Fatalf("BlastRadius = %+v, want nil", out.BlastRadius)
	}
}
