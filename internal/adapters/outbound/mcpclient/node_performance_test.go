package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// nodePerformanceGoldenPath is a VERBATIM copy of node-performance's
// own published tool registry golden
// (node-performance: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at node-performance origin/develop b7e5878). Refresh it with
//
//	git -C ../node-performance show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever node-performance amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const nodePerformanceGoldenPath = "testdata/node_performance_tools.golden.json"

// wantNodePerformanceTools is every tool node-performance publishes
// (two, the verbatim golden); the client calls both.
var wantNodePerformanceTools = []string{"get_scorecard", "list_scorecards_by_grade"}

// --- the published contract ------------------------------------------------

func TestNodePerformance_GoldenPinsExactlyTheTwoReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, nodePerformanceGoldenPath), "node-performance", wantNodePerformanceTools)
}

// TestNodePerformance_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema.
func TestNodePerformance_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, nodePerformanceGoldenPath)
	cases := map[string]reflect.Type{
		"get_scorecard":            reflect.TypeFor[ports.NodeScorecards](),
		"list_scorecards_by_grade": reflect.TypeFor[ports.ScorecardGradePage](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful node-performance stand-in ------------------------------

const nodeScorecardFixture = `{"scorecardId":"sc-1","nodeId":"node-1","window":"7d","state":"GRADED","grade":"B",
  "metrics":{"onTimeHandoverRate":0.95,"lateAckRate":0.05,"rejectRate":0.01,"expiryRate":0.02,
  "promiseKeptRate":0.9,"feedFreshnessP95Minutes":12,"observations":500},"computedAt":"2026-10-24T00:00:00Z"}`

func newNodePerformanceTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, nodePerformanceGoldenPath)
	return newGoldenUpstream(t, "node-performance-test", golden, map[string]goldenFixture{
		"get_scorecard": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["nodeId"].(string); id != "node-1" {
				return nil, goldenToolError("node-not-found: no scorecard for " + id)
			}
			return goldenJSON(t, `{"nodeId":"node-1","scorecards":[`+nodeScorecardFixture+`]}`), nil
		},
		"list_scorecards_by_grade": func(args map[string]any) (any, *mcp.CallToolResult) {
			if grade, _ := args["grade"].(string); grade != "B" {
				return nil, goldenToolError("invalid-grade: grade must be one of A, B, C, D or PROVISIONAL")
			}
			return goldenJSON(t, `{"items":[`+nodeScorecardFixture+`],"nextCursor":"sc-2"}`), nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestNodePerformance_GetScorecard_DecodesBothWindows(t *testing.T) {
	up := newNodePerformanceTestUpstream(t)
	c := NewNodePerformance(Config{Endpoint: up.URL})

	out, err := c.GetScorecard(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("GetScorecard: %v", err)
	}
	if out.NodeID != "node-1" || len(out.Scorecards) != 1 {
		t.Fatalf("unexpected scorecards: %+v", out)
	}
	sc := out.Scorecards[0]
	if sc.Window != "7d" || sc.State != "GRADED" || sc.Grade != "B" || sc.ComputedAt != "2026-10-24T00:00:00Z" {
		t.Fatalf("unexpected scorecard: %+v", sc)
	}
	if sc.Metrics.OnTimeHandoverRate != 0.95 || sc.Metrics.Observations != 500 || sc.Metrics.FeedFreshnessP95Minutes != 12 {
		t.Fatalf("unexpected metrics: %+v", sc.Metrics)
	}
	if got := up.lastArgs(); got["nodeId"] != "node-1" || len(got) != 1 {
		t.Fatalf("the node id must be sent under its published name and nothing else, sent %v", got)
	}
}

// TestNodePerformance_ListScorecardsByGrade_SendsOnlySetQueryFields pins
// the query contract: grade is always sent, the optional filters only
// when set.
func TestNodePerformance_ListScorecardsByGrade_SendsOnlySetQueryFields(t *testing.T) {
	up := newNodePerformanceTestUpstream(t)
	c := NewNodePerformance(Config{Endpoint: up.URL})
	ctx := context.Background()

	out, err := c.ListScorecardsByGrade(ctx, ports.ScorecardGradeQuery{Grade: "B"})
	if err != nil {
		t.Fatalf("ListScorecardsByGrade (bare): %v", err)
	}
	if got := up.lastArgs(); got["grade"] != "B" || len(got) != 1 {
		t.Fatalf("a bare query must send only the grade, sent %v", got)
	}
	if len(out.Items) != 1 || out.NextCursor != "sc-2" {
		t.Fatalf("unexpected page: %+v", out)
	}

	if _, err := c.ListScorecardsByGrade(ctx, ports.ScorecardGradeQuery{Grade: "B", Window: "30d", After: "sc-1", Limit: 50}); err != nil {
		t.Fatalf("ListScorecardsByGrade (full): %v", err)
	}
	got := up.lastArgs()
	if got["grade"] != "B" || got["window"] != "30d" || got["after"] != "sc-1" || got["limit"] != float64(50) || len(got) != 4 {
		t.Fatalf("a set query must send exactly its four published names, sent %v", got)
	}
}

func TestNodePerformance_InvalidGradeClassifiesAsInvalidInput(t *testing.T) {
	up := newNodePerformanceTestUpstream(t)
	c := NewNodePerformance(Config{Endpoint: up.URL})

	_, err := c.ListScorecardsByGrade(context.Background(), ports.ScorecardGradeQuery{Grade: "E"})
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatalf("invalid-grade must classify as an input rejection, got %v", err)
	}
}

func TestNodePerformance_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newNodePerformanceTestUpstream(t)
	c := NewNodePerformance(Config{Endpoint: up.URL})

	_, err := c.GetScorecard(context.Background(), "node-none")
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("node-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestNodePerformance_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newNodePerformanceTestUpstream(t)
	c := NewNodePerformance(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetScorecard(ctx, "node-1")
	_, _ = c.ListScorecardsByGrade(ctx, ports.ScorecardGradeQuery{Grade: "B"})

	assertOnlyPinnedToolsCalled(t, up, wantNodePerformanceTools)
}

func TestNodePerformance_UnreachableUpstream(t *testing.T) {
	up := newNodePerformanceTestUpstream(t)
	up.Close()
	c := NewNodePerformance(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetScorecard(ctx, "node-1"); err == nil {
		t.Error("GetScorecard: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListScorecardsByGrade(ctx, ports.ScorecardGradeQuery{Grade: "B"}); err == nil {
		t.Error("ListScorecardsByGrade: an unreachable upstream must surface as an error")
	}
}

func TestNodePerformance_RespectsCallerContext(t *testing.T) {
	up := newNodePerformanceTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewNodePerformance(Config{Endpoint: up.URL}).GetScorecard(ctx, "node-1"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
