package mcp_test

import (
	"context"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/authority"
)

// fullDeps wires every optional use case (zero values: registration never
// runs them) so tools/list advertises the complete tool set.
func fullDeps(t *testing.T) inboundmcp.Deps {
	t.Helper()
	outlook, err := usecases.NewLaborReallocationOutlook(&fakeSiteStaffing{})
	if err != nil {
		t.Fatal(err)
	}
	return inboundmcp.Deps{
		DailyBrief:               &usecases.DailyBrief{},
		FlowBalanceAdvisory:      &usecases.FlowBalanceAdvisory{},
		ExplainTravelFactor:      &usecases.ExplainTravelFactor{},
		StrandedReservation:      &usecases.DetectStrandedReservation{},
		MasterDataGaps:           &usecases.MasterDataGaps{},
		InboundOutlook:           &usecases.InboundOutlook{},
		TransferWatch:            &usecases.TransferWatch{},
		NetworkOrderOutlook:      &usecases.NetworkOrderOutlook{},
		NetworkNodeOutlook:       &usecases.NetworkNodeOutlook{},
		LaborReallocationOutlook: outlook,
	}
}

func listAllTools(t *testing.T, deps inboundmcp.Deps) []*sdk.Tool {
	t.Helper()
	srv := httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(deps)))
	t.Cleanup(srv.Close)
	res, err := connect(t, srv.URL).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	return res.Tools
}

// Every tool, existing and new, declares exactly one valid authority level
// under the com.warehouse/authority Meta key (contracts G1-G3): reads are
// L1, the labor_reallocation_outlook recommendation is L2, and nothing here
// is a write.
func TestEveryToolDeclaresExactlyOneAuthorityLevel(t *testing.T) {
	tools := listAllTools(t, fullDeps(t))
	if len(tools) != 13 {
		t.Fatalf("tools = %d, want all 13 registered (update the table deliberately)", len(tools))
	}
	got := map[string]string{}
	for _, tool := range tools {
		got[tool.Name] = declaredLevel(t, tool)
	}
	for name, level := range got {
		want := authority.L1
		if name == "labor_reallocation_outlook" {
			want = authority.L2
		}
		if level != want {
			t.Errorf("%s = %s, want %s", name, level, want)
		}
	}
	if got["labor_reallocation_outlook"] != authority.L2 {
		t.Errorf("labor_reallocation_outlook = %q, want L2", got["labor_reallocation_outlook"])
	}
}

// declaredLevel checks one tool's Meta and annotations and returns its level.
func declaredLevel(t *testing.T, tool *sdk.Tool) string {
	t.Helper()
	raw, ok := tool.Meta[authority.MetaKey]
	if !ok {
		t.Errorf("%s carries no %s in Meta", tool.Name, authority.MetaKey)
		return ""
	}
	level, ok := raw.(string)
	if !ok || !authority.Valid(level) {
		t.Errorf("%s declares authority %v, want exactly one of L1..L4", tool.Name, raw)
	}
	if len(tool.Meta) != 1 {
		t.Errorf("%s Meta = %v, want only the authority entry", tool.Name, tool.Meta)
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Errorf("%s is not annotated read-only: this agent has no write tool", tool.Name)
	}
	if level != authority.L1 && level != authority.L2 {
		t.Errorf("%s declares %s: a read tool is L1 or L2 (G3)", tool.Name, level)
	}
	return level
}

// An undeclared tool cannot be registered: addTool fails closed at build
// time of the server, which this test (and every server test) exercises.
func TestRegisteringAnUndeclaredToolPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(error).Error(), "no declared authority level") {
			t.Fatalf("recover = %v, want a panic naming the missing authority", r)
		}
	}()
	inboundmcp.RegisterUndeclaredToolForTest(sdk.NewServer(&sdk.Implementation{Name: "t", Version: "0"}, nil))
}

// The set of declared tools equals the set of registered tools: no stale
// table entry, no missing one.
func TestAuthorityTableMatchesTheRegisteredTools(t *testing.T) {
	var registered []string
	for _, tool := range listAllTools(t, fullDeps(t)) {
		registered = append(registered, tool.Name)
	}
	declared := inboundmcp.DeclaredTools()
	sort.Strings(registered)
	sort.Strings(declared)
	if strings.Join(registered, ",") != strings.Join(declared, ",") {
		t.Fatalf("registered %v != declared %v", registered, declared)
	}
}

// Spans keep their name and gain the G9 attributes (in-memory recorder).
func TestToolSpanCarriesTheAgenticAttributes(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	deps := inboundmcp.Deps{DailyBrief: &usecases.DailyBrief{Now: func() time.Time { return time.Unix(0, 0) }}}
	srv := httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(deps)))
	t.Cleanup(srv.Close)
	if _, err := connect(t, srv.URL).CallTool(context.Background(), &sdk.CallToolParams{Name: "get_daily_brief", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range rec.Ended() {
		if s.Name() != "mcp.tool get_daily_brief" {
			continue
		}
		found = true
		attrs := map[string]string{}
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.String()
		}
		for k, want := range map[string]string{
			"mcp.tool.name":             "get_daily_brief",
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
		t.Fatal("no span named \"mcp.tool get_daily_brief\" was recorded")
	}
}
