package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The test-side input structs mirror warehouse-planning's published tool
// argument names (snake_case) -- if the client sends a different key the
// recorded input is empty and the assertions below fail.
type planningPathCapacityTestIn struct {
	ID               string   `json:"id"`
	Location         string   `json:"location"`
	WindowStart      string   `json:"window_start"`
	WindowEnd        string   `json:"window_end"`
	UnitsPerOrder    *float64 `json:"units_per_order,omitempty"`
	PackagesPerOrder *float64 `json:"packages_per_order,omitempty"`
}

type planningPlanTestIn struct {
	ID string `json:"id"`
}

type planningLocationTestIn struct {
	Location string `json:"location"`
}

type planningOptLocationTestIn struct {
	Location string `json:"location,omitempty"`
}

// planningUpstream is a real Streamable-HTTP MCP server publishing the four
// warehouse-planning read tools the client uses PLUS one write tool
// (create_capacity_plan) that fails the test if it is ever invoked.
type planningUpstream struct {
	*httptest.Server
	mu      sync.Mutex
	lastReq planningPathCapacityTestIn
	lastLoc string
	calls   []string
}

func (u *planningUpstream) record(tool string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, tool)
}

func newPlanningTestUpstream(t *testing.T) *planningUpstream {
	t.Helper()
	up := &planningUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "warehouse-planning-test", Version: "0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "get_process_path_capacity"}, func(_ context.Context, _ *mcp.CallToolRequest, in planningPathCapacityTestIn) (*mcp.CallToolResult, ports.ProcessPathCapacity, error) {
		up.record("get_process_path_capacity")
		up.mu.Lock()
		up.lastReq = in
		up.mu.Unlock()
		if in.ID == "missing" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "process-path-not-found: no such path"}}}, ports.ProcessPathCapacity{}, nil
		}
		return nil, ports.ProcessPathCapacity{
			NormalizedRate: 120,
			NormalizedUnit: "ORDER",
			BottleneckStep: "PACK",
			StepBreakdown: []ports.PlanningStepBreakdown{
				{Step: "PICK", NormalizedRate: 300, BindingConstraint: "LABOR"},
				{Step: "PACK", NormalizedRate: 120, BindingConstraint: "STATION"},
			},
			Warnings: []string{"stations tallied without a declared standard: REBIN"},
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "get_capacity_plan"}, func(_ context.Context, _ *mcp.CallToolRequest, in planningPlanTestIn) (*mcp.CallToolResult, ports.CapacityPlan, error) {
		up.record("get_capacity_plan")
		if in.ID == "gone" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "capacity-plan-not-found: nope"}}}, ports.CapacityPlan{}, nil
		}
		return nil, ports.CapacityPlan{
			Id: in.ID, WarehouseId: "WH-1", Location: "SIM1",
			WindowStart: "2026-10-05T08:00:00Z", WindowEnd: "2026-10-05T16:00:00Z",
			PathId: "tote-path", AssignedDemand: 1200, Status: "DRAFT",
			PathCapacity: 120, BottleneckStep: "PACK", CapacityOverWindow: 960, Shortage: 240,
			CreatedAt: "2026-10-05T07:00:00Z", BottleneckConstraint: "STATION", Warnings: []string{},
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "get_storage_capacity"}, func(_ context.Context, _ *mcp.CallToolRequest, in planningLocationTestIn) (*mcp.CallToolResult, ports.StorageCapacity, error) {
		up.record("get_storage_capacity")
		up.mu.Lock()
		up.lastLoc = in.Location
		up.mu.Unlock()
		return nil, ports.StorageCapacity{
			Location:         in.Location,
			StoragePositions: []ports.StoragePositions{{ZoneId: "SIM1-A", LocationType: "BIN", Positions: 500}},
			Stations:         []ports.ZoneStations{{ZoneId: "SIM1-P", Activity: "PACK", Stations: 4}},
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "list_station_standards"}, func(_ context.Context, _ *mcp.CallToolRequest, in planningOptLocationTestIn) (*mcp.CallToolResult, ports.StationStandardList, error) {
		up.record("list_station_standards")
		up.mu.Lock()
		up.lastLoc = in.Location
		up.mu.Unlock()
		return nil, ports.StationStandardList{
			Location: in.Location,
			Standards: []ports.StationStandard{
				{Location: "SIM1", ProcessType: "PACK", Quantity: 180, Unit: "PACKAGE", PeriodSeconds: 3600},
			},
		}, nil
	})

	// A write tool the client must never reach.
	mcp.AddTool(server, &mcp.Tool{Name: "create_capacity_plan"}, func(_ context.Context, _ *mcp.CallToolRequest, in planningPlanTestIn) (*mcp.CallToolResult, ports.CapacityPlan, error) {
		t.Error("write tool create_capacity_plan was invoked")
		return nil, ports.CapacityPlan{}, nil
	})

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	up.Server = httptest.NewServer(h)
	return up
}

