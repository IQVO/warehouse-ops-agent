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

// slottingGoldenPath is a VERBATIM copy of slotting-optimization's own
// published tool registry golden
// (slotting-optimization: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at slotting-optimization origin/develop 1c2da6d-era, sha256
// 4f5a9040b5d076f4734763f0af369651ea7c8b0e749d2421707d8f7b60ad3fa1).
// Refresh it with
//
//	git -C ../slotting-optimization show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever slotting-optimization amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const slottingGoldenPath = "testdata/slotting_optimization_tools.golden.json"

// wantSlottingTools is every tool slotting-optimization publishes (four);
// the client itself deliberately calls only the two in
// wantSlottingCalledTools below.
var wantSlottingTools = []string{"get_forward_slots", "get_sku_velocity", "get_slot_plan", "list_slot_plans"}

// wantSlottingCalledTools is the exact set of tools this client calls (the
// two the slot-plan watch consumes, ADR 0024).
var wantSlottingCalledTools = []string{"get_slot_plan", "list_slot_plans"}

func loadSlottingGolden(t *testing.T) map[string]goldenTool {
	t.Helper()
	raw, err := os.ReadFile(slottingGoldenPath)
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

func TestSlotting_GoldenPinsExactlyTheFourReadOnlyTools(t *testing.T) {
	golden := loadSlottingGolden(t)

	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantSlottingTools, ",") {
		t.Fatalf("slotting-optimization publishes %v, this golden is built for exactly %v", names, wantSlottingTools)
	}
	for _, name := range wantSlottingTools {
		if golden[name].Annotations["readOnlyHint"] != true {
			t.Errorf("%s is not annotated readOnlyHint in slotting-optimization's registry: this agent may only call read tools (ADR 0024)", name)
		}
	}
}

// TestSlotting_PortDTOsMatchPublishedOutputSchemas walks every port DTO the
// client decodes against the published output schema: each json tag must be
// a published property (no invented field) and each required property must
// be mirrored (no silently dropped field).
func TestSlotting_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadSlottingGolden(t)
	cases := map[string]reflect.Type{
		"get_slot_plan":   reflect.TypeFor[ports.SlotPlan](),
		"list_slot_plans": reflect.TypeFor[ports.SlotPlanPage](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful slotting-optimization stand-in ------------------------

// slottingUpstream is a real Streamable-HTTP MCP server whose four tools
// carry slotting-optimization's PUBLISHED input and output schemas from the
// golden: the SDK validates the client's arguments (additionalProperties:
// false, so a misspelled key is rejected) and the canned results against
// them.
type slottingUpstream struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []string
	lastArgs map[string]any
}

func (u *slottingUpstream) record(tool string, args map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, tool)
	u.lastArgs = args
}

func (u *slottingUpstream) args() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastArgs
}

