package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// returnsProcessingGoldenPath is a VERBATIM copy of returns-processing's
// own published tool registry golden
// (returns-processing: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at returns-processing origin/develop 0abd3f1). Refresh it with
//
//	git -C ../returns-processing show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever returns-processing amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const returnsProcessingGoldenPath = "testdata/returns_processing_tools.golden.json"

// wantReturnsProcessingTools is every tool returns-processing publishes
// (three, the verbatim golden); the client calls all three.
var wantReturnsProcessingTools = []string{"get_disposition_policy", "get_return", "list_returns_by_order"}

// wantReturnsProcessingCalledTools is the exact set of tools this client
// calls (the whole published read surface).
var wantReturnsProcessingCalledTools = []string{"get_disposition_policy", "get_return", "list_returns_by_order"}

func loadReturnsProcessingGolden(t *testing.T) map[string]goldenTool {
	t.Helper()
	raw, err := os.ReadFile(returnsProcessingGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var tools []goldenTool
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	out := make(map[string]goldenTool, len(tools))
	for _, tool := range tools {
		out[tool.Name] = tool
	}
	return out
}

// --- the published contract ------------------------------------------------

func TestReturnsProcessing_GoldenPinsExactlyTheThreeReadOnlyTools(t *testing.T) {
	golden := loadReturnsProcessingGolden(t)

	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantReturnsProcessingTools, ",") {
		t.Fatalf("returns-processing publishes %v, this golden is built for exactly %v", names, wantReturnsProcessingTools)
	}
	for _, name := range wantReturnsProcessingTools {
		if golden[name].Annotations["readOnlyHint"] != true {
			t.Errorf("%s is not annotated readOnlyHint in returns-processing's registry: this agent may only call read tools", name)
		}
	}
}

// TestReturnsProcessing_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema: each json tag must be a
// published property (no invented field) and each required property must
// be mirrored (no silently dropped field).
func TestReturnsProcessing_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadReturnsProcessingGolden(t)
	cases := map[string]reflect.Type{
		"get_return":             reflect.TypeFor[ports.ReturnAuthorization](),
		"list_returns_by_order":  reflect.TypeFor[ports.ReturnsByOrder](),
		"get_disposition_policy": reflect.TypeFor[ports.DispositionPolicy](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful returns-processing stand-in ----------------------------

// returnsProcessingUpstream is a real Streamable-HTTP MCP server whose
// three tools carry returns-processing's PUBLISHED input and output
// schemas from the golden: the SDK validates the client's arguments
// (additionalProperties:false, so a misspelled key is rejected) and the
// canned results against them.
type returnsProcessingUpstream struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []string
	lastArgs map[string]any
}

func (u *returnsProcessingUpstream) record(tool string, args map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, tool)
	u.lastArgs = args
}

func (u *returnsProcessingUpstream) args() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastArgs
}

const (
	rpRmaAuthorized = `{"rma_id":"rma-1b4e","order_id":"ord-7c9e","line_no":2,"work_unit":"ord-7c9e-line-2",
	  "sku":"SKU-9","quantity":3,"site_id":"WH1","reason_code":"DAMAGED","status":"AUTHORIZED",
	  "window_ends_at":"2026-11-04T12:00:00Z","version":2}`

	rpRmaRejected = `{"rma_id":"rma-2c3d","order_id":"ord-7c9e","line_no":1,"sku":"SKU-1","quantity":1,
	  "site_id":"WH1","rejection_code":"WINDOW_EXPIRED","status":"REJECTED","version":1}`

	rpPolicy = `{"version":3,"return_window_days":30,"max_units_per_line":5,"veto_outcome":"DISPOSE",
	  "rules":[{"grade":"A","product_class":"*","outcome":"RESTOCK"},
	           {"grade":"C","product_class":"Hazmat","outcome":"DISPOSE"}],
	  "declared_at":"2026-10-01T09:00:00Z"}`
)

func rpFixture(raw string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}

func rpToolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// rpValidID mirrors returns-processing's own id validation: 1..64
// characters without white space, a slash or a NUL byte.
func rpValidID(id string) bool {
	return id != "" && len(id) <= 64 && !strings.ContainsAny(id, " \t/\x00")
}

func rpGetReturn(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	id, _ := in["rma_id"].(string)
	if !rpValidID(id) {
		return rpToolError("invalid-rma-id: rma_id must be 1..64 characters without white space, a slash or a NUL byte"), nil, nil
	}
	switch id {
	case "rma-1b4e":
		return nil, rpFixture(rpRmaAuthorized), nil
	case "rma-2c3d":
		return nil, rpFixture(rpRmaRejected), nil
	default:
		return rpToolError("rma-not-found: return authorization not found"), nil, nil
	}
}

func rpListReturnsByOrder(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	orderID, _ := in["order_id"].(string)
	if !rpValidID(orderID) {
		return rpToolError("invalid-order-id: order_id must be 1..64 characters without white space, a slash or a NUL byte"), nil, nil
	}
	if orderID == "ord-none" {
		return nil, map[string]any{"items": []any{}}, nil
	}
	return nil, map[string]any{"items": []any{rpFixture(rpRmaRejected), rpFixture(rpRmaAuthorized)}}, nil
}

