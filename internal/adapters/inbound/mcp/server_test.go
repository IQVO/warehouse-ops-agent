package mcp_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

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

// newServer builds a real MCP HTTP server wired to a DailyBrief use case
// seeded to produce one open exception, and returns its httptest URL.
func newServer(t *testing.T) string {
	t.Helper()
	dailyBrief := &usecases.DailyBrief{
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

	server := inboundmcp.NewServer(inboundmcp.Deps{DailyBrief: dailyBrief})
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

// newServerWithFlowBalance additionally wires a FlowBalanceAdvisory use
// case seeded to recommend assign_labor, so get_flow_balance_exception is
// registered and exercisable over the wire.
func newServerWithFlowBalance(t *testing.T) string {
	t.Helper()
	dailyBrief := &usecases.DailyBrief{Now: func() time.Time { return time.Unix(0, 0) }}
	flowBalanceAdvisory := &usecases.FlowBalanceAdvisory{
		Wes: &wesRebalanceFake{recommendation: ports.RebalanceRecommendation{
			PathId: "pick-a", Action: "ReassignLabor", BacklogDepth: 90, WIP: 30,
		}},
		WFM: &fakeWfm{gap: ports.StaffingGap{PathId: "pick-a", PlannedHeads: 10, ActiveHeads: 6, Understaffed: true}},
		FE:  &fakeFe{stuck: ports.StuckTasksResult{Count: 0}},
	}

	server := inboundmcp.NewServer(inboundmcp.Deps{DailyBrief: dailyBrief, FlowBalanceAdvisory: flowBalanceAdvisory})
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

// wesRebalanceFake is a minimal ports.WesWorkPlanningClient fake returning
// a fixed RebalanceRecommendation, used only by newServerWithFlowBalance.
type wesRebalanceFake struct {
	recommendation ports.RebalanceRecommendation
}

func (f *wesRebalanceFake) GetBacklogTelemetry(ctx context.Context, pathId string) (ports.BacklogTelemetry, error) {
	return ports.BacklogTelemetry{}, nil
}
func (f *wesRebalanceFake) GetRebalanceRecommendation(ctx context.Context, pathId string) (ports.RebalanceRecommendation, error) {
	return f.recommendation, nil
}

func connect(t *testing.T, url string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	transport := &sdk.StreamableClientTransport{Endpoint: url}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestServer_ToolsListAndCall(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	ctx := context.Background()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]bool{"get_daily_brief": false, "list_open_exceptions": false}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q not advertised", name)
		}
	}

	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "get_daily_brief", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	sites, ok := res.StructuredContent.(map[string]any)["sites"]
	if !ok {
		t.Fatalf("no sites in structured content: %+v", res.StructuredContent)
	}
	if len(sites.([]any)) != 1 {
		t.Fatalf("sites = %v, want 1 entry", sites)
	}
}

func TestServer_ListOpenExceptions_OverTheWire(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "list_open_exceptions",
		Arguments: map[string]any{"severity": "critical"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	count, ok := res.StructuredContent.(map[string]any)["count"]
	if !ok || count.(float64) != 1 {
		t.Fatalf("count = %v, want 1", count)
	}
}

func TestServer_CallToolRejectsUnknownSeverity(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "list_open_exceptions",
		Arguments: map[string]any{"severity": "meltdown"},
	})
	if err != nil {
		t.Fatalf("call tool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool-level error for unknown severity value")
	}
}

func TestServer_ToolsList_OmitsFlowBalanceExceptionWhenAdvisoryNotWired(t *testing.T) {
	url := newServer(t) // no FlowBalanceAdvisory
	session := connect(t, url)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "get_flow_balance_exception" {
			t.Fatal("get_flow_balance_exception should not be advertised when FlowBalanceAdvisory is not wired")
		}
	}
}

func TestServer_GetFlowBalanceException_OverTheWire(t *testing.T) {
	url := newServerWithFlowBalance(t)
	session := connect(t, url)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "get_flow_balance_exception" {
			found = true
		}
	}
	if !found {
		t.Fatal("get_flow_balance_exception not advertised when FlowBalanceAdvisory is wired")
	}

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "get_flow_balance_exception",
		Arguments: map[string]any{"buildingId": "bldg-1", "shiftId": "shift-1", "pathId": "pick-a"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	if action, _ := out["recommendedAction"].(string); action != "assign_labor" {
		t.Fatalf("recommendedAction = %v, want assign_labor", out["recommendedAction"])
	}
	evidence, ok := out["evidence"].([]any)
	if !ok || len(evidence) != 3 {
		t.Fatalf("evidence = %v, want 3 entries", out["evidence"])
	}
}
