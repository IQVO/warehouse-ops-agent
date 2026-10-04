package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
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

func getBriefBody(t *testing.T, uc *usecases.DailyBrief) map[string]any {
	t.Helper()
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{DailyBrief: uc}, "warehouse-ops-agent-test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/daily-brief", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func firstPath(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	sites := body["sites"].([]any)
	return sites[0].(map[string]any)["paths"].([]any)[0].(map[string]any)
}

func plannedBrief(planning ports.WarehousePlanningClient) *usecases.DailyBrief {
	uc := newTestDailyBrief()
	uc.Targets[0].PlanningPathId = "tote-path"
	if planning != nil {
		uc.Outlook = &usecases.CapacityOutlook{Planning: planning, Horizon: 8 * time.Hour}
	}
	return uc
}

func TestGetDailyBrief_NoOutlook_OmitsCapacityOutlookKey(t *testing.T) {
	path := firstPath(t, getBriefBody(t, plannedBrief(nil)))
	if _, present := path["capacityOutlook"]; present {
		t.Fatalf("an unconfigured outlook must not appear in the response at all: %+v", path)
	}
}

func TestGetDailyBrief_WithOutlook_SerialisesRateBottleneckAndConstraint(t *testing.T) {
	planning := &fakePlanning{capacity: ports.ProcessPathCapacity{
		NormalizedRate: 120, NormalizedUnit: "ORDER", BottleneckStep: "PACK",
		StepBreakdown: []ports.PlanningStepBreakdown{
			{Step: "PICK", NormalizedRate: 300, BindingConstraint: "LABOR"},
			{Step: "PACK", NormalizedRate: 120, BindingConstraint: "STATION"},
		},
		Warnings: []string{"stations tallied without a declared standard: REBIN"},
	}}
	path := firstPath(t, getBriefBody(t, plannedBrief(planning)))

	o, ok := path["capacityOutlook"].(map[string]any)
	if !ok {
		t.Fatalf("expected a capacityOutlook object next to the brief data: %+v", path)
	}
	if o["normalizedRate"] != float64(120) || o["bottleneckStep"] != "PACK" || o["bindingConstraint"] != "STATION" || o["planningPathId"] != "tote-path" {
		t.Errorf("unexpected outlook: %+v", o)
	}
	if _, has := o["omittedReason"]; has {
		t.Errorf("a populated outlook must not carry an omittedReason: %+v", o)
	}
	if steps, _ := o["steps"].([]any); len(steps) != 2 {
		t.Errorf("steps = %+v", o["steps"])
	}
	if w, _ := o["warnings"].([]any); len(w) != 1 {
		t.Errorf("warnings = %+v", o["warnings"])
	}
	if o["windowStart"] != "1970-01-01T00:00:00Z" || o["windowEnd"] != "1970-01-01T08:00:00Z" {
		t.Errorf("window = %v..%v", o["windowStart"], o["windowEnd"])
	}
	// The existing exception data is untouched.
	if ex, _ := path["exceptions"].([]any); len(ex) != 1 {
		t.Errorf("existing exceptions changed: %+v", path["exceptions"])
	}
}

func TestGetDailyBrief_OutlookOmitted_SerialisesReasonAndNoRate(t *testing.T) {
	planning := &fakePlanning{err: errors.New("warehouse-planning: connect: connection refused")}
	path := firstPath(t, getBriefBody(t, plannedBrief(planning)))

	o, ok := path["capacityOutlook"].(map[string]any)
	if !ok {
		t.Fatalf("expected an omitted outlook object: %+v", path)
	}
	if reason, _ := o["omittedReason"].(string); !strings.Contains(reason, "connection refused") {
		t.Errorf("omittedReason = %v", o["omittedReason"])
	}
	for _, k := range []string{"normalizedRate", "bottleneckStep", "bindingConstraint", "steps", "warnings"} {
		if _, has := o[k]; has {
			t.Errorf("an omitted outlook must not carry %q (a missing rate must never read as 0): %+v", k, o)
		}
	}
}

func TestGetDailyBrief_OutlookZeroRate_IsReportedNotOmitted(t *testing.T) {
	planning := &fakePlanning{capacity: ports.ProcessPathCapacity{NormalizedRate: 0, NormalizedUnit: "ORDER", BottleneckStep: "PACK",
		StepBreakdown: []ports.PlanningStepBreakdown{{Step: "PACK", NormalizedRate: 0, BindingConstraint: "STATION"}}}}
	o := firstPath(t, getBriefBody(t, plannedBrief(planning)))["capacityOutlook"].(map[string]any)
	if rate, present := o["normalizedRate"]; !present || rate != float64(0) {
		t.Fatalf("a real zero capacity must serialise as 0, got %+v", o)
	}
}