func TestWarehousePlanning_GetProcessPathCapacity(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	units, pkgs := 2.5, 1.2
	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	out, err := c.GetProcessPathCapacity(context.Background(), ports.ProcessPathCapacityRequest{
		PathId: "tote-path", Location: "SIM1",
		WindowStart: "2026-10-05T08:00:00Z", WindowEnd: "2026-10-05T16:00:00Z",
		UnitsPerOrder: &units, PackagesPerOrder: &pkgs,
	})
	if err != nil {
		t.Fatalf("GetProcessPathCapacity: %v", err)
	}
	if out.NormalizedRate != 120 || out.NormalizedUnit != "ORDER" || out.BottleneckStep != "PACK" {
		t.Fatalf("unexpected capacity: %+v", out)
	}
	if len(out.StepBreakdown) != 2 || out.StepBreakdown[1].BindingConstraint != "STATION" || out.StepBreakdown[0].NormalizedRate != 300 {
		t.Fatalf("unexpected step breakdown: %+v", out.StepBreakdown)
	}
	if len(out.Warnings) != 1 {
		t.Fatalf("unexpected warnings: %+v", out.Warnings)
	}

	got := up.lastReq
	if got.ID != "tote-path" || got.Location != "SIM1" || got.WindowStart != "2026-10-05T08:00:00Z" || got.WindowEnd != "2026-10-05T16:00:00Z" {
		t.Fatalf("tool received unexpected arguments: %+v", got)
	}
	if got.UnitsPerOrder == nil || *got.UnitsPerOrder != 2.5 || got.PackagesPerOrder == nil || *got.PackagesPerOrder != 1.2 {
		t.Fatalf("conversion factors must be passed through unchanged: %+v", got)
	}
}

func TestWarehousePlanning_GetProcessPathCapacity_OmitsUnsetFactors(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	if _, err := c.GetProcessPathCapacity(context.Background(), ports.ProcessPathCapacityRequest{
		PathId: "tote-path", Location: "SIM1", WindowStart: "2026-10-05T08:00:00Z", WindowEnd: "2026-10-05T16:00:00Z",
	}); err != nil {
		t.Fatalf("GetProcessPathCapacity: %v", err)
	}
	if up.lastReq.UnitsPerOrder != nil || up.lastReq.PackagesPerOrder != nil {
		t.Fatalf("an unset factor must be omitted, never defaulted: %+v", up.lastReq)
	}
}

func TestWarehousePlanning_GetProcessPathCapacity_ToolError(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	_, err := c.GetProcessPathCapacity(context.Background(), ports.ProcessPathCapacityRequest{PathId: "missing", Location: "SIM1"})
	if err == nil || !strings.Contains(err.Error(), "process-path-not-found") {
		t.Fatalf("a tool-level failure must surface with the upstream slug, got %v", err)
	}
}

