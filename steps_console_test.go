package main_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Steps for the console BFF (order lifecycle, report dashboards) and the
// runtime-signals report.

var reportContexts = map[string]bool{
	"order-management": true, "inventory-storage": true, "facility-layout": true,
	"wes-work-planning": true, "fulfillment-execution": true, "workforce-management": true,
	"labor-performance": true,
}

// ---------------------------------------------------------------- Given ----

func (w *world) orderManagementHasOrder(id, status string, qty int, sku string) error {
	w.orders.orders[id] = &ports.OrderDTO{
		ID: id, Status: status,
		Lines: []ports.OrderLineDTO{{LineNo: 1, SKU: sku, Quantity: qty, Status: "Pending"}},
	}
	return nil
}

func (w *world) inventoryHasReservation(status string, qty int, sku, demandRef string) error {
	w.orders.reservations[demandRef] = append(w.orders.reservations[demandRef], ports.ReservationDTO{
		ID: "RES-" + demandRef, SKU: sku, Quantity: qty, DemandRef: demandRef, Status: status,
	})
	return nil
}

func (w *world) wesHasWorkUnit(workUnitID, pathID, state, orderID string) error {
	w.orders.workUnits[orderID] = append(w.orders.workUnits[orderID], ports.WorkUnitDTO{
		Id: workUnitID, PathId: pathID, Reference: orderID, State: state,
	})
	return nil
}

func (w *world) feHasTask(status, taskType, taskID, workUnitID string) error {
	w.orders.tasks[workUnitID] = append(w.orders.tasks[workUnitID], ports.TaskDTO{
		Id: taskID, Type: taskType, Status: status, OrderRef: workUnitID,
	})
	return nil
}

func (w *world) restAPIIsUnreachable(upstream string) error {
	w.orders.down[upstream] = true
	return nil
}

func (w *world) funnelReportShows(received, allocated, partial, released, cancelled, failed int) error {
	w.reports.funnel.Rows = append(w.reports.funnel.Rows, ports.FunnelRow{
		OrdersReceived: received, OrdersAllocated: allocated, OrdersPartiallyAllocated: partial,
		OrdersReleased: released, OrdersCancelled: cancelled, OrdersAllocationFailed: failed,
	})
	return nil
}

func (w *world) flowAccuracyReportShows(stowed, picked, discrepancies, unlocated int) error {
	w.reports.flow.Rows = append(w.reports.flow.Rows, ports.FlowAccuracyRow{
		StowedCount: stowed, PickedQuantity: picked, DiscrepanciesDetected: discrepancies, UnlocatedCount: unlocated,
	})
	return nil
}

func (w *world) catalogGrowthReportHas(slots int, day string) error {
	w.reports.catalog.Rows = append(w.reports.catalog.Rows, ports.CatalogGrowthRow{DayBucket: day, SlotsRegistered: slots})
	return nil
}

func (w *world) planningThroughputReportHas(completed int, hour string) error {
	w.reports.planning.Rows = append(w.reports.planning.Rows, ports.PlanningThroughputRow{HourBucket: hour, WorkUnitCompleted: completed})
	return nil
}

func (w *world) fulfillmentThroughputReportHas(completions int, taskType string) error {
	w.reports.fulfillment.Rows = append(w.reports.fulfillment.Rows, ports.FulfillmentThroughputRow{TaskType: taskType, Completions: completions})
	return nil
}

func (w *world) laborReportShows(shifts, assigned, understaffing int) error {
	w.reports.labor.Rows = append(w.reports.labor.Rows, ports.LaborRow{
		ShiftsStarted: shifts, LaborAssigned: assigned, UnderstaffingEvents: understaffing,
	})
	return nil
}

func (w *world) laborPerformanceReportShows(taskType string, efficiency float64) error {
	w.reports.laborPerf.ByTaskType = append(w.reports.laborPerf.ByTaskType, ports.LaborPerformanceTaskType{
		TaskType: taskType, MeanEfficiencyPct: &efficiency,
	})
	return nil
}

