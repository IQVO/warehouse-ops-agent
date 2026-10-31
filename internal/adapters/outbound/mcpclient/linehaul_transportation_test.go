package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// linehaulTransportationGoldenPath is a VERBATIM copy of
// linehaul-transportation's own published tool registry golden
// (linehaul-transportation: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at linehaul-transportation origin/develop 7a6a9b6). Refresh it with
//
//	git -C ../linehaul-transportation show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever linehaul-transportation amends its tool surface; the tests
// below then say exactly which consumer-side assumption broke.
const linehaulTransportationGoldenPath = "testdata/linehaul_transportation_tools.golden.json"

// wantLinehaulTransportationTools is every tool linehaul-transportation
// publishes (two, the verbatim golden); the client calls both.
var wantLinehaulTransportationTools = []string{"get_departure", "list_departures"}

// --- the published contract ------------------------------------------------

func TestLinehaulTransportation_GoldenPinsExactlyTheTwoReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, linehaulTransportationGoldenPath), "linehaul-transportation", wantLinehaulTransportationTools)
}

// TestLinehaulTransportation_PortDTOsMatchPublishedOutputSchemas walks
// every port DTO against the published output schema.
func TestLinehaulTransportation_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, linehaulTransportationGoldenPath)
	cases := map[string]reflect.Type{
		"get_departure":   reflect.TypeFor[ports.LinehaulDeparture](),
		"list_departures": reflect.TypeFor[ports.LinehaulDeparturePage](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful linehaul-transportation stand-in ----------------------

const linehaulDepartureFixture = `{"departureId":"dep-7f3a","laneId":"lane-1","kind":"LINEHAUL",
  "originNodeId":"node-1","destinationNodeId":"node-2","cutoffAt":"2026-10-25T08:00:00Z",
  "departsAt":"2026-10-25T10:00:00Z","arrivesAt":"2026-10-25T18:00:00Z","capacityPackages":100,
  "bookedPackages":40,"remainingPackages":60,"state":"SCHEDULED"}`

func newLinehaulTransportationTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, linehaulTransportationGoldenPath)
	return newGoldenUpstream(t, "linehaul-transportation-test", golden, map[string]goldenFixture{
		"get_departure": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["departureId"].(string); id != "dep-7f3a" {
				return nil, goldenToolError("departure-not-found: no departure " + id)
			}
			return goldenJSON(t, linehaulDepartureFixture), nil
		},
		"list_departures": goldenResult(goldenJSON(t, `{"departures":[`+linehaulDepartureFixture+`]}`)),
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestLinehaulTransportation_GetDeparture_Decodes(t *testing.T) {
	up := newLinehaulTransportationTestUpstream(t)
	c := NewLinehaulTransportation(Config{Endpoint: up.URL})

	out, err := c.GetDeparture(context.Background(), "dep-7f3a")
	if err != nil {
		t.Fatalf("GetDeparture: %v", err)
	}
	if out.DepartureID != "dep-7f3a" || out.Kind != "LINEHAUL" || out.OriginNodeID != "node-1" ||
		out.DestinationNodeID != "node-2" || out.CapacityPackages != 100 || out.BookedPackages != 40 ||
		out.RemainingPackages != 60 || out.State != "SCHEDULED" || out.DepartedAt != "" {
		t.Fatalf("unexpected departure: %+v", out)
	}
	if got := up.lastArgs(); got["departureId"] != "dep-7f3a" || len(got) != 1 {
		t.Fatalf("the departure id must be sent under its published name and nothing else, sent %v", got)
	}
}

// TestLinehaulTransportation_ListDepartures_OmitsZeroQueryFields pins the
// optional-filter contract: an empty query sends NO argument keys, a set
// one sends exactly its published names.
func TestLinehaulTransportation_ListDepartures_OmitsZeroQueryFields(t *testing.T) {
	up := newLinehaulTransportationTestUpstream(t)
	c := NewLinehaulTransportation(Config{Endpoint: up.URL})

	if _, err := c.ListDepartures(context.Background(), ports.LinehaulDepartureQuery{}); err != nil {
		t.Fatalf("ListDepartures (empty query): %v", err)
	}
	if got := up.lastArgs(); len(got) != 0 {
		t.Fatalf("an empty query must send no argument keys so linehaul applies its own defaults, sent %v", got)
	}

	if _, err := c.ListDepartures(context.Background(), ports.LinehaulDepartureQuery{
		Origin: "node-1", Destination: "node-2", From: "2026-10-25T00:00:00Z", To: "2026-10-26T00:00:00Z", Limit: 10,
	}); err != nil {
		t.Fatalf("ListDepartures (full query): %v", err)
	}
	got := up.lastArgs()
	if got["origin"] != "node-1" || got["destination"] != "node-2" || got["from"] != "2026-10-25T00:00:00Z" ||
		got["to"] != "2026-10-26T00:00:00Z" || got["limit"] != float64(10) || len(got) != 5 {
		t.Fatalf("a set query must send exactly its five published names, sent %v", got)
	}
}

func TestLinehaulTransportation_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newLinehaulTransportationTestUpstream(t)
	c := NewLinehaulTransportation(Config{Endpoint: up.URL})

	_, err := c.GetDeparture(context.Background(), "dep-none")
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("departure-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestLinehaulTransportation_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newLinehaulTransportationTestUpstream(t)
	c := NewLinehaulTransportation(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetDeparture(ctx, "dep-7f3a")
	_, _ = c.ListDepartures(ctx, ports.LinehaulDepartureQuery{})

	assertOnlyPinnedToolsCalled(t, up, wantLinehaulTransportationTools)
}

func TestLinehaulTransportation_UnreachableUpstream(t *testing.T) {
	up := newLinehaulTransportationTestUpstream(t)
	up.Close()
	c := NewLinehaulTransportation(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetDeparture(ctx, "dep-7f3a"); err == nil {
		t.Error("GetDeparture: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListDepartures(ctx, ports.LinehaulDepartureQuery{}); err == nil {
		t.Error("ListDepartures: an unreachable upstream must surface as an error")
	}
}

func TestLinehaulTransportation_RespectsCallerContext(t *testing.T) {
	up := newLinehaulTransportationTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewLinehaulTransportation(Config{Endpoint: up.URL}).GetDeparture(ctx, "dep-7f3a"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
