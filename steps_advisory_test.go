package main_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Steps for the decision-support use cases: daily brief, flow-balance
// advisory and explain-travel-factor.

const (
	defaultBuilding = "B1"
	defaultShift    = "S1"
)

// ---------------------------------------------------------------- Given ----

func (w *world) facilityLayoutKnowsSite(code, name string) error {
	w.facility.sites = append(w.facility.sites, ports.SiteRef{Code: code, Name: name})
	return nil
}

func (w *world) theDailyBriefMonitorsPath(pathID, processPath, site, building, shift string) error {
	w.targets = append(w.targets, usecases.PathTarget{SiteCode: site, PathId: pathID, ProcessPath: processPath, BuildingId: building, ShiftId: shift})
	return nil
}

func (w *world) pathIsBoundToProcessPath(pathID, processPath string) error {
	w.bindings[pathID] = processPath
	return nil
}

func (w *world) wesReportsBacklog(depth, wip int, pathID, threshold string) error {
	w.wes.backlog[pathID] = ports.BacklogTelemetry{PathId: pathID, BacklogDepth: depth, WIP: wip, OverAlarmThreshold: threshold == "over"}
	return nil
}

func (w *world) wesRecommends(action, pathID string, depth, wip int) error {
	w.wes.rebalance[pathID] = ports.RebalanceRecommendation{PathId: pathID, Action: action, BacklogDepth: depth, WIP: wip}
	return nil
}

func (w *world) wfmReportsHeads(planned, active int, pathID string) error {
	w.wfm.gaps[pathID] = ports.StaffingGap{PathId: pathID, PlannedHeads: planned, ActiveHeads: active, Understaffed: active < planned}
	return nil
}

func (w *world) feReportsQueueDepth(depth int, processPath string) error {
	w.fe.queues[processPath] = depth
	return nil
}

func (w *world) feReportsStuckTasks(n int, taskType string) error {
	if taskType == "" {
		taskType = "PICK"
	}
	for i := 0; i < n; i++ {
		w.fe.stuck = append(w.fe.stuck, ports.StuckTask{
			TaskId: fmt.Sprintf("stuck-%d", len(w.fe.stuck)+1), Type: taskType, Reason: "lease expired",
		})
	}
	return nil
}

func (w *world) lpReportsUtilization(pct float64, taskType string, taskSeconds, idleSeconds int64) error {
	w.lp.util[taskType] = ports.TaskTypeUtilization{
		TaskType: taskType, Associates: 4, WindowSeconds: taskSeconds + idleSeconds,
		TaskSeconds: taskSeconds, IdleSeconds: idleSeconds, UtilizationPct: &pct,
	}
	return nil
}

func (w *world) lpObservedNothing(taskType string) error {
	w.lp.util[taskType] = ports.TaskTypeUtilization{TaskType: taskType, WindowSeconds: 3600}
	return nil
}

func (w *world) mcpToolsAreUnreachable(upstream string) error {
	switch upstream {
	case "wes-work-planning":
		w.wes.down = true
	case "workforce-management":
		w.wfm.down = true
	case "fulfillment-execution":
		w.fe.down = true
	case "facility-layout":
		w.facility.down = true
	case "labor-performance":
		w.lp.down = true
	default:
		return fmt.Errorf("unknown MCP upstream %q", upstream)
	}
	return nil
}

func (w *world) facilityEstimates(metres float64, basis, from, to string) error {
	w.facility.routes[from+"|"+to] = ports.TravelDistance{MetresM: metres, Estimated: basis == "estimated"}
	return nil
}

func (w *world) facilityRejectsTheCodes(message string) error {
	w.facility.failure = fmt.Errorf("%s: %w", message, ports.ErrUpstreamInvalidInput)
	return nil
}

func (w *world) facilityFailsWith(message string) error {
	w.facility.failure = errors.New(message)
	return nil
}

// ----------------------------------------------------------------- When ----

