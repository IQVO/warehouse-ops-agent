package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// vendorConnectivityGoldenPath is a VERBATIM copy of vendor-connectivity's
// own published tool registry golden
// (vendor-connectivity: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at vendor-connectivity origin/develop d9a4a19). Refresh it with
//
//	git -C ../vendor-connectivity show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever vendor-connectivity amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const vendorConnectivityGoldenPath = "testdata/vendor_connectivity_tools.golden.json"

// wantVendorConnectivityTools is every tool vendor-connectivity publishes
// (nine, the verbatim golden); the client calls all nine.
var wantVendorConnectivityTools = []string{"get_inbound_shipment", "get_node_scorecard", "get_purchase_order",
	"get_transaction", "get_vendor", "get_vendor_node", "list_vendor_nodes", "poll_inbound_shipments", "poll_purchase_orders"}

// --- the published contract ------------------------------------------------

func TestVendorConnectivity_GoldenPinsExactlyTheNineReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, vendorConnectivityGoldenPath), "vendor-connectivity", wantVendorConnectivityTools)
}

// TestVendorConnectivity_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema.
func TestVendorConnectivity_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, vendorConnectivityGoldenPath)
	cases := map[string]reflect.Type{
		"get_vendor":             reflect.TypeFor[ports.VendorAccount](),
		"get_vendor_node":        reflect.TypeFor[ports.VendorNode](),
		"list_vendor_nodes":      reflect.TypeFor[ports.VendorNodeList](),
		"get_purchase_order":     reflect.TypeFor[ports.VendorPurchaseOrder](),
		"poll_purchase_orders":   reflect.TypeFor[ports.VendorPurchaseOrderPage](),
		"get_inbound_shipment":   reflect.TypeFor[ports.VendorInboundShipment](),
		"poll_inbound_shipments": reflect.TypeFor[ports.VendorInboundShipmentPage](),
		"get_transaction":        reflect.TypeFor[ports.VendorTransaction](),
		"get_node_scorecard":     reflect.TypeFor[ports.VendorNodeScorecards](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful vendor-connectivity stand-in ---------------------------

const vendorPOFixture = `{"poNumber":"PO-1","vendorId":"vend-1","nodeId":"node-1","state":"ACCEPTED",
  "issuedAt":"2026-10-20T08:00:00Z","requiredShipBy":"2026-10-24T17:00:00Z","ackBy":"2026-10-21T08:00:00Z",
  "lines":[{"lineNo":1,"listingId":"list-1","quantity":10,
    "packaging":[{"level":"CASE","gtin":"10614141000000","unitsPerPack":12,"childGtin":"00614141000000"}]}],
  "labels":[{"labelRef":"lbl-1","trackingNumber":"RN001","carrier":"FLEET","packageNo":1}],
  "trackingNumbers":["RN001"],
  "acknowledgement":{"decision":"ACCEPTED","transactionId":"tx-1"}}`

const vendorShipmentFixture = `{"shipmentId":"shp-1","nodeId":"node-1","shippedAt":"2026-10-22T10:00:00Z","status":"SHIPPED",
  "sellerId":"seller-1",
  "pallets":[{"sscc":"00181234567890123456",
    "cartons":[{"sscc":"00181234567890123457","kind":"CARTON","parentSscc":"00181234567890123456",
      "grossWeightG":8000,"dimsMm":{"l":600,"w":400,"h":300},
      "contents":[{"gtin":"00614141000000","qtyEaches":24,"sku":"SKU-1","lot":"L-1","expiry":"2027-01-01"}]}]}],
  "cartons":[{"sscc":"00181234567890123458","kind":"CARTON",
    "contents":[{"gtin":"00614141000000","qtyEaches":6,"sku":"SKU-1"}]}]}`

func newVendorConnectivityTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, vendorConnectivityGoldenPath)
	return newGoldenUpstream(t, "vendor-connectivity-test", golden, map[string]goldenFixture{
		"get_vendor": withID("vendorId", "vend-1", "vendor-not-found",
			goldenJSON(t, `{"vendorId":"vend-1","name":"South Fulfilment","ackWindowMinutes":240,"state":"ACTIVE"}`)),
		"get_vendor_node": withID("nodeId", "node-1", "vendor-node-not-found",
			goldenJSON(t, `{"nodeId":"node-1","vendorId":"vend-1","vendorNodeCode":"VC-1","nodeType":"FULFILLMENT_CENTER",
			  "status":"ACTIVE","subtype":"GENERIC","regionId":"region-1","timezone":"UTC",
			  "handlingTimeMinutes":120,"profileRevision":4}`)),
		"list_vendor_nodes": goldenResult(goldenJSON(t, `{"nodes":[`+
			`{"nodeId":"node-1","vendorId":"vend-1","vendorNodeCode":"VC-1","nodeType":"FULFILLMENT_CENTER","status":"ACTIVE"}]}`)),
		"get_purchase_order": withID("poNumber", "PO-1", "po-not-found", goldenJSON(t, vendorPOFixture)),
		"poll_purchase_orders": withID("vendorId", "vend-1", "vendor-not-found",
			goldenJSON(t, `{"purchaseOrders":[`+vendorPOFixture+`],"nextToken":"tok-2"}`)),
		"get_inbound_shipment": withID("shipmentId", "shp-1", "shipment-not-found", goldenJSON(t, vendorShipmentFixture)),
		"poll_inbound_shipments": withID("vendorId", "vend-1", "vendor-not-found",
			goldenJSON(t, `{"inboundShipments":[`+vendorShipmentFixture+`],"nextToken":"tok-2"}`)),
		"get_transaction": withID("transactionId", "tx-1", "transaction-not-found",
			goldenJSON(t, `{"transactionId":"tx-1","vendorId":"vend-1","kind":"ACKNOWLEDGEMENT","status":"APPLIED",
			  "receivedAt":"2026-10-20T09:00:00Z","processedAt":"2026-10-20T09:00:05Z","poNumber":"PO-1",
			  "errors":[{"code":"UNKNOWN_LISTING","message":"listing not found","path":"lines[0].listingId"}]}`)),
		"get_node_scorecard": withID("nodeId", "node-1", "vendor-node-not-found",
			goldenJSON(t, `{"nodeId":"node-1","windows":[
			  {"window":"7d","grade":"A","observations":100,"computedAt":"2026-10-24T00:00:00Z",
			   "onTimeHandoverRate":0.97,"lateAckRate":0.03,"rejectRate":0.01,"expiryRate":0.0,
			   "promiseKeptRate":0.94,"feedFreshnessP95Minutes":11.5},
			  {"window":"30d","grade":"PROVISIONAL","observations":2,"computedAt":"2026-10-24T00:00:00Z",
			   "onTimeHandoverRate":null,"lateAckRate":null}]}`)),
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestVendorConnectivity_VendorReads_DecodeAndSendExactArgs(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	c := NewVendorConnectivity(Config{Endpoint: up.URL})
	ctx := context.Background()

	vendor, err := c.GetVendor(ctx, "vend-1")
	if err != nil {
		t.Fatalf("GetVendor: %v", err)
	}
	if vendor.Name != "South Fulfilment" || vendor.AckWindowMinutes != 240 || vendor.State != "ACTIVE" {
		t.Fatalf("unexpected vendor: %+v", vendor)
	}

	node, err := c.GetVendorNode(ctx, "vend-1", "node-1")
	if err != nil {
		t.Fatalf("GetVendorNode: %v", err)
	}
	if node.VendorNodeCode != "VC-1" || node.ProfileRevision != 4 || node.HandlingTimeMinutes != 120 {
		t.Fatalf("unexpected vendor node: %+v", node)
	}
	if got := up.lastArgs(); got["vendorId"] != "vend-1" || got["nodeId"] != "node-1" || len(got) != 2 {
		t.Fatalf("the vendor and node ids must be sent under their published names and nothing else, sent %v", got)
	}

	nodes, err := c.ListVendorNodes(ctx, "vend-1")
	if err != nil {
		t.Fatalf("ListVendorNodes: %v", err)
	}
	if len(nodes.Nodes) != 1 || nodes.Nodes[0].HandlingTimeMinutes != 0 {
		t.Fatalf("unexpected vendor node list: %+v", nodes.Nodes)
	}
}

func TestVendorConnectivity_GetPurchaseOrder_DecodesLinesLabelsAndAcknowledgement(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	c := NewVendorConnectivity(Config{Endpoint: up.URL})

	po, err := c.GetPurchaseOrder(context.Background(), "vend-1", "PO-1")
	if err != nil {
		t.Fatalf("GetPurchaseOrder: %v", err)
	}
	wantEq(t, "poNumber", po.PoNumber, "PO-1")
	wantEq(t, "state", po.State, "ACCEPTED")
	wantEq(t, "ackBy", po.AckBy, "2026-10-21T08:00:00Z")
	wantEq(t, "shippedAt (unset)", po.ShippedAt, "")
	wantEq(t, "len(lines)", len(po.Lines), 1)
	wantEq(t, "lines[0].quantity", po.Lines[0].Quantity, 10)
	wantEq(t, "len(lines[0].packaging)", len(po.Lines[0].Packaging), 1)
	wantEq(t, "lines[0].packaging[0].childGtin", po.Lines[0].Packaging[0].ChildGtin, "00614141000000")
	wantEq(t, "len(labels)", len(po.Labels), 1)
	wantEq(t, "labels[0].trackingNumber", po.Labels[0].TrackingNumber, "RN001")
	wantEq(t, "labels[0].packageNo", po.Labels[0].PackageNo, 1)
	if po.Acknowledgement == nil {
		t.Fatal("acknowledgement must decode")
	}
	wantEq(t, "acknowledgement.decision", po.Acknowledgement.Decision, "ACCEPTED")
	wantEq(t, "acknowledgement.transactionId", po.Acknowledgement.TransactionID, "tx-1")
}

// TestVendorConnectivity_Polls_SendOnlySetFields pins the poll query
// contract: the vendor is always sent, the optional filters only when
// set.
func TestVendorConnectivity_Polls_SendOnlySetFields(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	c := NewVendorConnectivity(Config{Endpoint: up.URL})
	ctx := context.Background()

	if _, err := c.PollPurchaseOrders(ctx, ports.VendorPollQuery{VendorID: "vend-1"}); err != nil {
		t.Fatalf("PollPurchaseOrders (bare): %v", err)
	}
	if got := up.lastArgs(); got["vendorId"] != "vend-1" || len(got) != 1 {
		t.Fatalf("a bare poll must send only the vendor id, sent %v", got)
	}

	if _, err := c.PollInboundShipments(ctx, ports.VendorPollQuery{
		VendorID: "vend-1", NodeID: "node-1", CreatedAfter: "2026-10-01T00:00:00Z", NextToken: "tok-1", Limit: 25,
	}); err != nil {
		t.Fatalf("PollInboundShipments (full): %v", err)
	}
	got := up.lastArgs()
	if got["vendorId"] != "vend-1" || got["nodeId"] != "node-1" || got["createdAfter"] != "2026-10-01T00:00:00Z" ||
		got["nextToken"] != "tok-1" || got["limit"] != float64(25) || len(got) != 5 {
		t.Fatalf("a set poll must send exactly its five published names, sent %v", got)
	}
}

func TestVendorConnectivity_GetInboundShipment_DecodesPalletsAndCartons(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	c := NewVendorConnectivity(Config{Endpoint: up.URL})

	ship, err := c.GetInboundShipment(context.Background(), "vend-1", "shp-1")
	if err != nil {
		t.Fatalf("GetInboundShipment: %v", err)
	}
	wantEq(t, "shipmentId", ship.ShipmentID, "shp-1")
	wantEq(t, "status", ship.Status, "SHIPPED")
	wantEq(t, "sellerId", ship.SellerID, "seller-1")
	wantEq(t, "len(pallets)", len(ship.Pallets), 1)
	wantEq(t, "pallets[0].sscc", ship.Pallets[0].SSCC, "00181234567890123456")
	wantEq(t, "len(pallets[0].cartons)", len(ship.Pallets[0].Cartons), 1)
	carton := ship.Pallets[0].Cartons[0]
	wantEq(t, "carton.parentSscc", carton.ParentSscc, "00181234567890123456")
	wantEq(t, "carton.grossWeightG", carton.GrossWeightG, 8000)
	if carton.DimsMm == nil {
		t.Fatal("carton.dimsMm must decode")
	}
	wantEq(t, "carton.dimsMm.l", carton.DimsMm.L, 600)
	wantEq(t, "carton.dimsMm.h", carton.DimsMm.H, 300)
	wantEq(t, "len(carton.contents)", len(carton.Contents), 1)
	wantEq(t, "contents[0].qtyEaches", carton.Contents[0].QtyEaches, 24)
	wantEq(t, "contents[0].lot", carton.Contents[0].Lot, "L-1")
	wantEq(t, "len(cartons)", len(ship.Cartons), 1)
	wantEq(t, "cartons[0].parentSscc (unset)", ship.Cartons[0].ParentSscc, "")
	wantEq(t, "len(cartons[0].contents)", len(ship.Cartons[0].Contents), 1)
}

func TestVendorConnectivity_GetTransaction_DecodesOutcome(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	c := NewVendorConnectivity(Config{Endpoint: up.URL})

	tx, err := c.GetTransaction(context.Background(), "vend-1", "tx-1")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.Kind != "ACKNOWLEDGEMENT" || tx.Status != "APPLIED" || tx.PoNumber != "PO-1" || tx.ProcessedAt != "2026-10-20T09:00:05Z" {
		t.Fatalf("unexpected transaction: %+v", tx)
	}
	if len(tx.Errors) != 1 || tx.Errors[0].Code != "UNKNOWN_LISTING" || tx.Errors[0].Path != "lines[0].listingId" {
		t.Fatalf("unexpected transaction errors: %+v", tx.Errors)
	}
}

func TestVendorConnectivity_GetNodeScorecard_DecodesNullableRates(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	c := NewVendorConnectivity(Config{Endpoint: up.URL})

	sc, err := c.GetNodeScorecard(context.Background(), "vend-1", "node-1")
	if err != nil {
		t.Fatalf("GetNodeScorecard: %v", err)
	}
	if len(sc.Windows) != 2 {
		t.Fatalf("unexpected scorecard windows: %+v", sc.Windows)
	}
	w7, w30 := sc.Windows[0], sc.Windows[1]
	if w7.Grade != "A" || w7.OnTimeHandoverRate == nil || *w7.OnTimeHandoverRate != 0.97 || w7.FeedFreshnessP95Minutes == nil {
		t.Fatalf("unexpected 7d window: %+v", w7)
	}
	if w30.Grade != "PROVISIONAL" || w30.OnTimeHandoverRate != nil || w30.LateAckRate != nil {
		t.Fatalf("an unobserved window must decode null rates as nil pointers: %+v", w30)
	}
}

func TestVendorConnectivity_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	c := NewVendorConnectivity(Config{Endpoint: up.URL})
	ctx := context.Background()

	if _, err := c.GetVendor(ctx, "vend-none"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("vendor-not-found must classify as upstream-not-found, got %v", err)
	}
	if _, err := c.GetPurchaseOrder(ctx, "vend-1", "PO-none"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("po-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestVendorConnectivity_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	c := NewVendorConnectivity(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetVendor(ctx, "vend-1")
	_, _ = c.GetVendorNode(ctx, "vend-1", "node-1")
	_, _ = c.ListVendorNodes(ctx, "vend-1")
	_, _ = c.GetPurchaseOrder(ctx, "vend-1", "PO-1")
	_, _ = c.PollPurchaseOrders(ctx, ports.VendorPollQuery{VendorID: "vend-1"})
	_, _ = c.GetInboundShipment(ctx, "vend-1", "shp-1")
	_, _ = c.PollInboundShipments(ctx, ports.VendorPollQuery{VendorID: "vend-1"})
	_, _ = c.GetTransaction(ctx, "vend-1", "tx-1")
	_, _ = c.GetNodeScorecard(ctx, "vend-1", "node-1")

	assertOnlyPinnedToolsCalled(t, up, wantVendorConnectivityTools)
}

func TestVendorConnectivity_UnreachableUpstream(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	up.Close()
	c := NewVendorConnectivity(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetVendor(ctx, "vend-1"); err == nil {
		t.Error("GetVendor: an unreachable upstream must surface as an error")
	}
	if _, err := c.PollPurchaseOrders(ctx, ports.VendorPollQuery{VendorID: "vend-1"}); err == nil {
		t.Error("PollPurchaseOrders: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetInboundShipment(ctx, "vend-1", "shp-1"); err == nil {
		t.Error("GetInboundShipment: an unreachable upstream must surface as an error")
	}
}

func TestVendorConnectivity_RespectsCallerContext(t *testing.T) {
	up := newVendorConnectivityTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewVendorConnectivity(Config{Endpoint: up.URL}).GetVendor(ctx, "vend-1"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
