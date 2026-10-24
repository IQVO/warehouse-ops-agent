package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// nodeTopologyGoldenPath is a VERBATIM copy of node-topology's own
// published tool registry golden
// (node-topology: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at node-topology origin/develop 0cc2992). Refresh it with
//
//	git -C ../node-topology show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever node-topology amends its tool surface; the tests below then
// say exactly which consumer-side assumption broke.
const nodeTopologyGoldenPath = "testdata/node_topology_tools.golden.json"

// wantNodeTopologyTools is every tool node-topology publishes (six, the
// verbatim golden); the client calls all six.
var wantNodeTopologyTools = []string{"get_node", "get_region", "list_lanes", "list_nodes", "list_regions", "resolve_service_area"}

// --- the published contract ------------------------------------------------

func TestNodeTopology_GoldenPinsExactlyTheSixReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, nodeTopologyGoldenPath), "node-topology", wantNodeTopologyTools)
}

// TestNodeTopology_PortDTOsMatchPublishedOutputSchemas walks every port
// DTO against the published output schema.
func TestNodeTopology_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, nodeTopologyGoldenPath)
	cases := map[string]reflect.Type{
		"get_node":             reflect.TypeFor[ports.NetworkNode](),
		"get_region":           reflect.TypeFor[ports.NetworkRegion](),
		"list_lanes":           reflect.TypeFor[ports.NetworkLaneList](),
		"list_nodes":           reflect.TypeFor[ports.NetworkNodeList](),
		"list_regions":         reflect.TypeFor[ports.NetworkRegionList](),
		"resolve_service_area": reflect.TypeFor[ports.ServiceArea](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful node-topology stand-in --------------------------------

const networkNodeFixture = `{"nodeId":"node-1","nodeType":"FULFILLMENT_CENTER","status":"ACTIVE","regionId":"region-1",
  "operator":"net-ops","timezone":"UTC","subtype":"GENERIC",
  "capabilities":{"outbound":true,"inbound":true,"returns":true,"sort":true,"lastMile":false,"air":false},
  "location":{"postalCode":"01310","countryCode":"US","lat":30.2,"lon":97.7},
  "operationalProfile":{"profileRevision":2,"timezone":"UTC",
    "operatingHoursByDay":{"mon":{"start":"08:00","end":"20:00"},"tue":{"start":"08:00","end":"20:00"}},
    "holidays":["2026-12-25"],"handlingTimeMinutes":120,
    "cutoffs":[{"localTime":"20:00","days":["mon","tue"],"maxOrders":500}],
    "throughputCap":{"value":1200,"timeUnit":"hour","unit":"package"},
    "declaredBy":"ops@example","declaredAt":"2026-10-01T00:00:00Z","sortDwellMinutes":6},
  "vendorRef":{"vendorId":"vend-1","vendorNodeCode":"VC-1"}}`

func newNodeTopologyTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, nodeTopologyGoldenPath)
	return newGoldenUpstream(t, "node-topology-test", golden, map[string]goldenFixture{
		"get_node": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["nodeId"].(string); id != "node-1" {
				return nil, goldenToolError("node-not-found: no node " + id)
			}
			return goldenJSON(t, networkNodeFixture), nil
		},
		"get_region": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["regionId"].(string); id != "region-1" {
				return nil, goldenToolError("region-not-found: no region " + id)
			}
			return goldenJSON(t, `{"regionId":"region-1","code":"US-S","name":"South"}`), nil
		},
		"list_nodes":   goldenResult(goldenJSON(t, `{"nodes":[`+networkNodeFixture+`]}`)),
		"list_regions": goldenResult(goldenJSON(t, `{"regions":[{"regionId":"region-1","code":"US-S","name":"South"}]}`)),
		"list_lanes": func(args map[string]any) (any, *mcp.CallToolResult) {
			if len(args) > 2 {
				return nil, goldenToolError("invalid-lane-filter: only origin and destination are published filters")
			}
			return goldenJSON(t, `{"lanes":[{"laneId":"lane-1","originNodeId":"node-1","destinationNodeId":"node-2",
			  "mode":"ROAD","nominalTransitMinutes":240,"status":"OPEN"}]}`), nil
		},
		"resolve_service_area": func(args map[string]any) (any, *mcp.CallToolResult) {
			if p, _ := args["postalPrefix"].(string); p != "013" {
				return nil, goldenToolError("service-area-not-found: no delivery station serves " + p)
			}
			return goldenJSON(t, `{"postalPrefix":"013","deliveryStationNodeId":"node-42","regionId":"region-1","assignmentVersion":7}`), nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestNodeTopology_GetNode_DecodesProfileCapabilitiesAndVendorRef(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	c := NewNodeTopology(Config{Endpoint: up.URL})

	out, err := c.GetNode(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	wantEq(t, "nodeId", out.NodeID, "node-1")
	wantEq(t, "nodeType", out.NodeType, "FULFILLMENT_CENTER")
	wantEq(t, "status", out.Status, "ACTIVE")
	wantEq(t, "subtype", out.Subtype, "GENERIC")
	wantEq(t, "capabilities.outbound", out.Capabilities.Outbound, true)
	wantEq(t, "capabilities.returns", out.Capabilities.Returns, true)
	wantEq(t, "capabilities.lastMile", out.Capabilities.LastMile, false)
	wantEq(t, "capabilities.air", out.Capabilities.Air, false)
	wantEq(t, "location.postalCode", out.Location.PostalCode, "01310")
	if out.Location.Lat == nil {
		t.Fatal("location.lat must decode")
	}
	wantEq(t, "location.lat", *out.Location.Lat, 30.2)
	p := out.OperationalProfile
	if p == nil {
		t.Fatal("operationalProfile must decode")
	}
	wantEq(t, "profile.profileRevision", p.ProfileRevision, 2)
	wantEq(t, "profile.handlingTimeMinutes", p.HandlingTimeMinutes, 120)
	wantEq(t, "profile.throughputCap.value", p.ThroughputCap.Value, 1200)
	wantEq(t, "profile.throughputCap.timeUnit", p.ThroughputCap.TimeUnit, "hour")
	wantEq(t, "len(profile.holidays)", len(p.Holidays), 1)
	if p.SortDwellMinutes == nil {
		t.Fatal("profile.sortDwellMinutes must decode")
	}
	wantEq(t, "profile.sortDwellMinutes", *p.SortDwellMinutes, 6)
	wantEq(t, "len(profile.cutoffs)", len(p.Cutoffs), 1)
	wantEq(t, "cutoffs[0].localTime", p.Cutoffs[0].LocalTime, "20:00")
	wantEq(t, "len(cutoffs[0].days)", len(p.Cutoffs[0].Days), 2)
	wantEq(t, "cutoffs[0].maxOrders", p.Cutoffs[0].MaxOrders, 500)
	wantEq(t, "operatingHoursByDay[mon].start", p.OperatingHoursByDay["mon"].Start, "08:00")
	wantEq(t, "operatingHoursByDay[mon].end", p.OperatingHoursByDay["mon"].End, "20:00")
	if out.VendorRef == nil {
		t.Fatal("vendorRef must decode")
	}
	wantEq(t, "vendorRef.vendorId", out.VendorRef.VendorID, "vend-1")
	wantEq(t, "vendorRef.vendorNodeCode", out.VendorRef.VendorNodeCode, "VC-1")
	if got := up.lastArgs(); got["nodeId"] != "node-1" || len(got) != 1 {
		t.Fatalf("the node id must be sent under its published name and nothing else, sent %v", got)
	}
}

func TestNodeTopology_ListNodes_OmitsZeroQueryFields(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	c := NewNodeTopology(Config{Endpoint: up.URL})
	ctx := context.Background()

	if _, err := c.ListNodes(ctx, ports.NodeListQuery{}); err != nil {
		t.Fatalf("ListNodes (empty query): %v", err)
	}
	if got := up.lastArgs(); len(got) != 0 {
		t.Fatalf("an empty query must send no argument keys so node-topology answers every node, sent %v", got)
	}
	if _, err := c.ListNodes(ctx, ports.NodeListQuery{Type: "DELIVERY_STATION", RegionID: "region-1", Status: "ACTIVE"}); err != nil {
		t.Fatalf("ListNodes (full query): %v", err)
	}
	got := up.lastArgs()
	if got["type"] != "DELIVERY_STATION" || got["regionId"] != "region-1" || got["status"] != "ACTIVE" || len(got) != 3 {
		t.Fatalf("a set query must send exactly its three published names, sent %v", got)
	}
}

func TestNodeTopology_ListRegions_Decodes(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	c := NewNodeTopology(Config{Endpoint: up.URL})

	out, err := c.ListRegions(context.Background())
	if err != nil {
		t.Fatalf("ListRegions: %v", err)
	}
	if len(out.Regions) != 1 || out.Regions[0].RegionID != "region-1" || out.Regions[0].Code != "US-S" || out.Regions[0].Name != "South" {
		t.Fatalf("unexpected regions: %+v", out.Regions)
	}
}

func TestNodeTopology_ListLanes_SendsOnlySetFilters(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	c := NewNodeTopology(Config{Endpoint: up.URL})
	ctx := context.Background()

	out, err := c.ListLanes(ctx, "", "")
	if err != nil {
		t.Fatalf("ListLanes (unfiltered): %v", err)
	}
	if len(out.Lanes) != 1 || out.Lanes[0].LaneID != "lane-1" || out.Lanes[0].NominalTransitMinutes != 240 || out.Lanes[0].ClosedAt != "" {
		t.Fatalf("unexpected lanes: %+v", out.Lanes)
	}
	if got := up.lastArgs(); len(got) != 0 {
		t.Fatalf("empty filters must be omitted so node-topology answers every lane, sent %v", got)
	}
	if _, err := c.ListLanes(ctx, "node-1", "node-2"); err != nil {
		t.Fatalf("ListLanes (filtered): %v", err)
	}
	if got := up.lastArgs(); got["origin"] != "node-1" || got["destination"] != "node-2" || len(got) != 2 {
		t.Fatalf("set filters must be sent under their published names, sent %v", got)
	}
}

func TestNodeTopology_ResolveServiceArea_Decodes(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	c := NewNodeTopology(Config{Endpoint: up.URL})

	out, err := c.ResolveServiceArea(context.Background(), "013")
	if err != nil {
		t.Fatalf("ResolveServiceArea: %v", err)
	}
	if out.PostalPrefix != "013" || out.DeliveryStationNodeID != "node-42" || out.RegionID != "region-1" || out.AssignmentVersion != 7 {
		t.Fatalf("unexpected service area: %+v", out)
	}
}

func TestNodeTopology_NotFoundAndServiceAreaClassify(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	c := NewNodeTopology(Config{Endpoint: up.URL})
	ctx := context.Background()

	if _, err := c.GetRegion(ctx, "region-none"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("region-not-found must classify as upstream-not-found, got %v", err)
	}
	if _, err := c.ResolveServiceArea(ctx, "999"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("service-area-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestNodeTopology_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	c := NewNodeTopology(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetNode(ctx, "node-1")
	_, _ = c.GetRegion(ctx, "region-1")
	_, _ = c.ListNodes(ctx, ports.NodeListQuery{})
	_, _ = c.ListRegions(ctx)
	_, _ = c.ListLanes(ctx, "", "")
	_, _ = c.ResolveServiceArea(ctx, "013")

	assertOnlyPinnedToolsCalled(t, up, wantNodeTopologyTools)
}

func TestNodeTopology_UnreachableUpstream(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	up.Close()
	c := NewNodeTopology(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetNode(ctx, "node-1"); err == nil {
		t.Error("GetNode: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListRegions(ctx); err == nil {
		t.Error("ListRegions: an unreachable upstream must surface as an error")
	}
}

func TestNodeTopology_RespectsCallerContext(t *testing.T) {
	up := newNodeTopologyTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewNodeTopology(Config{Endpoint: up.URL}).GetNode(ctx, "node-1"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
