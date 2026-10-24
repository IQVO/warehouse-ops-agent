package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// customerOrderingGoldenPath is a VERBATIM copy of customer-ordering's
// own published tool registry golden
// (customer-ordering: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at customer-ordering origin/develop 618e90d). Refresh it with
//
//	git -C ../customer-ordering show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever customer-ordering amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const customerOrderingGoldenPath = "testdata/customer_ordering_tools.golden.json"

// wantCustomerOrderingTools is every tool customer-ordering publishes
// (three, the verbatim golden); the client calls all three.
var wantCustomerOrderingTools = []string{"get_order", "get_order_tracking", "get_ship_to"}

// --- the published contract ------------------------------------------------

func TestCustomerOrdering_GoldenPinsExactlyTheThreeReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, customerOrderingGoldenPath), "customer-ordering", wantCustomerOrderingTools)
}

// TestCustomerOrdering_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema: each json tag must be a
// published property (no invented field) and each required property must
// be mirrored (no silently dropped field).
func TestCustomerOrdering_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, customerOrderingGoldenPath)
	cases := map[string]reflect.Type{
		"get_order":          reflect.TypeFor[ports.CustomerOrder](),
		"get_order_tracking": reflect.TypeFor[ports.OrderTracking](),
		"get_ship_to":        reflect.TypeFor[ports.ShipToAddress](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful customer-ordering stand-in ---------------------------

func newCustomerOrderingTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, customerOrderingGoldenPath)
	order := goldenJSON(t, `{"orderId":"ord-3f9c","shopperId":"shp-71","status":"IN_FULFILLMENT",
	  "destinationPostalPrefix":"013","placedAt":"2026-10-24T08:00:00Z",
	  "lines":[{"lineNo":1,"catalogItemId":"item-1","quantity":2},{"lineNo":2,"catalogItemId":"item-2","quantity":1}],
	  "groups":[{"groupNo":1,"promiseId":"prm-1","tier":"STANDARD","shipBy":"2026-10-25T10:00:00Z",
	    "deliverByEarliest":"2026-10-26T08:00:00Z","deliverByLatest":"2026-10-27T20:00:00Z",
	    "lineNos":[1,2],"status":"PROMISED"}]}`)
	tracking := goldenJSON(t, `{"orderId":"ord-3f9c","status":"IN_FULFILLMENT",
	  "groups":[{"groupNo":1,"promiseId":"prm-1","status":"SHIPPED","deliverByEarliest":"2026-10-26T08:00:00Z",
	    "deliverByLatest":"2026-10-27T20:00:00Z","promiseVerdict":"ON_TIME","lateByMinutes":null,
	    "packages":[{"trackingNumber":"RN001","state":"IN_TRANSIT","carrier":"FLEET","lastScanAt":"2026-10-25T06:00:00Z","lastScanNodeId":"node-1"}]}]}`)
	shipTo := goldenJSON(t, `{"name":"A. Shopper","line1":"1 Main St","line2":"Suite 2","city":"Austin",
	  "region":"TX","postalCode":"01310","countryCode":"US","phone":"+1-555-0100"}`)
	return newGoldenUpstream(t, "customer-ordering-test", golden, map[string]goldenFixture{
		"get_order": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["orderId"].(string); id != "ord-3f9c" {
				return nil, goldenToolError("order-not-found: no order " + id)
			}
			return order, nil
		},
		"get_order_tracking": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["orderId"].(string); id != "ord-3f9c" {
				return nil, goldenToolError("order-not-found: no order " + id)
			}
			return tracking, nil
		},
		"get_ship_to": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["orderId"].(string); id != "ord-3f9c" {
				return nil, goldenToolError("order-not-found: no order " + id)
			}
			return shipTo, nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestCustomerOrdering_GetOrder_DecodesLinesAndGroups(t *testing.T) {
	up := newCustomerOrderingTestUpstream(t)
	c := NewCustomerOrdering(Config{Endpoint: up.URL})

	order, err := c.GetOrder(context.Background(), "ord-3f9c")
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	wantEq(t, "orderId", order.OrderID, "ord-3f9c")
	wantEq(t, "shopperId", order.ShopperID, "shp-71")
	wantEq(t, "status", order.Status, "IN_FULFILLMENT")
	wantEq(t, "destinationPostalPrefix", order.DestinationPostalPrefix, "013")
	wantEq(t, "placedAt", order.PlacedAt, "2026-10-24T08:00:00Z")
	wantEq(t, "cancelledAt (unset)", order.CancelledAt, "")
	wantEq(t, "len(lines)", len(order.Lines), 2)
	wantEq(t, "lines[0].lineNo", order.Lines[0].LineNo, 1)
	wantEq(t, "lines[0].catalogItemId", order.Lines[0].CatalogItemID, "item-1")
	wantEq(t, "lines[1].quantity", order.Lines[1].Quantity, 1)
	wantEq(t, "len(groups)", len(order.Groups), 1)
	wantEq(t, "groups[0].groupNo", order.Groups[0].GroupNo, 1)
	wantEq(t, "groups[0].promiseId", order.Groups[0].PromiseID, "prm-1")
	wantEq(t, "groups[0].tier", order.Groups[0].Tier, "STANDARD")
	wantEq(t, "len(groups[0].lineNos)", len(order.Groups[0].LineNos), 2)
	wantEq(t, "groups[0].status", order.Groups[0].Status, "PROMISED")
	if got := up.lastArgs(); got["orderId"] != "ord-3f9c" || len(got) != 1 {
		t.Fatalf("the order id must be sent under its published name and nothing else, sent %v", got)
	}
}

func TestCustomerOrdering_GetOrderTracking_DecodesGroupsAndPackages(t *testing.T) {
	up := newCustomerOrderingTestUpstream(t)
	c := NewCustomerOrdering(Config{Endpoint: up.URL})

	tracking, err := c.GetOrderTracking(context.Background(), "ord-3f9c")
	if err != nil {
		t.Fatalf("GetOrderTracking: %v", err)
	}
	if len(tracking.Groups) != 1 {
		t.Fatalf("unexpected tracking groups: %+v", tracking.Groups)
	}
	g := tracking.Groups[0]
	if g.Status != "SHIPPED" || g.PromiseVerdict != "ON_TIME" || g.LateByMinutes != nil || len(g.Packages) != 1 {
		t.Fatalf("unexpected tracked group: %+v", g)
	}
	if p := g.Packages[0]; p.TrackingNumber != "RN001" || p.State != "IN_TRANSIT" || p.Carrier != "FLEET" || p.LastScanNodeID != "node-1" {
		t.Fatalf("unexpected tracked package: %+v", p)
	}
}

func TestCustomerOrdering_GetShipTo_DecodesAddress(t *testing.T) {
	up := newCustomerOrderingTestUpstream(t)
	c := NewCustomerOrdering(Config{Endpoint: up.URL})

	addr, err := c.GetShipTo(context.Background(), "ord-3f9c")
	if err != nil {
		t.Fatalf("GetShipTo: %v", err)
	}
	if addr.Name != "A. Shopper" || addr.Line1 != "1 Main St" || addr.Line2 != "Suite 2" || addr.City != "Austin" ||
		addr.Region != "TX" || addr.PostalCode != "01310" || addr.CountryCode != "US" || addr.Phone != "+1-555-0100" {
		t.Fatalf("unexpected ship-to: %+v", addr)
	}
}

func TestCustomerOrdering_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newCustomerOrderingTestUpstream(t)
	c := NewCustomerOrdering(Config{Endpoint: up.URL})

	_, err := c.GetOrder(context.Background(), "ord-none")
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("order-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestCustomerOrdering_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newCustomerOrderingTestUpstream(t)
	c := NewCustomerOrdering(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetOrder(ctx, "ord-3f9c")
	_, _ = c.GetOrderTracking(ctx, "ord-3f9c")
	_, _ = c.GetShipTo(ctx, "ord-3f9c")

	assertOnlyPinnedToolsCalled(t, up, wantCustomerOrderingTools)
}

// TestCustomerOrdering_UnreachableUpstream is the fail-open-absent-upstream
// path at the client layer: with the upstream gone, every call surfaces an
// ordinary error for the use case to degrade on — never a panic, never a hang.
func TestCustomerOrdering_UnreachableUpstream(t *testing.T) {
	up := newCustomerOrderingTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.
	c := NewCustomerOrdering(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetOrder(ctx, "ord-3f9c"); err == nil {
		t.Error("GetOrder: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetOrderTracking(ctx, "ord-3f9c"); err == nil {
		t.Error("GetOrderTracking: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetShipTo(ctx, "ord-3f9c"); err == nil {
		t.Error("GetShipTo: an unreachable upstream must surface as an error")
	}
}

func TestCustomerOrdering_RespectsCallerContext(t *testing.T) {
	up := newCustomerOrderingTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewCustomerOrdering(Config{Endpoint: up.URL}).GetOrder(ctx, "ord-3f9c"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
