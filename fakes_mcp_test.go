package main_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The fakes in this file stand in for the outbound MCP clients of the
// decision-support use cases. Each one is plain in-memory state configured by
// Given steps; none of them opens a connection.

var (
	errUnreachable = errors.New("connection refused")
	errNotUsed     = errors.New("fake: this tool is not used by any scenario")
)

// ---------------------------------------------------------------- wes ------

type fakeWes struct {
	down      bool
	backlog   map[string]ports.BacklogTelemetry
	rebalance map[string]ports.RebalanceRecommendation
}

func newFakeWes() *fakeWes {
	return &fakeWes{backlog: map[string]ports.BacklogTelemetry{}, rebalance: map[string]ports.RebalanceRecommendation{}}
}

func (f *fakeWes) GetBacklogTelemetry(_ context.Context, pathID string) (ports.BacklogTelemetry, error) {
	if f.down {
		return ports.BacklogTelemetry{}, errUnreachable
	}
	b, ok := f.backlog[pathID]
	if !ok {
		return ports.BacklogTelemetry{}, fmt.Errorf("wes-work-planning: no backlog telemetry for path %q", pathID)
	}
	return b, nil
}

func (f *fakeWes) GetRebalanceRecommendation(_ context.Context, pathID string) (ports.RebalanceRecommendation, error) {
	if f.down {
		return ports.RebalanceRecommendation{}, errUnreachable
	}
	r, ok := f.rebalance[pathID]
	if !ok {
		return ports.RebalanceRecommendation{}, fmt.Errorf("wes-work-planning: no rebalance recommendation for path %q", pathID)
	}
	return r, nil
}

// ---------------------------------------------------------------- wfm ------

type fakeWFM struct {
	down  bool
	gaps  map[string]ports.StaffingGap
	asked []string
}

func newFakeWFM() *fakeWFM { return &fakeWFM{gaps: map[string]ports.StaffingGap{}} }

func (f *fakeWFM) GetStaffingGap(_ context.Context, buildingID, shiftID, pathID string) (ports.StaffingGap, error) {
	f.asked = append(f.asked, buildingID+"|"+shiftID)
	if f.down {
		return ports.StaffingGap{}, errUnreachable
	}
	g, ok := f.gaps[pathID]
	if !ok {
		return ports.StaffingGap{}, fmt.Errorf("workforce-management: no staffing gap for path %q", pathID)
	}
	return g, nil
}

func (f *fakeWFM) ProposePathHeads(context.Context, string, string, float64, float64) (ports.ProposedHeads, error) {
	return ports.ProposedHeads{}, errNotUsed
}

// ----------------------------------------------------------------- fe ------

type fakeFE struct {
	down   bool
	queues map[string]int
	stuck  []ports.StuckTask
}

func (f *fakeFE) GetQueueStatus(_ context.Context, processPath string) (ports.QueueStatus, error) {
	if f.down {
		return ports.QueueStatus{}, errUnreachable
	}
	return ports.QueueStatus{ProcessPath: processPath, Depth: f.queues[processPath]}, nil
}

func (f *fakeFE) FindClaimableWork(context.Context, string) (ports.ClaimableWorkResult, error) {
	return ports.ClaimableWorkResult{}, errNotUsed
}

func (f *fakeFE) DiagnoseStuckTasks(context.Context, int) (ports.StuckTasksResult, error) {
	if f.down {
		return ports.StuckTasksResult{}, errUnreachable
	}
	return ports.StuckTasksResult{Count: len(f.stuck), Tasks: f.stuck}, nil
}

// ----------------------------------------------------------- facility ------

type fakeFacility struct {
	down    bool
	sites   []ports.SiteRef
	routes  map[string]ports.TravelDistance
	failure error
	calls   []string
}

func newFakeFacility() *fakeFacility {
	return &fakeFacility{routes: map[string]ports.TravelDistance{}}
}

func (f *fakeFacility) ListSites(context.Context) (ports.SitesResult, error) {
	if f.down {
		return ports.SitesResult{}, errUnreachable
	}
	return ports.SitesResult{Sites: f.sites}, nil
}

func (f *fakeFacility) GetSiteLayout(context.Context, string) (ports.SiteLayout, error) {
	return ports.SiteLayout{}, errNotUsed
}

func (f *fakeFacility) GetZoneGrid(context.Context, string) (ports.ZoneGrid, error) {
	return ports.ZoneGrid{}, errNotUsed
}

func (f *fakeFacility) EstimateTravelDistance(_ context.Context, from, to string) (ports.TravelDistance, error) {
	f.calls = append(f.calls, from+"|"+to)
	if f.down {
		return ports.TravelDistance{}, errUnreachable
	}
	if f.failure != nil {
		return ports.TravelDistance{}, f.failure
	}
	d, ok := f.routes[from+"|"+to]
	if !ok {
		return ports.TravelDistance{}, fmt.Errorf("facility-layout: no route between %q and %q", from, to)
	}
	return d, nil
}

// ----------------------------------------------------------------- lp ------

type fakeLP struct {
	down bool
	util map[string]ports.TaskTypeUtilization
}

func (f *fakeLP) GetTaskTypeUtilization(_ context.Context, taskType string, _ int64) (ports.TaskTypeUtilization, error) {
	if f.down {
		return ports.TaskTypeUtilization{}, errUnreachable
	}
	u, ok := f.util[taskType]
	if !ok {
		return ports.TaskTypeUtilization{}, fmt.Errorf("labor-performance: no utilization for task type %q", taskType)
	}
	return u, nil
}

func (f *fakeLP) GetAssociateScorecard(context.Context, string) (ports.AssociateScorecard, error) {
	return ports.AssociateScorecard{}, errNotUsed
}

func (f *fakeLP) GetTaskTypePerformance(context.Context, string) (ports.TaskTypePerformance, error) {
	return ports.TaskTypePerformance{}, errNotUsed
}

func (f *fakeLP) GetLaborStandard(context.Context, string) (ports.LaborStandard, error) {
	return ports.LaborStandard{}, errNotUsed
}

// ------------------------------------------------------------ reasoner -----

// fakeReasoner is the ports.Reasoner a scenario wires in place of a model. It
// records what the use case showed it.
type fakeReasoner struct {
	mu    sync.Mutex
	plan  ports.Plan
	err   error
	calls int
	brief ports.Brief
}

func (f *fakeReasoner) Reason(_ context.Context, brief ports.Brief) (ports.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.brief = brief
	if f.err != nil {
		return ports.Plan{}, f.err
	}
	return f.plan, nil
}

type arbitrationRecord struct {
	useCase, mode, source string
	agree                 *bool
}

// fakeMetrics is the ports.ArbitrationMetrics a scenario can inspect.
type fakeMetrics struct {
	mu      sync.Mutex
	records []arbitrationRecord
}

func (f *fakeMetrics) RecordArbitration(_ context.Context, useCase, mode, source string, agree *bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, arbitrationRecord{useCase: useCase, mode: mode, source: source, agree: agree})
}

func agreementWord(agree *bool) string {
	switch {
	case agree == nil:
		return "unknown"
	case *agree:
		return "true"
	default:
		return "false"
	}
}

func quoteList(items []string) string { return strings.Join(items, ", ") }
