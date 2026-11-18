package mcpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/authority"
)

// The yard services (ADR 0032) are the first upstreams in this package
// whose published registry mixes L1 read tools with L3 write tools in the
// SAME server, so the golden pin is split in two: the read set the client
// wraps (every tool readOnlyHint, authority L1) and the write set the
// client must never reach (every tool a write, authority L3). Together
// they must be exactly the verbatim golden, so a tool added upstream fails
// here until a person classifies it.

// assertPinnedReadWriteSplit pins a golden to exactly reads + writes and
// checks every tool's annotation and declared authority agree with the
// side it is pinned on.
func assertPinnedReadWriteSplit(t *testing.T, golden map[string]goldenTool, upstream string, reads, writes []string) {
	t.Helper()
	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	want := append(append([]string(nil), reads...), writes...)
	sort.Strings(names)
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("%s publishes %v, this golden is pinned as exactly %v (reads) + %v (writes): classify the new or removed tool", upstream, names, reads, writes)
	}
	for _, name := range reads {
		if golden[name].Annotations["readOnlyHint"] != true || golden[name].Meta[authority.MetaKey] != authority.L1 {
			t.Errorf("%s/%s is pinned as a read tool but is not readOnlyHint + %s in the registry (annotations %v, meta %v)", upstream, name, authority.L1, golden[name].Annotations, golden[name].Meta)
		}
	}
	for _, name := range writes {
		if golden[name].Annotations["readOnlyHint"] != false || golden[name].Meta[authority.MetaKey] != authority.L3 {
			t.Errorf("%s/%s is pinned as a write tool but is not a non-readOnly %s tool in the registry (annotations %v, meta %v)", upstream, name, authority.L3, golden[name].Annotations, golden[name].Meta)
		}
	}
}

// registryUpstream is a real Streamable-HTTP MCP server publishing EVERY tool
// of an upstream's verbatim registry golden - reads and writes - with its
// published schemas, annotations and authority _meta, exactly what a
// tools/list against the real yard server returns. A write tool that gets
// CALLED is recorded so the tests can prove none ever is.
type registryUpstream struct {
	*httptest.Server
	mu      sync.Mutex
	invoked []string
}

func (u *registryUpstream) called() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.invoked...)
}

func newRegistryUpstream(t *testing.T, name string, golden map[string]goldenTool) *registryUpstream {
	t.Helper()
	up := &registryUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: name, Version: "0"}, nil)
	for _, g := range golden {
		g := g
		mcp.AddTool(server, &mcp.Tool{
			Name:        g.Name,
			Description: "published " + g.Name,
			Meta:        mcp.Meta(g.Meta),
			InputSchema: g.InputSchema,
		}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
			up.mu.Lock()
			up.invoked = append(up.invoked, g.Name)
			up.mu.Unlock()
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "internal-error: registry stand-in executes nothing"}}}, nil, nil
		})
	}
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

// yardUpstreams builds both stand-ins and the ToolInvoker sessions over them.
func yardUpstreams(t *testing.T) (dock, yard *registryUpstream, sessions map[string]*Session) {
	t.Helper()
	dock = newRegistryUpstream(t, "dock-scheduling-test", loadGoldenTools(t, dockSchedulingGoldenPath))
	yard = newRegistryUpstream(t, "yard-management-test", loadGoldenTools(t, yardManagementGoldenPath))
	sessions = map[string]*Session{
		"dock-scheduling": New(Config{Name: "dock-scheduling", Endpoint: dock.URL}),
		"yard-management": New(Config{Name: "yard-management", Endpoint: yard.URL}),
	}
	return dock, yard, sessions
}

func qualified(upstream string, tools []string) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, upstream+"/"+tool)
	}
	return out
}

