package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// offerAvailabilityGoldenPath is a VERBATIM copy of offer-availability's
// own published tool registry golden
// (offer-availability: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at offer-availability origin/develop cf6368c). Refresh it with
//
//	git -C ../offer-availability show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever offer-availability amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const offerAvailabilityGoldenPath = "testdata/offer_availability_tools.golden.json"

// wantOfferAvailabilityTools is every tool offer-availability publishes
// (four, the verbatim golden); the client calls all four.
var wantOfferAvailabilityTools = []string{"browse_catalog", "get_catalog_item", "get_inventory_feed", "list_listings"}

// --- the published contract ------------------------------------------------

func TestOfferAvailability_GoldenPinsExactlyTheFourReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, offerAvailabilityGoldenPath), "offer-availability", wantOfferAvailabilityTools)
}

// TestOfferAvailability_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema.
func TestOfferAvailability_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, offerAvailabilityGoldenPath)
	cases := map[string]reflect.Type{
		"browse_catalog":     reflect.TypeFor[ports.CatalogPage](),
		"get_catalog_item":   reflect.TypeFor[ports.CatalogItem](),
		"get_inventory_feed": reflect.TypeFor[ports.InventoryFeed](),
		"list_listings":      reflect.TypeFor[ports.ListingPage](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful offer-availability stand-in ----------------------------

const catalogItemFixture = `{"catalogItemId":"item-1","title":"Widget","lengthMm":100,"widthMm":80,"heightMm":50,
  "weightG":900,"hazmat":false,"fragile":true,"state":"ACTIVE",
  "identifiers":{"gtin":"00614141000000"},
  "packaging":[{"level":"CASE","gtin":"10614141000000","unitsPerPack":12,"active":true,
    "childGtin":"00614141000000","grossWeightG":11000,"dimsMm":{"lengthMm":600,"widthMm":400,"heightMm":300}}]}`

func newOfferAvailabilityTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, offerAvailabilityGoldenPath)
	return newGoldenUpstream(t, "offer-availability-test", golden, map[string]goldenFixture{
		"browse_catalog": goldenResult(goldenJSON(t, `{"items":[`+catalogItemFixture+`],"nextCursor":"item-2"}`)),
		"get_catalog_item": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["catalogItemId"].(string); id != "item-1" {
				return nil, goldenToolError("catalog-item-not-found: no item " + id)
			}
			return goldenJSON(t, catalogItemFixture), nil
		},
		"get_inventory_feed": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["feedId"].(string); id != "feed-7" {
				return nil, goldenToolError("feed-not-found: no feed " + id)
			}
			return goldenJSON(t, `{"feedId":"feed-7","nodeId":"node-1","fullUpdate":false,"asOf":"2026-10-24T06:00:00Z",
			  "state":"APPLIED","lineCount":10,"appliedCount":9,"staleCount":1,"zeroedCount":0,
			  "receivedAt":"2026-10-24T06:01:00Z","appliedAt":"2026-10-24T06:02:00Z"}`), nil
		},
		"list_listings": func(args map[string]any) (any, *mcp.CallToolResult) {
			if len(args) > 4 {
				return nil, goldenToolError("invalid-listing-filter: only after, limit, nodeId and catalogItemId are published")
			}
			return goldenJSON(t, `{"listings":[{"listingId":"list-1","catalogItemId":"item-1","nodeId":"node-1",
			  "state":"ACTIVE","availability":{"availableQty":42,"basis":"FEED","asOf":"2026-10-24T06:00:00Z","feedId":"feed-7"}}],
			  "nextCursor":"list-2"}`), nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestOfferAvailability_BrowseCatalog_DecodesPackaging(t *testing.T) {
	up := newOfferAvailabilityTestUpstream(t)
	c := NewOfferAvailability(Config{Endpoint: up.URL})

	out, err := c.BrowseCatalog(context.Background(), ports.CatalogPageQuery{})
	if err != nil {
		t.Fatalf("BrowseCatalog: %v", err)
	}
	wantEq(t, "len(items)", len(out.Items), 1)
	wantEq(t, "nextCursor", out.NextCursor, "item-2")
	it := out.Items[0]
	wantEq(t, "catalogItemId", it.CatalogItemID, "item-1")
	wantEq(t, "title", it.Title, "Widget")
	wantEq(t, "fragile", it.Fragile, true)
	wantEq(t, "hazmat", it.Hazmat, false)
	wantEq(t, "state", it.State, "ACTIVE")
	if it.Identifiers == nil {
		t.Fatal("identifiers must decode")
	}
	wantEq(t, "identifiers.gtin", it.Identifiers.GTIN, "00614141000000")
	wantEq(t, "len(packaging)", len(it.Packaging), 1)
	wantEq(t, "packaging[0].level", it.Packaging[0].Level, "CASE")
	wantEq(t, "packaging[0].unitsPerPack", it.Packaging[0].UnitsPerPack, 12)
	if it.Packaging[0].DimsMm == nil {
		t.Fatal("packaging[0].dimsMm must decode")
	}
	wantEq(t, "packaging[0].dimsMm.lengthMm", it.Packaging[0].DimsMm.LengthMm, 600)
	if got := up.lastArgs(); len(got) != 0 {
		t.Fatalf("an empty query must send no argument keys so offer-availability applies its defaults, sent %v", got)
	}
}

func TestOfferAvailability_GetCatalogItem_SendsExactArgs(t *testing.T) {
	up := newOfferAvailabilityTestUpstream(t)
	c := NewOfferAvailability(Config{Endpoint: up.URL})

	if _, err := c.GetCatalogItem(context.Background(), "item-1"); err != nil {
		t.Fatalf("GetCatalogItem: %v", err)
	}
	if got := up.lastArgs(); got["catalogItemId"] != "item-1" || len(got) != 1 {
		t.Fatalf("the item id must be sent under its published name and nothing else, sent %v", got)
	}
}

func TestOfferAvailability_GetInventoryFeed_DecodesOutcome(t *testing.T) {
	up := newOfferAvailabilityTestUpstream(t)
	c := NewOfferAvailability(Config{Endpoint: up.URL})

	out, err := c.GetInventoryFeed(context.Background(), "feed-7")
	if err != nil {
		t.Fatalf("GetInventoryFeed: %v", err)
	}
	if out.FeedID != "feed-7" || out.State != "APPLIED" || out.LineCount != 10 || out.AppliedCount != 9 ||
		out.StaleCount != 1 || out.ZeroedCount != 0 || out.AppliedAt != "2026-10-24T06:02:00Z" || out.FailureCode != "" {
		t.Fatalf("unexpected feed: %+v", out)
	}
}

func TestOfferAvailability_ListListings_SendsOnlySetFilters(t *testing.T) {
	up := newOfferAvailabilityTestUpstream(t)
	c := NewOfferAvailability(Config{Endpoint: up.URL})
	ctx := context.Background()

	out, err := c.ListListings(ctx, ports.ListingListQuery{NodeID: "node-1", CatalogItemID: "item-1", After: "list-1", Limit: 50})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	got := up.lastArgs()
	if got["nodeId"] != "node-1" || got["catalogItemId"] != "item-1" || got["after"] != "list-1" || got["limit"] != float64(50) || len(got) != 4 {
		t.Fatalf("a set query must send exactly its four published names, sent %v", got)
	}
	if len(out.Listings) != 1 || out.Listings[0].Availability.AvailableQty != 42 || out.Listings[0].Availability.Basis != "FEED" ||
		out.Listings[0].Availability.FeedID != "feed-7" || out.NextCursor != "list-2" {
		t.Fatalf("unexpected listings: %+v", out)
	}
}

func TestOfferAvailability_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newOfferAvailabilityTestUpstream(t)
	c := NewOfferAvailability(Config{Endpoint: up.URL})

	_, err := c.GetCatalogItem(context.Background(), "item-none")
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("catalog-item-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestOfferAvailability_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newOfferAvailabilityTestUpstream(t)
	c := NewOfferAvailability(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.BrowseCatalog(ctx, ports.CatalogPageQuery{})
	_, _ = c.GetCatalogItem(ctx, "item-1")
	_, _ = c.GetInventoryFeed(ctx, "feed-7")
	_, _ = c.ListListings(ctx, ports.ListingListQuery{})

	assertOnlyPinnedToolsCalled(t, up, wantOfferAvailabilityTools)
}

func TestOfferAvailability_UnreachableUpstream(t *testing.T) {
	up := newOfferAvailabilityTestUpstream(t)
	up.Close()
	c := NewOfferAvailability(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.BrowseCatalog(ctx, ports.CatalogPageQuery{}); err == nil {
		t.Error("BrowseCatalog: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetCatalogItem(ctx, "item-1"); err == nil {
		t.Error("GetCatalogItem: an unreachable upstream must surface as an error")
	}
}

func TestOfferAvailability_RespectsCallerContext(t *testing.T) {
	up := newOfferAvailabilityTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewOfferAvailability(Config{Endpoint: up.URL}).GetCatalogItem(ctx, "item-1"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
