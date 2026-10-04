package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakePlanning is an in-memory ports.WarehousePlanningClient. Only
// GetProcessPathCapacity is used by the outlook; the other three read tools
// count calls so a test can prove the outlook touches nothing else.
type fakePlanning struct {
	capacity ports.ProcessPathCapacity
	err      error

	reqs      []ports.ProcessPathCapacityRequest
	otherCall int
}

func (f *fakePlanning) GetProcessPathCapacity(_ context.Context, req ports.ProcessPathCapacityRequest) (ports.ProcessPathCapacity, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return ports.ProcessPathCapacity{}, f.err
	}
	return f.capacity, nil
}
func (f *fakePlanning) GetCapacityPlan(context.Context, string) (ports.CapacityPlan, error) {
	f.otherCall++
	return ports.CapacityPlan{}, errors.New("not used")
}
func (f *fakePlanning) GetStorageCapacity(context.Context, string) (ports.StorageCapacity, error) {
	f.otherCall++
	return ports.StorageCapacity{}, errors.New("not used")
}
func (f *fakePlanning) ListStationStandards(context.Context, string) (ports.StationStandardList, error) {
	f.otherCall++
	return ports.StationStandardList{}, errors.New("not used")
}

var _ ports.WarehousePlanningClient = (*fakePlanning)(nil)

func goodCapacity() ports.ProcessPathCapacity {
	return ports.ProcessPathCapacity{
		NormalizedRate: 120,
		NormalizedUnit: "ORDER",
		BottleneckStep: "PACK",
		StepBreakdown: []ports.PlanningStepBreakdown{
			{Step: "PICK", NormalizedRate: 300, BindingConstraint: "LABOR"},
			{Step: "PACK", NormalizedRate: 120, BindingConstraint: "STATION"},
		},
		Warnings: []string{"w1"},
	}
}

func f64(v float64) *float64 { return &v }

var outlookNow = time.Date(2026, 10, 5, 8, 0, 0, 500_000_000, time.UTC)

func plannedTarget() usecases.PathTarget {
	return usecases.PathTarget{SiteCode: "SIM1", PathId: "pick-zone-a", ProcessPath: "PICK", PlanningPathId: "tote-path"}
}

func TestCapacityOutlook_NotConfigured_ReturnsNil(t *testing.T) {
	var nilUC *usecases.CapacityOutlook
	if got := nilUC.Execute(context.Background(), plannedTarget(), outlookNow); got != nil {
		t.Fatalf("a nil use case must return nil, got %+v", got)
	}
	noPort := &usecases.CapacityOutlook{Horizon: time.Hour}
	if got := noPort.Execute(context.Background(), plannedTarget(), outlookNow); got != nil {
		t.Fatalf("a use case without a planning port must return nil, got %+v", got)
	}
}

func TestCapacityOutlook_Success_AsksPlanningForTheNextNHours(t *testing.T) {
	planning := &fakePlanning{capacity: goodCapacity()}
	uc := &usecases.CapacityOutlook{Planning: planning, Horizon: 8 * time.Hour}
	target := plannedTarget()
	target.UnitsPerOrder = f64(2.5)
	target.PackagesPerOrder = f64(1.2)

	got := uc.Execute(context.Background(), target, outlookNow)

	if got == nil || got.OmittedReason != "" {
		t.Fatalf("expected a populated outlook, got %+v", got)
	}
	if got.NormalizedRate != 120 || got.BottleneckStep != "PACK" || got.BindingConstraint != "STATION" || len(got.Warnings) != 1 || len(got.Steps) != 2 {
		t.Errorf("unexpected outlook: %+v", got)
	}
	if !got.WindowStart.Equal(time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)) || !got.WindowEnd.Equal(time.Date(2026, 10, 5, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("outlook window = [%v, %v)", got.WindowStart, got.WindowEnd)
	}
	if len(planning.reqs) != 1 || planning.otherCall != 0 {
		t.Fatalf("expected exactly one get_process_path_capacity call and no other tool, got reqs=%d other=%d", len(planning.reqs), planning.otherCall)
	}
	assertOutlookRequest(t, planning.reqs[0])
}

func assertOutlookRequest(t *testing.T, r ports.ProcessPathCapacityRequest) {
	t.Helper()
	if r.PathId != "tote-path" {
		t.Errorf("path id = %q, want the configured PLANNING path id (not the wes PathId)", r.PathId)
	}
	if r.Location != "SIM1" {
		t.Errorf("location = %q, want the path's site code", r.Location)
	}
	if r.WindowStart != "2026-10-05T08:00:00Z" || r.WindowEnd != "2026-10-05T16:00:00Z" {
		t.Errorf("window = [%s, %s), want [now, now+8h) truncated to the second", r.WindowStart, r.WindowEnd)
	}
	if r.UnitsPerOrder == nil || *r.UnitsPerOrder != 2.5 || r.PackagesPerOrder == nil || *r.PackagesPerOrder != 1.2 {
		t.Errorf("configured factors must be passed through: %+v", r)
	}
}

