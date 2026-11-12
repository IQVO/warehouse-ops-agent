package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// deliveryPromiseGoldenPath is a VERBATIM copy of delivery-promise's own
// published tool registry golden
// (delivery-promise: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at delivery-promise origin/develop a8b587c). Refresh it with
//
//	git -C ../delivery-promise show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever delivery-promise amends its tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const deliveryPromiseGoldenPath = "testdata/delivery_promise_tools.golden.json"

// wantDeliveryPromiseTools is every tool delivery-promise publishes
// (six, the verbatim golden); the client calls all six.
var wantDeliveryPromiseTools = []string{"estimate_delivery", "get_current_policy", "get_delivery_offer", "get_promise", "list_promise_books", "list_promises_by_order"}

// --- the published contract ------------------------------------------------

func TestDeliveryPromise_GoldenPinsExactlyTheSixReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, deliveryPromiseGoldenPath), "delivery-promise", wantDeliveryPromiseTools)
}

// TestDeliveryPromise_PortDTOsMatchPublishedOutputSchemas walks every
// port DTO against the published output schema.
func TestDeliveryPromise_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, deliveryPromiseGoldenPath)
	cases := map[string]reflect.Type{
		"estimate_delivery":      reflect.TypeFor[ports.DeliveryEstimates](),
		"get_current_policy":     reflect.TypeFor[ports.PromisePolicy](),
		"get_delivery_offer":     reflect.TypeFor[ports.DeliveryOffer](),
		"get_promise":            reflect.TypeFor[ports.DeliveryPromise](),
		"list_promise_books":     reflect.TypeFor[ports.PromiseBooks](),
		"list_promises_by_order": reflect.TypeFor[ports.PromisesByOrder](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful delivery-promise stand-in -----------------------------

func newDeliveryPromiseTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, deliveryPromiseGoldenPath)
	policy := goldenJSON(t, `{"policyVersion":"v3","version":3,"offerTtlMinutes":15,"holdTtlMinutes":30,
	  "packagesPerOrder":4,"capacitySharePercent":60,"transitWindowDays":21,"transitServiceLevelPercent":85,"minTransitObservations":15,
	  "targetOnTimeProbabilityPercent":95,"minCalibrationObservations":20,"calibrationWindowDays":14,"maxCalibrationBufferMinutes":240,
	  "orderDeadlines":[{"tier":"STANDARD","localTime":"20:00"},{"tier":"EXPRESS","localTime":"22:00","regionId":"region-1"}],
	  "publishedAt":"2026-10-01T00:00:00Z"}`)
	estimate := goldenJSON(t, `{"computedAt":"2026-10-24T08:00:00Z","destinationPostalPrefix":"013",
	  "estimates":[{"catalogItemId":"item-1","quantity":2,
	    "options":[{"tier":"STANDARD","window":{"start":"2026-10-26T08:00:00Z","end":"2026-10-27T20:00:00Z","timeZone":"America/Chicago"},
	      "basis":"SCHEDULED","confidence":0.97,"orderByAt":"2026-10-24T11:00:00Z","capacityBand":"AMPLE"},
	      {"tier":"EXPRESS","window":{"start":"2026-10-25T08:00:00Z","end":"2026-10-25T20:00:00Z","timeZone":"America/Chicago"},"basis":"NOMINAL"}],
	    "constraints":[{"code":"PAST_ORDER_DEADLINE","tier":"SAME_DAY"}]}]}`)
	offer := goldenJSON(t, `{"offerId":"off-1","state":"QUOTED","groupNo":1,"destinationPostalPrefix":"013","tier":"STANDARD",
	  "lines":[{"catalogItemId":"item-1","quantity":2,"ownerRef":"seller-77/lst-9"}],
	  "window":{"earliest":"2026-10-26T08:00:00Z","latest":"2026-10-27T20:00:00Z"},
	  "witnessPath":{"nodeId":"node-1","legs":["lane-1","lane-2"],"deliveryWaveId":"wave-1","pickupDepartureId":"dep-1"},
	  "basis":"NETWORK","confidence":0.97,"policyVersion":"v3","quotedAt":"2026-10-24T08:00:00Z","expiresAt":"2026-10-24T08:15:00Z","orderByAt":"2026-10-24T11:00:00Z"}`)
	promise := goldenJSON(t, `{"promiseId":"prm-1","orderId":"ord-3f9c","offerId":"off-1","state":"KEPT",
	  "groupNo":1,"destinationPostalPrefix":"013","holdExpiresAt":"2026-10-24T09:00:00Z",
	  "lines":[{"catalogItemId":"item-1","quantity":2}],
	  "terms":{"tier":"STANDARD","shipBy":"2026-10-25T10:00:00Z","deliverByEarliest":"2026-10-26T08:00:00Z",
	    "deliverByLatest":"2026-10-27T20:00:00Z","basis":"NETWORK","confidence":0.97,"policyVersion":"v3"},
	  "witnessPath":{"nodeId":"node-1","legs":[]},
	  "revisions":[{"revisionNo":1,"previousTerms":{"tier":"STANDARD","shipBy":"2026-10-25T08:00:00Z",
	    "deliverByEarliest":"2026-10-26T08:00:00Z","deliverByLatest":"2026-10-27T18:00:00Z","basis":"NETWORK","policyVersion":"v2"},
	    "reason":"POLICY_REPUBLISH","revisedAt":"2026-10-24T10:00:00Z"}],
	  "judgement":{"outcome":"KEPT","cause":"ON_TIME","deliveredAt":"2026-10-27T09:00:00Z","lateByMinutes":null},
	  "version":2}`)
	books := goldenJSON(t, `{"items":[{"bookId":"book-1","nodeId":"node-1","pickupDepartureId":"dep-1",
	  "cutoffAt":"2026-10-25T08:00:00Z","state":"OPEN","capacity":{"share":60,"provisional":1,"confirmed":2},"version":3}]}`)
	return newGoldenUpstream(t, "delivery-promise-test", golden, map[string]goldenFixture{
		"estimate_delivery": func(args map[string]any) (any, *mcp.CallToolResult) {
			if p, _ := args["destinationPostalPrefix"].(string); p != "013" {
				return nil, goldenToolError("estimate-invalid: bad prefix " + p)
			}
			return estimate, nil
		},
		"get_current_policy": goldenResult(policy),
		"get_delivery_offer": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["offerId"].(string); id != "off-1" {
				return nil, goldenToolError("offer-not-found: no offer " + id)
			}
			return offer, nil
		},
		"get_promise": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["promiseId"].(string); id != "prm-1" {
				return nil, goldenToolError("promise-not-found: no promise " + id)
			}
			return promise, nil
		},
		"list_promise_books": func(args map[string]any) (any, *mcp.CallToolResult) {
			if args["nodeId"] != "node-1" || args["date"] != "2026-10-25" {
				return nil, goldenToolError("invalid-book-query: nodeId and date must name a node's cutoff date")
			}
			return books, nil
		},
		"list_promises_by_order": goldenResult(goldenJSON(t, `{"items":[]}`)),
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestDeliveryPromise_GetCurrentPolicy_Decodes(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})

	out, err := c.GetCurrentPolicy(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentPolicy: %v", err)
	}
	if out.PolicyVersion != "v3" || out.Version != 3 || out.OfferTtlMinutes != 15 || out.HoldTtlMinutes != 30 ||
		out.PackagesPerOrder != 4 || out.CapacitySharePercent != 60 || out.PublishedAt != "2026-10-01T00:00:00Z" {
		t.Fatalf("unexpected policy: %+v", out)
	}
	wantEq(t, "targetOnTimeProbabilityPercent", out.TargetOnTimeProbabilityPercent, 95)
	wantEq(t, "minCalibrationObservations", out.MinCalibrationObservations, 20)
	wantEq(t, "calibrationWindowDays", out.CalibrationWindowDays, 14)
	wantEq(t, "maxCalibrationBufferMinutes", out.MaxCalibrationBufferMinutes, 240)
	if len(out.OrderDeadlines) != 2 || out.OrderDeadlines[0].Tier != "STANDARD" || out.OrderDeadlines[0].RegionID != "" ||
		out.OrderDeadlines[1].RegionID != "region-1" {
		t.Fatalf("unexpected order deadlines: %+v", out.OrderDeadlines)
	}
}