func (w *world) laborPerformanceReportShowsNothingScorable(taskType string) error {
	w.reports.laborPerf.ByTaskType = append(w.reports.laborPerf.ByTaskType, ports.LaborPerformanceTaskType{TaskType: taskType})
	return nil
}

func knownReportContext(name string) error {
	if !reportContexts[name] {
		return fmt.Errorf("unknown report context %q", name)
	}
	return nil
}

func (w *world) reportsReaderIsUnreachable(name string) error {
	if err := knownReportContext(name); err != nil {
		return err
	}
	w.reports.down[name] = true
	return nil
}

func (w *world) reportsReaderIsNotConfigured(name string) error {
	if err := knownReportContext(name); err != nil {
		return err
	}
	w.unwired["reports:"+name] = true
	return nil
}

func (w *world) reportsFreshnessIs(name string, seconds float64) error {
	if err := knownReportContext(name); err != nil {
		return err
	}
	w.reports.freshness[name] = seconds
	return nil
}

func (w *world) reportsFreshnessIsUnavailable(name string) error {
	if err := knownReportContext(name); err != nil {
		return err
	}
	w.reports.freshnessDown[name] = true
	return nil
}

func (w *world) runtimeSignalsMonitor(services string) error {
	w.runtimeServices = strings.Split(services, ", ")
	return nil
}

func (w *world) serviceServed(service string, total, failed, p99 float64) error {
	w.telemetry.stats[service] = serviceStats{total: total, failed: failed, p99: p99}
	return nil
}

func (w *world) lokiHoldsErrorLines(n int, service string) error {
	if w.logs.lines == nil {
		w.logs.lines = map[string]int{}
	}
	w.logs.lines[service] = n
	return nil
}

func (w *world) lokiIsUnreachable() error {
	w.logs.down = true
	return nil
}

func (w *world) prometheusIsUnreachable() error {
	w.telemetry.down = true
	return nil
}

func (w *world) lokiIsNotConfigured() error {
	w.unwired["loki"] = true
	return nil
}

// ----------------------------------------------------------------- When ----

func (w *world) iOpenTheLifecycleOfOrder(ctx context.Context, orderID string) error {
	return w.get(ctx, "/console/orders/"+url.PathEscape(orderID)+"/lifecycle")
}

func (w *world) iOpenTheDashboard(ctx context.Context, kind string) error {
	return w.get(ctx, "/console/reports/"+strings.ToLower(kind))
}

func (w *world) iOpenTheDashboardBetween(ctx context.Context, kind, from, to string) error {
	q := url.Values{"from": {from}, "to": {to}}
	return w.get(ctx, "/console/reports/"+strings.ToLower(kind)+"?"+q.Encode())
}

func (w *world) iRequestTheRuntimeSignals(ctx context.Context) error {
	return w.get(ctx, "/runtime-signals")
}

// ----------------------------------------------------------------- Then ----

func (w *world) fulfillmentWasAskedForTheTasksOf(workUnitID string) error {
	w.orders.mu.Lock()
	defer w.orders.mu.Unlock()
	for _, asked := range w.orders.taskAsks {
		if asked == workUnitID {
			return nil
		}
	}
	return fmt.Errorf("expected fulfillment-execution to be asked for the tasks of %q, got %v", workUnitID, w.orders.taskAsks)
}

func (w *world) dashboardSection(id string) (map[string]any, error) {
	sections, err := w.arrayAt("sections")
	if err != nil {
		return nil, err
	}
	for _, s := range sections {
		if m, ok := s.(map[string]any); ok && scalar(m["id"]) == id {
			return m, nil
		}
	}
	return nil, fmt.Errorf("expected a dashboard section %q, got %s", id, scalar(sections))
}

