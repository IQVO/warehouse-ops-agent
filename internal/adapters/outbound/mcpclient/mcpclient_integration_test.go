//go:build integration

// Integration tests for the warehouse-ops-agent's REAL cross-context
// integration path: the mcpclient typed clients (Session over Streamable
// HTTP, fresh session per call) talking to REAL MCP servers built with the
// same SDK the upstream contexts use, and the inbound MCP server answering
// over its real Streamable HTTP handler. This is the ADR-0008 wire contract
// this agent depends on — proven here end-to-end, HTTP bytes and all, not
// through the in-package fakes the unit tests use.
//
// There is no database in this service: its integration seams are HTTP and
// MCP, so testcontainers containers would add nothing — the REAL wire
// endpoints are exercised instead (httptest servers carrying the production
// StreamableHTTPHandler). Never an env var, never t.Skip.
package mcpclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/mcpclient"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// upstreamServer builds a real SDK MCP server exposing one tool that
// replies with the given JSON, served over the production Streamable HTTP
// handler. It returns the base URL to point a typed client at.
func upstreamServer(t *testing.T, toolName, description, replyJSON string) string {
	t.Helper()
	server := mcp.NewServer(
		&mcp.Implementation{Name: "upstream", Version: "1.0.0"}, nil,
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolName,
		Description: description,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		var payload map[string]any
		if err := json.Unmarshal([]byte(replyJSON), &payload); err != nil {
			return nil, nil, err
		}
		return nil, payload, nil
	})
	hs := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(hs.Close)
	return hs.URL
}

func TestMCPClient_WesWorkPlanningBacklogTelemetry(t *testing.T) {
	ctx := context.Background()
	base := upstreamServer(t, "get_backlog_telemetry",
		"backlog telemetry for a path",
		`{"pathId":"pick-zone-a","backlogDepth":12,"wip":4,"mode":"ReleaseFed","overAlarmThreshold":true}`)

	client := mcpclient.NewWesWorkPlanning(mcpclient.Config{Endpoint: base, Timeout: 5 * time.Second})
	got, err := client.GetBacklogTelemetry(ctx, "pick-zone-a")
	if err != nil {
		t.Fatalf("GetBacklogTelemetry over real Streamable HTTP: %v", err)
	}
	want := ports.BacklogTelemetry{PathId: "pick-zone-a", BacklogDepth: 12, WIP: 4, Mode: "ReleaseFed", OverAlarmThreshold: true}
	if got != want {
		t.Fatalf("decoded DTO mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestMCPClient_InventoryStorageAvailability(t *testing.T) {
	ctx := context.Background()
	base := upstreamServer(t, "check_availability",
		"availability for a sku",
		`{"sku":"SKU-1","usable":42}`)

	client := mcpclient.NewInventoryStorage(mcpclient.Config{Endpoint: base, Timeout: 5 * time.Second})
	got, err := client.CheckAvailability(ctx, "SKU-1")
	if err != nil {
		t.Fatalf("CheckAvailability over real Streamable HTTP: %v", err)
	}
	if got.SKU != "SKU-1" {
		t.Fatalf("decoded DTO mismatch: %+v", got)
	}
}

func TestMCPClient_ToolErrorIsTransportedAsError(t *testing.T) {
	ctx := context.Background()
	// An upstream that answers every call with a tool error — the wire
	// shape every context uses for domain rejections.
	server := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "get_backlog_telemetry", Description: "d"},
		func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return &mcp.CallToolResult{IsError: true}, nil, nil
		})
	hs := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(hs.Close)

	client := mcpclient.NewWesWorkPlanning(mcpclient.Config{Endpoint: hs.URL, Timeout: 5 * time.Second})
	if _, err := client.GetBacklogTelemetry(ctx, "nope"); err == nil {
		t.Fatal("a tool error must surface as a Go error in the typed client, not a silent zero DTO")
	}
}

func TestMCPClient_UnreachableEndpointFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcpclient.NewWesWorkPlanning(mcpclient.Config{Endpoint: "http://127.0.0.1:1", Timeout: 2 * time.Second})
	if _, err := client.GetBacklogTelemetry(ctx, "any"); err == nil {
		t.Fatal("an unreachable upstream must return an error, not hang or return a zero DTO")
	}
}

// TestMCPClient_ProcessPathManagement is the ADR-0026 client's integration
// seam: the three-tool read surface over a real Streamable-HTTP server.
func TestMCPClient_ProcessPathManagement(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "process-path-management", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "get_process_path", Description: "one catalogue entry"},
		func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{
				"pathId": "PICK", "matchPrefix": "PICK-", "direct": false,
				"requiredCapabilities": []any{"CARTON"}, "status": "ACTIVE", "active": true,
				"createdAt": "2026-01-05T09:00:00Z", "updatedAt": "2026-02-01T12:00:00Z",
				"cycleTimeP95": "45m0s", "eligibility": map[string]any{}, "intake": []any{"CUSTOMER_ORDER"},
			}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "get_cpt_schedule", Description: "one site's CPT schedule"},
		func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{
				"siteId": "sp1", "timezone": "America/Chicago",
				"cutoffs": []any{map[string]any{
					"cptId": "cpt-eod", "localTime": "17:00", "daysOfWeek": []any{"MON", "FRI"},
					"shipMethod": "GROUND", "eligiblePathIds": []any{"PICK"},
				}},
				"createdAt": "2026-01-05T09:00:00Z", "updatedAt": "2026-02-01T12:00:00Z",
			}, nil
		})
	hs := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(hs.Close)

	client := mcpclient.NewProcessPathManagement(mcpclient.Config{Endpoint: hs.URL, Timeout: 5 * time.Second})
	entry, err := client.GetProcessPath(ctx, "PICK")
	if err != nil {
		t.Fatalf("GetProcessPath over real Streamable HTTP: %v", err)
	}
	if entry.PathID != "PICK" || entry.Status != "ACTIVE" || !entry.Active || len(entry.Intake) != 1 {
		t.Fatalf("decoded path DTO mismatch: %+v", entry)
	}
	schedule, err := client.GetCPTSchedule(ctx, "sp1")
	if err != nil {
		t.Fatalf("GetCPTSchedule over real Streamable HTTP: %v", err)
	}
	if schedule.Timezone != "America/Chicago" || len(schedule.Cutoffs) != 1 || schedule.Cutoffs[0].LocalTime != "17:00" {
		t.Fatalf("decoded CPT schedule DTO mismatch: %+v", schedule)
	}
}