func TestDeliveryPromise_GetDeliveryOffer_DecodesWitnessPath(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})

	out, err := c.GetDeliveryOffer(context.Background(), "off-1")
	if err != nil {
		t.Fatalf("GetDeliveryOffer: %v", err)
	}
	if out.OfferID != "off-1" || out.State != "QUOTED" || out.Window.Earliest != "2026-10-26T08:00:00Z" || out.Basis != "NETWORK" {
		t.Fatalf("unexpected offer: %+v", out)
	}
	if len(out.WitnessPath.Legs) != 2 || out.WitnessPath.NodeID != "node-1" || out.WitnessPath.DeliveryWaveID != "wave-1" {
		t.Fatalf("unexpected witness path: %+v", out.WitnessPath)
	}
	if out.Confidence == nil || *out.Confidence != 0.97 || out.Lines[0].OwnerRef != "seller-77/lst-9" || out.CapacityBand != "" {
		t.Fatalf("optional confidence, ownerRef and capacityBand must decode tolerantly: %+v", out)
	}
	if got := up.lastArgs(); got["offerId"] != "off-1" || len(got) != 1 {
		t.Fatalf("the offer id must be sent under its published name and nothing else, sent %v", got)
	}
}

// TestDeliveryPromise_EstimateDelivery_Decodes: the advisory estimate
// decodes per item and tier, and an absent confidence stays nil
// (unknown, not zero).
func TestDeliveryPromise_EstimateDelivery_Decodes(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})

	out, err := c.EstimateDelivery(context.Background(), ports.EstimateDeliveryQuery{
		DestinationPostalPrefix: "013",
		Items:                   []ports.EstimateDeliveryItem{{CatalogItemID: "item-1", Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("EstimateDelivery: %v", err)
	}
	wantEq(t, "computedAt", out.ComputedAt, "2026-10-24T08:00:00Z")
	wantEq(t, "destinationPostalPrefix", out.DestinationPostalPrefix, "013")
	wantEq(t, "len(estimates)", len(out.Estimates), 1)
	first := out.Estimates[0]
	wantEq(t, "estimates[0].catalogItemId", first.CatalogItemID, "item-1")
	wantEq(t, "estimates[0].quantity", first.Quantity, 2)
	wantEq(t, "len(options)", len(first.Options), 2)
	wantEq(t, "len(constraints)", len(first.Constraints), 1)
	wantEq(t, "options[0].tier", first.Options[0].Tier, "STANDARD")
	wantEq(t, "options[0].window.timeZone", first.Options[0].Window.TimeZone, "America/Chicago")
	wantEq(t, "options[0].capacityBand", first.Options[0].CapacityBand, "AMPLE")
	wantEq(t, "options[0].orderByAt", first.Options[0].OrderByAt, "2026-10-24T11:00:00Z")
	if first.Options[0].Confidence == nil || *first.Options[0].Confidence != 0.97 {
		t.Fatalf("confidence must decode, got %+v", first.Options[0].Confidence)
	}
	if first.Options[1].Confidence != nil {
		t.Fatalf("an absent confidence stays nil (unknown, not zero), got %+v", first.Options[1].Confidence)
	}
	wantEq(t, "constraints[0].code", first.Constraints[0].Code, "PAST_ORDER_DEADLINE")
	wantEq(t, "constraints[0].tier", first.Constraints[0].Tier, "SAME_DAY")
}

// TestDeliveryPromise_EstimateDelivery_SendsExactArgs: the arguments are
// sent under their published names only (tiers omitted when empty).
func TestDeliveryPromise_EstimateDelivery_SendsExactArgs(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})

	_, err := c.EstimateDelivery(context.Background(), ports.EstimateDeliveryQuery{
		DestinationPostalPrefix: "013",
		Items:                   []ports.EstimateDeliveryItem{{CatalogItemID: "item-1", Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("EstimateDelivery: %v", err)
	}
	got := up.lastArgs()
	prefix, _ := got["destinationPostalPrefix"].(string)
	wantEq(t, "destinationPostalPrefix", prefix, "013")
	wantEq(t, "len(args)", len(got), 2)
	items, _ := got["items"].([]any)
	wantEq(t, "len(items)", len(items), 1)
	item, _ := items[0].(map[string]any)
	id, _ := item[argCatalogItemID].(string)
	wantEq(t, "items[0].catalogItemId", id, "item-1")
	qty, _ := item["quantity"].(float64)
	wantEq(t, "items[0].quantity", qty, 2.0)
}

func TestDeliveryPromise_GetPromise_DecodesTermsRevisionsAndJudgement(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})

	out, err := c.GetPromise(context.Background(), "prm-1")
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}
	if out.PromiseID != "prm-1" || out.State != "KEPT" || out.Terms.Tier != "STANDARD" || out.Terms.PolicyVersion != "v3" {
		t.Fatalf("unexpected promise: %+v", out)
	}
	if len(out.Revisions) != 1 || out.Revisions[0].RevisionNo != 1 || out.Revisions[0].Reason != "POLICY_REPUBLISH" ||
		out.Revisions[0].PreviousTerms.PolicyVersion != "v2" {
		t.Fatalf("unexpected revisions: %+v", out.Revisions)
	}
	if out.Judgement == nil || out.Judgement.Outcome != "KEPT" || out.Judgement.LateByMinutes != nil {
		t.Fatalf("unexpected judgement: %+v", out.Judgement)
	}
}