const (
	soSummary1 = `{"plan_id":"plan-1","site_id":"WH1","state":"Approved","policy":"velocity",
	  "window_from":"2026-10-01T00:00:00Z","window_to":"2026-10-29T00:00:00Z",
	  "generated_at":"2026-10-15T08:00:00Z","approved_at":"2026-10-15T09:00:00Z",
	  "assignment_count":3,"move_count":2,"unassigned_count":1,"version":2}`

	soPlan1 = `{"plan_id":"plan-1","site_id":"WH1","state":"Approved","policy":"velocity",
	  "window_from":"2026-10-01T00:00:00Z","window_to":"2026-10-29T00:00:00Z",
	  "generated_at":"2026-10-15T08:00:00Z","approved_at":"2026-10-15T09:00:00Z",
	  "assignments":[{"sku":"SKU-1","slot":"F-A01","abc_class":"A","picks":50,"units":120}],
	  "moves":[{"sku":"SKU-1","kind":"Move","from_slot":"B-01","to_slot":"F-A01"},{"sku":"SKU-2","kind":"Move","from_slot":"B-02","to_slot":"F-A02"}],
	  "unassigned":[{"sku":"SKU-9","reason":"no empty forward slot"}],
	  "execution":{"moves_total":2,"moves_executed":1,"last_executed_at":"2026-10-16T10:00:00Z",
	    "moves":[{"move_seq":1,"sku":"SKU-1","kind":"Move","executed":true,"executed_quantity":120,"last_executed_at":"2026-10-16T10:00:00Z"},
	             {"move_seq":2,"sku":"SKU-2","kind":"Move","executed":false,"executed_quantity":0}]},
	  "version":2}`

	// soPlan2 has moves but NO execution key: slotting-optimization has
	// recorded no relocations against it yet.
	soPlan2 = `{"plan_id":"plan-2","site_id":"WH2","state":"Approved","policy":"velocity",
	  "window_from":"2026-10-01T00:00:00Z","window_to":"2026-10-29T00:00:00Z",
	  "generated_at":"2026-10-14T08:00:00Z","approved_at":"2026-10-14T09:00:00Z",
	  "assignments":[],"moves":[{"sku":"SKU-3","kind":"Move","from_slot":"B-03","to_slot":"F-A03"}],
	  "unassigned":[],"version":1}`
)

func soFixture(raw string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}

func soToolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func soGetSlotPlan(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	switch in["plan_id"] {
	case "plan-1":
		return nil, soFixture(soPlan1), nil
	case "plan-2":
		return nil, soFixture(soPlan2), nil
	case "bad":
		return soToolError("invalid-plan-id: malformed plan id"), nil, nil
	}
	return soToolError("plan-not-found: no plan " + strOf(in["plan_id"])), nil, nil
}

func soListSlotPlans(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	if state, _ := in["state"].(string); state != "" && state != "Approved" && state != "Draft" && state != "Rejected" && state != "Superseded" {
		return soToolError("invalid-state: unknown state " + state), nil, nil
	}
	if in["cursor"] == "c2" {
		return nil, map[string]any{"items": []any{}}, nil
	}
	return nil, map[string]any{"items": []any{soFixture(soSummary1)}, "next_cursor": "c2"}, nil
}

func soGetForwardSlots(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return nil, map[string]any{"site_id": "WH1", "assignments": []any{}}, nil
}

func soGetSkuVelocity(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return nil, map[string]any{
		"site_id": "WH1", "window_from": "2026-09-19T00:00:00Z", "window_to": "2026-10-17T00:00:00Z",
		"items": []any{map[string]any{"sku": "SKU-1", "picks": 50, "units": 120}},
	}, nil
}

// strOf renders a JSON argument value for an error message.
func strOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "?"
	}
	return string(b)
}

func newSlottingTestUpstream(t *testing.T) *slottingUpstream {
	t.Helper()
	golden := loadSlottingGolden(t)
	up := &slottingUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "slotting-optimization-test", Version: "0"}, nil)
	handlers := map[string]mcp.ToolHandlerFor[map[string]any, any]{
		"get_slot_plan":     soGetSlotPlan,
		"list_slot_plans":   soListSlotPlans,
		"get_forward_slots": soGetForwardSlots,
		"get_sku_velocity":  soGetSkuVelocity,
	}
	for name, h := range handlers {
		g, h := golden[name], h
		mcp.AddTool(server, &mcp.Tool{Name: name, InputSchema: g.InputSchema, OutputSchema: g.OutputSchema},
			func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				up.record(g.Name, in)
				return h(ctx, req, in)
			})
	}
	// A write tool slotting-optimization does NOT publish; the client must
	// never reach anything like it.
	mcp.AddTool(server, &mcp.Tool{Name: "approve_slot_plan"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		t.Error("write tool approve_slot_plan was invoked")
		return nil, map[string]any{}, nil
	})
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

// --- behaviour over the wire --------------------------------------------------