func (w *world) iRequestTheDailyBrief(ctx context.Context) error { return w.get(ctx, "/daily-brief") }

func (w *world) iRequestTheFlowBalanceAdvisory(ctx context.Context, pathID string) error {
	return w.iRequestTheFlowBalanceAdvisoryScoped(ctx, pathID, defaultBuilding, defaultShift)
}

func (w *world) iRequestTheFlowBalanceAdvisoryScoped(ctx context.Context, pathID, building, shift string) error {
	q := url.Values{"buildingId": {building}, "shiftId": {shift}}
	return w.get(ctx, "/flow-balance/"+url.PathEscape(pathID)+"?"+q.Encode())
}

// iRequestTheFlowBalanceAdvisoryTimes repeats the request and requires every
// answer to be a 200: a degraded dependency must never leak to the caller as
// an error. The last response is the one the Then steps see.
func (w *world) iRequestTheFlowBalanceAdvisoryTimes(ctx context.Context, pathID string, n int) error {
	for i := 1; i <= n; i++ {
		if err := w.iRequestTheFlowBalanceAdvisory(ctx, pathID); err != nil {
			return err
		}
		if w.status != 200 {
			return fmt.Errorf("request %d of %d: expected 200, got %d: %s", i, n, w.status, string(w.body))
		}
	}
	return nil
}

func (w *world) iAskToExplainTheTravelFactor(ctx context.Context, pathID, from, to string) error {
	q := url.Values{"pathId": {pathID}, "fromLocationCode": {from}, "toLocationCode": {to}}
	return w.get(ctx, "/explain-travel-factor?"+q.Encode())
}

// ----------------------------------------------------------------- Then ----

func (w *world) theRecommendedActionIs(action string, heads int) error {
	if err := w.theJSONFieldEquals("recommendedAction", action); err != nil {
		return err
	}
	// proposedHeads is omitted from the body when it is zero.
	v, found, err := w.jsonAt("proposedHeads")
	if err != nil {
		return err
	}
	if !found {
		if heads != 0 {
			return fmt.Errorf("expected %d proposed heads, but proposedHeads is absent", heads)
		}
		return nil
	}
	if got := scalar(v); got != strconv.Itoa(heads) {
		return fmt.Errorf("expected %d proposed heads, got %s", heads, got)
	}
	return nil
}

func (w *world) theDecisionSourceIs(source string) error {
	return w.theJSONFieldEquals("source", source)
}

func (w *world) theDecisionIsPartial(negation string) error {
	want := "true"
	if negation != "" {
		want = "false"
	}
	return w.theJSONFieldEquals("partial", want)
}

func (w *world) theRationaleMentions(fragment string) error {
	return w.theJSONFieldContains("rationale", fragment)
}

func (w *world) theEvidenceCites(source string) error {
	arr, err := w.arrayAt("evidence")
	if err != nil {
		return err
	}
	for _, entry := range arr {
		if m, ok := entry.(map[string]any); ok && scalar(m["source"]) == source {
			return nil
		}
	}
	return fmt.Errorf("expected the evidence to cite %q, got %s", source, scalar(arr))
}

func (w *world) wfmWasAskedAbout(building, shift string) error {
	want := building + "|" + shift
	for _, asked := range w.wfm.asked {
		if asked == want {
			return nil
		}
	}
	return fmt.Errorf("expected workforce-management to be asked about %q, got %v", want, w.wfm.asked)
}

func (w *world) facilityWasAskedForTheRoute(from, to string) error {
	want := from + "|" + to
	for _, call := range w.facility.calls {
		if call == want {
			return nil
		}
	}
	return fmt.Errorf("expected facility-layout to be asked for route %q, got %v", want, w.facility.calls)
}

func (w *world) facilityWasNotCalled() error {
	if len(w.facility.calls) != 0 {
		return fmt.Errorf("expected facility-layout not to be called, got %v", w.facility.calls)
	}
	return nil
}

func registerAdvisorySteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^facility-layout knows site "([^"]*)" as "([^"]*)"$`, w.facilityLayoutKnowsSite)
	sc.Step(`^the daily brief (?:also )?monitors path "([^"]*)" with process path "([^"]*)" at site "([^"]*)" in building "([^"]*)" for shift "([^"]*)"$`, w.theDailyBriefMonitorsPath)
	sc.Step(`^path "([^"]*)" is bound to process path "([^"]*)"$`, w.pathIsBoundToProcessPath)
	sc.Step(`^wes-work-planning reports backlog depth (\d+) and WIP (\d+) for path "([^"]*)" (over|within) its alarm threshold$`, w.wesReportsBacklog)
	sc.Step(`^wes-work-planning recommends "([^"]*)" for path "([^"]*)" with backlog depth (\d+) and WIP (\d+)$`, w.wesRecommends)
	sc.Step(`^workforce-management reports (\d+) planned and (\d+) active heads for path "([^"]*)"$`, w.wfmReportsHeads)
	sc.Step(`^fulfillment-execution reports a queue depth of (\d+) for process path "([^"]*)"$`, w.feReportsQueueDepth)
	sc.Step(`^fulfillment-execution reports (\d+) stuck tasks?(?: of type "([^"]*)")?$`, w.feReportsStuckTasks)
	sc.Step(`^labor-performance reports ([\d.]+) percent utilization for task type "([^"]*)" with (\d+) task seconds and (\d+) idle seconds$`, w.lpReportsUtilization)
	sc.Step(`^labor-performance observed nothing for task type "([^"]*)"$`, w.lpObservedNothing)
	sc.Step(`^(wes-work-planning|workforce-management|fulfillment-execution|facility-layout|labor-performance)'s MCP tools are unreachable$`, w.mcpToolsAreUnreachable)
	sc.Step(`^facility-layout estimates ([\d.]+) metres (measured|estimated) between "([^"]*)" and "([^"]*)"$`, w.facilityEstimates)
	sc.Step(`^facility-layout rejects the location codes with "([^"]*)"$`, w.facilityRejectsTheCodes)
	sc.Step(`^facility-layout fails with "([^"]*)"$`, w.facilityFailsWith)

	sc.Step(`^I request the daily brief$`, w.iRequestTheDailyBrief)
	sc.Step(`^I request the flow-balance advisory for path "([^"]*)"$`, w.iRequestTheFlowBalanceAdvisory)
	sc.Step(`^I request the flow-balance advisory for path "([^"]*)" in building "([^"]*)" shift "([^"]*)"$`, w.iRequestTheFlowBalanceAdvisoryScoped)
	sc.Step(`^I request the flow-balance advisory for path "([^"]*)" (\d+) times$`, w.iRequestTheFlowBalanceAdvisoryTimes)
	sc.Step(`^I ask to explain the travel factor for path "([^"]*)" from "([^"]*)" to "([^"]*)"$`, w.iAskToExplainTheTravelFactor)

	sc.Step(`^the recommended action is "([^"]*)" with (\d+) proposed heads$`, w.theRecommendedActionIs)
	sc.Step(`^the decision source is "([^"]*)"$`, w.theDecisionSourceIs)
	sc.Step(`^the decision is (not )?partial$`, w.theDecisionIsPartial)
	sc.Step(`^the rationale mentions "([^"]*)"$`, w.theRationaleMentions)
	sc.Step(`^the evidence cites "([^"]*)"$`, w.theEvidenceCites)
	sc.Step(`^workforce-management was asked about building "([^"]*)" and shift "([^"]*)"$`, w.wfmWasAskedAbout)
	sc.Step(`^facility-layout was asked for the route from "([^"]*)" to "([^"]*)"$`, w.facilityWasAskedForTheRoute)
	sc.Step(`^facility-layout was not called$`, w.facilityWasNotCalled)
}
