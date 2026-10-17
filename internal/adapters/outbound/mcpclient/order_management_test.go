package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// orderManagementGoldenPath is a VERBATIM copy of order-management's own
// published tool registry golden
// (order-management: internal/adapters/inbound/mcp/testdata/tool_registry.golden,
// a name<TAB>readOnlyHint<TAB>openWorldHint TSV). Refresh it with
//
//	git -C ../order-management show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden
//
// whenever order-management amends its tool surface.
const orderManagementGoldenPath = "testdata/order_management_tools.golden"

// wantOrderManagementCalledTools is the exact set of tools this client
// calls: get_promise_health only (ADR 0024). get_order stays published by
// order-management but deliberately unwired here — no use case of this
// agent calls it (gap A8's wired-but-unused lesson).
var wantOrderManagementCalledTools = []string{"get_promise_health"}

func TestOrderManagement_GoldenPinsBothPublishedToolsAsReadOnly(t *testing.T) {
	raw, err := os.ReadFile(orderManagementGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("order-management publishes %d tools, want exactly 2 (get_order, get_promise_health): %q", len(lines), string(raw))
	}
	seen := map[string]bool{}
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			t.Fatalf("golden line %q is not name<TAB>readOnly<TAB>openWorld", line)
		}
		if parts[1] != "true" {
			t.Errorf("%s is not readOnlyHint in order-management's registry: this agent may only call read tools", parts[0])
		}
		seen[parts[0]] = true
	}
	if !seen["get_promise_health"] || !seen["get_order"] {
		t.Fatalf("the golden must pin order-management's two published tools, got %v", seen)
	}
}

type promiseHealthTestIn struct {
	From   string `json:"from"`
	To     string `json:"to"`
	PathId string `json:"pathId,omitempty"`
}

