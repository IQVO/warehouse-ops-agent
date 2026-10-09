package main_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Fakes for the console BFF's REST upstreams and the observability readers.

// ------------------------------------------------------ order lifecycle ----

// fakeOrderStack plays order-management, inventory-storage, wes-work-planning
// and fulfillment-execution's REST APIs at once: their four client ports have
// disjoint method names, so one struct satisfies all of them.
type fakeOrderStack struct {
	mu           sync.Mutex
	orders       map[string]*ports.OrderDTO
	reservations map[string][]ports.ReservationDTO
	workUnits    map[string][]ports.WorkUnitDTO
	tasks        map[string][]ports.TaskDTO
	down         map[string]bool
	taskAsks     []string
}

func newFakeOrderStack() *fakeOrderStack {
	return &fakeOrderStack{
		orders:       map[string]*ports.OrderDTO{},
		reservations: map[string][]ports.ReservationDTO{},
		workUnits:    map[string][]ports.WorkUnitDTO{},
		tasks:        map[string][]ports.TaskDTO{},
		down:         map[string]bool{},
	}
}

func (f *fakeOrderStack) GetOrder(_ context.Context, orderID string) (*ports.OrderDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down["order-management"] {
		return nil, errUnreachable
	}
	o, ok := f.orders[orderID]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return o, nil
}

func (f *fakeOrderStack) GetReservationsByDemandRef(_ context.Context, demandRef string) ([]ports.ReservationDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down["inventory-storage"] {
		return nil, errUnreachable
	}
	return append([]ports.ReservationDTO{}, f.reservations[demandRef]...), nil
}

func (f *fakeOrderStack) GetWorkUnitsByReference(_ context.Context, reference string) ([]ports.WorkUnitDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down["wes-work-planning"] {
		return nil, errUnreachable
	}
	return append([]ports.WorkUnitDTO{}, f.workUnits[reference]...), nil
}

func (f *fakeOrderStack) GetTasksByOrderRef(_ context.Context, orderRef string) ([]ports.TaskDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskAsks = append(f.taskAsks, orderRef)
	if f.down["fulfillment-execution"] {
		return nil, errUnreachable
	}
	return append([]ports.TaskDTO{}, f.tasks[orderRef]...), nil
}

// ------------------------------------------------------------- reports -----

// fakeReports plays the seven *-reports readers. down and freshnessDown are
// keyed by the bounded context's name.
type fakeReports struct {
	mu            sync.Mutex
	funnel        ports.FunnelReport
	flow          ports.FlowAccuracyReport
	catalog       ports.CatalogGrowthReport
	planning      ports.PlanningThroughputReport
	fulfillment   ports.FulfillmentThroughputReport
	labor         ports.LaborReport
	laborPerf     ports.LaborPerformanceReport
	down          map[string]bool
	freshness     map[string]float64
	freshnessDown map[string]bool
}

func newFakeReports() *fakeReports {
	return &fakeReports{down: map[string]bool{}, freshness: map[string]float64{}, freshnessDown: map[string]bool{}}
}

func (f *fakeReports) reportErr(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[name] {
		return errUnreachable
	}
	return nil
}

func (f *fakeReports) lag(name string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[name] || f.freshnessDown[name] {
		return 0, errUnreachable
	}
	return f.freshness[name], nil
}

func (f *fakeReports) GetFunnelReport(context.Context, time.Time, time.Time) (ports.FunnelReport, error) {
	return f.funnel, f.reportErr("order-management")
}

func (f *fakeReports) GetFunnelFreshnessLagSeconds(context.Context) (float64, error) {
	return f.lag("order-management")
}

func (f *fakeReports) GetFlowAccuracyReport(context.Context, time.Time, time.Time) (ports.FlowAccuracyReport, error) {
	return f.flow, f.reportErr("inventory-storage")
}

func (f *fakeReports) GetFlowAccuracyFreshnessLagSeconds(context.Context) (float64, error) {
	return f.lag("inventory-storage")
}

func (f *fakeReports) GetCatalogGrowthReport(context.Context, time.Time, time.Time) (ports.CatalogGrowthReport, error) {
	return f.catalog, f.reportErr("facility-layout")
}

func (f *fakeReports) GetCatalogGrowthFreshnessLagSeconds(context.Context) (float64, error) {
	return f.lag("facility-layout")
}

func (f *fakeReports) GetPlanningThroughputReport(context.Context, time.Time, time.Time) (ports.PlanningThroughputReport, error) {
	return f.planning, f.reportErr("wes-work-planning")
}

func (f *fakeReports) GetPlanningThroughputFreshnessLagSeconds(context.Context) (float64, error) {
	return f.lag("wes-work-planning")
}

func (f *fakeReports) GetFulfillmentThroughputReport(context.Context, time.Time, time.Time) (ports.FulfillmentThroughputReport, error) {
	return f.fulfillment, f.reportErr("fulfillment-execution")
}

func (f *fakeReports) GetFulfillmentThroughputFreshnessLagSeconds(context.Context) (float64, error) {
	return f.lag("fulfillment-execution")
}

func (f *fakeReports) GetLaborReport(context.Context, time.Time, time.Time) (ports.LaborReport, error) {
	return f.labor, f.reportErr("workforce-management")
}

func (f *fakeReports) GetLaborFreshnessLagSeconds(context.Context) (float64, error) {
	return f.lag("workforce-management")
}

func (f *fakeReports) GetLaborPerformanceReport(context.Context, time.Time, time.Time) (ports.LaborPerformanceReport, error) {
	return f.laborPerf, f.reportErr("labor-performance")
}

func (f *fakeReports) GetLaborPerformanceFreshnessLagSeconds(context.Context) (float64, error) {
	return f.lag("labor-performance")
}

// ------------------------------------------------------- observability -----

type serviceStats struct{ total, failed, p99 float64 }

// fakeTelemetry answers the three PromQL shapes RuntimeSignals issues
// (request total, 5xx total, p99 latency) from per-service statistics.
type fakeTelemetry struct {
	down  bool
	stats map[string]serviceStats
}

var serviceLabel = regexp.MustCompile(`destination_service_name="([^"]+)"`)

func (f *fakeTelemetry) InstantQuery(_ context.Context, promQL string) ([]ports.MetricSample, error) {
	if f.down {
		return nil, errUnreachable
	}
	m := serviceLabel.FindStringSubmatch(promQL)
	if m == nil {
		return nil, nil
	}
	st, ok := f.stats[m[1]]
	if !ok {
		return nil, nil
	}
	switch {
	case strings.Contains(promQL, "histogram_quantile"):
		return []ports.MetricSample{{Value: st.p99}}, nil
	case strings.Contains(promQL, `response_code=~"5.."`):
		return []ports.MetricSample{{Value: st.failed}}, nil
	default:
		return []ports.MetricSample{{Value: st.total}}, nil
	}
}

func (f *fakeTelemetry) RangeQuery(context.Context, string, time.Time, time.Time, time.Duration) ([]ports.MetricSample, error) {
	return nil, errNotUsed
}

// fakeLogs answers the Loki error-line query from per-service counts.
type fakeLogs struct {
	down  bool
	lines map[string]int
}

func (f *fakeLogs) QueryErrorLines(context.Context, string, int64, int) ([]ports.LogEntry, error) {
	if f.down {
		return nil, errUnreachable
	}
	var out []ports.LogEntry
	for service, n := range f.lines {
		for i := 0; i < n; i++ {
			out = append(out, ports.LogEntry{Labels: map[string]string{"app": service}, Line: "level=error boom"})
		}
	}
	return out, nil
}