func TestCapacityOutlook_NeverInventsWorkloadFactors(t *testing.T) {
	planning := &fakePlanning{capacity: goodCapacity()}
	uc := &usecases.CapacityOutlook{Planning: planning, Horizon: 8 * time.Hour}

	uc.Execute(context.Background(), plannedTarget(), outlookNow)

	if len(planning.reqs) != 1 {
		t.Fatalf("expected 1 call, got %d", len(planning.reqs))
	}
	if planning.reqs[0].UnitsPerOrder != nil || planning.reqs[0].PackagesPerOrder != nil {
		t.Fatalf("an unconfigured factor must be omitted, never defaulted: %+v", planning.reqs[0])
	}
}

func TestCapacityOutlook_OmittedWithReason_WithoutCallingPlanning(t *testing.T) {
	cases := []struct {
		name    string
		target  usecases.PathTarget
		horizon time.Duration
		want    string
	}{
		{"no planning path id", usecases.PathTarget{SiteCode: "SIM1", PathId: "p"}, time.Hour, "planningPathId"},
		{"no site code", usecases.PathTarget{PathId: "p", PlanningPathId: "tote-path"}, time.Hour, "siteCode"},
		{"zero horizon", plannedTarget(), 0, "horizon"},
		{"negative horizon", plannedTarget(), -time.Hour, "horizon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			planning := &fakePlanning{capacity: goodCapacity()}
			uc := &usecases.CapacityOutlook{Planning: planning, Horizon: tc.horizon}

			got := uc.Execute(context.Background(), tc.target, outlookNow)

			if got == nil || !strings.Contains(got.OmittedReason, tc.want) {
				t.Fatalf("expected an omitted outlook mentioning %q, got %+v", tc.want, got)
			}
			if len(planning.reqs) != 0 {
				t.Fatalf("planning must not be called when the input is unusable, got %d calls", len(planning.reqs))
			}
			if got.NormalizedRate != 0 {
				t.Fatalf("an omitted outlook must carry no rate: %+v", got)
			}
		})
	}
}

func TestCapacityOutlook_PlanningError_FailsOpenWithReason(t *testing.T) {
	for _, msg := range []string{
		"warehouse-planning: call get_process_path_capacity: connection refused",
		"warehouse-planning: tool get_process_path_capacity reported an error: missing-step-capacity: PACK has no covering window",
	} {
		planning := &fakePlanning{err: errors.New(msg)}
		uc := &usecases.CapacityOutlook{Planning: planning, Horizon: 8 * time.Hour}

		got := uc.Execute(context.Background(), plannedTarget(), outlookNow)

		if got == nil {
			t.Fatal("a configured outlook must return a (omitted) outlook on failure, not nil")
		}
		if !strings.Contains(got.OmittedReason, msg) || !strings.Contains(got.OmittedReason, "get_process_path_capacity") {
			t.Errorf("reason must carry the upstream error: %q", got.OmittedReason)
		}
		if got.PlanningPathId != "tote-path" || got.WindowStart.IsZero() {
			t.Errorf("omitted outlook must still identify path and window: %+v", got)
		}
	}
}

func TestCapacityOutlook_ContractViolation_IsRejected(t *testing.T) {
	bad := goodCapacity()
	bad.NormalizedUnit = "UNIT"
	uc := &usecases.CapacityOutlook{Planning: &fakePlanning{capacity: bad}, Horizon: time.Hour}

	got := uc.Execute(context.Background(), plannedTarget(), outlookNow)

	if got == nil || got.OmittedReason == "" || got.NormalizedRate != 0 {
		t.Fatalf("a non-ORDER reading must be omitted with a reason, got %+v", got)
	}
}

// --- DailyBrief integration -------------------------------------------------

