package mcp_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// --- narrow-port fakes for the wire tests ------------------------------------

type netOrdersFake struct{}

func (f *netOrdersFake) GetOrder(context.Context, string) (ports.CustomerOrder, error) {
	return ports.CustomerOrder{
		OrderID: "ord-9", Status: "IN_FULFILLMENT", ShopperID: "shp-1",
		DestinationPostalPrefix: "013", PlacedAt: "2026-10-24T08:00:00Z",
		Lines:  []ports.CustomerOrderLine{{LineNo: 1, CatalogItemID: "item-1", Quantity: 2}},
		Groups: []ports.CustomerOrderGroup{{GroupNo: 1, PromiseID: "prm-9", Tier: "STANDARD", ShipBy: "2026-10-25T10:00:00Z", LineNos: []int{1}, Status: "PROMISED"}},
	}, nil
}

func (f *netOrdersFake) GetOrderTracking(context.Context, string) (ports.OrderTracking, error) {
	return ports.OrderTracking{
		OrderID: "ord-9", Status: "IN_FULFILLMENT",
		Groups: []ports.TrackedOrderGroup{{
			GroupNo: 1, PromiseID: "prm-9", Status: "SHIPPED", PromiseVerdict: "ON_TIME",
			Packages: []ports.TrackedPackage{{TrackingNumber: "RN-9", State: "IN_TRANSIT", Carrier: "FLEET"}},
		}},
	}, nil
}

func (f *netOrdersFake) GetShipTo(context.Context, string) (ports.ShipToAddress, error) {
	return ports.ShipToAddress{Name: "A. Shopper", Line1: "1 Main St", City: "Austin", Region: "TX", PostalCode: "01310", CountryCode: "US"}, nil
}

type netPromisesFake struct{}

func (f *netPromisesFake) ListPromisesByOrder(context.Context, string) (ports.PromisesByOrder, error) {
	return ports.PromisesByOrder{Items: []ports.DeliveryPromise{{
		PromiseID: "prm-9", State: "CONFIRMED", GroupNo: 1, Version: 3,
		Terms:     ports.PromiseTerms{Tier: "STANDARD", ShipBy: "2026-10-25T10:00:00Z", DeliverByEarliest: "2026-10-26T08:00:00Z", DeliverByLatest: "2026-10-27T20:00:00Z"},
		Judgement: &ports.PromiseJudgement{Outcome: "KEPT"},
	}}}, nil
}

func (f *netPromisesFake) GetPromise(_ context.Context, id string) (ports.DeliveryPromise, error) {
	return ports.DeliveryPromise{}, nil
}

type netSourcingFake struct{}

func (f *netSourcingFake) ListAssignments(context.Context, ports.AssignmentListQuery) (ports.AssignmentPage, error) {
	return ports.AssignmentPage{Assignments: []ports.FulfillmentAssignment{{
		AssignmentID: "asg-9", OrderID: "ord-9", GroupNo: 1, State: "LOCKED",
		TrackingNumbers: []string{"RN-9"},
		Candidate:       &ports.AssignmentCandidate{NodeID: "node-42", ETA: "2026-10-25T09:00:00Z"},
	}}}, nil
}

type netJourneysFake struct{}

func (f *netJourneysFake) GetJourney(_ context.Context, number string) (ports.PackageJourney, error) {
	return ports.PackageJourney{
		TrackingNumber: number, State: "IN_TRANSIT", PoNumber: "PO-1", AssignmentID: "asg-9", Tier: "STANDARD",
		Scans: []ports.JourneyScan{{ScanID: "s-1", NodeID: "node-42", ScanType: "DEPART", At: "2026-10-25T06:00:00Z", Applied: true}},
	}, nil
}

func newServerWithNetworkOrderOutlook(t *testing.T) string {
	t.Helper()
	server := inboundmcp.NewServer(inboundmcp.Deps{
		DailyBrief: &usecases.DailyBrief{},
		NetworkOrderOutlook: &usecases.NetworkOrderOutlook{
			Orders:   &netOrdersFake{},
			Promises: &netPromisesFake{},
			Sourcing: &netSourcingFake{},
			Journeys: &netJourneysFake{},
			Now:      func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) },
		},
	})
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

func TestServer_GetNetworkOrderOutlook_OnlyAdvertisedWhenWiredAndReadOnly(t *testing.T) {
	if advertised(t, newServer(t), "get_network_order_outlook") != nil {
		t.Fatal("get_network_order_outlook must not be advertised when customer-ordering is not configured")
	}
	tool := advertised(t, newServerWithNetworkOrderOutlook(t), "get_network_order_outlook")
	if tool == nil {
		t.Fatal("get_network_order_outlook not advertised when the outlook is wired")
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("get_network_order_outlook must be read-only: %+v", tool.Annotations)
	}
}

