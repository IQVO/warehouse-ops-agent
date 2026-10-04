package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakePlanning struct {
	capacity ports.ProcessPathCapacity
	err      error
}

func (f *fakePlanning) GetProcessPathCapacity(context.Context, ports.ProcessPathCapacityRequest) (ports.ProcessPathCapacity, error) {
	return f.capacity, f.err
}
func (f *fakePlanning) GetCapacityPlan(context.Context, string) (ports.CapacityPlan, error) {
	return ports.CapacityPlan{}, errors.New("not used")
}
func (f *fakePlanning) GetStorageCapacity(context.Context, string) (ports.StorageCapacity, error) {
	return ports.StorageCapacity{}, errors.New("not used")
}
func (f *fakePlanning) ListStationStandards(context.Context, string) (ports.StationStandardList, error) {
	return ports.StationStandardList{}, errors.New("not used")
}

func plannedDeps() Deps {
	deps := newTestDeps(true)
	deps.DailyBrief.Targets[0].PlanningPathId = "tote-path"
	return deps
}

func firstPathOutlook(t *testing.T, deps Deps) (*capacityOutlookDTO, dailyBriefOutput) {
	t.Helper()
	out, err := deps.getDailyBrief(context.Background(), dailyBriefInput{})
	if err != nil {
		t.Fatalf("a planning outage must never fail the brief: %v", err)
	}
	return out.Sites[0].Paths[0].CapacityOutlook, out
}

func TestGetDailyBrief_CapacityOutlook_UnconfiguredIsAbsent(t *testing.T) {
	o, _ := firstPathOutlook(t, plannedDeps())
	if o != nil {
		t.Fatalf("an unconfigured outlook must be absent: %+v", o)
	}
}

func TestGetDailyBrief_CapacityOutlook_Populated(t *testing.T) {
	deps := plannedDeps()
	deps.DailyBrief.Outlook = &usecases.CapacityOutlook{Horizon: 8 * time.Hour, Planning: &fakePlanning{capacity: ports.ProcessPathCapacity{
		NormalizedRate: 120, NormalizedUnit: "ORDER", BottleneckStep: "PACK",
		StepBreakdown: []ports.PlanningStepBreakdown{{Step: "PACK", NormalizedRate: 120, BindingConstraint: "STATION"}},
		Warnings:      []string{"w"},
	}}}

	o, out := firstPathOutlook(t, deps)

	if o == nil || o.NormalizedRate == nil || *o.NormalizedRate != 120 {
		t.Fatalf("unexpected outlook DTO: %+v", o)
	}
	if o.BottleneckStep != "PACK" || o.BindingConstraint != "STATION" || o.OmittedReason != "" {
		t.Errorf("unexpected outlook DTO: %+v", o)
	}
	if len(o.Steps) != 1 || len(o.Warnings) != 1 {
		t.Errorf("steps/warnings: %+v", o)
	}
	if len(out.OpenExceptions) != 1 {
		t.Errorf("the outlook must not change exceptions, got %d", len(out.OpenExceptions))
	}
}

func TestGetDailyBrief_CapacityOutlook_PlanningDownIsOmittedWithReason(t *testing.T) {
	deps := plannedDeps()
	deps.DailyBrief.Outlook = &usecases.CapacityOutlook{Horizon: 8 * time.Hour, Planning: &fakePlanning{err: errors.New("boom")}}

	o, _ := firstPathOutlook(t, deps)

	if o == nil || o.NormalizedRate != nil || !strings.Contains(o.OmittedReason, "boom") {
		t.Fatalf("expected an omitted outlook DTO with the reason, got %+v", o)
	}
}
