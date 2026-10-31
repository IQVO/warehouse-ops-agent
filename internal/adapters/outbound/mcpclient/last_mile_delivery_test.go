package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// lastMileDeliveryGoldenPath is a VERBATIM copy of last-mile-delivery's
// own published tool registry golden
// (last-mile-delivery: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at last-mile-delivery origin/develop 5e52bd9). Refresh it with
//
//	git -C ../last-mile-delivery show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever last-mile-delivery amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const lastMileDeliveryGoldenPath = "testdata/last_mile_delivery_tools.golden.json"

// wantLastMileDeliveryTools is every tool last-mile-delivery publishes
// (three, the verbatim golden); the client calls all three.
var wantLastMileDeliveryTools = []string{"get_route", "get_wave_stops", "list_waves"}

// --- the published contract ------------------------------------------------

func TestLastMileDelivery_GoldenPinsExactlyTheThreeReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, lastMileDeliveryGoldenPath), "last-mile-delivery", wantLastMileDeliveryTools)
}

// TestLastMileDelivery_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema.
func TestLastMileDelivery_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, lastMileDeliveryGoldenPath)
	cases := map[string]reflect.Type{
		"get_route":      reflect.TypeFor[ports.DeliveryRoute](),
		"get_wave_stops": reflect.TypeFor[ports.WaveStops](),
		"list_waves":     reflect.TypeFor[ports.DeliveryWaves](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful last-mile-delivery stand-in ---------------------------

func newLastMileDeliveryTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, lastMileDeliveryGoldenPath)
	waves := goldenJSON(t, `{"items":[{"waveId":"wave-1","stationNodeId":"node-42","serviceDate":"2026-10-24",
	  "inductCutoffAt":"2026-10-24T12:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z",
	  "stopCapacity":80,"assignedStops":3,"remainingStops":77,"state":"OPEN"}]}`)
	stops := goldenJSON(t, `{"items":[{"stopKey":"pkg:RN001","trackingNumbers":["RN001"],"destinationPostalPrefix":"013"},
	  {"stopKey":"stop-9f2c","trackingNumbers":["RN002","RN003"]}]}`)
	route := goldenJSON(t, `{"routeId":"route-9f2c","waveId":"wave-1","dspId":"dsp-1","stationNodeId":"node-42",
	  "state":"OUT_FOR_DELIVERY","stopCount":2,
	  "stops":[{"stopNo":1,"trackingNumbers":["RN001"],"outcome":"DELIVERED"},{"stopNo":2,"trackingNumbers":["RN002","RN003"]}],
	  "attempts":[{"routeId":"route-9f2c","trackingNumber":"RN001","outcome":"DELIVERED","at":"2026-10-24T18:30:00Z"},
	             {"routeId":"route-9f2c","trackingNumber":"RN002","outcome":"FAILED","at":"2026-10-24T18:45:00Z","failureReason":"NO_ACCESS"}]}`)
	return newGoldenUpstream(t, "last-mile-delivery-test", golden, map[string]goldenFixture{
		"list_waves": func(args map[string]any) (any, *mcp.CallToolResult) {
			if args["stationNodeId"] != "node-42" || args["serviceDate"] != "2026-10-24" {
				return nil, goldenToolError("invalid-wave-query: stationNodeId and serviceDate must name a station's local service date")
			}
			return waves, nil
		},
		"get_wave_stops": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["waveId"].(string); id != "wave-1" {
				return nil, goldenToolError("wave-not-found: no wave " + id)
			}
			return stops, nil
		},
		"get_route": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["routeId"].(string); id != "route-9f2c" {
				return nil, goldenToolError("route-not-found: no route " + id)
			}
			return route, nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestLastMileDelivery_ListWaves_DecodesAndSendsExactArgs(t *testing.T) {
	up := newLastMileDeliveryTestUpstream(t)
	c := NewLastMileDelivery(Config{Endpoint: up.URL})

	out, err := c.ListWaves(context.Background(), "node-42", "2026-10-24")
	if err != nil {
		t.Fatalf("ListWaves: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].WaveID != "wave-1" || out.Items[0].State != "OPEN" ||
		out.Items[0].StopCapacity != 80 || out.Items[0].AssignedStops != 3 || out.Items[0].RemainingStops != 77 ||
		out.Items[0].InductCutoffAt != "2026-10-24T12:00:00Z" || out.Items[0].CancelReason != "" {
		t.Fatalf("unexpected waves: %+v", out.Items)
	}
	if got := up.lastArgs(); got["stationNodeId"] != "node-42" || got["serviceDate"] != "2026-10-24" || len(got) != 2 {
		t.Fatalf("the station and service date must be sent under their published names and nothing else, sent %v", got)
	}
}

func TestLastMileDelivery_GetWaveStops_DecodesStops(t *testing.T) {
	up := newLastMileDeliveryTestUpstream(t)
	c := NewLastMileDelivery(Config{Endpoint: up.URL})

	out, err := c.GetWaveStops(context.Background(), "wave-1")
	if err != nil {
		t.Fatalf("GetWaveStops: %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].StopKey != "pkg:RN001" || len(out.Items[0].TrackingNumbers) != 1 ||
		out.Items[0].DestinationPostalPrefix != "013" || out.Items[1].DestinationPostalPrefix != "" || len(out.Items[1].TrackingNumbers) != 2 {
		t.Fatalf("unexpected wave stops: %+v", out.Items)
	}
}

func TestLastMileDelivery_GetRoute_DecodesStopsAndAttempts(t *testing.T) {
	up := newLastMileDeliveryTestUpstream(t)
	c := NewLastMileDelivery(Config{Endpoint: up.URL})

	out, err := c.GetRoute(context.Background(), "route-9f2c")
	if err != nil {
		t.Fatalf("GetRoute: %v", err)
	}
	if out.RouteID != "route-9f2c" || out.DspID != "dsp-1" || out.State != "OUT_FOR_DELIVERY" || out.StopCount != 2 {
		t.Fatalf("unexpected route header: %+v", out)
	}
	if len(out.Stops) != 2 || out.Stops[0].Outcome != "DELIVERED" || out.Stops[1].Outcome != "" || len(out.Stops[1].TrackingNumbers) != 2 {
		t.Fatalf("unexpected route stops: %+v", out.Stops)
	}
	if len(out.Attempts) != 2 || out.Attempts[1].FailureReason != "NO_ACCESS" || out.Attempts[0].FailureReason != "" {
		t.Fatalf("unexpected route attempts: %+v", out.Attempts)
	}
}

func TestLastMileDelivery_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newLastMileDeliveryTestUpstream(t)
	c := NewLastMileDelivery(Config{Endpoint: up.URL})

	_, err := c.GetRoute(context.Background(), "route-none")
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("route-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestLastMileDelivery_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newLastMileDeliveryTestUpstream(t)
	c := NewLastMileDelivery(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.ListWaves(ctx, "node-42", "2026-10-24")
	_, _ = c.GetWaveStops(ctx, "wave-1")
	_, _ = c.GetRoute(ctx, "route-9f2c")

	assertOnlyPinnedToolsCalled(t, up, wantLastMileDeliveryTools)
}

func TestLastMileDelivery_UnreachableUpstream(t *testing.T) {
	up := newLastMileDeliveryTestUpstream(t)
	up.Close()
	c := NewLastMileDelivery(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.ListWaves(ctx, "node-42", "2026-10-24"); err == nil {
		t.Error("ListWaves: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetWaveStops(ctx, "wave-1"); err == nil {
		t.Error("GetWaveStops: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetRoute(ctx, "route-9f2c"); err == nil {
		t.Error("GetRoute: an unreachable upstream must surface as an error")
	}
}

func TestLastMileDelivery_RespectsCallerContext(t *testing.T) {
	up := newLastMileDeliveryTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewLastMileDelivery(Config{Endpoint: up.URL}).GetRoute(ctx, "route-9f2c"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
