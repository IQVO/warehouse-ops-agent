package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// inboundReceivingGoldenPath is a VERBATIM copy of inbound-receiving's own
// published tool registry golden
// (inbound-receiving: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at inbound-receiving origin/develop 139f73a). Refresh it with
//
//	git -C ../inbound-receiving show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever inbound-receiving amends its ADR 0005 tool surface; the tests
// below then say exactly which consumer-side assumption broke.
const inboundReceivingGoldenPath = "testdata/inbound_receiving_tools.golden.json"

// wantInboundReceivingTools is the exact set of tools this client calls.
var wantInboundReceivingTools = []string{
	"get_appointment", "get_asn", "get_receipt", "list_appointments", "list_asns", "list_docks", "list_receipts",
}

func loadInboundReceivingGolden(t *testing.T) map[string]goldenTool {
	t.Helper()
	raw, err := os.ReadFile(inboundReceivingGoldenPath)
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

func TestInboundReceiving_GoldenPinsExactlyTheSevenReadOnlyTools(t *testing.T) {
	golden := loadInboundReceivingGolden(t)

	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantInboundReceivingTools, ",") {
		t.Fatalf("inbound-receiving publishes %v, this client is built for exactly %v", names, wantInboundReceivingTools)
	}
	for _, name := range wantInboundReceivingTools {
		if golden[name].Annotations["readOnlyHint"] != true {
			t.Errorf("%s is not annotated readOnlyHint in inbound-receiving's registry: this agent may only call read tools (ADR 0021)", name)
		}
	}
}

// TestInboundReceiving_PortDTOsMatchPublishedOutputSchemas walks every port
// DTO against the published output schema: each json tag must be a published
// property (no invented field) and each required property must be mirrored
// (no silently dropped field).
func TestInboundReceiving_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadInboundReceivingGolden(t)
	cases := map[string]reflect.Type{
		"get_asn":           reflect.TypeFor[ports.InboundAsn](),
		"list_asns":         reflect.TypeFor[ports.InboundAsnPage](),
		"get_appointment":   reflect.TypeFor[ports.InboundAppointment](),
		"list_appointments": reflect.TypeFor[ports.InboundAppointmentPage](),
		"get_receipt":       reflect.TypeFor[ports.InboundReceipt](),
		"list_receipts":     reflect.TypeFor[ports.InboundReceiptPage](),
		"list_docks":        reflect.TypeFor[ports.InboundDockList](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful inbound-receiving stand-in ----------------------------

// inboundReceivingUpstream is a real Streamable-HTTP MCP server whose seven
// tools carry inbound-receiving's PUBLISHED input and output schemas from the
// golden: the SDK validates the client's arguments (additionalProperties:false,
// so a misspelled key is rejected) and the canned results against them.
type inboundReceivingUpstream struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []string
	lastArgs map[string]any
}

func (u *inboundReceivingUpstream) record(tool string, args map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, tool)
	u.lastArgs = args
}

func (u *inboundReceivingUpstream) args() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastArgs
}

