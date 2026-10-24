package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// pickFaceReplenishmentGoldenPath is a VERBATIM copy of
// pick-face-replenishment's own published tool registry golden
// (pick-face-replenishment: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at pick-face-replenishment origin/develop 4f175e8). Refresh it with
//
//	git -C ../pick-face-replenishment show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever pick-face-replenishment amends its tool surface; the tests
// below then say exactly which consumer-side assumption broke.
const pickFaceReplenishmentGoldenPath = "testdata/pick_face_replenishment_tools.golden.json"

// wantPickFaceReplenishmentTools is every tool pick-face-replenishment
// publishes (five, the verbatim golden); the client calls all five.
var wantPickFaceReplenishmentTools = []string{"get_policy", "get_replenishment", "list_pick_faces", "list_policies", "list_replenishments"}

// --- the published contract ------------------------------------------------

func TestPickFaceReplenishment_GoldenPinsExactlyTheFiveReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, pickFaceReplenishmentGoldenPath), "pick-face-replenishment", wantPickFaceReplenishmentTools)
}

// TestPickFaceReplenishment_PortDTOsMatchPublishedOutputSchemas walks
// every port DTO against the published output schema.
func TestPickFaceReplenishment_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, pickFaceReplenishmentGoldenPath)
	cases := map[string]reflect.Type{
		"get_policy":          reflect.TypeFor[ports.ReplenishmentPolicy](),
		"get_replenishment":   reflect.TypeFor[ports.ReplenishmentRequest](),
		"list_pick_faces":     reflect.TypeFor[ports.PickFacePage](),
		"list_policies":       reflect.TypeFor[ports.ReplenishmentPolicyPage](),
		"list_replenishments": reflect.TypeFor[ports.ReplenishmentPage](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful pick-face-replenishment stand-in -----------------------

const replenishmentPolicyFixture = `{"site_id":"SITE-1","sku":"SKU-1","min_qty":10,"max_qty":40,"multiple":5,
  "source":"PACK_PROFILE","stale":false,"version":3,"declared_at":"2026-10-01T00:00:00Z"}`

const replenishmentRequestFixture = `{"request_id":"repl-1","site_id":"SITE-1","sku":"SKU-1","forward_bin":"F-01",
  "source_bin":"R-99","quantity":20,"priority":"NORMAL","state":"Requested","policy_min":10,"policy_max":40,
  "requested_at":"2026-10-24T10:00:00Z","version":1}`

func newPickFaceReplenishmentTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, pickFaceReplenishmentGoldenPath)
	return newGoldenUpstream(t, "pick-face-replenishment-test", golden, map[string]goldenFixture{
		"get_policy": func(args map[string]any) (any, *mcp.CallToolResult) {
			if args["site_id"] != "SITE-1" || args["sku"] != "SKU-1" {
				return nil, goldenToolError("policy-not-found: no policy declared for this site and SKU")
			}
			return goldenJSON(t, replenishmentPolicyFixture), nil
		},
		"get_replenishment": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["request_id"].(string); id != "repl-1" {
				return nil, goldenToolError("replenishment-not-found: no request " + id)
			}
			return goldenJSON(t, replenishmentRequestFixture), nil
		},
		"list_policies": func(args map[string]any) (any, *mcp.CallToolResult) {
			if len(args) > 4 {
				return nil, goldenToolError("invalid-policy-filter: only cursor, limit, site_id and sku are published")
			}
			return goldenJSON(t, `{"items":[`+replenishmentPolicyFixture+`],"next_cursor":"SITE-1|SKU-2"}`), nil
		},
		"list_replenishments": func(args map[string]any) (any, *mcp.CallToolResult) {
			if len(args) > 5 {
				return nil, goldenToolError("invalid-request-filter: only cursor, limit, site_id, sku and state are published")
			}
			return goldenJSON(t, `{"items":[`+replenishmentRequestFixture+`]}`), nil
		},
		"list_pick_faces": func(args map[string]any) (any, *mcp.CallToolResult) {
			if args["site_id"] != "SITE-1" {
				return nil, goldenToolError("invalid-site: site_id must name a site")
			}
			return goldenJSON(t, `{"items":[{"site_id":"SITE-1","forward_bin":"F-01","sku":"SKU-1","on_hand":8,
			  "reserved":2,"available":6,"status":"BELOW_MIN","min_qty":10,"max_qty":40,"open_request_id":"repl-1"}],
			  "next_cursor":"SITE-1|F-02"}`), nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestPickFaceReplenishment_GetPolicy_DecodesAndSendsExactArgs(t *testing.T) {
	up := newPickFaceReplenishmentTestUpstream(t)
	c := NewPickFaceReplenishment(Config{Endpoint: up.URL})

	out, err := c.GetPolicy(context.Background(), "SITE-1", "SKU-1")
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if out.SiteID != "SITE-1" || out.SKU != "SKU-1" || out.MinQty != 10 || out.MaxQty != 40 || out.Multiple != 5 ||
		out.Source != "PACK_PROFILE" || out.Stale || out.Version != 3 {
		t.Fatalf("unexpected policy: %+v", out)
	}
	if got := up.lastArgs(); got["site_id"] != "SITE-1" || got["sku"] != "SKU-1" || len(got) != 2 {
		t.Fatalf("the site and sku must be sent under their published names and nothing else, sent %v", got)
	}
}

func TestPickFaceReplenishment_GetReplenishment_Decodes(t *testing.T) {
	up := newPickFaceReplenishmentTestUpstream(t)
	c := NewPickFaceReplenishment(Config{Endpoint: up.URL})

	out, err := c.GetReplenishment(context.Background(), "repl-1")
	if err != nil {
		t.Fatalf("GetReplenishment: %v", err)
	}
	if out.RequestID != "repl-1" || out.ForwardBin != "F-01" || out.SourceBin != "R-99" || out.Quantity != 20 ||
		out.Priority != "NORMAL" || out.State != "Requested" || out.PolicyMin != 10 || out.PolicyMax != 40 ||
		out.MovedQuantity != 0 || out.CompletedAt != "" {
		t.Fatalf("unexpected request: %+v", out)
	}
}

func TestPickFaceReplenishment_ListPickFaces_DecodesAndSendsRequiredSite(t *testing.T) {
	up := newPickFaceReplenishmentTestUpstream(t)
	c := NewPickFaceReplenishment(Config{Endpoint: up.URL})

	out, err := c.ListPickFaces(context.Background(), ports.PickFaceListQuery{SiteID: "SITE-1", Status: "BELOW_MIN"})
	if err != nil {
		t.Fatalf("ListPickFaces: %v", err)
	}
	if len(out.Items) != 1 || out.NextCursor != "SITE-1|F-02" {
		t.Fatalf("unexpected pick-face page: %+v", out)
	}
	face := out.Items[0]
	if face.ForwardBin != "F-01" || face.OnHand != 8 || face.Reserved != 2 || face.Available != 6 ||
		face.Status != "BELOW_MIN" || face.MinQty == nil || *face.MinQty != 10 || face.OpenRequestID != "repl-1" {
		t.Fatalf("unexpected pick face: %+v", face)
	}
	got := up.lastArgs()
	if got["site_id"] != "SITE-1" || got["status"] != "BELOW_MIN" || len(got) != 2 {
		t.Fatalf("the required site and set status must be sent under their published names, sent %v", got)
	}
}

// TestPickFaceReplenishment_Lists_OmitZeroOptionalFields pins the paging
// contract: the cursor and limit are omitted when unset.
func TestPickFaceReplenishment_Lists_OmitZeroOptionalFields(t *testing.T) {
	up := newPickFaceReplenishmentTestUpstream(t)
	c := NewPickFaceReplenishment(Config{Endpoint: up.URL})
	ctx := context.Background()

	if _, err := c.ListPolicies(ctx, ports.ReplenishmentPolicyListQuery{}); err != nil {
		t.Fatalf("ListPolicies (empty query): %v", err)
	}
	if got := up.lastArgs(); len(got) != 0 {
		t.Fatalf("an empty query must send no argument keys, sent %v", got)
	}
	if _, err := c.ListReplenishments(ctx, ports.ReplenishmentListQuery{SiteID: "SITE-1", SKU: "SKU-1", State: "Requested", Cursor: "c", Limit: 50}); err != nil {
		t.Fatalf("ListReplenishments (full query): %v", err)
	}
	got := up.lastArgs()
	if got["site_id"] != "SITE-1" || got["sku"] != "SKU-1" || got["state"] != "Requested" || got["cursor"] != "c" || got["limit"] != float64(50) || len(got) != 5 {
		t.Fatalf("a set query must send exactly its five published names, sent %v", got)
	}
}

func TestPickFaceReplenishment_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newPickFaceReplenishmentTestUpstream(t)
	c := NewPickFaceReplenishment(Config{Endpoint: up.URL})
	ctx := context.Background()

	if _, err := c.GetPolicy(ctx, "SITE-9", "SKU-9"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("policy-not-found must classify as upstream-not-found, got %v", err)
	}
	if _, err := c.GetReplenishment(ctx, "repl-none"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("replenishment-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestPickFaceReplenishment_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newPickFaceReplenishmentTestUpstream(t)
	c := NewPickFaceReplenishment(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetPolicy(ctx, "SITE-1", "SKU-1")
	_, _ = c.GetReplenishment(ctx, "repl-1")
	_, _ = c.ListPickFaces(ctx, ports.PickFaceListQuery{SiteID: "SITE-1"})
	_, _ = c.ListPolicies(ctx, ports.ReplenishmentPolicyListQuery{})
	_, _ = c.ListReplenishments(ctx, ports.ReplenishmentListQuery{})

	assertOnlyPinnedToolsCalled(t, up, wantPickFaceReplenishmentTools)
}

func TestPickFaceReplenishment_UnreachableUpstream(t *testing.T) {
	up := newPickFaceReplenishmentTestUpstream(t)
	up.Close()
	c := NewPickFaceReplenishment(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetPolicy(ctx, "SITE-1", "SKU-1"); err == nil {
		t.Error("GetPolicy: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListPickFaces(ctx, ports.PickFaceListQuery{SiteID: "SITE-1"}); err == nil {
		t.Error("ListPickFaces: an unreachable upstream must surface as an error")
	}
}

func TestPickFaceReplenishment_RespectsCallerContext(t *testing.T) {
	up := newPickFaceReplenishmentTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewPickFaceReplenishment(Config{Endpoint: up.URL}).GetPolicy(ctx, "SITE-1", "SKU-1"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