func TestDeliveryPromise_ListPromiseBooks_DecodesAndSendsExactArgs(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})

	out, err := c.ListPromiseBooks(context.Background(), "node-1", "2026-10-25")
	if err != nil {
		t.Fatalf("ListPromiseBooks: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].BookID != "book-1" || out.Items[0].Capacity.Share != 60 ||
		out.Items[0].Capacity.Provisional != 1 || out.Items[0].Capacity.Confirmed != 2 || out.Items[0].Version != 3 {
		t.Fatalf("unexpected books: %+v", out.Items)
	}
	if got := up.lastArgs(); got["nodeId"] != "node-1" || got["date"] != "2026-10-25" || len(got) != 2 {
		t.Fatalf("the node and date must be sent under their published names and nothing else, sent %v", got)
	}
}

// TestDeliveryPromise_ListPromisesByOrder_EmptyIsNotAnError: an order
// with no promises answers an empty list, never an error.
func TestDeliveryPromise_ListPromisesByOrder_EmptyIsNotAnError(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})

	out, err := c.ListPromisesByOrder(context.Background(), "ord-none")
	if err != nil {
		t.Fatalf("an order without promises answers an empty list, never an error: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("unexpected promises: %+v", out.Items)
	}
}

func TestDeliveryPromise_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})

	_, err := c.GetPromise(context.Background(), "prm-none")
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("promise-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestDeliveryPromise_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	c := NewDeliveryPromise(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.EstimateDelivery(ctx, ports.EstimateDeliveryQuery{DestinationPostalPrefix: "013",
		Items: []ports.EstimateDeliveryItem{{CatalogItemID: "item-1", Quantity: 2}}})
	_, _ = c.GetCurrentPolicy(ctx)
	_, _ = c.GetDeliveryOffer(ctx, "off-1")
	_, _ = c.GetPromise(ctx, "prm-1")
	_, _ = c.ListPromiseBooks(ctx, "node-1", "2026-10-25")
	_, _ = c.ListPromisesByOrder(ctx, "ord-3f9c")

	assertOnlyPinnedToolsCalled(t, up, wantDeliveryPromiseTools)
}

func TestDeliveryPromise_UnreachableUpstream(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	up.Close()
	c := NewDeliveryPromise(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetCurrentPolicy(ctx); err == nil {
		t.Error("GetCurrentPolicy: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetPromise(ctx, "prm-1"); err == nil {
		t.Error("GetPromise: an unreachable upstream must surface as an error")
	}
}

func TestDeliveryPromise_RespectsCallerContext(t *testing.T) {
	up := newDeliveryPromiseTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewDeliveryPromise(Config{Endpoint: up.URL}).GetPromise(ctx, "prm-1"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