const (
	irAsn1 = `{"asn_number":"ASN-1001","supplier_ref":"PO-77","expected_arrival":"2026-10-08T14:00:00Z","state":"Registered",
	  "lines":[{"line_no":1,"sku":"SKU-1","expected_qty":10},{"line_no":2,"sku":"SKU-2","expected_qty":4}],"version":2}`
	irAsn2 = `{"asn_number":"ASN-1002","supplier_ref":"PO-78","state":"Registered","lines":[{"line_no":1,"sku":"SKU-3","expected_qty":1}],"version":1}`

	irAppt1 = `{"appointment_id":"appt-1","door_code":"D1","carrier":"ACME","window_start":"2026-10-08T14:00:00Z","window_end":"2026-10-08T15:00:00Z",
	  "asn_numbers":["ASN-1001"],"state":"Booked","version":1}`

	irReceiptClosed = `{"receipt_id":"rcpt-1","asn_number":"ASN-1001","appointment_id":"appt-1","door_code":"D1","state":"Closed",
	  "lines":[{"line_no":1,"sku":"SKU-1","expected_qty":10,"received_good":8,"received_damaged":1}],
	  "opened_at":"2026-10-08T09:00:00Z","closed_at":"2026-10-08T10:00:00Z",
	  "discrepancies":[{"line_no":1,"sku":"SKU-1","kind":"Short","expected_qty":10,"received_qty":9,"damaged_qty":1}],"version":4}`
	irReceiptOpen = `{"receipt_id":"rcpt-2","asn_number":"ASN-1002","state":"Open",
	  "lines":[{"line_no":1,"sku":"SKU-3","expected_qty":1,"received_good":0,"received_damaged":0}],
	  "opened_at":"2026-10-07T09:00:00Z","discrepancies":[],"version":1}`
)

func irFixture(raw string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}

func irToolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func irGetAsn(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	switch in["asn_number"] {
	case "ASN-1001":
		return nil, irFixture(irAsn1), nil
	case "ASN-1002":
		return nil, irFixture(irAsn2), nil
	case "bad":
		return irToolError("invalid-asn-number: malformed ASN number"), nil, nil
	}
	return irToolError("asn-not-found: no ASN " + fmt.Sprint(in["asn_number"])), nil, nil
}

func irListAsns(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	if limit, ok := in["limit"].(float64); ok && (limit < 1 || limit > 500) {
		return irToolError("invalid-query: limit must be between 1 and 500"), nil, nil
	}
	if in["cursor"] == "c2" {
		return nil, map[string]any{"items": []any{irFixture(irAsn2)}}, nil
	}
	return nil, map[string]any{"items": []any{irFixture(irAsn1)}, "next_cursor": "c2"}, nil
}

func irGetAppointment(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	if in["appointment_id"] == "appt-1" {
		return nil, irFixture(irAppt1), nil
	}
	return irToolError("appointment-not-found: no appointment " + fmt.Sprint(in["appointment_id"])), nil, nil
}

func irListAppointments(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return nil, map[string]any{"items": []any{irFixture(irAppt1)}}, nil
}

func irGetReceipt(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	switch in["receipt_id"] {
	case "rcpt-1":
		return nil, irFixture(irReceiptClosed), nil
	case "rcpt-2":
		return nil, irFixture(irReceiptOpen), nil
	}
	return irToolError("receipt-not-found: no receipt " + fmt.Sprint(in["receipt_id"])), nil, nil
}

func irListReceipts(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	if in["state"] == "Open" {
		return nil, map[string]any{"items": []any{irFixture(irReceiptOpen)}}, nil
	}
	return nil, map[string]any{"items": []any{irFixture(irReceiptClosed), irFixture(irReceiptOpen)}}, nil
}

func irListDocks(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return nil, map[string]any{"mode": "kafka", "items": []any{
		map[string]any{"door_code": "D1", "dock_flow": "Inbound"},
		map[string]any{"door_code": "D2", "dock_flow": "Both"},
	}}, nil
}

func newInboundReceivingTestUpstream(t *testing.T) *inboundReceivingUpstream {
	t.Helper()
	golden := loadInboundReceivingGolden(t)
	up := &inboundReceivingUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "inbound-receiving-test", Version: "0"}, nil)
	handlers := map[string]mcp.ToolHandlerFor[map[string]any, any]{
		"get_asn":           irGetAsn,
		"list_asns":         irListAsns,
		"get_appointment":   irGetAppointment,
		"list_appointments": irListAppointments,
		"get_receipt":       irGetReceipt,
		"list_receipts":     irListReceipts,
		"list_docks":        irListDocks,
	}
	for name, h := range handlers {
		g, h := golden[name], h
		mcp.AddTool(server, &mcp.Tool{Name: name, InputSchema: g.InputSchema, OutputSchema: g.OutputSchema},
			func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				up.record(g.Name, in)
				return h(ctx, req, in)
			})
	}
	// A write tool inbound-receiving does NOT publish; the client must never
	// reach anything like it.
	mcp.AddTool(server, &mcp.Tool{Name: "close_receipt"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		t.Error("write tool close_receipt was invoked")
		return nil, map[string]any{}, nil
	})
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

