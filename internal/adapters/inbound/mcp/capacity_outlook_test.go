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

func TestGetDailyBrief_CapacityOutlook(t *testing.T) {
	deps := newTestDeps(true)
	deps.DailyBrief.Targets[0].PlanningPathId = "tote-path"

	// Unconfigured: the DTO carries no outlook.
	out, err := deps.getDailyBrief(context.Background(), dailyBriefInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Sites[0].Paths[0].CapacityOutlook != nil {
		t.Fatalf("an unconfigured outlook must be absent: %+v", out.Sites[0].Paths[0].CapacityOutlook)
	}

	// Configured and healthy.
	deps.DailyBrief.Outlook = &usecases.CapacityOutlook{Horizon: 8 * time.Hour, Planning: &fakePlanning{capacity: ports.ProcessPathCapacity{
		NormalizedRate: 120, NormalizedUnit: "ORDER", BottleneckStep: "PACK",
		StepBreakdown: []ports.PlanningStepBreakdown{{Step: "PACK", NormalizedRate: 120, BindingConstraint: "STATION"}},
		Warnings:      []string{"w"},
	}}}
	out, err = deps.getDailyBrief(context.Background(), dailyBriefInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o := out.Sites[0].Paths[0].CapacityOutlook
	if o == nil || o.NormalizedRate == nil || *o.NormalizedRate != 120 || o.BottleneckStep != "PACK" || o.BindingConstraint != "STATION" || len(o.Steps) != 1 || len(o.Warnings) != 1 || o.OmittedReason != "" {
		t.Fatalf("unexpected outlook DTO: %+v", o)
	}
	if len(out.OpenExceptions) != 1 {
		t.Fatalf("the outlook must not change exceptions, got %d", len(out.OpenExceptions))
	}

	// Configured but planning is down: omitted with a reason, no rate.
	deps.DailyBrief.Outlook.Planning = &fakePlanning{err: errors.New("boom")}
	out, err = deps.getDailyBrief(context.Background(), dailyBriefInput{})
	if err != nil {
		t.Fatalf("a planning outage must not fail the brief: %v", err)
	}
	o = out.Sites[0].Paths[0].CapacityOutlook
	if o == nil || o.NormalizedRate != nil || !strings.Contains(o.OmittedReason, "boom") {
		t.Fatalf("expected an omitted outlook DTO with the reason, got %+v", o)
	}
}
