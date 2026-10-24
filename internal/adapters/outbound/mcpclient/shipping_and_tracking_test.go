package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// shippingAndTrackingGoldenPath is a VERBATIM copy of
// shipping-and-tracking's own published tool registry golden
// (shipping-and-tracking: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at shipping-and-tracking origin/develop 522904d). Refresh it with
//
//	git -C ../shipping-and-tracking show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever shipping-and-tracking amends its tool surface; the tests
// below then say exactly which consumer-side assumption broke.
const shippingAndTrackingGoldenPath = "testdata/shipping_and_tracking_tools.golden.json"

// wantShippingAndTrackingTools is every tool shipping-and-tracking
// publishes (one, the verbatim golden); the client calls it.
var wantShippingAndTrackingTools = []string{"get_journey"}

// --- the published contract ------------------------------------------------

func TestShippingAndTracking_GoldenPinsExactlyTheOneReadOnlyTool(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, shippingAndTrackingGoldenPath), "shipping-and-tracking", wantShippingAndTrackingTools)
}

// TestShippingAndTracking_PortDTOsMatchPublishedOutputSchemas walks the
// port DTO against the published output schema.
func TestShippingAndTracking_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, shippingAndTrackingGoldenPath)
	cases := map[string]reflect.Type{
		"get_journey": reflect.TypeFor[ports.PackageJourney](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful shipping-and-tracking stand-in -------------------------

func newShippingAndTrackingTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, shippingAndTrackingGoldenPath)
	return newGoldenUpstream(t, "shipping-and-tracking-test", golden, map[string]goldenFixture{
		"get_journey": func(args map[string]any) (any, *mcp.CallToolResult) {
			if tn, _ := args["trackingNumber"].(string); tn != "RN0123456789ABCDEF" {
				return nil, goldenToolError("tracking-not-found: no package " + tn)
			}
			return goldenJSON(t, `{"trackingNumber":"RN0123456789ABCDEF","state":"IN_TRANSIT","poNumber":"PO-1",
			  "assignmentId":"fa-1","orderId":"ord-3f9c","tier":"STANDARD",
			  "scans":[{"scanId":"scan-1","nodeId":"node-1","scanType":"DEPART","at":"2026-10-25T06:00:00Z","applied":true},
			           {"scanId":"scan-2","nodeId":"node-2","scanType":"ARRIVE","at":"2026-10-25T12:00:00Z","applied":false}],
			  "handedOverAt":"2026-10-24T20:00:00Z"}`), nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestShippingAndTracking_GetJourney_DecodesScans(t *testing.T) {
	up := newShippingAndTrackingTestUpstream(t)
	c := NewShippingAndTracking(Config{Endpoint: up.URL})

	out, err := c.GetJourney(context.Background(), "RN0123456789ABCDEF")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if out.TrackingNumber != "RN0123456789ABCDEF" || out.State != "IN_TRANSIT" || out.PoNumber != "PO-1" ||
		out.AssignmentID != "fa-1" || out.OrderID != "ord-3f9c" || out.Tier != "STANDARD" ||
		out.HandedOverAt != "2026-10-24T20:00:00Z" || out.DeliveredAt != "" {
		t.Fatalf("unexpected journey: %+v", out)
	}
	if len(out.Scans) != 2 || !out.Scans[0].Applied || out.Scans[1].Applied {
		t.Fatalf("unexpected scans: %+v", out.Scans)
	}
	if got := up.lastArgs(); got["trackingNumber"] != "RN0123456789ABCDEF" || len(got) != 1 {
		t.Fatalf("the tracking number must be sent under its published name and nothing else, sent %v", got)
	}
}

func TestShippingAndTracking_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newShippingAndTrackingTestUpstream(t)
	c := NewShippingAndTracking(Config{Endpoint: up.URL})

	_, err := c.GetJourney(context.Background(), "RN0000000000000000")
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("tracking-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestShippingAndTracking_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newShippingAndTrackingTestUpstream(t)
	c := NewShippingAndTracking(Config{Endpoint: up.URL})
	_, _ = c.GetJourney(context.Background(), "RN0123456789ABCDEF")

	assertOnlyPinnedToolsCalled(t, up, wantShippingAndTrackingTools)
}

func TestShippingAndTracking_UnreachableUpstream(t *testing.T) {
	up := newShippingAndTrackingTestUpstream(t)
	up.Close()
	if _, err := NewShippingAndTracking(Config{Endpoint: up.URL}).GetJourney(context.Background(), "RN0123456789ABCDEF"); err == nil {
		t.Error("GetJourney: an unreachable upstream must surface as an error")
	}
}

func TestShippingAndTracking_RespectsCallerContext(t *testing.T) {
	up := newShippingAndTrackingTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewShippingAndTracking(Config{Endpoint: up.URL}).GetJourney(ctx, "RN0123456789ABCDEF"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