// --- behaviour over the wire --------------------------------------------------

func TestInboundReceiving_GetAsn_DecodesLinesAndOptionalArrival(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})

	a, err := c.GetAsn(context.Background(), "ASN-1001")
	if err != nil {
		t.Fatalf("GetAsn: %v", err)
	}
	if a.AsnNumber != "ASN-1001" || a.SupplierRef != "PO-77" || a.State != "Registered" || a.Version != 2 ||
		a.ExpectedArrival != "2026-10-08T14:00:00Z" || len(a.Lines) != 2 || a.Lines[1].Sku != "SKU-2" || a.Lines[1].ExpectedQty != 4 {
		t.Fatalf("unexpected ASN: %+v", a)
	}
	if up.args()["asn_number"] != "ASN-1001" {
		t.Fatalf("asn_number argument not sent: %v", up.args())
	}

	none, err := c.GetAsn(context.Background(), "ASN-1002")
	if err != nil {
		t.Fatalf("GetAsn (no arrival): %v", err)
	}
	if none.ExpectedArrival != "" {
		t.Fatalf("an ASN with no expected arrival must decode as empty, got %q", none.ExpectedArrival)
	}
}

func TestInboundReceiving_GetAsn_ErrorSlugs(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})

	_, err := c.GetAsn(context.Background(), "ASN-404")
	var te *ToolError
	if !errors.As(err, &te) || te.Slug != "asn-not-found" {
		t.Fatalf("want an asn-not-found ToolError, got %v", err)
	}
	if errors.Is(err, ports.ErrUpstreamInvalidInput) || !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatal("not-found is an upstream answer, not an input rejection")
	}

	_, err = c.GetAsn(context.Background(), "bad")
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatalf("invalid-asn-number must classify as an input rejection, got %v", err)
	}
}

func TestInboundReceiving_ListAsns_OmitsUnsetArgumentsAndFollowsCursor(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})

	first, err := c.ListAsns(context.Background(), ports.InboundAsnListQuery{})
	if err != nil {
		t.Fatalf("ListAsns: %v", err)
	}
	if len(up.args()) != 0 {
		t.Fatalf("a zero query must send no argument at all (inbound-receiving defaults apply), sent %v", up.args())
	}
	if len(first.Items) != 1 || first.Items[0].AsnNumber != "ASN-1001" || first.NextCursor != "c2" {
		t.Fatalf("unexpected first page: %+v", first)
	}

	second, err := c.ListAsns(context.Background(), ports.InboundAsnListQuery{Cursor: first.NextCursor, State: "Registered", Limit: 500})
	if err != nil {
		t.Fatalf("ListAsns (page 2): %v", err)
	}
	got := up.args()
	if got["cursor"] != "c2" || got["state"] != "Registered" || got["limit"] != float64(500) {
		t.Fatalf("filters not sent under inbound-receiving's names: %v", got)
	}
	if len(second.Items) != 1 || second.Items[0].AsnNumber != "ASN-1002" || second.NextCursor != "" {
		t.Fatalf("unexpected last page: %+v", second)
	}
}

func TestInboundReceiving_ListAsns_InvalidQueryIsInvalidInput(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	_, err := NewInboundReceiving(Config{Endpoint: up.URL}).ListAsns(context.Background(), ports.InboundAsnListQuery{Limit: 501})
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) || !strings.Contains(err.Error(), "invalid-query") {
		t.Fatalf("invalid-query must classify as an input rejection, got %v", err)
	}
}