// newOrderManagementTestUpstream serves a real Streamable-HTTP MCP server
// exposing get_promise_health, unauthenticated, so OrderManagement is
// exercised over the wire exactly as it would be against
// order-management's cmd/mcp. lastIn receives the most recent decoded
// input.
func newOrderManagementTestUpstream(t *testing.T) (*httptest.Server, *promiseHealthTestIn, *[]string) {
	t.Helper()
	var lastIn promiseHealthTestIn
	var calls []string
	server := mcp.NewServer(&mcp.Implementation{Name: "order-management-test", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "get_promise_health"}, func(_ context.Context, _ *mcp.CallToolRequest, in promiseHealthTestIn) (*mcp.CallToolResult, ports.PromiseHealth, error) {
		lastIn = in
		calls = append(calls, "get_promise_health")
		if in.From == "" || in.To == "" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "from and to are required"}}}, ports.PromiseHealth{}, nil
		}
		if _, err := time.Parse(time.RFC3339, in.From); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "from must be an RFC3339 timestamp"}}}, ports.PromiseHealth{}, nil
		}
		if in.PathId == "bogus-path" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "path-not-found: no process path bogus-path"}}}, ports.PromiseHealth{}, nil
		}
		return nil, ports.PromiseHealth{
			PromiseBasisCapability:    70,
			PromiseBasisLeadTime:      25,
			PromiseBasisNetwork:       5,
			OrdersAllocatedTotal:      100,
			OrdersSplitShipment:       10,
			SplitShipmentRate:         0.1,
			OrdersRepromised:          4,
			RepromiseRate:             0.04,
			PromiseToCutoffGapSeconds: 5400,
		}, nil
	})
	// get_order is published by order-management but must never be called
	// by this client.
	mcp.AddTool(server, &mcp.Tool{Name: "get_order"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		t.Error("get_order was invoked: no use case of this agent calls it")
		return nil, map[string]any{}, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, &lastIn, &calls
}

func TestOrderManagement_OnlyThePinnedReadToolIsCalled(t *testing.T) {
	up, _, calls := newOrderManagementTestUpstream(t)
	c := NewOrderManagement(Config{Endpoint: up.URL})
	if _, err := c.GetPromiseHealth(context.Background(), ports.PromiseHealthQuery{From: time.Now(), To: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("GetPromiseHealth: %v", err)
	}
	if strings.Join(*calls, ",") != strings.Join(wantOrderManagementCalledTools, ",") {
		t.Fatalf("tools called = %v, want exactly %v (get_order is published but not this agent's to call)", *calls, wantOrderManagementCalledTools)
	}
}

func TestOrderManagement_GetPromiseHealth_DecodesKPIs(t *testing.T) {
	up, _, _ := newOrderManagementTestUpstream(t)
	from := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	out, err := NewOrderManagement(Config{Endpoint: up.URL}).GetPromiseHealth(context.Background(), ports.PromiseHealthQuery{From: from, To: to})
	if err != nil {
		t.Fatalf("GetPromiseHealth: %v", err)
	}
	if out.PromiseBasisCapability != 70 || out.PromiseBasisLeadTime != 25 || out.PromiseBasisNetwork != 5 || out.OrdersAllocatedTotal != 100 {
		t.Fatalf("basis distribution not decoded: %+v", out)
	}
	if out.OrdersSplitShipment != 10 || out.SplitShipmentRate != 0.1 || out.OrdersRepromised != 4 || out.RepromiseRate != 0.04 {
		t.Fatalf("rates not decoded: %+v", out)
	}
	if out.PromiseToCutoffGapSeconds != 5400 {
		t.Fatalf("promise-to-cutoff gap not decoded: %+v", out)
	}
}

func TestOrderManagement_GetPromiseHealth_SendsWindowAndOptionalPath(t *testing.T) {
	up, lastIn, _ := newOrderManagementTestUpstream(t)
	c := NewOrderManagement(Config{Endpoint: up.URL})
	from := time.Date(2026, 10, 17, 9, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	to := from.Add(3 * time.Hour)

	// No path filter: only from/to under their published names, in UTC.
	if _, err := c.GetPromiseHealth(context.Background(), ports.PromiseHealthQuery{From: from, To: to}); err != nil {
		t.Fatalf("GetPromiseHealth: %v", err)
	}
	if lastIn.From != "2026-10-17T12:00:00Z" || lastIn.To != "2026-10-17T15:00:00Z" || lastIn.PathId != "" {
		t.Fatalf("window not sent as UTC RFC 3339 under the published names (pathId omitted): %+v", *lastIn)
	}

	if _, err := c.GetPromiseHealth(context.Background(), ports.PromiseHealthQuery{From: from, To: to, PathId: "PICK"}); err != nil {
		t.Fatalf("GetPromiseHealth (path filter): %v", err)
	}
	if lastIn.PathId != "PICK" {
		t.Fatalf("a set path filter must be sent under its published name: %+v", *lastIn)
	}
}

func TestOrderManagement_GetPromiseHealth_ToolErrorSlug(t *testing.T) {
	up, _, _ := newOrderManagementTestUpstream(t)
	from := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

	_, err := NewOrderManagement(Config{Endpoint: up.URL}).GetPromiseHealth(context.Background(), ports.PromiseHealthQuery{From: from, To: from.Add(time.Hour), PathId: "bogus-path"})
	var te *ToolError
	if !errors.As(err, &te) || te.Slug != "path-not-found" {
		t.Fatalf("want a path-not-found ToolError, got %v", err)
	}
}

func TestOrderManagement_GetPromiseHealth_UnreachableUpstream(t *testing.T) {
	up, _, _ := newOrderManagementTestUpstream(t)
	up.Close() // closed before use: any call must surface a connection error.

	c := NewOrderManagement(Config{Endpoint: up.URL})
	if _, err := c.GetPromiseHealth(context.Background(), ports.PromiseHealthQuery{From: time.Now(), To: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("an unreachable upstream endpoint must surface as an error")
	}
}

func TestOrderManagement_RespectsCallerContext(t *testing.T) {
	up, _, _ := newOrderManagementTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewOrderManagement(Config{Endpoint: up.URL}).GetPromiseHealth(ctx, ports.PromiseHealthQuery{From: time.Now(), To: time.Now().Add(time.Hour)})
	if err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
}

func TestOrderManagement_ImplementsPort(t *testing.T) {
	var _ ports.OrderManagementMCPClient = NewOrderManagement(Config{Endpoint: "http://example.invalid"})
}