func TestServer_GetNetworkOrderOutlook_OverTheWire(t *testing.T) {
	session := connect(t, newServerWithNetworkOrderOutlook(t))
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "get_network_order_outlook",
		Arguments: map[string]any{"orderId": "ord-9"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	if out["orderId"] != "ord-9" || out["generatedAt"] != "2026-10-24T12:00:00Z" {
		t.Fatalf("echoed inputs: %v", out)
	}

	assertOrderWireSections(t, out)
}

func assertOrderWireSections(t *testing.T, out map[string]any) {
	t.Helper()
	order := out["order"].(map[string]any)
	if order["count"] != float64(1) || order["complete"] != true {
		t.Errorf("order section: %v", order)
	}
	orderItem := order["items"].([]any)[0].(map[string]any)
	if orderItem["status"] != "IN_FULFILLMENT" || len(orderItem["groups"].([]any)) != 1 {
		t.Errorf("order item: %v", orderItem)
	}

	promises := out["promises"].(map[string]any)
	promise := promises["items"].([]any)[0].(map[string]any)
	if promise["state"] != "CONFIRMED" || promise["judgement"].(map[string]any)["outcome"] != "KEPT" {
		t.Errorf("promise item: %v", promise)
	}

	sourcing := out["sourcing"].(map[string]any)
	assignment := sourcing["items"].([]any)[0].(map[string]any)
	if assignment["candidateNodeId"] != "node-42" || assignment["state"] != "LOCKED" {
		t.Errorf("assignment item: %v", assignment)
	}

	journeys := out["journeys"].(map[string]any)
	journey := journeys["items"].([]any)[0].(map[string]any)
	if journey["trackingNumber"] != "RN-9" || journey["scanCount"] != float64(1) || journey["lastScanNodeId"] != "node-42" {
		t.Errorf("journey item: %v", journey)
	}
}

// TestServer_GetNetworkOrderOutlook_UnconfiguredSectionOmitsNotConfigured
// proves the per-section degradation reaches the wire: with only the
// anchor configured, every optional section carries the not-configured
// reason, and the call still succeeds.
func TestServer_GetNetworkOrderOutlook_UnconfiguredSectionOmitsNotConfigured(t *testing.T) {
	server := inboundmcp.NewServer(inboundmcp.Deps{
		DailyBrief:          &usecases.DailyBrief{},
		NetworkOrderOutlook: &usecases.NetworkOrderOutlook{Orders: &netOrdersFake{}},
	})
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)

	session := connect(t, httpSrv.URL)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "get_network_order_outlook",
		Arguments: map[string]any{"orderId": "ord-9"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("an unconfigured non-anchor upstream must omit its section, not fail the tool: %+v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	for _, section := range []string{"promises", "sourcing", "journeys"} {
		omitted, _ := out[section].(map[string]any)["omitted"].(string)
		if omitted == "" {
			t.Errorf("%s section omitted reason is empty, want not-configured", section)
		}
	}
}

// --- the node outlook over the wire -------------------------------------------

type netTopologyFake struct{}

func (f *netTopologyFake) GetNode(context.Context, string) (ports.NetworkNode, error) {
	return ports.NetworkNode{
		NodeID: "node-42", NodeType: "DELIVERY_STATION", Status: "ACTIVE", RegionID: "reg-1",
		Operator: "FLEET", Timezone: "America/Chicago",
		Capabilities: ports.NodeCapabilities{Outbound: true, LastMile: true},
		Location:     ports.NodeLocation{PostalCode: "73301", CountryCode: "US"},
		VendorRef:    &ports.NodeVendorRef{VendorID: "ven-9", VendorNodeCode: "CHI-1"},
	}, nil
}

func (f *netTopologyFake) ListLanes(_ context.Context, origin, destination string) (ports.NetworkLaneList, error) {
	if origin == "node-42" {
		return ports.NetworkLaneList{Lanes: []ports.NetworkLane{{LaneID: "lane-out", OriginNodeID: "node-42", DestinationNodeID: "node-7", Mode: "ROAD", NominalTransitMinutes: 180, Status: "OPEN"}}}, nil
	}
	return ports.NetworkLaneList{Lanes: []ports.NetworkLane{{LaneID: "lane-in", OriginNodeID: "node-7", DestinationNodeID: "node-42", Mode: "ROAD", NominalTransitMinutes: 175, Status: "OPEN"}}}, nil
}

type netVendorFake struct{}

func (f *netVendorFake) GetVendorNode(_ context.Context, vendorID, nodeID string) (ports.VendorNode, error) {
	return ports.VendorNode{NodeID: nodeID, VendorID: vendorID, VendorNodeCode: "CHI-1", NodeType: "DELIVERY_STATION", Status: "ACTIVE"}, nil
}

func (f *netVendorFake) GetNodeScorecard(_ context.Context, vendorID, nodeID string) (ports.VendorNodeScorecards, error) {
	rate := 0.97
	return ports.VendorNodeScorecards{NodeID: nodeID, Windows: []ports.VendorScorecardWindow{{
		Window: "7d", Grade: "A", Observations: 412, OnTimeHandoverRate: &rate,
	}}}, nil
}

type netWavesFake struct {
	serviceDate string
}

func (f *netWavesFake) ListWaves(_ context.Context, stationNodeID, serviceDate string) (ports.DeliveryWaves, error) {
	f.serviceDate = serviceDate
	return ports.DeliveryWaves{Items: []ports.DeliveryWave{{
		WaveID: "wave-1", StationNodeID: stationNodeID, ServiceDate: serviceDate, State: "OPEN",
		InductCutoffAt: "2026-10-24T21:00:00Z", StopCapacity: 120, AssignedStops: 90, RemainingStops: 30,
	}}}, nil
}

func newServerWithNetworkNodeOutlook(t *testing.T, waves *netWavesFake) string {
	t.Helper()
	server := inboundmcp.NewServer(inboundmcp.Deps{
		DailyBrief: &usecases.DailyBrief{},
		NetworkNodeOutlook: &usecases.NetworkNodeOutlook{
			Topology: &netTopologyFake{},
			Vendor:   &netVendorFake{},
			Waves:    waves,
			Now:      func() time.Time { return time.Date(2026, 10, 24, 18, 30, 0, 0, time.UTC) },
		},
	})
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

func TestServer_GetNetworkNodeOutlook_OnlyAdvertisedWhenWiredAndReadOnly(t *testing.T) {
	if advertised(t, newServer(t), "get_network_node_outlook") != nil {
		t.Fatal("get_network_node_outlook must not be advertised when node-topology is not configured")
	}
	tool := advertised(t, newServerWithNetworkNodeOutlook(t, &netWavesFake{}), "get_network_node_outlook")
	if tool == nil {
		t.Fatal("get_network_node_outlook not advertised when the outlook is wired")
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("get_network_node_outlook must be read-only: %+v", tool.Annotations)
	}
}

func TestServer_GetNetworkNodeOutlook_OverTheWire(t *testing.T) {
	waves := &netWavesFake{}
	session := connect(t, newServerWithNetworkNodeOutlook(t, waves))
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "get_network_node_outlook",
		Arguments: map[string]any{"nodeId": "node-42"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	if out["nodeId"] != "node-42" || out["generatedAt"] != "2026-10-24T18:30:00Z" {
		t.Fatalf("echoed inputs: %v", out)
	}

	node := out["node"].(map[string]any)
	nodeItem := node["items"].([]any)[0].(map[string]any)
	if nodeItem["nodeType"] != "DELIVERY_STATION" || nodeItem["vendorRef"].(map[string]any)["vendorId"] != "ven-9" {
		t.Errorf("node item: %v", nodeItem)
	}

	inboundLane := out["inboundLanes"].(map[string]any)["items"].([]any)[0].(map[string]any)
	outboundLane := out["outboundLanes"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if inboundLane["laneId"] != "lane-in" || outboundLane["laneId"] != "lane-out" {
		t.Errorf("lanes: in %v out %v", inboundLane, outboundLane)
	}

	vendor := out["vendor"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if vendor["vendorNodeCode"] != "CHI-1" {
		t.Errorf("vendor item: %v", vendor)
	}

	wave := out["waves"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if wave["state"] != "OPEN" || wave["remainingStops"] != float64(30) {
		t.Errorf("wave item: %v", wave)
	}
	// 18:30 UTC is 13:30 in Chicago: the same local service date.
	if waves.serviceDate != "2026-10-24" {
		t.Errorf("list_waves service date = %q, want the node's local 2026-10-24", waves.serviceDate)
	}

	// node-performance / offer-availability / linehaul-transportation are
	// not wired in this server: their sections carry the not-configured
	// reason on the wire, never an error.
	for _, section := range []string{"scorecards", "listings", "inventoryFeed", "departures"} {
		omitted, _ := out[section].(map[string]any)["omitted"].(string)
		if omitted == "" {
			t.Errorf("%s section omitted reason is empty, want not-configured", section)
		}
	}
}
