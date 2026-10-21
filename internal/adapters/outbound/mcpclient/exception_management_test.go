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

// exceptionManagementGoldenPath is a VERBATIM copy of
// exception-management's own published tool registry golden
// (exception-management: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at exception-management origin/develop 0c04820). Refresh it with
//
//	git -C ../exception-management show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever exception-management amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const exceptionManagementGoldenPath = "testdata/exception_management_tools.golden.json"

// wantExceptionManagementTools is every tool exception-management publishes
// (four, the verbatim golden); the client calls all four.
var wantExceptionManagementTools = []string{"get_exception", "get_exception_policies", "get_sla_report", "list_open_exceptions"}

// wantExceptionManagementCalledTools is the exact set of tools this client
// calls (the whole published read surface).
var wantExceptionManagementCalledTools = []string{"get_exception", "get_exception_policies", "get_sla_report", "list_open_exceptions"}

func loadExceptionManagementGolden(t *testing.T) map[string]goldenTool {
	t.Helper()
	raw, err := os.ReadFile(exceptionManagementGoldenPath)
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

func TestExceptionManagement_GoldenPinsExactlyTheFourReadOnlyTools(t *testing.T) {
	golden := loadExceptionManagementGolden(t)

	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantExceptionManagementTools, ",") {
		t.Fatalf("exception-management publishes %v, this golden is built for exactly %v", names, wantExceptionManagementTools)
	}
	for _, name := range wantExceptionManagementTools {
		if golden[name].Annotations["readOnlyHint"] != true {
			t.Errorf("%s is not annotated readOnlyHint in exception-management's registry: this agent may only call read tools", name)
		}
	}
}

// TestExceptionManagement_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema: each json tag must be a
// published property (no invented field) and each required property must
// be mirrored (no silently dropped field).
func TestExceptionManagement_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadExceptionManagementGolden(t)
	cases := map[string]reflect.Type{
		"get_exception":          reflect.TypeFor[ports.ExceptionCase](),
		"list_open_exceptions":   reflect.TypeFor[ports.ExceptionCaseList](),
		"get_exception_policies": reflect.TypeFor[ports.ExceptionPolicyList](),
		"get_sla_report":         reflect.TypeFor[ports.ExceptionSLAReport](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful exception-management stand-in --------------------------

// exceptionManagementUpstream is a real Streamable-HTTP MCP server whose
// four tools carry exception-management's PUBLISHED input and output
// schemas from the golden: the SDK validates the client's arguments
// (additionalProperties:false, so a misspelled key is rejected) and the
// canned results against them.
type exceptionManagementUpstream struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []string
	lastArgs map[string]any
}

func (u *exceptionManagementUpstream) record(tool string, args map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, tool)
	u.lastArgs = args
}

func (u *exceptionManagementUpstream) args() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastArgs
}

const (
	emCaseOpen = `{"case_id":"case-8f21","source_type":"com.warehouse.wes.fulfillment-execution.task.TaskCPTMissed",
	  "source_subject":"task-8a1f","site_id":"WH1","status":"OPEN","severity":"warning","queue":"fulfillment",
	  "occurrence_count":2,"escalated":false,"opened_at":"2026-10-21T08:00:00Z","last_seen_at":"2026-10-21T08:30:00Z",
	  "sla_minutes":120,"sla_due_at":"2026-10-21T10:00:00Z","source_event_id":"evt-1",
	  "source_payload":{"task_id":"task-8a1f","cpt":"2026-10-21T09:00:00Z"},
	  "audit":[{"action":"opened","actor":"sweeper","at":"2026-10-21T08:00:00Z"}],"version":2}`

	emCaseAutoResolved = `{"case_id":"case-9a11","source_type":"com.warehouse.wes.order-management.order.OrderAllocated",
	  "source_subject":"wu-8a1f","status":"RESOLVED","severity":"info","queue":"order-correlation","occurrence_count":1,
	  "escalated":false,"resolution_code":"Correlated","root_cause":"AllocationConfirmed","resolved_at":"2026-10-21T09:05:00Z",
	  "auto_resolve_rule_id":"com.warehouse.wes.order-management.order.OrderAllocated->com.warehouse.wes.order-management.order.OrderRepromised",
	  "correlated_type":"com.warehouse.wes.order-management.order.OrderRepromised","correlated_event_id":"evt-2",
	  "opened_at":"2026-10-21T08:50:00Z","last_seen_at":"2026-10-21T09:00:00Z","sla_minutes":1440,
	  "sla_due_at":"2026-10-22T08:50:00Z","source_event_id":"evt-3","source_payload":{"order_id":"ord-7c9e"},
	  "audit":[{"action":"opened","actor":"sweeper","at":"2026-10-21T08:50:00Z"},
	           {"action":"auto-resolved","actor":"rule","at":"2026-10-21T09:05:00Z"}],"version":3}`

	emPolicies = `{"items":[
	  {"source_type":"com.warehouse.wes.fulfillment-execution.task.TaskCPTMissed","version":2,"severity":"warning",
	   "queue":"fulfillment","sla_minutes":120,"auto_resolve":null,"declared_at":"2026-10-20T10:00:00Z"},
	  {"source_type":"com.warehouse.wes.order-management.order.OrderAllocated","version":1,"severity":"info",
	   "queue":"order-correlation","sla_minutes":1440,
	   "auto_resolve":{"on_type":"com.warehouse.wes.order-management.order.OrderRepromised","within_minutes":240},
	   "declared_at":"2026-10-20T10:05:00Z"}]}`

	emSLAReport = `{"generated_at":"2026-10-21T09:30:00Z","open_cases":12,"breached":2,"escalated":1,
	  "by_severity":{"warning":9,"info":3},"by_queue":{"fulfillment":8,"order-correlation":4}}`
)