func TestSlotting_GetSlotPlan_DecodesExecutionView(t *testing.T) {
	up := newSlottingTestUpstream(t)
	c := NewSlottingOptimization(Config{Endpoint: up.URL})

	plan, err := c.GetSlotPlan(context.Background(), "plan-1")
	if err != nil {
		t.Fatalf("GetSlotPlan: %v", err)
	}
	assertSlotPlanIdentity(t, plan)
	assertSlotPlanExecution(t, plan)
	assertSlotPlanMoves(t, plan)
	if up.args()["plan_id"] != "plan-1" {
		t.Fatalf("plan_id argument not sent: %v", up.args())
	}
}

func assertSlotPlanIdentity(t *testing.T, plan ports.SlotPlan) {
	t.Helper()
	if plan.PlanId != "plan-1" || plan.SiteId != "WH1" || plan.State != "Approved" || plan.Version != 2 {
		t.Fatalf("unexpected plan identity: %+v", plan)
	}
	if plan.ApprovedAt != "2026-10-15T09:00:00Z" {
		t.Fatalf("approved_at not decoded: %+v", plan)
	}
}

func assertSlotPlanExecution(t *testing.T, plan ports.SlotPlan) {
	t.Helper()
	if plan.Execution == nil {
		t.Fatalf("execution view not decoded: %+v", plan)
	}
	if plan.Execution.MovesTotal != 2 || plan.Execution.MovesExecuted != 1 || plan.Execution.LastExecutedAt != "2026-10-16T10:00:00Z" {
		t.Fatalf("unexpected execution counters: %+v", plan.Execution)
	}
	if len(plan.Execution.Moves) != 2 || !plan.Execution.Moves[0].Executed || plan.Execution.Moves[1].Executed || plan.Execution.Moves[0].ExecutedQuantity != 120 {
		t.Fatalf("unexpected execution moves: %+v", plan.Execution.Moves)
	}
}

func assertSlotPlanMoves(t *testing.T, plan ports.SlotPlan) {
	t.Helper()
	if len(plan.Moves) != 2 || plan.Moves[0].FromSlot != "B-01" || plan.Moves[0].ToSlot != "F-A01" {
		t.Fatalf("unexpected planned moves: %+v", plan.Moves)
	}
	if len(plan.Unassigned) != 1 || plan.Unassigned[0].Sku != "SKU-9" || plan.Unassigned[0].Reason == "" {
		t.Fatalf("unexpected unassigned: %+v", plan.Unassigned)
	}
}

func TestSlotting_GetSlotPlan_NullExecutionDecodesAsNil(t *testing.T) {
	up := newSlottingTestUpstream(t)
	plan, err := NewSlottingOptimization(Config{Endpoint: up.URL}).GetSlotPlan(context.Background(), "plan-2")
	if err != nil {
		t.Fatalf("GetSlotPlan (no execution): %v", err)
	}
	if plan.Execution != nil {
		t.Fatalf("a plan with no execution recorded must decode a nil execution, got %+v", plan.Execution)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("planned moves must still decode: %+v", plan.Moves)
	}
}

func TestSlotting_GetSlotPlan_ErrorSlugs(t *testing.T) {
	up := newSlottingTestUpstream(t)
	c := NewSlottingOptimization(Config{Endpoint: up.URL})

	_, err := c.GetSlotPlan(context.Background(), "plan-404")
	var te *ToolError
	if !errors.As(err, &te) || te.Slug != "plan-not-found" {
		t.Fatalf("want a plan-not-found ToolError, got %v", err)
	}
	if errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatal("not-found is an upstream answer, not an input rejection")
	}

	_, err = c.GetSlotPlan(context.Background(), "bad")
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatalf("invalid-plan-id must classify as an input rejection, got %v", err)
	}
}

