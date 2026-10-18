package usecases_test

import (
	"context"
	"errors"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakeFacility, fakeWes, fakeFe, fakeWfm are table-driven, in-memory fakes
// of the outbound MCP-client ports this use case orchestrates — no live
// server, per the T4 card's guardrail ("Unit-tested against FAKED MCP
// client ports").

type fakeFacility struct {
	sites     ports.SitesResult
	err       error
	travel    ports.TravelDistance
	travelErr error
}

func (f *fakeFacility) ListSites(ctx context.Context) (ports.SitesResult, error) {
	if f.err != nil {
		return ports.SitesResult{}, f.err
	}
	return f.sites, nil
}
func (f *fakeFacility) EstimateTravelDistance(ctx context.Context, from, to string) (ports.TravelDistance, error) {
	if f.travelErr != nil {
		return ports.TravelDistance{}, f.travelErr
	}
	return f.travel, nil
}

type fakeWes struct {
	backlog map[string]ports.BacklogTelemetry
	err     map[string]error
}

func (f *fakeWes) GetBacklogTelemetry(ctx context.Context, pathId string) (ports.BacklogTelemetry, error) {
	if err, ok := f.err[pathId]; ok {
		return ports.BacklogTelemetry{}, err
	}
	return f.backlog[pathId], nil
}
func (f *fakeWes) GetRebalanceRecommendation(ctx context.Context, pathId string) (ports.RebalanceRecommendation, error) {
	return ports.RebalanceRecommendation{}, errors.New("not implemented in fake")
}

type fakeFe struct {
	queue map[string]ports.QueueStatus
	stuck ports.StuckTasksResult
	err   map[string]error
	// stuckErr, when non-nil, is returned by DiagnoseStuckTasks
	// unconditionally, independent of the queue-keyed err map above.
	stuckErr error
}

func (f *fakeFe) GetQueueStatus(ctx context.Context, processPath string) (ports.QueueStatus, error) {
	if err, ok := f.err[processPath]; ok {
		return ports.QueueStatus{}, err
	}
	return f.queue[processPath], nil
}
func (f *fakeFe) DiagnoseStuckTasks(ctx context.Context, withinSeconds int) (ports.StuckTasksResult, error) {
	if f.stuckErr != nil {
		return ports.StuckTasksResult{}, f.stuckErr
	}
	return f.stuck, nil
}

type fakeWfm struct {
	gaps map[string]ports.StaffingGap
	err  map[string]error
}

func (f *fakeWfm) GetStaffingGap(ctx context.Context, buildingId, shiftId, pathId string) (ports.StaffingGap, error) {
	if err, ok := f.err[pathId]; ok {
		return ports.StaffingGap{}, err
	}
	return f.gaps[pathId], nil
}

// fakeSlotting is a table-driven fake of the slotting-optimization port:
// canned list pages keyed by cursor, canned plans keyed by id, and optional
// errors for either call.
type fakeSlotting struct {
	pages     map[string]ports.SlotPlanPage // cursor -> page ("" is the first)
	plans     map[string]ports.SlotPlan
	listErr   error
	getErr    map[string]error
	lastQuery ports.SlotPlanListQuery
	getCalls  int
}

func (f *fakeSlotting) ListSlotPlans(ctx context.Context, query ports.SlotPlanListQuery) (ports.SlotPlanPage, error) {
	f.lastQuery = query
	if f.listErr != nil {
		return ports.SlotPlanPage{}, f.listErr
	}
	if page, ok := f.pages[query.Cursor]; ok {
		return page, nil
	}
	return ports.SlotPlanPage{}, nil
}

func (f *fakeSlotting) GetSlotPlan(ctx context.Context, planId string) (ports.SlotPlan, error) {
	f.getCalls++
	if err, ok := f.getErr[planId]; ok {
		return ports.SlotPlan{}, err
	}
	return f.plans[planId], nil
}

// fakeOrderMgmtMCP is a table-driven fake of the order-management MCP
// port: one canned promise-health reading, or an error.
type fakeOrderMgmtMCP struct {
	health ports.PromiseHealth
	err    error
	query  ports.PromiseHealthQuery
}

func (f *fakeOrderMgmtMCP) GetPromiseHealth(ctx context.Context, query ports.PromiseHealthQuery) (ports.PromiseHealth, error) {
	f.query = query
	if f.err != nil {
		return ports.PromiseHealth{}, f.err
	}
	return f.health, nil
}

var (
	_ ports.FacilityLayoutClient       = (*fakeFacility)(nil)
	_ ports.WesWorkPlanningClient      = (*fakeWes)(nil)
	_ ports.FulfillmentExecutionClient = (*fakeFe)(nil)
	_ ports.WorkforceManagementClient  = (*fakeWfm)(nil)
	_ ports.SlottingOptimizationClient = (*fakeSlotting)(nil)
	_ ports.OrderManagementMCPClient   = (*fakeOrderMgmtMCP)(nil)
)