func emFixture(raw string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}

func emToolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func emListOpenExceptions(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	if in["status"] == "RESOLVED" {
		return nil, map[string]any{"items": []any{emFixture(emCaseAutoResolved)}}, nil
	}
	return nil, map[string]any{"items": []any{emFixture(emCaseOpen)}}, nil
}

func emGetException(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	id, _ := in["case_id"].(string)
	if id == "" {
		return emToolError("invalid-case-id: case_id must name the case to read, starting with case-"), nil, nil
	}
	switch id {
	case "case-8f21":
		return nil, emFixture(emCaseOpen), nil
	case "case-9a11":
		return nil, emFixture(emCaseAutoResolved), nil
	default:
		return emToolError("case-not-found: exception case not found"), nil, nil
	}
}

func emGetExceptionPolicies(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
	return nil, emFixture(emPolicies), nil
}

func emGetSLAReport(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
	return nil, emFixture(emSLAReport), nil
}

func newExceptionManagementTestUpstream(t *testing.T) *exceptionManagementUpstream {
	t.Helper()
	golden := loadExceptionManagementGolden(t)
	up := &exceptionManagementUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "exception-management-test", Version: "0"}, nil)
	handlers := map[string]mcp.ToolHandlerFor[map[string]any, any]{
		"list_open_exceptions":   emListOpenExceptions,
		"get_exception":          emGetException,
		"get_exception_policies": emGetExceptionPolicies,
		"get_sla_report":         emGetSLAReport,
	}
	for name, h := range handlers {
		g, h := golden[name], h
		mcp.AddTool(server, &mcp.Tool{Name: name, InputSchema: g.InputSchema, OutputSchema: g.OutputSchema},
			func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				up.record(g.Name, in)
				return h(ctx, req, in)
			})
	}
	// A write tool exception-management does NOT publish (triage, assign,
	// escalate and resolve stay on its REST surface); the client must never
	// reach anything like it.
	mcp.AddTool(server, &mcp.Tool{Name: "resolve_exception"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		t.Error("write tool resolve_exception was invoked")
		return nil, map[string]any{}, nil
	})
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

// --- behaviour over the wire --------------------------------------------------

func TestExceptionManagement_ListOpenExceptions_DecodesAndSendsFilters(t *testing.T) {
	up := newExceptionManagementTestUpstream(t)
	c := NewExceptionManagement(Config{Endpoint: up.URL})

	open, err := c.ListOpenExceptions(context.Background(), ports.ExceptionListQuery{})
	if err != nil {
		t.Fatalf("ListOpenExceptions: %v", err)
	}
	if len(open.Items) != 1 || open.Items[0].CaseID != "case-8f21" {
		t.Fatalf("unexpected case list: %+v", open.Items)
	}
	if len(up.args()) != 0 {
		t.Fatalf("a zero query must send no argument at all (every status, queue, site and source type alike), sent %v", up.args())
	}
	assertOpenCase(t, open.Items[0])

	resolved, err := c.ListOpenExceptions(context.Background(), ports.ExceptionListQuery{
		Status: "RESOLVED", Queue: "order-correlation", SiteID: "WH1",
		SourceType: "com.warehouse.wes.order-management.order.OrderAllocated",
	})
	if err != nil {
		t.Fatalf("ListOpenExceptions (resolved): %v", err)
	}
	got := up.args()
	if got["status"] != "RESOLVED" || got["queue"] != "order-correlation" || got["site_id"] != "WH1" ||
		got["source_type"] != "com.warehouse.wes.order-management.order.OrderAllocated" {
		t.Fatalf("filters not sent under exception-management's names: %v", got)
	}
	if len(resolved.Items) != 1 || resolved.Items[0].CaseID != "case-9a11" {
		t.Fatalf("unexpected resolved list: %+v", resolved.Items)
	}
}

