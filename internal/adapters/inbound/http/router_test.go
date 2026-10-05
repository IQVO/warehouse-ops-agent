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
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakeFacility, fakeWes, fakeFe, fakeWfm mirror the application-layer
// fakes (kept package-local here since Go test fakes aren't exported
// across packages).

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

func newTestDailyBrief() *usecases.DailyBrief {
	return &usecases.DailyBrief{
		Facility: &fakeFacility{sites: ports.SitesResult{Sites: []ports.SiteRef{{Code: "WH1", Name: "One"}}}},
		Wes:      &fakeWes{backlog: ports.BacklogTelemetry{PathId: "pick-zone-a", BacklogDepth: 50, WIP: 10, OverAlarmThreshold: true}},
		Fe: &fakeFe{
			queue: ports.QueueStatus{ProcessPath: "PICK", Depth: 40},
			stuck: ports.StuckTasksResult{Count: 1, Tasks: []ports.StuckTask{{TaskId: "t1", Type: "PICK"}}},
		},
		Wfm:     &fakeWfm{gap: ports.StaffingGap{PathId: "pick-zone-a", PlannedHeads: 5, ActiveHeads: 2, Understaffed: true}},
		Targets: []usecases.PathTarget{{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK"}},
		Now:     func() time.Time { return time.Unix(0, 0) },
	}
}

func TestGetDailyBrief_Returns200WithSynthesizedBrief(t *testing.T) {
	handlers := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/daily-brief", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		Sites []struct {
			SiteCode string `json:"siteCode"`
			Paths    []struct {
				PathId     string `json:"pathId"`
				Exceptions []struct {
					Severity string `json:"severity"`
				} `json:"exceptions"`
			} `json:"paths"`
		} `json:"sites"`
		OpenExceptions []struct {
			Severity string `json:"severity"`
		} `json:"openExceptions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, rec.Body.String())
	}

	if len(body.Sites) != 1 || body.Sites[0].SiteCode != "WH1" {
		t.Fatalf("unexpected sites in response: %+v", body.Sites)
	}
	if len(body.OpenExceptions) != 1 {
		t.Fatalf("expected 1 open exception (3 correlated signals), got %d", len(body.OpenExceptions))
	}
	if body.OpenExceptions[0].Severity != string(policy.SeverityCritical) {
		t.Errorf("severity = %q, want critical", body.OpenExceptions[0].Severity)
	}
}

func TestGetDailyBrief_EmptyTargets_Returns200EmptyBrief(t *testing.T) {
	uc := &usecases.DailyBrief{
		Facility: &fakeFacility{},
		Wes:      &fakeWes{},
		Fe:       &fakeFe{},
		Wfm:      &fakeWfm{},
		Now:      func() time.Time { return time.Unix(0, 0) },
	}
	handlers := &inboundhttp.Handlers{DailyBrief: uc}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/daily-brief", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHealthz_Returns200(t *testing.T) {
	handlers := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// --- flow-balance ------------------------------------------------------

type fbFakeWes struct{ recommendation ports.RebalanceRecommendation }

func (f *fbFakeWes) GetBacklogTelemetry(ctx context.Context, pathId string) (ports.BacklogTelemetry, error) {
	return ports.BacklogTelemetry{}, nil
}
func (f *fbFakeWes) GetRebalanceRecommendation(ctx context.Context, pathId string) (ports.RebalanceRecommendation, error) {
	return f.recommendation, nil
}

func newTestFlowBalanceAdvisory() *usecases.FlowBalanceAdvisory {
	return &usecases.FlowBalanceAdvisory{
		Wes: &fbFakeWes{recommendation: ports.RebalanceRecommendation{
			PathId: "pick-a", Action: "ReassignLabor", BacklogDepth: 90, WIP: 30,
		}},
		WFM: &fakeWfm{gap: ports.StaffingGap{PathId: "pick-a", PlannedHeads: 10, ActiveHeads: 6, Understaffed: true}},
		FE:  &fakeFe{stuck: ports.StuckTasksResult{Count: 0}},
	}
}

func TestGetFlowBalanceException_Returns200WithDecision(t *testing.T) {
	handlers := &inboundhttp.Handlers{
		DailyBrief:          newTestDailyBrief(),
		FlowBalanceAdvisory: newTestFlowBalanceAdvisory(),
	}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/flow-balance/pick-a?buildingId=bldg-1&shiftId=shift-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		PathId            string `json:"pathId"`
		RecommendedAction string `json:"recommendedAction"`
		ProposedHeads     int    `json:"proposedHeads"`
		Source            string `json:"source"`
		Evidence          []struct {
			Source string `json:"source"`
			Detail string `json:"detail"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, rec.Body.String())
	}
	if body.RecommendedAction != "assign_labor" {
		t.Errorf("recommendedAction = %q, want assign_labor", body.RecommendedAction)
	}
	if body.ProposedHeads != 4 {
		t.Errorf("proposedHeads = %d, want 4", body.ProposedHeads)
	}
	if len(body.Evidence) != 3 {
		t.Errorf("len(evidence) = %d, want 3: %+v", len(body.Evidence), body.Evidence)
	}
	// ADR-0004: with no Reasoner wired (LLM_MODE=off implied), Source
	// must round-trip as "deterministic", never empty.
	if body.Source != "deterministic" {
		t.Errorf("source = %q, want deterministic", body.Source)
	}
}