func TestInboundReceiving_GetAppointment(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})

	a, err := c.GetAppointment(context.Background(), "appt-1")
	if err != nil {
		t.Fatalf("GetAppointment: %v", err)
	}
	if a.AppointmentID != "appt-1" || a.DoorCode != "D1" || a.Carrier != "ACME" || a.State != "Booked" ||
		a.WindowStart != "2026-10-08T14:00:00Z" || a.WindowEnd != "2026-10-08T15:00:00Z" || strings.Join(a.AsnNumbers, ",") != "ASN-1001" {
		t.Fatalf("unexpected appointment: %+v", a)
	}

	_, err = c.GetAppointment(context.Background(), "appt-404")
	var te *ToolError
	if !errors.As(err, &te) || te.Slug != "appointment-not-found" {
		t.Fatalf("want appointment-not-found, got %v", err)
	}
}

func TestInboundReceiving_ListAppointments_SendsEveryFilterUnderItsPublishedName(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})
	from := time.Date(2026, 10, 8, 12, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	to := from.Add(24 * time.Hour)

	page, err := c.ListAppointments(context.Background(), ports.InboundAppointmentListQuery{
		Limit: 50, Cursor: "c1", Door: "D1", State: "Booked", From: from, To: to,
	})
	if err != nil {
		t.Fatalf("ListAppointments: %v (a key outside the published input schema is rejected by the stand-in)", err)
	}
	got := up.args()
	if got["limit"] != float64(50) || got["cursor"] != "c1" || got["door"] != "D1" || got["state"] != "Booked" ||
		got["from"] != "2026-10-08T15:00:00Z" || got["to"] != "2026-10-09T15:00:00Z" {
		t.Fatalf("filters not sent under inbound-receiving's names (instants as UTC RFC 3339): %v", got)
	}
	if len(page.Items) != 1 || page.Items[0].AppointmentID != "appt-1" {
		t.Fatalf("unexpected page: %+v", page)
	}

	if _, err := c.ListAppointments(context.Background(), ports.InboundAppointmentListQuery{}); err != nil {
		t.Fatalf("ListAppointments (zero query): %v", err)
	}
	if len(up.args()) != 0 {
		t.Fatalf("a zero query (zero times included) must send no argument at all, sent %v", up.args())
	}
}

func TestInboundReceiving_GetReceipt_DecodesDiscrepancies(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})

	closed, err := c.GetReceipt(context.Background(), "rcpt-1")
	if err != nil {
		t.Fatalf("GetReceipt: %v", err)
	}
	if closed.State != "Closed" || closed.ClosedAt != "2026-10-08T10:00:00Z" || closed.AppointmentID != "appt-1" || closed.DoorCode != "D1" {
		t.Fatalf("unexpected closed receipt: %+v", closed)
	}
	if len(closed.Lines) != 1 || closed.Lines[0].ReceivedGood != 8 || closed.Lines[0].ReceivedDamaged != 1 {
		t.Fatalf("unexpected closed receipt lines: %+v", closed.Lines)
	}
	if len(closed.Discrepancies) != 1 || closed.Discrepancies[0].Kind != "Short" || closed.Discrepancies[0].DamagedQty != 1 {
		t.Fatalf("unexpected closed receipt discrepancies: %+v", closed.Discrepancies)
	}
}

func TestInboundReceiving_GetReceipt_DecodesWalkInsAndNotFound(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})

	open, err := c.GetReceipt(context.Background(), "rcpt-2")
	if err != nil {
		t.Fatalf("GetReceipt (open walk-in): %v", err)
	}
	if open.State != "Open" || open.ClosedAt != "" || open.AppointmentID != "" || open.DoorCode != "" || len(open.Discrepancies) != 0 {
		t.Fatalf("an open walk-in receipt must decode with empty optional fields: %+v", open)
	}

	_, err = c.GetReceipt(context.Background(), "rcpt-404")
	var te *ToolError
	if !errors.As(err, &te) || te.Slug != "receipt-not-found" {
		t.Fatalf("want receipt-not-found, got %v", err)
	}
}

