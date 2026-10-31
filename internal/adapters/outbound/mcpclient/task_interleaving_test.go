package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// taskInterleavingGoldenPath is a VERBATIM copy of task-interleaving's
// own published tool registry golden
// (task-interleaving: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at task-interleaving origin/develop df901f1). Refresh it with
//
//	git -C ../task-interleaving show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever task-interleaving amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const taskInterleavingGoldenPath = "testdata/task_interleaving_tools.golden.json"

// wantTaskInterleavingTools is every tool task-interleaving publishes
// (four, the verbatim golden); the client calls all four.
var wantTaskInterleavingTools = []string{"get_hint", "get_site_policy", "get_station", "list_hints"}

// --- the published contract ------------------------------------------------

func TestTaskInterleaving_GoldenPinsExactlyTheFourReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, taskInterleavingGoldenPath), "task-interleaving", wantTaskInterleavingTools)
}

// TestTaskInterleaving_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema.
func TestTaskInterleaving_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, taskInterleavingGoldenPath)
	cases := map[string]reflect.Type{
		"get_hint":        reflect.TypeFor[ports.InterleavingHint](),
		"get_site_policy": reflect.TypeFor[ports.InterleavingPolicy](),
		"get_station":     reflect.TypeFor[ports.StationState](),
		"list_hints":      reflect.TypeFor[ports.HintPage](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful task-interleaving stand-in -----------------------------

const hintFixture = `{"hintId":"hint-1","stationId":"STN-07","siteId":"SITE-1","state":"Active",
  "afterTaskId":"task-9","originLocation":"A-01-02","issuedAt":"2026-10-24T10:00:00Z",
  "expiresAt":"2026-10-24T10:05:00Z","policyVersion":2,"version":1,
  "candidates":[{"rank":1,"taskId":"task-10","taskType":"PUT","location":"B-03-01","detourM":12.5},
                {"rank":2,"taskId":"task-11","taskType":"COUNT","location":"C-01-07","detourM":22.0}]}`

func newTaskInterleavingTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, taskInterleavingGoldenPath)
	return newGoldenUpstream(t, "task-interleaving-test", golden, map[string]goldenFixture{
		"get_hint": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["hintId"].(string); id != "hint-1" {
				return nil, goldenToolError("hint-not-found: no hint " + id)
			}
			return goldenJSON(t, hintFixture), nil
		},
		"get_site_policy": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["siteId"].(string); id != "SITE-1" {
				return nil, goldenToolError("policy-not-found: no policy declared for " + id)
			}
			return goldenJSON(t, `{"siteId":"SITE-1","maxDetourM":30.0,"maxCandidates":3,"hintTtlSeconds":300,
			  "eligibleTaskTypes":["PUT","COUNT"],"verticalWeight":0.2,"version":2,"declaredAt":"2026-10-01T00:00:00Z"}`), nil
		},
		"get_station": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["stationId"].(string); id != "STN-07" {
				return nil, goldenToolError("station-not-found: no station " + id)
			}
			return goldenJSON(t, `{"stationId":"STN-07","siteId":"SITE-1",
			  "position":{"location":"ST-07","point":{"xM":1.0,"yM":2.0,"zM":0.5}},
			  "lastCompletedTaskId":"task-9","lastCompletedAt":"2026-10-24T09:59:00Z","version":3,
			  "activeHint":{"hintId":"hint-1","state":"Active","issuedAt":"2026-10-24T10:00:00Z",
			    "expiresAt":"2026-10-24T10:05:00Z","policyVersion":2,
			    "candidates":[{"rank":1,"taskId":"task-10","taskType":"PUT","location":"B-03-01","detourM":12.5}]}}`), nil
		},
		"list_hints": func(args map[string]any) (any, *mcp.CallToolResult) {
			if len(args) > 5 {
				return nil, goldenToolError("invalid-hint-filter: only cursor, limit, siteId, state and stationId are published")
			}
			return goldenJSON(t, `{"items":[`+hintFixture+`],"nextCursor":"hint-2"}`), nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestTaskInterleaving_GetHint_DecodesCandidates(t *testing.T) {
	up := newTaskInterleavingTestUpstream(t)
	c := NewTaskInterleaving(Config{Endpoint: up.URL})

	out, err := c.GetHint(context.Background(), "hint-1")
	if err != nil {
		t.Fatalf("GetHint: %v", err)
	}
	if out.HintID != "hint-1" || out.StationID != "STN-07" || out.State != "Active" || out.AfterTaskID != "task-9" ||
		out.PolicyVersion != 2 || out.ConsumedAt != "" {
		t.Fatalf("unexpected hint: %+v", out)
	}
	if len(out.Candidates) != 2 || out.Candidates[0].Rank != 1 || out.Candidates[0].DetourM != 12.5 ||
		out.Candidates[1].TaskType != "COUNT" {
		t.Fatalf("unexpected candidates: %+v", out.Candidates)
	}
	if got := up.lastArgs(); got["hintId"] != "hint-1" || len(got) != 1 {
		t.Fatalf("the hint id must be sent under its published name and nothing else, sent %v", got)
	}
}

func TestTaskInterleaving_GetSitePolicy_Decodes(t *testing.T) {
	up := newTaskInterleavingTestUpstream(t)
	c := NewTaskInterleaving(Config{Endpoint: up.URL})

	out, err := c.GetSitePolicy(context.Background(), "SITE-1")
	if err != nil {
		t.Fatalf("GetSitePolicy: %v", err)
	}
	if out.MaxDetourM != 30.0 || out.MaxCandidates != 3 || out.HintTtlSeconds != 300 || out.VerticalWeight != 0.2 ||
		len(out.EligibleTaskTypes) != 2 || out.Version != 2 {
		t.Fatalf("unexpected policy: %+v", out)
	}
}

func TestTaskInterleaving_GetStation_DecodesPositionAndActiveHint(t *testing.T) {
	up := newTaskInterleavingTestUpstream(t)
	c := NewTaskInterleaving(Config{Endpoint: up.URL})

	out, err := c.GetStation(context.Background(), "STN-07")
	if err != nil {
		t.Fatalf("GetStation: %v", err)
	}
	if out.Position.Location != "ST-07" || out.Position.Point == nil || out.Position.Point.ZM != 0.5 {
		t.Fatalf("unexpected position: %+v", out.Position)
	}
	if out.LastCompletedTaskID != "task-9" || out.Version != 3 {
		t.Fatalf("unexpected station: %+v", out)
	}
	if out.ActiveHint == nil || out.ActiveHint.HintID != "hint-1" || len(out.ActiveHint.Candidates) != 1 {
		t.Fatalf("unexpected active hint: %+v", out.ActiveHint)
	}
}

// TestTaskInterleaving_ListHints_OmitsZeroQueryFields pins the query
// contract: an empty query sends NO argument keys.
func TestTaskInterleaving_ListHints_OmitsZeroQueryFields(t *testing.T) {
	up := newTaskInterleavingTestUpstream(t)
	c := NewTaskInterleaving(Config{Endpoint: up.URL})
	ctx := context.Background()

	out, err := c.ListHints(ctx, ports.HintListQuery{})
	if err != nil {
		t.Fatalf("ListHints (empty query): %v", err)
	}
	if got := up.lastArgs(); len(got) != 0 {
		t.Fatalf("an empty query must send no argument keys so task-interleaving applies its defaults, sent %v", got)
	}
	if len(out.Items) != 1 || out.NextCursor != "hint-2" {
		t.Fatalf("unexpected hint page: %+v", out)
	}
	if _, err := c.ListHints(ctx, ports.HintListQuery{StationID: "STN-07", SiteID: "SITE-1", State: "Active", Cursor: "hint-1", Limit: 50}); err != nil {
		t.Fatalf("ListHints (full query): %v", err)
	}
	got := up.lastArgs()
	if got["stationId"] != "STN-07" || got["siteId"] != "SITE-1" || got["state"] != "Active" ||
		got["cursor"] != "hint-1" || got["limit"] != float64(50) || len(got) != 5 {
		t.Fatalf("a set query must send exactly its five published names, sent %v", got)
	}
}

func TestTaskInterleaving_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newTaskInterleavingTestUpstream(t)
	c := NewTaskInterleaving(Config{Endpoint: up.URL})
	ctx := context.Background()

	if _, err := c.GetHint(ctx, "hint-none"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("hint-not-found must classify as upstream-not-found, got %v", err)
	}
	if _, err := c.GetSitePolicy(ctx, "SITE-none"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("policy-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestTaskInterleaving_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newTaskInterleavingTestUpstream(t)
	c := NewTaskInterleaving(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetHint(ctx, "hint-1")
	_, _ = c.GetSitePolicy(ctx, "SITE-1")
	_, _ = c.GetStation(ctx, "STN-07")
	_, _ = c.ListHints(ctx, ports.HintListQuery{})

	assertOnlyPinnedToolsCalled(t, up, wantTaskInterleavingTools)
}

func TestTaskInterleaving_UnreachableUpstream(t *testing.T) {
	up := newTaskInterleavingTestUpstream(t)
	up.Close()
	c := NewTaskInterleaving(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetHint(ctx, "hint-1"); err == nil {
		t.Error("GetHint: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetStation(ctx, "STN-07"); err == nil {
		t.Error("GetStation: an unreachable upstream must surface as an error")
	}
}

func TestTaskInterleaving_RespectsCallerContext(t *testing.T) {
	up := newTaskInterleavingTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewTaskInterleaving(Config{Endpoint: up.URL}).GetHint(ctx, "hint-1"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
