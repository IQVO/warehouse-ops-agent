package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/authority"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// workforceManagementGoldenPath is workforce-management's published tool
// registry, copied VERBATIM from its origin/develop
// internal/adapters/inbound/mcp/testdata/tool_registry.golden (tab-separated:
// name, readOnlyHint, destructiveHint). That upstream pins a TSV registry, not
// the JSON-with-schemas form most clients here pin, so the INPUT schema below
// is generated from a hand-mirrored copy of its get_staffing_gap input struct
// (tools.go staffingGapInput); refresh both when the upstream amends the tool.
const workforceManagementGoldenPath = "testdata/workforce_management_tools.golden"

// staffingGapInput mirrors the upstream's get_staffing_gap input struct field
// for field (names, tags, optionality). The go-sdk generates the same strict
// schema (additionalProperties:false) from it that the upstream serves.
type staffingGapInput struct {
	SiteCode   string `json:"siteCode,omitempty" jsonschema:"the canonical site code"`
	BuildingId string `json:"buildingId,omitempty" jsonschema:"DEPRECATED, use siteCode"`
	ShiftId    string `json:"shiftId" jsonschema:"the shift whose committed plan to read the gap from"`
	PathId     string `json:"pathId" jsonschema:"the process path to measure planned-vs-active heads for"`
}

// staffingGapOutput mirrors the upstream's staffingGap DTO (mapping.go).
type staffingGapOutput struct {
	BuildingId   string `json:"buildingId"`
	ShiftId      string `json:"shiftId"`
	PathId       string `json:"pathId"`
	PlannedHeads int    `json:"plannedHeads"`
	ActiveHeads  int    `json:"activeHeads"`
	Understaffed bool   `json:"understaffed"`
	SiteCode     string `json:"siteCode,omitempty"`
}

type workforceUpstream struct {
	*httptest.Server
	mu    sync.Mutex
	calls []map[string]any
	write int
}

func (u *workforceUpstream) seen() []map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]map[string]any(nil), u.calls...)
}

// newWorkforceUpstream serves get_staffing_gap with the upstream's strict
// input schema, plus the two other published tools the client must never call
// (they fail the test if invoked).
func newWorkforceUpstream(t *testing.T) *workforceUpstream {
	t.Helper()
	up := &workforceUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "workforce-management-test", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "get_staffing_gap", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in staffingGapInput) (*mcp.CallToolResult, staffingGapOutput, error) {
			up.mu.Lock()
			up.calls = append(up.calls, map[string]any{"siteCode": in.SiteCode, "buildingId": in.BuildingId, "shiftId": in.ShiftId, "pathId": in.PathId})
			up.mu.Unlock()
			if in.PathId == "nope" {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "validation-failed: unknown pathId"}}}, staffingGapOutput{}, nil
			}
			return nil, staffingGapOutput{BuildingId: in.SiteCode, ShiftId: in.ShiftId, PathId: in.PathId, PlannedHeads: 10, ActiveHeads: 7, Understaffed: true, SiteCode: in.SiteCode}, nil
		})
	for _, name := range []string{"assign_labor", "propose_path_heads"} {
		mcp.AddTool(server, &mcp.Tool{Name: name}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			up.mu.Lock()
			up.write++
			up.mu.Unlock()
			return nil, map[string]any{}, nil
		})
	}
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

func TestWorkforceManagement_GoldenPinsGetStaffingGapAsReadOnly(t *testing.T) {
	raw, err := os.ReadFile(workforceManagementGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			t.Fatalf("golden row %q: want name, readOnlyHint, destructiveHint", line)
		}
		rows[f[0]] = f[1:]
	}
	if got := rows["get_staffing_gap"]; len(got) != 2 || got[0] != "true" || got[1] != "false" {
		t.Fatalf("get_staffing_gap = %v, want read-only and non-destructive in the upstream registry", got)
	}
	if got := rows["assign_labor"]; len(got) != 2 || got[0] != "false" || got[1] != "true" {
		t.Fatalf("assign_labor = %v: the upstream write tool must stay destructive (and is never called over MCP here)", got)
	}
}