func TestInboundReceiving_ListReceipts_SendsFiltersUnderTheirPublishedNames(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})

	page, err := c.ListReceipts(context.Background(), ports.InboundReceiptListQuery{Limit: 500, Cursor: "c1", AsnNumber: "ASN-1002", State: "Open"})
	if err != nil {
		t.Fatalf("ListReceipts: %v", err)
	}
	got := up.args()
	if got["limit"] != float64(500) || got["cursor"] != "c1" || got["asn_number"] != "ASN-1002" || got["state"] != "Open" {
		t.Fatalf("filters not sent under inbound-receiving's names: %v", got)
	}
	if len(page.Items) != 1 || page.Items[0].ReceiptID != "rcpt-2" {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestInboundReceiving_ListDocks(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	docks, err := NewInboundReceiving(Config{Endpoint: up.URL}).ListDocks(context.Background())
	if err != nil {
		t.Fatalf("ListDocks: %v", err)
	}
	if docks.Mode != "kafka" || len(docks.Items) != 2 || docks.Items[1].DoorCode != "D2" || docks.Items[1].DockFlow != "Both" {
		t.Fatalf("unexpected docks: %+v", docks)
	}
}

// TestInboundReceiving_StandInRejectsUnpublishedArgumentKeys proves the
// stand-in can fail: a camelCase key that inbound-receiving does not publish
// is rejected by its input schema, so the argument-name assertions above are
// real.
func TestInboundReceiving_StandInRejectsUnpublishedArgumentKeys(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	err := New(Config{Name: "inbound-receiving", Endpoint: up.URL}).callTool(context.Background(), "get_asn", map[string]any{"asnNumber": "ASN-1001"}, nil)
	if err == nil {
		t.Fatal("an argument key outside inbound-receiving's published input schema must be rejected")
	}
}

func TestInboundReceiving_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	c := NewInboundReceiving(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetAppointment(ctx, "appt-1")
	_, _ = c.GetAsn(ctx, "ASN-1001")
	_, _ = c.GetReceipt(ctx, "rcpt-1")
	_, _ = c.ListAppointments(ctx, ports.InboundAppointmentListQuery{})
	_, _ = c.ListAsns(ctx, ports.InboundAsnListQuery{})
	_, _ = c.ListDocks(ctx)
	_, _ = c.ListReceipts(ctx, ports.InboundReceiptListQuery{})

	up.mu.Lock()
	defer up.mu.Unlock()
	if strings.Join(up.calls, ",") != strings.Join(wantInboundReceivingTools, ",") {
		t.Fatalf("tools called = %v, want exactly %v", up.calls, wantInboundReceivingTools)
	}
}

func TestInboundReceiving_UnreachableUpstream(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.

	c := NewInboundReceiving(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetAsn(ctx, "ASN-1001"); err == nil {
		t.Error("GetAsn: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListAsns(ctx, ports.InboundAsnListQuery{}); err == nil {
		t.Error("ListAsns: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetAppointment(ctx, "appt-1"); err == nil {
		t.Error("GetAppointment: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListAppointments(ctx, ports.InboundAppointmentListQuery{}); err == nil {
		t.Error("ListAppointments: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetReceipt(ctx, "rcpt-1"); err == nil {
		t.Error("GetReceipt: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListReceipts(ctx, ports.InboundReceiptListQuery{}); err == nil {
		t.Error("ListReceipts: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListDocks(ctx); err == nil {
		t.Error("ListDocks: an unreachable upstream must surface as an error")
	}
}

func TestInboundReceiving_RespectsCallerContext(t *testing.T) {
	up := newInboundReceivingTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewInboundReceiving(Config{Endpoint: up.URL}).GetAsn(ctx, "ASN-1001")
	if err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
}
