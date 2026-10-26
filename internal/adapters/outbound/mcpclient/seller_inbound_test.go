package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// sellerInboundGoldenPath is a VERBATIM copy of seller-inbound's own
// published tool registry golden
// (seller-inbound: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at seller-inbound origin/develop 60eb7262). Refresh it with
//
//	git -C ../seller-inbound show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever seller-inbound amends its tool surface; the tests below then
// say exactly which consumer-side assumption broke.
const sellerInboundGoldenPath = "testdata/seller_inbound_tools.golden.json"

// wantSellerInboundTools is every tool seller-inbound publishes (four, the
// verbatim golden); the client calls all four.
var wantSellerInboundTools = []string{"get_inbound_plan", "get_inbound_shipment", "get_seller_scorecard", "get_shipment_labels"}

// --- the published contract ------------------------------------------------

func TestSellerInbound_GoldenPinsExactlyTheFourReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, sellerInboundGoldenPath), "seller-inbound", wantSellerInboundTools)
}

// TestSellerInbound_PortDTOsMatchPublishedOutputSchemas walks every port
// DTO against the published output schema.
func TestSellerInbound_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, sellerInboundGoldenPath)
	cases := map[string]reflect.Type{
		"get_seller_scorecard": reflect.TypeFor[ports.SellerScorecard](),
		"get_inbound_plan":     reflect.TypeFor[ports.InboundPlan](),
		"get_shipment_labels":  reflect.TypeFor[ports.ShipmentLabels](),
		"get_inbound_shipment": reflect.TypeFor[ports.InboundShipment](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful seller-inbound stand-in ------------------------------

const sellerScorecardFixture = `{"sellerId":"seller-77","window":"30d",
  "windowStart":"2026-09-25T00:00:00Z","windowEnd":"2026-10-25T00:00:00Z",
  "shipmentsReceived":9,"cartonsReported":120,"cartonsReceived":118,
  "receiptAccuracyRate":0.983,"discrepancyRatePerShipment":0.22,
  "discrepanciesByKind":{"DAMAGED":1,"SHORTAGE":1},"complianceViolations":2,
  "complianceTier":"WATCH"}`

const planLineFixture = `{"catalogItemId":"cat-1","gtin":"00614141000001","qtyEaches":12}`

const cartonFixture = `{"sscc":"006141410000000017","kind":"CASE",
  "contents":[{"catalogItemId":"cat-1","gtin":"00614141000001","qtyEaches":6,"lot":"L-1","expiry":"2027-01-01"}],
  "grossWeightG":4200,"dimsMm":{"l":600,"w":400,"h":400},"sealed":true,"receiptStatus":"RECEIVED"}`

const labelFixture = `{"labelRef":"lbl-1","sscc":"006141410000000017","gs1128":"(00)006141410000000017",
  "humanReadable":"SSCC 006141410000000017","kind":"CASE",
  "contents":[{"catalogItemId":"cat-1","gtin":"00614141000001","qtyEaches":6}],
  "contentSummary":"6 x cat-1","totalEaches":6,"destinationNodeId":"node-7"}`

func newSellerInboundTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, sellerInboundGoldenPath)
	return newGoldenUpstream(t, "seller-inbound-test", golden, map[string]goldenFixture{
		"get_seller_scorecard": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["sellerId"].(string); id != "seller-77" {
				return nil, goldenToolError("seller-not-found: no seller " + id)
			}
			return goldenJSON(t, sellerScorecardFixture), nil
		},
		"get_inbound_plan": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["planId"].(string); id != "pln-0f3a91" {
				return nil, goldenToolError("plan-not-found: no plan " + id)
			}
			return goldenJSON(t, `{"planId":"pln-0f3a91","sellerId":"seller-77","destinationNodeId":"node-7",
			  "packingOption":"INDIVIDUAL","state":"CONFIRMED","lines":[`+planLineFixture+`],
			  "createdAt":"2026-10-20T10:00:00Z","confirmedAt":"2026-10-20T11:00:00Z"}`), nil
		},
		"get_shipment_labels": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["shipmentId"].(string); id != "shp-0f3a91" {
				return nil, goldenToolError("shipment-not-found: no shipment " + id)
			}
			return goldenJSON(t, `{"shipmentId":"shp-0f3a91","state":"SHIPPED","destinationNodeId":"node-7",
			  "labels":[`+labelFixture+`]}`), nil
		},
		"get_inbound_shipment": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["shipmentId"].(string); id != "shp-0f3a91" {
				return nil, goldenToolError("shipment-not-found: no shipment " + id)
			}
			return goldenJSON(t, `{"shipmentId":"shp-0f3a91","planId":"pln-0f3a91","sellerId":"seller-77",
			  "nodeId":"node-7","state":"RECEIVED","cartons":[`+cartonFixture+`],
			  "createdAt":"2026-10-21T08:00:00Z","labelledAt":"2026-10-21T09:00:00Z",
			  "shippedAt":"2026-10-21T10:00:00Z","receivedAt":"2026-10-22T06:00:00Z"}`), nil
		},
	})
}