// assertCaseIdentity pins the dedup identity, SLA clock and status fields
// of the OPEN fixture.
func assertCaseIdentity(t *testing.T, c ports.ExceptionCase) {
	t.Helper()
	if c.SourceType != "com.warehouse.wes.fulfillment-execution.task.TaskCPTMissed" || c.SourceSubject != "task-8a1f" ||
		c.SiteID != "WH1" || c.Status != "OPEN" || c.Severity != "warning" || c.Queue != "fulfillment" ||
		c.OccurrenceCount != 2 || c.Escalated || c.OpenedAt != "2026-10-21T08:00:00Z" || c.LastSeenAt != "2026-10-21T08:30:00Z" ||
		c.SLAMinutes != 120 || c.SLADueAt != "2026-10-21T10:00:00Z" || c.SourceEventID != "evt-1" || c.Version != 2 {
		t.Fatalf("unexpected open case: %+v", c)
	}
}

// assertSourcePayloadAndAudit pins the frozen opening fact and the audit
// trail of the OPEN fixture; an open case must also decode with empty
// resolution fields.
func assertSourcePayloadAndAudit(t *testing.T, c ports.ExceptionCase) {
	t.Helper()
	if c.SourcePayload["task_id"] != "task-8a1f" || c.SourcePayload["cpt"] != "2026-10-21T09:00:00Z" {
		t.Fatalf("the frozen source payload must decode verbatim: %+v", c.SourcePayload)
	}
	if len(c.Audit) != 1 || c.Audit[0].Action != "opened" || c.Audit[0].Actor != "sweeper" || c.Audit[0].At != "2026-10-21T08:00:00Z" {
		t.Fatalf("unexpected audit trail: %+v", c.Audit)
	}
	if c.ResolutionCode != "" || c.AutoResolveRuleID != "" || c.CorrelatedType != "" || c.ResolvedAt != "" {
		t.Fatalf("an open case must decode with empty resolution fields: %+v", c)
	}
}

func assertOpenCase(t *testing.T, c ports.ExceptionCase) {
	t.Helper()
	assertCaseIdentity(t, c)
	assertSourcePayloadAndAudit(t, c)
}

// assertAutoResolvedCase pins the decoded shape of the auto-RESOLVED
// fixture: the resolution fields plus the correlated event and rule.
func assertAutoResolvedCase(t *testing.T, auto ports.ExceptionCase) {
	t.Helper()
	if auto.Status != "RESOLVED" || auto.ResolutionCode != "Correlated" || auto.RootCause != "AllocationConfirmed" ||
		auto.ResolvedAt != "2026-10-21T09:05:00Z" ||
		auto.AutoResolveRuleID != "com.warehouse.wes.order-management.order.OrderAllocated->com.warehouse.wes.order-management.order.OrderRepromised" ||
		auto.CorrelatedType != "com.warehouse.wes.order-management.order.OrderRepromised" || auto.CorrelatedEventID != "evt-2" {
		t.Fatalf("unexpected auto-resolved case: %+v", auto)
	}
	if len(auto.Audit) != 2 || auto.Audit[1].Action != "auto-resolved" {
		t.Fatalf("unexpected audit trail: %+v", auto.Audit)
	}
}

func TestExceptionManagement_GetException_DecodesAutoResolvedAndClassifiesErrors(t *testing.T) {
	up := newExceptionManagementTestUpstream(t)
	c := NewExceptionManagement(Config{Endpoint: up.URL})

	auto, err := c.GetException(context.Background(), "case-9a11")
	if err != nil {
		t.Fatalf("GetException: %v", err)
	}
	assertAutoResolvedCase(t, auto)
	if got := up.args(); got["case_id"] != "case-9a11" || len(got) != 1 {
		t.Fatalf("the case id must be sent under its published name and nothing else, sent %v", got)
	}

	_, err = c.GetException(context.Background(), "case-unknown")
	if !errors.Is(err, ports.ErrUpstreamNotFound) || !strings.Contains(err.Error(), "case-not-found") {
		t.Fatalf("case-not-found must classify as upstream-not-found, got %v", err)
	}
	_, err = c.GetException(context.Background(), "")
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) || !strings.Contains(err.Error(), "invalid-case-id") {
		t.Fatalf("invalid-case-id must classify as an input rejection, got %v", err)
	}
}