func rpGetDispositionPolicy(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	if v, ok := in["version"].(float64); ok && int64(v) != 3 {
		return rpToolError("policy-not-found: no disposition policy version exists"), nil, nil
	}
	return nil, rpFixture(rpPolicy), nil
}

func newReturnsProcessingTestUpstream(t *testing.T) *returnsProcessingUpstream {
	t.Helper()
	golden := loadReturnsProcessingGolden(t)
	up := &returnsProcessingUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "returns-processing-test", Version: "0"}, nil)
	handlers := map[string]mcp.ToolHandlerFor[map[string]any, any]{
		"get_return":             rpGetReturn,
		"list_returns_by_order":  rpListReturnsByOrder,
		"get_disposition_policy": rpGetDispositionPolicy,
	}
	for name, h := range handlers {
		g, h := golden[name], h
		mcp.AddTool(server, &mcp.Tool{Name: name, InputSchema: g.InputSchema, OutputSchema: g.OutputSchema},
			func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				up.record(g.Name, in)
				return h(ctx, req, in)
			})
	}
	// A write tool returns-processing does NOT publish (its writes are
	// REST-only); the client must never reach anything like it.
	mcp.AddTool(server, &mcp.Tool{Name: "authorize_return"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		t.Error("write tool authorize_return was invoked")
		return nil, map[string]any{}, nil
	})
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

// --- behaviour over the wire --------------------------------------------------

// assertAuthorizedRMA pins the decoded shape of the AUTHORIZED fixture.
func assertAuthorizedRMA(t *testing.T, authorized ports.ReturnAuthorization) {
	t.Helper()
	if authorized.RmaID != "rma-1b4e" || authorized.OrderID != "ord-7c9e" || authorized.LineNo != 2 ||
		authorized.WorkUnit != "ord-7c9e-line-2" || authorized.Sku != "SKU-9" || authorized.Quantity != 3 ||
		authorized.SiteID != "WH1" || authorized.ReasonCode != "DAMAGED" || authorized.Status != "AUTHORIZED" ||
		authorized.WindowEndsAt != "2026-11-04T12:00:00Z" || authorized.Version != 2 || authorized.RejectionCode != "" {
		t.Fatalf("unexpected authorized RMA: %+v", authorized)
	}
}

// assertRejectedRMA pins the decoded shape of the REJECTED fixture: a
// rejected return decodes with empty accepted-side fields.
func assertRejectedRMA(t *testing.T, rejected ports.ReturnAuthorization) {
	t.Helper()
	if rejected.RmaID != "rma-2c3d" || rejected.Status != "REJECTED" || rejected.RejectionCode != "WINDOW_EXPIRED" ||
		rejected.ReasonCode != "" || rejected.WindowEndsAt != "" || rejected.WorkUnit != "" {
		t.Fatalf("a rejected return must decode with empty accepted-side fields: %+v", rejected)
	}
}

func TestReturnsProcessing_GetReturn_DecodesAuthorizedAndRejected(t *testing.T) {
	up := newReturnsProcessingTestUpstream(t)
	c := NewReturnsProcessing(Config{Endpoint: up.URL})

	authorized, err := c.GetReturn(context.Background(), "rma-1b4e")
	if err != nil {
		t.Fatalf("GetReturn: %v", err)
	}
	assertAuthorizedRMA(t, authorized)

	rejected, err := c.GetReturn(context.Background(), "rma-2c3d")
	if err != nil {
		t.Fatalf("GetReturn (rejected): %v", err)
	}
	assertRejectedRMA(t, rejected)
}

func TestReturnsProcessing_GetReturn_ClassifiesNotFoundAndInvalidInput(t *testing.T) {
	up := newReturnsProcessingTestUpstream(t)
	c := NewReturnsProcessing(Config{Endpoint: up.URL})

	_, err := c.GetReturn(context.Background(), "rma-unknown")
	if !errors.Is(err, ports.ErrUpstreamNotFound) || !strings.Contains(err.Error(), "rma-not-found") {
		t.Fatalf("rma-not-found must classify as upstream-not-found, got %v", err)
	}
	_, err = c.GetReturn(context.Background(), "bad id")
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) || !strings.Contains(err.Error(), "invalid-rma-id") {
		t.Fatalf("invalid-rma-id must classify as an input rejection, got %v", err)
	}
}

func TestReturnsProcessing_ListReturnsByOrder_DecodesAndEmptyIsNotAnError(t *testing.T) {
	up := newReturnsProcessingTestUpstream(t)
	c := NewReturnsProcessing(Config{Endpoint: up.URL})

	both, err := c.ListReturnsByOrder(context.Background(), "ord-7c9e")
	if err != nil {
		t.Fatalf("ListReturnsByOrder: %v", err)
	}
	if len(both.Items) != 2 || both.Items[0].RmaID != "rma-2c3d" || both.Items[1].RmaID != "rma-1b4e" {
		t.Fatalf("unexpected RMA list: %+v", both.Items)
	}
	if got := up.args(); got["order_id"] != "ord-7c9e" || len(got) != 1 {
		t.Fatalf("the order id must be sent under its published name and nothing else, sent %v", got)
	}

	none, err := c.ListReturnsByOrder(context.Background(), "ord-none")
	if err != nil {
		t.Fatalf("an order with no returns answers an empty list, never an error: %v", err)
	}
	if len(none.Items) != 0 {
		t.Fatalf("unexpected list for an order without returns: %+v", none.Items)
	}
}

