//go:build integration

// Integration tests for the agent's OWN inbound MCP server over its REAL
// Streamable HTTP handler (the same handler cmd/agent mounts at /mcp),
// driven by the SDK client. The upstream-dependent tools are wired through
// the real mcpclient typed clients against real SDK servers on httptest —
// the full inbound->usecase->outbound-client->wire path — so the tool
// contract the model host sees is proven end-to-end. No database is
// involved in this service; its integration seams are HTTP and MCP.
// Never an env var, never t.Skip.
package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	agentmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/mcpclient"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
)

// upstream serves one tool echoing replyJSON, production handler included.
func upstream(t *testing.T, toolName, replyJSON string) string {
	t.Helper()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "upstream", Version: "1.0.0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: toolName, Description: "d"},
		func(ctx context.Context, req *sdkmcp.CallToolRequest, in map[string]any) (*sdkmcp.CallToolResult, map[string]any, error) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(replyJSON), &payload); err != nil {
				return nil, nil, err
			}
			return nil, payload, nil
		})
	hs := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
	t.Cleanup(hs.Close)
	return hs.URL
}

// connectAgent serves the agent's real MCP server and returns a connected
// SDK client session, exactly like a model host.
func connectAgent(t *testing.T, deps agentmcp.Deps) *sdkmcp.ClientSession {
	t.Helper()
	server := agentmcp.NewServer(deps)
	hs := httptest.NewServer(agentmcp.Handler(server))
	t.Cleanup(hs.Close)

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "itcov-host", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &sdkmcp.StreamableClientTransport{
		Endpoint: hs.URL, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestAgentMCP_DailyBriefToolOverRealWire(t *testing.T) {
	ctx := context.Background()
	wes := upstream(t, "get_backlog_telemetry",
		`{"pathId":"pick-a","backlogDepth":5,"wip":1,"mode":"ReleaseFed","overAlarmThreshold":false}`)
	fe := upstream(t, "diagnose_stuck_tasks", `{"stuck":[]}`)
	wfm := upstream(t, "get_labor_plan", `{"heads":2}`)
	fl := upstream(t, "list_sites", `{"sites":[{"siteCode":"WH1","name":"WH One"}]}`)

	brief := &usecases.DailyBrief{
		Facility:   mcpclient.NewFacilityLayout(mcpclient.Config{Endpoint: fl, Timeout: 5 * time.Second}),
		Wes:        mcpclient.NewWesWorkPlanning(mcpclient.Config{Endpoint: wes, Timeout: 5 * time.Second}),
		Fe:         mcpclient.NewFulfillmentExecution(mcpclient.Config{Endpoint: fe, Timeout: 5 * time.Second}),
		Wfm:        mcpclient.NewWorkforceManagement(mcpclient.Config{Endpoint: wfm, Timeout: 5 * time.Second}),
		Targets:    []usecases.PathTarget{{PathId: "pick-a", SiteCode: "WH1", ProcessPath: "pick"}},
		Now:        func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) },
		WithinSecs: 0,
	}

	session := connectAgent(t, agentmcp.Deps{DailyBrief: brief})
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "get_daily_brief",
	})
	if err != nil {
		t.Fatalf("tools/call get_daily_brief: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_daily_brief returned a tool error: %+v", res)
	}
}

func TestAgentMCP_NilDepsRegisterOnlyTheAlwaysOnTools(t *testing.T) {
	session := connectAgent(t, agentmcp.Deps{}) // no optional use cases wired

	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := make([]string, 0, len(list.Tools))
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	// get_daily_brief and list_open_exceptions are always registered
	// (nil-safe by design); every optional tool (flow balance, travel
	// factor, stranded reservation, master-data gaps, inbound outlook,
	// transfer watch) must be absent when its use case is nil.
	if len(names) != 2 || names[0] != "get_daily_brief" || names[1] != "list_open_exceptions" {
		t.Fatalf("with every optional use case nil only the two always-on tools may be registered, got %v", names)
	}
}