func (w *world) theDashboardListsTheSections(ids string) error {
	sections, err := w.arrayAt("sections")
	if err != nil {
		return err
	}
	got := make([]string, 0, len(sections))
	for _, s := range sections {
		if m, ok := s.(map[string]any); ok {
			got = append(got, scalar(m["id"]))
		}
	}
	if joined := strings.Join(got, ", "); joined != ids {
		return fmt.Errorf("expected the sections %q in that order, got %q", ids, joined)
	}
	return nil
}

func seriesOf(section map[string]any) (string, error) {
	points, ok := section["series"].([]any)
	if !ok {
		return "", fmt.Errorf("section %q has no series array (a degraded section must serialise [] , never null): %s", scalar(section["id"]), scalar(section["series"]))
	}
	parts := make([]string, 0, len(points))
	for _, p := range points {
		m, _ := p.(map[string]any)
		parts = append(parts, scalar(m["label"])+"="+scalar(m["value"]))
	}
	return strings.Join(parts, ", "), nil
}

func (w *world) theDashboardSectionIsAvailableWithTheSeries(id, series string) error {
	section, err := w.dashboardSection(id)
	if err != nil {
		return err
	}
	if section["available"] != true || section["error"] != nil {
		return fmt.Errorf("expected section %q to be available without an error, got available=%v error=%v", id, section["available"], section["error"])
	}
	got, err := seriesOf(section)
	if err != nil {
		return err
	}
	if got != series {
		return fmt.Errorf("expected section %q to carry the series %q, got %q", id, series, got)
	}
	return nil
}

func (w *world) theDashboardSectionIsUnavailableWithTheError(id, message string) error {
	section, err := w.dashboardSection(id)
	if err != nil {
		return err
	}
	if section["available"] != false || scalar(section["error"]) != message {
		return fmt.Errorf("expected section %q unavailable with error %q, got available=%v error=%v", id, message, section["available"], section["error"])
	}
	got, err := seriesOf(section)
	if err != nil {
		return err
	}
	if got != "" {
		return fmt.Errorf("expected an unavailable section %q to carry an empty series, got %q", id, got)
	}
	return nil
}

func (w *world) theDashboardSectionReportsFreshnessLag(id string, seconds float64) error {
	section, err := w.dashboardSection(id)
	if err != nil {
		return err
	}
	if got, ok := section["freshnessLagSeconds"].(float64); !ok || got != seconds {
		return fmt.Errorf("expected section %q to report a freshness lag of %v seconds, got %v", id, seconds, section["freshnessLagSeconds"])
	}
	return nil
}

func (w *world) theDashboardSectionHasNoFreshnessLag(id string) error {
	section, err := w.dashboardSection(id)
	if err != nil {
		return err
	}
	if v, present := section["freshnessLagSeconds"]; !present || v != nil {
		return fmt.Errorf("expected section %q to serialise an explicit null freshnessLagSeconds, got %v (present=%t)", id, v, present)
	}
	return nil
}

func registerConsoleSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^order-management has order "([^"]*)" with status "([^"]*)" and a line of (\d+) units of SKU "([^"]*)"$`, w.orderManagementHasOrder)
	sc.Step(`^inventory-storage has an? "([^"]*)" reservation of (\d+) units of SKU "([^"]*)" for demand "([^"]*)"$`, w.inventoryHasReservation)
	sc.Step(`^wes-work-planning has work unit "([^"]*)" on path "([^"]*)" in state "([^"]*)" for order "([^"]*)"$`, w.wesHasWorkUnit)
	sc.Step(`^fulfillment-execution has a "([^"]*)" "([^"]*)" task "([^"]*)" for work unit "([^"]*)"$`, w.feHasTask)
	sc.Step(`^the (order-management|inventory-storage|wes-work-planning|fulfillment-execution) REST API is unreachable$`, w.restAPIIsUnreachable)

	sc.Step(`^order-management's funnel report shows (\d+) received, (\d+) allocated, (\d+) partially allocated, (\d+) released, (\d+) cancelled and (\d+) allocation failed$`, w.funnelReportShows)
	sc.Step(`^inventory-storage's flow accuracy report shows (\d+) stowed, (\d+) picked, (\d+) discrepancies and (\d+) unlocated$`, w.flowAccuracyReportShows)
	sc.Step(`^facility-layout's catalog growth report has (\d+) slots registered on day "([^"]*)"$`, w.catalogGrowthReportHas)
	sc.Step(`^wes-work-planning's throughput report has (\d+) completed work units in hour "([^"]*)"$`, w.planningThroughputReportHas)
	sc.Step(`^fulfillment-execution's throughput report has (\d+) completions for task type "([^"]*)"$`, w.fulfillmentThroughputReportHas)
	sc.Step(`^workforce-management's labor report shows (\d+) shifts started, (\d+) labor assigned and (\d+) understaffing events$`, w.laborReportShows)
	sc.Step(`^labor-performance's report shows task type "([^"]*)" at ([\d.]+) percent mean efficiency$`, w.laborPerformanceReportShows)
	sc.Step(`^labor-performance's report shows task type "([^"]*)" with no scorable tasks$`, w.laborPerformanceReportShowsNothingScorable)
	sc.Step(`^the "([^"]*)" reports reader is unreachable$`, w.reportsReaderIsUnreachable)
	sc.Step(`^the "([^"]*)" reports reader is not configured$`, w.reportsReaderIsNotConfigured)
	sc.Step(`^the "([^"]*)" reports freshness is ([\d.]+) seconds$`, w.reportsFreshnessIs)
	sc.Step(`^the "([^"]*)" reports freshness is unavailable$`, w.reportsFreshnessIsUnavailable)

	sc.Step(`^runtime signals monitor the services "([^"]*)"$`, w.runtimeSignalsMonitor)
	sc.Step(`^service "([^"]*)" served ([\d.]+) requests of which ([\d.]+) failed with 5xx, with a p99 latency of ([\d.]+) ms$`, w.serviceServed)
	sc.Step(`^Loki holds (\d+) error log lines for service "([^"]*)"$`, w.lokiHoldsErrorLines)
	sc.Step(`^Loki is unreachable$`, w.lokiIsUnreachable)
	sc.Step(`^Prometheus is unreachable$`, w.prometheusIsUnreachable)
	sc.Step(`^Loki is not configured$`, w.lokiIsNotConfigured)

	sc.Step(`^I open the lifecycle of order "([^"]*)"$`, w.iOpenTheLifecycleOfOrder)
	sc.Step(`^I open the (WMS|WES) dashboard$`, w.iOpenTheDashboard)
	sc.Step(`^I open the (WMS|WES) dashboard from "([^"]*)" to "([^"]*)"$`, w.iOpenTheDashboardBetween)
	sc.Step(`^I request the runtime signals$`, w.iRequestTheRuntimeSignals)

	sc.Step(`^fulfillment-execution was asked for the tasks of "([^"]*)"$`, w.fulfillmentWasAskedForTheTasksOf)
	sc.Step(`^the dashboard lists the sections "([^"]*)"$`, w.theDashboardListsTheSections)
	sc.Step(`^the dashboard section "([^"]*)" is available with the series "([^"]*)"$`, w.theDashboardSectionIsAvailableWithTheSeries)
	sc.Step(`^the dashboard section "([^"]*)" is unavailable with the error "([^"]*)"$`, w.theDashboardSectionIsUnavailableWithTheError)
	sc.Step(`^the dashboard section "([^"]*)" reports a freshness lag of ([\d.]+) seconds$`, w.theDashboardSectionReportsFreshnessLag)
	sc.Step(`^the dashboard section "([^"]*)" has no freshness lag$`, w.theDashboardSectionHasNoFreshnessLag)
}