func TestSlotting_ListSlotPlans_OmitsUnsetArgumentsAndFollowsCursor(t *testing.T) {
	up := newSlottingTestUpstream(t)
	c := NewSlottingOptimization(Config{Endpoint: up.URL})

	first, err := c.ListSlotPlans(context.Background(), ports.SlotPlanListQuery{})
	if err != nil {
		t.Fatalf("ListSlotPlans: %v", err)
	}
	if len(up.args()) != 0 {
		t.Fatalf("a zero query must send no argument at all (slotting-optimization defaults apply), sent %v", up.args())
	}
	if len(first.Items) != 1 || first.Items[0].PlanId != "plan-1" || first.NextCursor != "c2" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	if first.Items[0].ApprovedAt != "2026-10-15T09:00:00Z" || first.Items[0].MoveCount != 2 || first.Items[0].State != "Approved" {
		t.Fatalf("summary fields not decoded: %+v", first.Items[0])
	}
}

func TestSlotting_ListSlotPlans_SendsEveryFilterUnderItsPublishedName(t *testing.T) {
	up := newSlottingTestUpstream(t)
	c := NewSlottingOptimization(Config{Endpoint: up.URL})

	second, err := c.ListSlotPlans(context.Background(), ports.SlotPlanListQuery{Cursor: "c2", State: "Approved", SiteId: "WH1", Limit: 500})
	if err != nil {
		t.Fatalf("ListSlotPlans: %v (a key outside the published input schema is rejected by the stand-in)", err)
	}
	got := up.args()
	if got["cursor"] != "c2" || got["state"] != "Approved" || got["site_id"] != "WH1" || got["limit"] != float64(500) {
		t.Fatalf("filters not sent under slotting-optimization's names: %v", got)
	}
	if len(second.Items) != 0 || second.NextCursor != "" {
		t.Fatalf("unexpected last page: %+v", second)
	}
}

func TestSlotting_ListSlotPlans_UnknownStateIsInvalidInput(t *testing.T) {
	up := newSlottingTestUpstream(t)
	_, err := NewSlottingOptimization(Config{Endpoint: up.URL}).ListSlotPlans(context.Background(), ports.SlotPlanListQuery{State: "Bogus"})
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) || !strings.Contains(err.Error(), "invalid-state") {
		t.Fatalf("invalid-state must classify as an input rejection, got %v", err)
	}
}

// TestSlotting_StandInRejectsUnpublishedArgumentKeys proves the stand-in can
// fail: a camelCase key slotting-optimization does not publish is rejected
// by its input schema, so the argument-name assertions above are real.
func TestSlotting_StandInRejectsUnpublishedArgumentKeys(t *testing.T) {
	up := newSlottingTestUpstream(t)
	err := New(Config{Name: "slotting-optimization", Endpoint: up.URL}).callTool(context.Background(), "get_slot_plan", map[string]any{"planId": "plan-1"}, nil)
	if err == nil {
		t.Fatal("an argument key outside slotting-optimization's published input schema must be rejected")
	}
}

func TestSlotting_OnlyTheTwoPinnedReadToolsAreCalled(t *testing.T) {
	up := newSlottingTestUpstream(t)
	c := NewSlottingOptimization(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetSlotPlan(ctx, "plan-1")
	_, _ = c.ListSlotPlans(ctx, ports.SlotPlanListQuery{})

	up.mu.Lock()
	defer up.mu.Unlock()
	if strings.Join(up.calls, ",") != strings.Join(wantSlottingCalledTools, ",") {
		t.Fatalf("tools called = %v, want exactly %v (the other two published tools are not this agent's to call)", up.calls, wantSlottingCalledTools)
	}
}

func TestSlotting_UnreachableUpstream(t *testing.T) {
	up := newSlottingTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.

	c := NewSlottingOptimization(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetSlotPlan(ctx, "plan-1"); err == nil {
		t.Error("GetSlotPlan: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListSlotPlans(ctx, ports.SlotPlanListQuery{}); err == nil {
		t.Error("ListSlotPlans: an unreachable upstream must surface as an error")
	}
}

func TestSlotting_RespectsCallerContext(t *testing.T) {
	up := newSlottingTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewSlottingOptimization(Config{Endpoint: up.URL}).GetSlotPlan(ctx, "plan-1")
	if err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
}
