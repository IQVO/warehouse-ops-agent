package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
)

// The yard wiring (ADR 0032): both clients are OFF by default, and once an
// operator sets the endpoints the reasoner's boot-time tool discovery lists
// exactly the pinned read tools of both yard servers.

const (
	dockGolden = "../../internal/adapters/outbound/mcpclient/testdata/dock_scheduling_tools.golden.json"
	yardGolden = "../../internal/adapters/outbound/mcpclient/testdata/yard_management_tools.golden.json"
)

var (
	wantDockReads = []string{"get_appointment", "get_carrier", "get_dock_calendar", "get_door_availability", "list_appointments", "list_at_risk_appointments", "list_carriers"}
	wantYardReads = []string{"get_inspection", "get_trailer", "get_visit", "get_yard_board", "get_yard_dwell_report", "get_yard_policy", "list_moves", "list_trailer_inspections", "list_trailers", "list_visits"}
)

// yardRegistryServer serves EVERY tool of a verbatim registry golden (reads
// and L3 writes, with their authority _meta) over Streamable HTTP and
// records any call, so a test can prove nothing was executed.
func yardRegistryServer(t *testing.T, goldenPath string) (*httptest.Server, func() []string) {
	t.Helper()
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var tools []struct {
		Name        string         `json:"name"`
		Meta        map[string]any `json:"_meta"`
		InputSchema map[string]any `json:"inputSchema"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	var mu sync.Mutex
	var invoked []string
	server := mcp.NewServer(&mcp.Implementation{Name: "yard-registry-test", Version: "0"}, nil)
	for _, g := range tools {
		mcp.AddTool(server, &mcp.Tool{Name: g.Name, Description: "published " + g.Name, Meta: mcp.Meta(g.Meta), InputSchema: g.InputSchema},
			func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
				mu.Lock()
				invoked = append(invoked, g.Name)
				mu.Unlock()
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "internal-error: stand-in"}}}, nil, nil
			})
	}
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), invoked...)
	}
}

func yardShadowConfig(dockURL, yardURL string, allow []string) config.Config {
	cfg := config.Config{
		DockScheduling: config.UpstreamConfig{Endpoint: dockURL},
		YardManagement: config.UpstreamConfig{Endpoint: yardURL},
	}
	cfg.LLM.Mode = "shadow"
	cfg.LLM.APIKey = "test-key-never-used"
	cfg.LLM.ToolAllowList = allow
	return cfg
}

func TestYardClients_AreOffByDefault(t *testing.T) {
	c := newOutboundClients(config.Config{})
	if c.dockScheduling != nil || c.yardManagement != nil {
		t.Fatalf("an unset endpoint must build no yard client: dock=%v yard=%v", c.dockScheduling, c.yardManagement)
	}
	c = newOutboundClients(config.Config{
		DockScheduling: config.UpstreamConfig{Endpoint: "http://dock-scheduling-mcp:8090/mcp"},
		YardManagement: config.UpstreamConfig{Endpoint: "http://yard-management-mcp:8090/mcp"},
	})
	if c.dockScheduling == nil || c.yardManagement == nil {
		t.Fatal("a set endpoint must build its yard client")
	}
}

// TestWireReasoner_AgentListsYardTools is the Y0.4 acceptance through the
// real composition root: both yard endpoints set, the read tools allow-listed,
// and the agent's discovered tool list is exactly the 17 yard read tools,
// none of the 18 write tools, and nothing was executed upstream.
func TestWireReasoner_AgentListsYardTools(t *testing.T) {
	dock, dockCalls := yardRegistryServer(t, dockGolden)
	yard, yardCalls := yardRegistryServer(t, yardGolden)
	var allow []string
	for _, tool := range wantDockReads {
		allow = append(allow, "dock-scheduling/"+tool)
	}
	for _, tool := range wantYardReads {
		allow = append(allow, "yard-management/"+tool)
	}
	fb := &usecases.FlowBalanceAdvisory{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := wireReasoner(context.Background(), yardShadowConfig(dock.URL, yard.URL, allow), logger, fb); err != nil {
		t.Fatalf("wireReasoner: %v", err)
	}
	var got []string
	for _, spec := range fb.ReasonerTools {
		got = append(got, spec.Upstream+"/"+spec.Name)
	}
	sort.Strings(got)
	want := append([]string(nil), allow...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the agent lists %v, want exactly %v", got, want)
	}
	if calls := append(dockCalls(), yardCalls()...); len(calls) != 0 {
		t.Fatalf("listing must execute nothing, saw %v", calls)
	}
}

// TestWireReasoner_YardWriteToolOnTheAllowListIsAStartupError: D-Y4. A yard
// write tool, or any tool outside the pinned read set, aborts boot instead of
// reaching the model.
func TestWireReasoner_YardWriteToolOnTheAllowListIsAStartupError(t *testing.T) {
	dock, _ := yardRegistryServer(t, dockGolden)
	yard, _ := yardRegistryServer(t, yardGolden)
	for _, entry := range []string{"yard-management/request_move", "yard-management/place_hold", "dock-scheduling/book_appointment", "dock-scheduling/report_eta", "yard-management/unknown_tool"} {
		fb := &usecases.FlowBalanceAdvisory{}
		err := wireReasoner(context.Background(), yardShadowConfig(dock.URL, yard.URL, []string{"yard-management/get_trailer", entry}),
			slog.New(slog.NewTextHandler(io.Discard, nil)), fb)
		if err == nil || !strings.Contains(err.Error(), entry) {
			t.Errorf("%s must be a startup error naming it, got %v", entry, err)
		}
		if fb.Reasoner != nil || len(fb.ReasonerTools) != 0 {
			t.Errorf("%s: a rejected allow-list must leave the use case untouched", entry)
		}
	}
}

// TestWireReasoner_DefaultAllowListOffersNoYardTool: with the yard endpoints
// set but the default allow-list, the model is offered no yard tool (the
// default allow-list is unchanged by ADR 0032).
func TestWireReasoner_DefaultAllowListOffersNoYardTool(t *testing.T) {
	dock, _ := yardRegistryServer(t, dockGolden)
	yard, _ := yardRegistryServer(t, yardGolden)
	cfg := yardShadowConfig(dock.URL, yard.URL, nil)
	cfg.LLM.ToolAllowList = []string{"workforce-management/get_staffing_gap"}
	fb := &usecases.FlowBalanceAdvisory{}
	if err := wireReasoner(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), fb); err != nil {
		t.Fatalf("wireReasoner: %v", err)
	}
	for _, spec := range fb.ReasonerTools {
		if spec.Upstream == "dock-scheduling" || spec.Upstream == "yard-management" {
			t.Fatalf("a yard tool was offered without being allow-listed: %+v", spec)
		}
	}
}