func TestWarehousePlanning_GetCapacityPlan(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	out, err := c.GetCapacityPlan(context.Background(), "plan-1")
	if err != nil {
		t.Fatalf("GetCapacityPlan: %v", err)
	}
	if out.Id != "plan-1" || out.Shortage != 240 || out.BottleneckStep != "PACK" || out.BottleneckConstraint != "STATION" || out.Status != "DRAFT" || out.PublishedAt != nil {
		t.Fatalf("unexpected plan: %+v", out)
	}

	if _, err := c.GetCapacityPlan(context.Background(), "gone"); err == nil || !strings.Contains(err.Error(), "capacity-plan-not-found") {
		t.Fatalf("unknown plan must surface the upstream slug, got %v", err)
	}
}

func TestWarehousePlanning_GetStorageCapacity(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	out, err := c.GetStorageCapacity(context.Background(), "SIM1")
	if err != nil {
		t.Fatalf("GetStorageCapacity: %v", err)
	}
	if up.lastLoc != "SIM1" || out.Location != "SIM1" || len(out.StoragePositions) != 1 || out.StoragePositions[0].Positions != 500 || len(out.Stations) != 1 || out.Stations[0].Stations != 4 {
		t.Fatalf("unexpected storage capacity: %+v (sent location %q)", out, up.lastLoc)
	}
}

func TestWarehousePlanning_ListStationStandards(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	out, err := c.ListStationStandards(context.Background(), "SIM1")
	if err != nil {
		t.Fatalf("ListStationStandards: %v", err)
	}
	if up.lastLoc != "SIM1" || out.Location != "SIM1" || len(out.Standards) != 1 || out.Standards[0].Quantity != 180 || out.Standards[0].Unit != "PACKAGE" {
		t.Fatalf("unexpected standards: %+v (sent location %q)", out, up.lastLoc)
	}

	// An empty location omits the filter entirely.
	if _, err := c.ListStationStandards(context.Background(), ""); err != nil {
		t.Fatalf("ListStationStandards (unfiltered): %v", err)
	}
	if up.lastLoc != "" {
		t.Fatalf("an empty location must not be sent as a filter, got %q", up.lastLoc)
	}
}

func TestWarehousePlanning_OnlyReadToolsAreCalled(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetProcessPathCapacity(ctx, ports.ProcessPathCapacityRequest{PathId: "p", Location: "SIM1"})
	_, _ = c.GetCapacityPlan(ctx, "plan-1")
	_, _ = c.GetStorageCapacity(ctx, "SIM1")
	_, _ = c.ListStationStandards(ctx, "")

	want := []string{"get_process_path_capacity", "get_capacity_plan", "get_storage_capacity", "list_station_standards"}
	up.mu.Lock()
	defer up.mu.Unlock()
	if strings.Join(up.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("tools called = %v, want exactly %v", up.calls, want)
	}
}

func TestWarehousePlanning_UnreachableUpstream(t *testing.T) {
	up := newPlanningTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.

	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetProcessPathCapacity(ctx, ports.ProcessPathCapacityRequest{PathId: "p", Location: "SIM1"}); err == nil {
		t.Fatal("an unreachable upstream must surface as an error")
	}
	if _, err := c.GetCapacityPlan(ctx, "plan-1"); err == nil {
		t.Fatal("an unreachable upstream must surface as an error")
	}
	if _, err := c.GetStorageCapacity(ctx, "SIM1"); err == nil {
		t.Fatal("an unreachable upstream must surface as an error")
	}
	if _, err := c.ListStationStandards(ctx, ""); err == nil {
		t.Fatal("an unreachable upstream must surface as an error")
	}
}

func TestWarehousePlanning_RespectsCallerContext(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	_, err := c.GetStorageCapacity(ctx, "SIM1")
	if err == nil || !(errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "canceled")) {
		t.Fatalf("a cancelled context must abort the call, got %v", err)
	}
}

func TestWarehousePlanning_ImplementsPort(t *testing.T) {
	var _ ports.WarehousePlanningClient = NewWarehousePlanning(Config{Endpoint: "http://example.invalid"})
}
