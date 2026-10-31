package mcpclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// processPathManagementGoldenPath holds this repo's JSON-convention copy of
// process-path-management's published tool registry. That upstream still
// pins its registry as an older TSV golden (name + readOnlyHint +
// destructiveHint: get_cpt_schedule, get_process_path, list_process_paths —
// see process-path-management internal/adapters/inbound/mcp/testdata/
// tool_registry.golden); this golden was converted to the JSON registry
// convention every other client here pins (names, descriptions,
// annotations, input AND output schemas, rendered with the same go-sdk
// v1.8.0 the upstream serves with). Refresh it whenever process-path-
// management amends its tool surface; the tests below then say exactly
// which consumer-side assumption broke.
const processPathManagementGoldenPath = "testdata/process_path_management_tools.golden.json"

// wantProcessPathManagementTools is every tool process-path-management
// publishes on its default surface (three, the verbatim golden); the client
// calls all three.
var wantProcessPathManagementTools = []string{"get_cpt_schedule", "get_process_path", "list_process_paths"}

// --- the published contract ------------------------------------------------

func TestProcessPathManagement_GoldenPinsExactlyTheThreeReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, processPathManagementGoldenPath), "process-path-management", wantProcessPathManagementTools)
}

// TestProcessPathManagement_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema: each json tag must be a
// published property (no invented field) and each required property must be
// mirrored (no silently dropped field).
func TestProcessPathManagement_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, processPathManagementGoldenPath)
	cases := map[string]reflect.Type{
		"get_process_path":   reflect.TypeFor[ports.ProcessPathEntry](),
		"list_process_paths": reflect.TypeFor[ports.ProcessPathList](),
		"get_cpt_schedule":   reflect.TypeFor[ports.CPTSchedule](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful process-path-management stand-in ---------------------

func newProcessPathManagementTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, processPathManagementGoldenPath)
	path := goldenJSON(t, `{"pathId":"PICK","matchPrefix":"PICK-","direct":false,
	  "requiredCapabilities":["CARTON"],"destinationLocationRole":"PACK",
	  "status":"ACTIVE","active":true,"createdAt":"2026-01-05T09:00:00Z","updatedAt":"2026-02-01T12:00:00Z",
	  "cycleTimeP95":"45m0s","eligibility":{"maxUnitsPerLine":12,"nonSortable":false},
	  "intake":["CUSTOMER_ORDER","STOCK_TRANSFER"]}`)
	paths := goldenJSON(t, `{"paths":[{"pathId":"PICK","matchPrefix":"PICK-","direct":false,
	  "requiredCapabilities":["CARTON"],"status":"ACTIVE","active":true,
	  "createdAt":"2026-01-05T09:00:00Z","updatedAt":"2026-02-01T12:00:00Z",
	  "cycleTimeP95":"45m0s","eligibility":{},"intake":[]}]}`)
	cpt := goldenJSON(t, `{"siteId":"sp1","timezone":"America/Chicago",
	  "cutoffs":[{"cptId":"cpt-eod","localTime":"17:00","daysOfWeek":["MON","TUE","WED","THU","FRI"],
	  "shipMethod":"GROUND","eligiblePathIds":["PICK","PACK"]}],
	  "createdAt":"2026-01-05T09:00:00Z","updatedAt":"2026-02-01T12:00:00Z"}`)
	return newGoldenUpstream(t, "process-path-management-test", golden, map[string]goldenFixture{
		"get_process_path": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["pathId"].(string); id != "PICK" {
				return nil, goldenToolError("usecases: process path not found")
			}
			return path, nil
		},
		"list_process_paths": goldenResult(paths),
		"get_cpt_schedule": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["siteId"].(string); id != "sp1" {
				return nil, goldenToolError("usecases: cpt schedule not found")
			}
			return cpt, nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestProcessPathManagement_GetProcessPath_DecodesTheFullPath(t *testing.T) {
	up := newProcessPathManagementTestUpstream(t)
	c := NewProcessPathManagement(Config{Endpoint: up.URL})

	entry, err := c.GetProcessPath(context.Background(), "PICK")
	if err != nil {
		t.Fatalf("GetProcessPath: %v", err)
	}
	wantEq(t, "pathId", entry.PathID, "PICK")
	wantEq(t, "matchPrefix", entry.MatchPrefix, "PICK-")
	wantEq(t, "direct", entry.Direct, false)
	wantEq(t, "len(requiredCapabilities)", len(entry.RequiredCapabilities), 1)
	wantEq(t, "requiredCapabilities[0]", entry.RequiredCapabilities[0], "CARTON")
	wantEq(t, "destinationLocationRole", entry.DestinationLocationRole, "PACK")
	wantEq(t, "status", entry.Status, "ACTIVE")
	wantEq(t, "active", entry.Active, true)
	wantEq(t, "cycleTimeP95", entry.CycleTimeP95, "45m0s")
	wantEq(t, "len(intake)", len(entry.Intake), 2)
	if entry.Eligibility.MaxUnitsPerLine == nil || *entry.Eligibility.MaxUnitsPerLine != 12 {
		t.Fatalf("eligibility.maxUnitsPerLine = %+v, want 12", entry.Eligibility.MaxUnitsPerLine)
	}
	if got := up.lastArgs(); got["pathId"] != "PICK" || len(got) != 1 {
		t.Fatalf("the path id must be sent under its published name and nothing else, sent %v", got)
	}
}

func TestProcessPathManagement_ListProcessPaths_SendsActiveOnly(t *testing.T) {
	up := newProcessPathManagementTestUpstream(t)
	c := NewProcessPathManagement(Config{Endpoint: up.URL})

	list, err := c.ListProcessPaths(context.Background(), false)
	if err != nil {
		t.Fatalf("ListProcessPaths: %v", err)
	}
	wantEq(t, "len(paths)", len(list.Paths), 1)
	wantEq(t, "paths[0].pathId", list.Paths[0].PathID, "PICK")
	wantEq(t, "paths[0].active", list.Paths[0].Active, true)
	if got := up.lastArgs(); got["activeOnly"] != false || len(got) != 1 {
		t.Fatalf("activeOnly must be sent under its published name and nothing else, sent %v", got)
	}
}

func TestProcessPathManagement_GetCPTSchedule_DecodesCutoffs(t *testing.T) {
	up := newProcessPathManagementTestUpstream(t)
	c := NewProcessPathManagement(Config{Endpoint: up.URL})

	schedule, err := c.GetCPTSchedule(context.Background(), "sp1")
	if err != nil {
		t.Fatalf("GetCPTSchedule: %v", err)
	}
	wantEq(t, "siteId", schedule.SiteID, "sp1")
	wantEq(t, "timezone", schedule.Timezone, "America/Chicago")
	wantEq(t, "len(cutoffs)", len(schedule.Cutoffs), 1)
	cutoff := schedule.Cutoffs[0]
	wantEq(t, "cutoffs[0].cptId", cutoff.CptID, "cpt-eod")
	wantEq(t, "cutoffs[0].localTime", cutoff.LocalTime, "17:00")
	wantEq(t, "cutoffs[0].shipMethod", cutoff.ShipMethod, "GROUND")
	wantEq(t, "len(cutoffs[0].eligiblePathIds)", len(cutoff.EligiblePathIDs), 2)
}

// TestProcessPathManagement_NotFoundIsAnOrdinaryError pins the honest
// classification: process-path-management's not-found tool errors are
// pre-convention prose ("usecases: process path not found" — the leading
// word is not a kebab *-not-found slug), so they surface as ordinary
// upstream-failure errors, NOT ports.ErrUpstreamNotFound. The consuming use
// case degrades on the error itself.
func TestProcessPathManagement_NotFoundIsAnOrdinaryError(t *testing.T) {
	up := newProcessPathManagementTestUpstream(t)
	c := NewProcessPathManagement(Config{Endpoint: up.URL})

	if _, err := c.GetProcessPath(context.Background(), "NOPE"); err == nil {
		t.Fatal("an unknown path id must surface as an error")
	} else if _, ok := err.(*ToolError); !ok {
		t.Fatalf("a tool error result must surface as *ToolError, got %T", err)
	}
}

func TestProcessPathManagement_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newProcessPathManagementTestUpstream(t)
	c := NewProcessPathManagement(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetProcessPath(ctx, "PICK")
	_, _ = c.ListProcessPaths(ctx, true)
	_, _ = c.GetCPTSchedule(ctx, "sp1")

	assertOnlyPinnedToolsCalled(t, up, wantProcessPathManagementTools)
}

// TestProcessPathManagement_UnreachableUpstream is the fail-open-absent-
// upstream path at the client layer: with the upstream gone, every call
// surfaces an ordinary error for the use case to degrade on — never a
// panic, never a hang.
func TestProcessPathManagement_UnreachableUpstream(t *testing.T) {
	up := newProcessPathManagementTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.
	c := NewProcessPathManagement(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetProcessPath(ctx, "PICK"); err == nil {
		t.Error("GetProcessPath: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListProcessPaths(ctx, true); err == nil {
		t.Error("ListProcessPaths: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetCPTSchedule(ctx, "sp1"); err == nil {
		t.Error("GetCPTSchedule: an unreachable upstream must surface as an error")
	}
}

func TestProcessPathManagement_RespectsCallerContext(t *testing.T) {
	up := newProcessPathManagementTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewProcessPathManagement(Config{Endpoint: up.URL}).GetProcessPath(ctx, "PICK"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