// assertCurrentPolicy pins the decoded shape of the version-3 policy
// fixture (the rule table included).
func assertCurrentPolicy(t *testing.T, current ports.DispositionPolicy) {
	t.Helper()
	if current.Version != 3 || current.ReturnWindowDays != 30 || current.MaxUnitsPerLine != 5 ||
		current.VetoOutcome != "DISPOSE" || current.DeclaredAt != "2026-10-01T09:00:00Z" || len(current.Rules) != 2 {
		t.Fatalf("unexpected policy: %+v", current)
	}
	if current.Rules[0].Grade != "A" || current.Rules[0].ProductClass != "*" || current.Rules[0].Outcome != "RESTOCK" ||
		current.Rules[1].Grade != "C" || current.Rules[1].ProductClass != "Hazmat" || current.Rules[1].Outcome != "DISPOSE" {
		t.Fatalf("unexpected policy rules: %+v", current.Rules)
	}
}

func TestReturnsProcessing_GetDispositionPolicy_OmitsZeroVersionAndSendsDeclared(t *testing.T) {
	up := newReturnsProcessingTestUpstream(t)
	c := NewReturnsProcessing(Config{Endpoint: up.URL})

	current, err := c.GetDispositionPolicy(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetDispositionPolicy (current): %v", err)
	}
	if len(up.args()) != 0 {
		t.Fatalf("version 0 must omit the argument so returns-processing answers the CURRENT policy, sent %v", up.args())
	}
	assertCurrentPolicy(t, current)

	declared, err := c.GetDispositionPolicy(context.Background(), 3)
	if err != nil {
		t.Fatalf("GetDispositionPolicy (declared): %v", err)
	}
	if got := up.args(); got["version"] != float64(3) || len(got) != 1 {
		t.Fatalf("a declared version must be sent under its published name and nothing else, sent %v", got)
	}
	if declared.Version != 3 {
		t.Fatalf("unexpected declared policy: %+v", declared)
	}

	_, err = c.GetDispositionPolicy(context.Background(), 99)
	if !errors.Is(err, ports.ErrUpstreamNotFound) || !strings.Contains(err.Error(), "policy-not-found") {
		t.Fatalf("policy-not-found must classify as upstream-not-found, got %v", err)
	}
}

// TestReturnsProcessing_StandInRejectsUnpublishedArgumentKeys proves the
// stand-in can fail: a camelCase key that returns-processing does not
// publish is rejected by its input schema, so the argument-name assertions
// above are real.
func TestReturnsProcessing_StandInRejectsUnpublishedArgumentKeys(t *testing.T) {
	up := newReturnsProcessingTestUpstream(t)
	err := New(Config{Name: "returns-processing", Endpoint: up.URL}).callTool(context.Background(), "get_return", map[string]any{"rmaId": "rma-1b4e"}, nil)
	if err == nil {
		t.Fatal("an argument key outside returns-processing's published input schema must be rejected")
	}
}

func TestReturnsProcessing_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newReturnsProcessingTestUpstream(t)
	c := NewReturnsProcessing(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetReturn(ctx, "rma-1b4e")
	_, _ = c.ListReturnsByOrder(ctx, "ord-7c9e")
	_, _ = c.GetDispositionPolicy(ctx, 0)

	up.mu.Lock()
	defer up.mu.Unlock()
	sort.Strings(up.calls)
	if strings.Join(up.calls, ",") != strings.Join(wantReturnsProcessingCalledTools, ",") {
		t.Fatalf("tools called = %v, want exactly %v", up.calls, wantReturnsProcessingCalledTools)
	}
}

// TestReturnsProcessing_UnreachableUpstream is the fail-open-absent-upstream
// path at the client layer: with the upstream gone, every call surfaces an
// ordinary error for the use case to degrade on — never a panic, never a hang.
func TestReturnsProcessing_UnreachableUpstream(t *testing.T) {
	up := newReturnsProcessingTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.

	c := NewReturnsProcessing(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetReturn(ctx, "rma-1b4e"); err == nil {
		t.Error("GetReturn: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListReturnsByOrder(ctx, "ord-7c9e"); err == nil {
		t.Error("ListReturnsByOrder: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetDispositionPolicy(ctx, 0); err == nil {
		t.Error("GetDispositionPolicy: an unreachable upstream must surface as an error")
	}
}

func TestReturnsProcessing_RespectsCallerContext(t *testing.T) {
	up := newReturnsProcessingTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewReturnsProcessing(Config{Endpoint: up.URL}).GetReturn(ctx, "rma-1b4e")
	if err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
}