// TestYardUpstreams_AgentListsOnlyTheYardReadTools is the Y0.4 acceptance:
// with both yard endpoints set and the pinned read tools allow-listed, the
// agent's tool discovery (ToolInvoker.Specs, the call the reasoner makes at
// boot) lists exactly the seventeen yard read tools with their published
// descriptions and input schemas, and none of the eighteen write tools,
// although both servers publish all of them.
func TestYardUpstreams_AgentListsOnlyTheYardReadTools(t *testing.T) {
	dock, yard, sessions := yardUpstreams(t)
	inv := NewToolInvoker(sessions, map[string][]string{
		"dock-scheduling": DockSchedulingReadTools,
		"yard-management": YardManagementReadTools,
	})

	specs, errs := inv.Specs(context.Background())
	if len(errs) != 0 {
		t.Fatalf("discovery errors: %v", errs)
	}
	var got []string
	for _, s := range specs {
		got = append(got, s.Upstream+"/"+s.Name)
		if s.Description == "" || s.InputSchema["type"] != "object" {
			t.Errorf("%s/%s must carry the published description and object input schema: %+v", s.Upstream, s.Name, s)
		}
	}
	want := append(qualified("dock-scheduling", DockSchedulingReadTools), qualified("yard-management", YardManagementReadTools)...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the agent lists %v, want exactly the pinned read tools %v", got, want)
	}
	if len(got) != 17 {
		t.Fatalf("the yard read surface is 7 + 10 tools, listed %d", len(got))
	}
	if calls := append(dock.called(), yard.called()...); len(calls) != 0 {
		t.Fatalf("listing tools must execute nothing, the stand-ins saw %v", calls)
	}
}

// TestYardUpstreams_WriteToolsAreNeverReachable: naming a yard write tool,
// even on an allow-list, is refused before any network call.
func TestYardUpstreams_WriteToolsAreNeverReachable(t *testing.T) {
	dock, yard, sessions := yardUpstreams(t)
	ctx := context.Background()

	inv := NewToolInvoker(sessions, map[string][]string{
		"dock-scheduling": DockSchedulingReadTools,
		"yard-management": YardManagementReadTools,
	})
	for upstream, writes := range map[string][]string{
		"dock-scheduling": dockSchedulingWriteTools,
		"yard-management": yardManagementWriteTools,
	} {
		for _, tool := range writes {
			if _, err := inv.Invoke(ctx, upstream, tool, map[string]any{}); err == nil || !strings.Contains(err.Error(), "allow-list") {
				t.Errorf("%s/%s is a write tool and must be refused by the allow-list, got %v", upstream, tool, err)
			}
		}
	}
	if calls := append(dock.called(), yard.called()...); len(calls) != 0 {
		t.Fatalf("a write tool reached the wire: %v", calls)
	}

	// Even a (mis)configured allow-list naming a write tool is rejected at
	// startup by CheckReasonerAllowList, so it never reaches ToolInvoker.
	for _, entry := range []string{"dock-scheduling/book_appointment", "yard-management/request_move", "yard-management/not_a_tool"} {
		upstream, tool, _ := strings.Cut(entry, "/")
		err := CheckReasonerAllowList(map[string][]string{upstream: {tool}})
		if err == nil || !strings.Contains(err.Error(), entry) {
			t.Errorf("allow-list entry %s must be a startup error naming it, got %v", entry, err)
		}
	}
}

func TestCheckReasonerAllowList_AcceptsPinnedReadsAndIgnoresOtherUpstreams(t *testing.T) {
	ok := map[string][]string{
		"dock-scheduling":       DockSchedulingReadTools,
		"yard-management":       YardManagementReadTools,
		"workforce-management":  {"get_staffing_gap"}, // legacy upstream: untouched
		"fulfillment-execution": {"anything_goes_here"},
	}
	if err := CheckReasonerAllowList(ok); err != nil {
		t.Fatalf("the pinned read tools and unpinned upstreams must pass: %v", err)
	}
	if err := CheckReasonerAllowList(nil); err != nil {
		t.Fatalf("an empty allow-list must pass: %v", err)
	}
}

// TestYardUpstreams_EmptyEndpointsListNothing: the yard is OFF by default;
// an unset endpoint drops the session so no yard tool is offered.
func TestYardUpstreams_EmptyEndpointsListNothing(t *testing.T) {
	inv := NewToolInvoker(map[string]*Session{
		"dock-scheduling": New(Config{Name: "dock-scheduling", Endpoint: ""}),
		"yard-management": New(Config{Name: "yard-management", Endpoint: ""}),
	}, map[string][]string{"dock-scheduling": DockSchedulingReadTools, "yard-management": YardManagementReadTools})
	specs, errs := inv.Specs(context.Background())
	if len(specs) != 0 || len(errs) != 0 {
		t.Fatalf("an unconfigured yard must list nothing and fail nothing, got %v %v", specs, errs)
	}
	if _, err := inv.Invoke(context.Background(), "yard-management", "get_trailer", nil); err == nil || !strings.Contains(err.Error(), "unknown upstream") {
		t.Fatalf("an unconfigured yard upstream must be unknown, got %v", err)
	}
}