// --- the client against the stand-in ----------------------------------------

func TestSellerInbound_CallsOnlyPinnedTools(t *testing.T) {
	up := newSellerInboundTestUpstream(t)
	c := NewSellerInbound(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetSellerScorecard(ctx, "seller-77", ""); err != nil {
		t.Fatalf("scorecard: %v", err)
	}
	if _, err := c.GetInboundPlan(ctx, "pln-0f3a91"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := c.GetShipmentLabels(ctx, "shp-0f3a91"); err != nil {
		t.Fatalf("labels: %v", err)
	}
	if _, err := c.GetInboundShipment(ctx, "shp-0f3a91"); err != nil {
		t.Fatalf("shipment: %v", err)
	}
	assertOnlyPinnedToolsCalled(t, up, wantSellerInboundTools)
}

func TestSellerInbound_EmptyWindowIsOmittedSoTheDefaultApplies(t *testing.T) {
	up := newSellerInboundTestUpstream(t)
	c := NewSellerInbound(Config{Endpoint: up.URL})
	if _, err := c.GetSellerScorecard(context.Background(), "seller-77", ""); err != nil {
		t.Fatalf("scorecard: %v", err)
	}
	got := up.lastArgs()
	if got["sellerId"] != "seller-77" {
		t.Fatalf("the seller id must be sent under its published name, sent %v", got)
	}
	if _, present := got["window"]; present {
		t.Fatalf("an empty window must be omitted so seller-inbound applies its own default, sent %v", got)
	}
}

func TestSellerInbound_DecodesScorecardPlanLabelsAndShipment(t *testing.T) {
	up := newSellerInboundTestUpstream(t)
	c := NewSellerInbound(Config{Endpoint: up.URL})
	ctx := context.Background()

	score, err := c.GetSellerScorecard(ctx, "seller-77", "7d")
	if err != nil {
		t.Fatalf("scorecard: %v", err)
	}
	wantEq(t, "sellerId", score.SellerID, "seller-77")
	wantEq(t, "complianceTier", score.ComplianceTier, "WATCH")
	wantEq(t, "cartonsReceived", score.CartonsReceived, 118)
	if score.ReceiptAccuracyRate == nil || *score.ReceiptAccuracyRate != 0.983 {
		t.Fatalf("receiptAccuracyRate must decode, got %+v", score.ReceiptAccuracyRate)
	}
	wantEq(t, "len(discrepanciesByKind)", len(score.DiscrepanciesByKind), 2)

	plan, err := c.GetInboundPlan(ctx, "pln-0f3a91")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	wantEq(t, "state", plan.State, "CONFIRMED")
	wantEq(t, "len(lines)", len(plan.Lines), 1)
	wantEq(t, "lines[0].qtyEaches", plan.Lines[0].QtyEaches, 12)
	if plan.ConfirmedAt == nil || plan.CancelledAt != nil {
		t.Fatalf("optional instants decoded wrong: confirmed=%v cancelled=%v", plan.ConfirmedAt, plan.CancelledAt)
	}

	labels, err := c.GetShipmentLabels(ctx, "shp-0f3a91")
	if err != nil {
		t.Fatalf("labels: %v", err)
	}
	wantEq(t, "labels.state", labels.State, "SHIPPED")
	wantEq(t, "len(labels.labels)", len(labels.Labels), 1)
	wantEq(t, "labels[0].totalEaches", labels.Labels[0].TotalEaches, 6)

	shipment, err := c.GetInboundShipment(ctx, "shp-0f3a91")
	if err != nil {
		t.Fatalf("shipment: %v", err)
	}
	wantEq(t, "shipment.state", shipment.State, "RECEIVED")
	wantEq(t, "len(cartons)", len(shipment.Cartons), 1)
	wantEq(t, "cartons[0].dimsMm.l", shipment.Cartons[0].DimsMm.L, 600)
	if shipment.ReceivedAt == nil {
		t.Fatal("receivedAt must decode")
	}
}

func TestSellerInbound_NotFoundMapsToErrUpstreamNotFound(t *testing.T) {
	up := newSellerInboundTestUpstream(t)
	c := NewSellerInbound(Config{Endpoint: up.URL})
	if _, err := c.GetInboundPlan(context.Background(), "pln-nope"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("a plan-not-found tool error must map to ports.ErrUpstreamNotFound, got %v", err)
	}
	if _, err := c.GetInboundShipment(context.Background(), "shp-nope"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("a shipment-not-found tool error must map to ports.ErrUpstreamNotFound, got %v", err)
	}
}