func TestWorkforceManagement_GetStaffingGapForSiteSendsSiteCodeOnly(t *testing.T) {
	up := newWorkforceUpstream(t)
	c := NewWorkforceManagement(Config{Endpoint: up.URL, Timeout: 5 * time.Second})
	got, err := c.GetStaffingGapForSite(context.Background(), "WH1", "S1", "pack")
	if err != nil {
		t.Fatalf("GetStaffingGapForSite: %v", err)
	}
	wantEq(t, "plannedHeads", got.PlannedHeads, 10)
	wantEq(t, "activeHeads", got.ActiveHeads, 7)
	wantEq(t, "understaffed", got.Understaffed, true)
	wantEq(t, "siteCode", got.SiteCode, "WH1")
	calls := up.seen()
	if len(calls) != 1 {
		t.Fatalf("calls = %v", calls)
	}
	wantEq(t, "siteCode sent", calls[0]["siteCode"], any("WH1"))
	wantEq(t, "buildingId sent (the deprecated alias must stay unused)", calls[0]["buildingId"], any(""))
	wantEq(t, "shiftId sent", calls[0]["shiftId"], any("S1"))
	wantEq(t, "pathId sent", calls[0]["pathId"], any("pack"))
}

func TestWorkforceManagement_LegacyBuildingIdReadIsUnchanged(t *testing.T) {
	up := newWorkforceUpstream(t)
	c := NewWorkforceManagement(Config{Endpoint: up.URL, Timeout: 5 * time.Second})
	if _, err := c.GetStaffingGap(context.Background(), "B1", "S1", "pack"); err != nil {
		t.Fatal(err)
	}
	calls := up.seen()
	wantEq(t, "buildingId sent", calls[0]["buildingId"], any("B1"))
	wantEq(t, "siteCode sent", calls[0]["siteCode"], any(""))
}

func TestWorkforceManagement_NeverCallsAWriteOrSizingTool(t *testing.T) {
	up := newWorkforceUpstream(t)
	c := NewWorkforceManagement(Config{Endpoint: up.URL, Timeout: 5 * time.Second})
	_, _ = c.GetStaffingGapForSite(context.Background(), "WH1", "S1", "pack")
	_, _ = c.GetStaffingGap(context.Background(), "B1", "S1", "pack")
	if up.write != 0 {
		t.Fatalf("the client invoked %d tool(s) other than get_staffing_gap", up.write)
	}
}

func TestWorkforceManagement_UpstreamValidationRejectionIsClassified(t *testing.T) {
	up := newWorkforceUpstream(t)
	c := NewWorkforceManagement(Config{Endpoint: up.URL, Timeout: 5 * time.Second})
	_, err := c.GetStaffingGapForSite(context.Background(), "WH1", "S1", "nope")
	if err == nil || !strings.Contains(err.Error(), "validation-failed") {
		t.Fatalf("err = %v, want the upstream validation text", err)
	}
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatalf("err = %v, want it classified as ports.ErrUpstreamInvalidInput", err)
	}
}

// Every upstream MCP call runs in an "mcp.client <tool>" span carrying the
// agentic-standard attributes: execute_tool, this agent's name, authority L1
// (in-memory span recorder).
func TestSession_CallToolSpanCarriesTheAgenticAttributes(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	up := newWorkforceUpstream(t)
	c := NewWorkforceManagement(Config{Endpoint: up.URL, Timeout: 5 * time.Second})
	if _, err := c.GetStaffingGapForSite(context.Background(), "WH1", "S1", "pack"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range rec.Ended() {
		if s.Name() != "mcp.client get_staffing_gap" {
			continue
		}
		found = true
		attrs := map[string]string{}
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.String()
		}
		for k, want := range map[string]string{
			"mcp.tool.name":             "get_staffing_gap",
			"mcp.upstream":              "workforce-management",
			authority.AttrOperationName: authority.OpExecuteTool,
			authority.AttrAgentName:     "warehouse-ops-agent",
			authority.AttrAuthority:     authority.L1,
		} {
			if attrs[k] != want {
				t.Errorf("span attribute %s = %q, want %q (all: %v)", k, attrs[k], want, attrs)
			}
		}
	}
	if !found {
		t.Fatal("no span named \"mcp.client get_staffing_gap\" was recorded")
	}
}