func TestGetFlowBalanceException_NotConfigured_Returns503(t *testing.T) {
	handlers := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/flow-balance/pick-a?buildingId=bldg-1&shiftId=shift-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// --- console-bff: order lifecycle ---------------------------------------

type fakeOM struct {
	order *ports.OrderDTO
	err   error
}

func (f *fakeOM) GetOrder(ctx context.Context, orderId string) (*ports.OrderDTO, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.order, nil
}

type fakeInv struct{ reservations []ports.ReservationDTO }

func (f *fakeInv) GetReservationsByDemandRef(ctx context.Context, demandRef string) ([]ports.ReservationDTO, error) {
	return f.reservations, nil
}

type fakeWU struct{ units []ports.WorkUnitDTO }

func (f *fakeWU) GetWorkUnitsByReference(ctx context.Context, reference string) ([]ports.WorkUnitDTO, error) {
	return f.units, nil
}

type fakeTasks struct{ tasks []ports.TaskDTO }

func (f *fakeTasks) GetTasksByOrderRef(ctx context.Context, orderRef string) ([]ports.TaskDTO, error) {
	return f.tasks, nil
}

func TestGetOrderLifecycle_Returns200WithAssembledStages(t *testing.T) {
	var om ports.OrderManagementClient = &fakeOM{order: &ports.OrderDTO{
		ID:     "ord-1",
		Status: "Released",
		Lines:  []ports.OrderLineDTO{{LineNo: 1, SKU: "SKU-1", Quantity: 2, Status: "Released"}},
	}}
	uc := &usecases.OrderLifecycle{
		OrderManagement: &om,
		Inventory:       &fakeInv{reservations: []ports.ReservationDTO{{SKU: "SKU-1", Quantity: 2, Status: "CONFIRMED"}}},
		WorkUnits:       &fakeWU{units: []ports.WorkUnitDTO{{Id: "ord-1-line-1", PathId: "pick-zone-a", State: "Released"}}},
		Tasks:           &fakeTasks{tasks: []ports.TaskDTO{{Id: "task-1", Type: "PICK", Status: "COMPLETED"}}},
	}
	handlers := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief(), OrderLifecycle: uc}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/console/orders/ord-1/lifecycle", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		OrderId         string `json:"orderId"`
		OrderManagement struct {
			Status string `json:"status"`
		} `json:"orderManagement"`
		Inventory struct {
			Reservations []struct {
				SKU string `json:"sku"`
			} `json:"reservations"`
		} `json:"inventory"`
		Planning struct {
			WorkUnits []struct {
				WorkUnitId string `json:"workUnitId"`
			} `json:"workUnits"`
		} `json:"planning"`
		Fulfillment struct {
			Tasks []struct {
				TaskId string `json:"taskId"`
			} `json:"tasks"`
		} `json:"fulfillment"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, rec.Body.String())
	}
	if body.OrderId != "ord-1" || body.OrderManagement.Status != "Released" {
		t.Fatalf("unexpected order stage: %+v", body)
	}
	if len(body.Inventory.Reservations) != 1 || body.Inventory.Reservations[0].SKU != "SKU-1" {
		t.Fatalf("unexpected inventory stage: %+v", body.Inventory)
	}
	if len(body.Planning.WorkUnits) != 1 || body.Planning.WorkUnits[0].WorkUnitId != "ord-1-line-1" {
		t.Fatalf("unexpected planning stage: %+v", body.Planning)
	}
	if len(body.Fulfillment.Tasks) != 1 || body.Fulfillment.Tasks[0].TaskId != "task-1" {
		t.Fatalf("unexpected fulfillment stage: %+v", body.Fulfillment)
	}
}

func TestGetOrderLifecycle_OrderNotFound_Returns404(t *testing.T) {
	var om ports.OrderManagementClient = &fakeOM{err: ports.ErrNotFound}
	uc := &usecases.OrderLifecycle{OrderManagement: &om}
	handlers := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief(), OrderLifecycle: uc}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/console/orders/missing/lifecycle", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

func TestGetOrderLifecycle_NotConfigured_Returns503(t *testing.T) {
	handlers := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/console/orders/ord-1/lifecycle", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestCORS_PreflightAllowsConsoleOrigin(t *testing.T) {
	handlers := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodOptions, "/daily-brief", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want http://localhost:5173", got)
	}
}

// --- console-bff: report dashboards -----------------------------------

// stubFunnelReports / stubLaborReports etc. are the minimum fakes needed
// to drive the two dashboard routes end to end through the real router.

type stubFunnelReports struct {
	report ports.FunnelReport
	err    error
}

func (s *stubFunnelReports) GetFunnelReport(ctx context.Context, from, to time.Time) (ports.FunnelReport, error) {
	return s.report, s.err
}
func (s *stubFunnelReports) GetFunnelFreshnessLagSeconds(ctx context.Context) (float64, error) {
	return 12.5, nil
}

type stubFlowAccuracyReports struct{ err error }

func (s *stubFlowAccuracyReports) GetFlowAccuracyReport(ctx context.Context, from, to time.Time) (ports.FlowAccuracyReport, error) {
	if s.err != nil {
		return ports.FlowAccuracyReport{}, s.err
	}
	return ports.FlowAccuracyReport{Rows: []ports.FlowAccuracyRow{{StowedCount: 4, PickedQuantity: 9}}}, nil
}
func (s *stubFlowAccuracyReports) GetFlowAccuracyFreshnessLagSeconds(ctx context.Context) (float64, error) {
	return 1, nil
}

type stubPlanningReports struct{}

func (s *stubPlanningReports) GetPlanningThroughputReport(ctx context.Context, from, to time.Time) (ports.PlanningThroughputReport, error) {
	return ports.PlanningThroughputReport{Rows: []ports.PlanningThroughputRow{
		{HourBucket: "2026-09-04T00:00:00Z", WorkUnitCompleted: 6},
	}}, nil
}
func (s *stubPlanningReports) GetPlanningThroughputFreshnessLagSeconds(ctx context.Context) (float64, error) {
	return 2, nil
}

type stubLaborPerformanceReports struct{ err error }

func (s *stubLaborPerformanceReports) GetLaborPerformanceReport(ctx context.Context, from, to time.Time) (ports.LaborPerformanceReport, error) {
	if s.err != nil {
		return ports.LaborPerformanceReport{}, s.err
	}
	pct := 88.0
	return ports.LaborPerformanceReport{ByTaskType: []ports.LaborPerformanceTaskType{
		{TaskType: "PICK", MeanEfficiencyPct: &pct},
		{TaskType: "PACK", MeanEfficiencyPct: nil},
	}}, nil
}
func (s *stubLaborPerformanceReports) GetLaborPerformanceFreshnessLagSeconds(ctx context.Context) (float64, error) {
	return 3, nil
}

// dashboardResponse decodes the wire envelope as the console sees it --
// deliberately a separate local struct, not the adapter's unexported
// DTO, so a rename of a JSON tag fails this test.
type dashboardResponse struct {
	From        string             `json:"from"`
	To          string             `json:"to"`
	GeneratedAt string             `json:"generatedAt"`
	Sections    []dashboardSection `json:"sections"`
}

// dashboardSection is one chart-ready section of the dashboard envelope.
type dashboardSection struct {
	Id                  string   `json:"id"`
	Title               string   `json:"title"`
	SourceContext       string   `json:"sourceContext"`
	ChartKind           string   `json:"chartKind"`
	Available           bool     `json:"available"`
	Error               *string  `json:"error"`
	FreshnessLagSeconds *float64 `json:"freshnessLagSeconds"`
	Series              []struct {
		Label string  `json:"label"`
		Value float64 `json:"value"`
	} `json:"series"`
}

func newTestConsoleReports() *usecases.ConsoleReports {
	return &usecases.ConsoleReports{
		OrderFunnel: &stubFunnelReports{report: ports.FunnelReport{Rows: []ports.FunnelRow{
			{OrdersReceived: 10, OrdersAllocated: 7, OrdersReleased: 5, OrdersCancelled: 1},
		}}},
		InventoryFlowAccuracy: &stubFlowAccuracyReports{},
		PlanningThroughput:    &stubPlanningReports{},
		LaborPerformance:      &stubLaborPerformanceReports{},
		Now:                   func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) },
	}
}

func TestGetWMSDashboard_Returns200WithChartReadySections(t *testing.T) {
	rec := serveDashboardRequest(t, newTestConsoleReports(), "/console/reports/wms?from=2026-09-04T00:00:00Z&to=2026-09-05T00:00:00Z")
	body := decodeDashboardResponse(t, rec)

	assertDashboardWindow(t, body, "2026-09-04T00:00:00Z", "2026-09-05T00:00:00Z", 3)
	assertOrderFunnelSection(t, body.Sections[0])
	// The unwired facility-layout client degrades to an unavailable
	// section rather than failing the whole response.
	assertSectionDegraded(t, body.Sections[2], "catalog-growth")
}

// serveDashboardRequest wires the console-reports use case into the router,
// serves one GET, and requires a 200 back.
func serveDashboardRequest(t *testing.T, uc *usecases.ConsoleReports, target string) *httptest.ResponseRecorder {
	t.Helper()
	handlers := &inboundhttp.Handlers{ConsoleReports: uc}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

// decodeDashboardResponse decodes the recorder's body as the console's
// wire envelope.
func decodeDashboardResponse(t *testing.T, rec *httptest.ResponseRecorder) dashboardResponse {
	t.Helper()
	var body dashboardResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// assertDashboardWindow checks the echoed query window, the generatedAt
// timestamp, and the section count.
func assertDashboardWindow(t *testing.T, body dashboardResponse, wantFrom, wantTo string, wantSections int) {
	t.Helper()
	if body.From != wantFrom || body.To != wantTo {
		t.Fatalf("window not echoed: %+v", body)
	}
	if body.GeneratedAt == "" {
		t.Fatal("expected a generatedAt timestamp")
	}
	if len(body.Sections) != wantSections {
		t.Fatalf("expected %d sections, got %d", wantSections, len(body.Sections))
	}
}

// assertOrderFunnelSection checks the first WMS section: the order funnel
// chart with its freshness lag and series.
func assertOrderFunnelSection(t *testing.T, funnel dashboardSection) {
	t.Helper()
	if funnel.Id != "order-funnel" || funnel.ChartKind != "funnel" || funnel.SourceContext != "order-management" {
		t.Fatalf("unexpected first section: %+v", funnel)
	}
	if !funnel.Available || funnel.Error != nil {
		t.Fatalf("expected an available section with a null error, got %+v", funnel)
	}
	if funnel.FreshnessLagSeconds == nil || *funnel.FreshnessLagSeconds != 12.5 {
		t.Fatalf("expected freshnessLagSeconds 12.5, got %v", funnel.FreshnessLagSeconds)
	}
	if len(funnel.Series) != 4 || funnel.Series[0].Label != "Received" || funnel.Series[0].Value != 10 {
		t.Fatalf("unexpected funnel series: %+v", funnel.Series)
	}
}

// assertSectionDegraded checks that a section degraded to unavailable with
// a non-null error instead of failing the whole response.
func assertSectionDegraded(t *testing.T, section dashboardSection, wantId string) {
	t.Helper()
	if section.Id != wantId || section.Available || section.Error == nil {
		t.Fatalf("expected %s to degrade, got %+v", wantId, section)
	}
}

func TestGetWESDashboard_DegradedSectionSerialisesAsEmptyArrayNotNull(t *testing.T) {
	uc := newTestConsoleReports()
	uc.LaborPerformance = &stubLaborPerformanceReports{err: errors.New("connection refused")}
	handlers := &inboundhttp.Handlers{ConsoleReports: uc}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/console/reports/wes", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("a dead upstream must never fail the dashboard; got %d: %s", rec.Code, rec.Body.String())
	}
	// A chart component binding to null crashes; binding to [] renders an
	// honest empty chart. Assert on the raw JSON, since a decode into a
	// Go slice would hide the difference.
	if strings.Contains(rec.Body.String(), `"series":null`) {
		t.Fatalf("series must serialise as [], never null: %s", rec.Body.String())
	}

	var body dashboardResponse
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sections) != 4 {
		t.Fatalf("expected 4 WES sections, got %d", len(body.Sections))
	}
	perf := body.Sections[3]
	if perf.Id != "labor-performance" || perf.Available {
		t.Fatalf("expected labor-performance to be degraded, got %+v", perf)
	}
	if perf.Error == nil || *perf.Error != "labor-performance reports not available" {
		t.Fatalf("unexpected error message: %+v", perf.Error)
	}
	if body.Sections[0].Id != "planning-throughput" || !body.Sections[0].Available {
		t.Fatalf("sibling sections must survive: %+v", body.Sections[0])
	}
}

// TestGetDashboard_OmittedWindow_DefaultsToTrailing24Hours covers the one
// place these BFF routes deliberately diverge from their strict
// upstreams: from/to are optional here.
func TestGetDashboard_OmittedWindow_DefaultsToTrailing24Hours(t *testing.T) {
	handlers := &inboundhttp.Handlers{ConsoleReports: newTestConsoleReports()}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/console/reports/wms", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body dashboardResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.To != "2026-09-05T00:00:00Z" || body.From != "2026-09-04T00:00:00Z" {
		t.Fatalf("expected a trailing 24h window ending at the injected now, got %s..%s", body.From, body.To)
	}
}

func TestGetDashboard_MalformedTimestamp_Returns400(t *testing.T) {
	handlers := &inboundhttp.Handlers{ConsoleReports: newTestConsoleReports()}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	for _, target := range []string{
		"/console/reports/wms?from=yesterday",
		"/console/reports/wes?to=not-a-time",
		// An inverted window is rejected here rather than fanned out, so
		// the caller learns their request was wrong instead of seeing
		// every section report its upstream as unavailable.
		"/console/reports/wms?from=2026-09-05T00:00:00Z&to=2026-09-04T00:00:00Z",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d: %s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestGetDashboards_NotConfigured_Returns503(t *testing.T) {
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{}, "warehouse-ops-agent-test")

	for _, target := range []string{"/console/reports/wms", "/console/reports/wes"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: expected 503, got %d", target, rec.Code)
		}
	}
}