func TestExceptionManagement_GetExceptionPolicies_DecodesAutoResolveAndNull(t *testing.T) {
	up := newExceptionManagementTestUpstream(t)
	c := NewExceptionManagement(Config{Endpoint: up.URL})

	policies, err := c.GetExceptionPolicies(context.Background())
	if err != nil {
		t.Fatalf("GetExceptionPolicies: %v", err)
	}
	if len(policies.Items) != 2 {
		t.Fatalf("unexpected policy list: %+v", policies.Items)
	}
	first := policies.Items[0]
	if first.SourceType != "com.warehouse.wes.fulfillment-execution.task.TaskCPTMissed" || first.Version != 2 ||
		first.Severity != "warning" || first.Queue != "fulfillment" || first.SLAMinutes != 120 ||
		first.AutoResolve != nil || first.DeclaredAt != "2026-10-20T10:00:00Z" {
		t.Fatalf("unexpected policy without a rule: %+v", first)
	}
	second := policies.Items[1]
	if second.AutoResolve == nil || second.AutoResolve.OnType != "com.warehouse.wes.order-management.order.OrderRepromised" ||
		second.AutoResolve.WithinMinutes != 240 {
		t.Fatalf("unexpected auto-resolve rule: %+v", second.AutoResolve)
	}
	if len(up.args()) != 0 {
		t.Fatalf("get_exception_policies takes no argument, sent %v", up.args())
	}
}

func TestExceptionManagement_GetSLAReport_DecodesCounts(t *testing.T) {
	up := newExceptionManagementTestUpstream(t)
	c := NewExceptionManagement(Config{Endpoint: up.URL})

	report, err := c.GetSLAReport(context.Background())
	if err != nil {
		t.Fatalf("GetSLAReport: %v", err)
	}
	if report.GeneratedAt != "2026-10-21T09:30:00Z" || report.OpenCases != 12 || report.Breached != 2 || report.Escalated != 1 ||
		report.BySeverity["warning"] != 9 || report.BySeverity["info"] != 3 ||
		report.ByQueue["fulfillment"] != 8 || report.ByQueue["order-correlation"] != 4 {
		t.Fatalf("unexpected SLA report: %+v", report)
	}
	if len(up.args()) != 0 {
		t.Fatalf("get_sla_report takes no argument, sent %v", up.args())
	}
}

// TestExceptionManagement_StandInRejectsUnpublishedArgumentKeys proves the
// stand-in can fail: a key that exception-management does not publish is
// rejected by its input schema, so the no-argument assertions above are
// real.
func TestExceptionManagement_StandInRejectsUnpublishedArgumentKeys(t *testing.T) {
	up := newExceptionManagementTestUpstream(t)
	err := New(Config{Name: "exception-management", Endpoint: up.URL}).callTool(context.Background(), "get_sla_report", map[string]any{"siteId": "WH1"}, nil)
	if err == nil {
		t.Fatal("an argument key outside exception-management's published input schema must be rejected")
	}
}

func TestExceptionManagement_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newExceptionManagementTestUpstream(t)
	c := NewExceptionManagement(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.ListOpenExceptions(ctx, ports.ExceptionListQuery{})
	_, _ = c.GetException(ctx, "case-8f21")
	_, _ = c.GetExceptionPolicies(ctx)
	_, _ = c.GetSLAReport(ctx)

	up.mu.Lock()
	defer up.mu.Unlock()
	sort.Strings(up.calls)
	if strings.Join(up.calls, ",") != strings.Join(wantExceptionManagementCalledTools, ",") {
		t.Fatalf("tools called = %v, want exactly %v", up.calls, wantExceptionManagementCalledTools)
	}
}

// TestExceptionManagement_UnreachableUpstream is the
// fail-open-absent-upstream path at the client layer: with the upstream
// gone, every call surfaces an ordinary error for the use case to degrade
// on — never a panic, never a hang.
func TestExceptionManagement_UnreachableUpstream(t *testing.T) {
	up := newExceptionManagementTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.

	c := NewExceptionManagement(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.ListOpenExceptions(ctx, ports.ExceptionListQuery{}); err == nil {
		t.Error("ListOpenExceptions: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetException(ctx, "case-8f21"); err == nil {
		t.Error("GetException: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetExceptionPolicies(ctx); err == nil {
		t.Error("GetExceptionPolicies: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetSLAReport(ctx); err == nil {
		t.Error("GetSLAReport: an unreachable upstream must surface as an error")
	}
}

func TestExceptionManagement_RespectsCallerContext(t *testing.T) {
	up := newExceptionManagementTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewExceptionManagement(Config{Endpoint: up.URL}).GetException(ctx, "case-8f21")
	if err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
}