func briefWithOutlook(outlook *usecases.CapacityOutlook, target usecases.PathTarget) *usecases.DailyBrief {
	return &usecases.DailyBrief{
		Facility: &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "SIM1", Name: "Sim One"}}}},
		Wes: &fakeWes{backlog: map[string]ports.BacklogTelemetry{
			"pick-zone-a": {PathId: "pick-zone-a", BacklogDepth: 50, WIP: 10, OverAlarmThreshold: true},
		}},
		Fe: &fakeFe{
			queue: map[string]ports.QueueStatus{"PICK": {ProcessPath: "PICK", Depth: 40}},
			stuck: ports.StuckTasksResult{Count: 1, Tasks: []ports.StuckTask{{TaskId: "t1", Type: "PICK"}}},
		},
		Wfm: &fakeWfm{gaps: map[string]ports.StaffingGap{
			"pick-zone-a": {PathId: "pick-zone-a", PlannedHeads: 5, ActiveHeads: 2, Understaffed: true},
		}},
		Targets: []usecases.PathTarget{target},
		Now:     func() time.Time { return outlookNow },
		Outlook: outlook,
	}
}

func TestDailyBrief_NoOutlook_IsIdenticalToOutlookAbsent(t *testing.T) {
	target := plannedTarget() // even a path WITH a planning id must not change anything when unconfigured.
	without := briefWithOutlook(nil, target).Execute(context.Background())

	if without.Sites[0].Paths[0].CapacityOutlook != nil {
		t.Fatalf("an unconfigured outlook must leave the section absent: %+v", without.Sites[0].Paths[0].CapacityOutlook)
	}
	if got := without.Sites[0].Paths[0].Unavailable; len(got) != 0 {
		t.Fatalf("an unconfigured outlook must not add an unavailable entry: %v", got)
	}

	withPortNil := briefWithOutlook(&usecases.CapacityOutlook{Horizon: time.Hour}, target).Execute(context.Background())
	if !reflect.DeepEqual(without, withPortNil) {
		t.Fatalf("a use case without a planning port must equal no use case:\n%+v\n%+v", without, withPortNil)
	}
}

func TestDailyBrief_WithOutlook_AddsSectionAndChangesNothingElse(t *testing.T) {
	target := plannedTarget()
	base := briefWithOutlook(nil, target).Execute(context.Background())

	planning := &fakePlanning{capacity: goodCapacity()}
	got := briefWithOutlook(&usecases.CapacityOutlook{Planning: planning, Horizon: 8 * time.Hour}, target).Execute(context.Background())

	outlook := got.Sites[0].Paths[0].CapacityOutlook
	if outlook == nil || outlook.OmittedReason != "" || outlook.NormalizedRate != 120 || outlook.BindingConstraint != "STATION" {
		t.Fatalf("expected a populated outlook next to the brief data, got %+v", outlook)
	}

	// Strip the section and the brief must be exactly the baseline: the
	// outlook is purely additive and never feeds the exception rules.
	got.Sites[0].Paths[0].CapacityOutlook = nil
	if !reflect.DeepEqual(base, got) {
		t.Fatalf("the outlook changed the rest of the brief:\n%+v\n%+v", base, got)
	}
	if len(got.OpenExceptions) != 1 {
		t.Fatalf("setup: expected the 3-signal exception, got %d", len(got.OpenExceptions))
	}
}

func TestDailyBrief_PlanningDown_BriefStillSucceedsWithReason(t *testing.T) {
	target := plannedTarget()
	planning := &fakePlanning{err: errors.New("warehouse-planning: connect: connection refused")}
	got := briefWithOutlook(&usecases.CapacityOutlook{Planning: planning, Horizon: 8 * time.Hour}, target).Execute(context.Background())

	pb := got.Sites[0].Paths[0]
	if pb.CapacityOutlook == nil || !strings.Contains(pb.CapacityOutlook.OmittedReason, "connection refused") {
		t.Fatalf("expected the section omitted with the reason, got %+v", pb.CapacityOutlook)
	}
	if len(got.OpenExceptions) != 1 || pb.Backlog == nil || pb.Staffing == nil || pb.Queue == nil || pb.Stuck == nil || len(pb.Unavailable) != 0 {
		t.Fatalf("an unreachable planning must not degrade any other part of the brief: %+v", pb)
	}
}

func TestDailyBrief_UnmappedPath_OmitsSectionWithReason(t *testing.T) {
	target := plannedTarget()
	target.PlanningPathId = ""
	planning := &fakePlanning{capacity: goodCapacity()}
	got := briefWithOutlook(&usecases.CapacityOutlook{Planning: planning, Horizon: 8 * time.Hour}, target).Execute(context.Background())

	pb := got.Sites[0].Paths[0]
	if pb.CapacityOutlook == nil || !strings.Contains(pb.CapacityOutlook.OmittedReason, "planningPathId") {
		t.Fatalf("an unmapped path must say why its section is omitted, got %+v", pb.CapacityOutlook)
	}
	if len(planning.reqs) != 0 {
		t.Fatal("planning must not be asked about an unmapped path")
	}
}
