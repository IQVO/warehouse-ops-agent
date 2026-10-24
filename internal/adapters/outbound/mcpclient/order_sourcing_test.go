package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// orderSourcingGoldenPath is a VERBATIM copy of order-sourcing's own
// published tool registry golden
// (order-sourcing: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at order-sourcing origin/develop 1421d88). Refresh it with
//
//	git -C ../order-sourcing show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever order-sourcing amends its tool surface; the tests below then
// say exactly which consumer-side assumption broke.
const orderSourcingGoldenPath = "testdata/order_sourcing_tools.golden.json"

// wantOrderSourcingTools is every tool order-sourcing publishes (two,
// the verbatim golden); the client calls both.
var wantOrderSourcingTools = []string{"get_assignment", "list_assignments"}

// --- the published contract ------------------------------------------------

func TestOrderSourcing_GoldenPinsExactlyTheTwoReadOnlyTools(t *testing.T) {
	assertPinnedGoldenSet(t, loadGoldenTools(t, orderSourcingGoldenPath), "order-sourcing", wantOrderSourcingTools)
}

// TestOrderSourcing_PortDTOsMatchPublishedOutputSchemas walks every port
// DTO against the published output schema.
func TestOrderSourcing_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, orderSourcingGoldenPath)
	cases := map[string]reflect.Type{
		"get_assignment":   reflect.TypeFor[ports.FulfillmentAssignment](),
		"list_assignments": reflect.TypeFor[ports.AssignmentPage](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful order-sourcing stand-in --------------------------------

const assignmentFixture = `{"assignmentId":"fa-1","orderId":"ord-3f9c","groupNo":1,"destinationPostalPrefix":"013",
  "state":"QUEUED","assignmentVersion":1,
  "promise":{"promiseId":"prm-1","tier":"STANDARD","deliverByLatest":"2026-10-27T20:00:00Z","witnessNodeId":"node-1","revisionNo":2},
  "lines":[{"lineNo":1,"catalogItemId":"item-1","quantity":2,"listingId":"list-1"}],
  "excludedNodes":[{"nodeId":"node-9","reason":"NO_CAPACITY"}],
  "candidate":{"nodeId":"node-1","pickupDepartureId":"dep-1","pickupCutoffAt":"2026-10-25T08:00:00Z",
    "legs":["lane-1"],"deliveryWaveId":"wave-1","eta":"2026-10-26T08:00:00Z","cost":120},
  "releases":[{"releaseNo":1,"nodeId":"node-2","releasedAt":"2026-10-24T12:00:00Z",
    "requiredShipBy":"2026-10-25T10:00:00Z","outcome":"DECLINED","reasonCode":"NO_CAPACITY"}],
  "trackingNumbers":[]}`

func newOrderSourcingTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, orderSourcingGoldenPath)
	return newGoldenUpstream(t, "order-sourcing-test", golden, map[string]goldenFixture{
		"get_assignment": func(args map[string]any) (any, *mcp.CallToolResult) {
			if id, _ := args["assignmentId"].(string); id != "fa-1" {
				return nil, goldenToolError("assignment-not-found: no assignment " + id)
			}
			return goldenJSON(t, assignmentFixture), nil
		},
		"list_assignments": func(args map[string]any) (any, *mcp.CallToolResult) {
			if len(args) > 4 {
				return nil, goldenToolError("invalid-assignment-filter: only limit, nodeId, orderId and state are published")
			}
			return goldenJSON(t, `{"assignments":[`+assignmentFixture+`]}`), nil
		},
	})
}

// --- behaviour over the wire --------------------------------------------------

func TestOrderSourcing_GetAssignment_DecodesCandidatePromiseAndReleases(t *testing.T) {
	up := newOrderSourcingTestUpstream(t)
	c := NewOrderSourcing(Config{Endpoint: up.URL})

	out, err := c.GetAssignment(context.Background(), "fa-1")
	if err != nil {
		t.Fatalf("GetAssignment: %v", err)
	}
	wantEq(t, "assignmentId", out.AssignmentID, "fa-1")
	wantEq(t, "state", out.State, "QUEUED")
	wantEq(t, "assignmentVersion", out.AssignmentVersion, 1)
	wantEq(t, "groupNo", out.GroupNo, 1)
	wantEq(t, "promise.promiseId", out.Promise.PromiseID, "prm-1")
	wantEq(t, "promise.tier", out.Promise.Tier, "STANDARD")
	wantEq(t, "promise.witnessNodeId", out.Promise.WitnessNodeID, "node-1")
	wantEq(t, "promise.revisionNo", out.Promise.RevisionNo, 2)
	if out.Candidate == nil {
		t.Fatal("candidate must decode")
	}
	wantEq(t, "candidate.nodeId", out.Candidate.NodeID, "node-1")
	wantEq(t, "candidate.cost", out.Candidate.Cost, 120)
	wantEq(t, "len(candidate.legs)", len(out.Candidate.Legs), 1)
	wantEq(t, "len(releases)", len(out.Releases), 1)
	wantEq(t, "releases[0].outcome", out.Releases[0].Outcome, "DECLINED")
	wantEq(t, "releases[0].reasonCode", out.Releases[0].ReasonCode, "NO_CAPACITY")
	wantEq(t, "len(excludedNodes)", len(out.ExcludedNodes), 1)
	wantEq(t, "excludedNodes[0].reason", out.ExcludedNodes[0].Reason, "NO_CAPACITY")
	if got := up.lastArgs(); got["assignmentId"] != "fa-1" || len(got) != 1 {
		t.Fatalf("the assignment id must be sent under its published name and nothing else, sent %v", got)
	}
}

// TestOrderSourcing_ListAssignments_OmitsZeroQueryFields pins the query
// contract: an empty query sends NO argument keys.
func TestOrderSourcing_ListAssignments_OmitsZeroQueryFields(t *testing.T) {
	up := newOrderSourcingTestUpstream(t)
	c := NewOrderSourcing(Config{Endpoint: up.URL})
	ctx := context.Background()

	if _, err := c.ListAssignments(ctx, ports.AssignmentListQuery{}); err != nil {
		t.Fatalf("ListAssignments (empty query): %v", err)
	}
	if got := up.lastArgs(); len(got) != 0 {
		t.Fatalf("an empty query must send no argument keys so order-sourcing applies its defaults, sent %v", got)
	}
	if _, err := c.ListAssignments(ctx, ports.AssignmentListQuery{OrderID: "ord-3f9c", NodeID: "node-1", State: "QUEUED", Limit: 10}); err != nil {
		t.Fatalf("ListAssignments (full query): %v", err)
	}
	got := up.lastArgs()
	if got["orderId"] != "ord-3f9c" || got["nodeId"] != "node-1" || got["state"] != "QUEUED" || got["limit"] != float64(10) || len(got) != 4 {
		t.Fatalf("a set query must send exactly its four published names, sent %v", got)
	}
}

func TestOrderSourcing_NotFoundClassifiesAsUpstreamNotFound(t *testing.T) {
	up := newOrderSourcingTestUpstream(t)
	c := NewOrderSourcing(Config{Endpoint: up.URL})

	_, err := c.GetAssignment(context.Background(), "fa-none")
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("assignment-not-found must classify as upstream-not-found, got %v", err)
	}
}

func TestOrderSourcing_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newOrderSourcingTestUpstream(t)
	c := NewOrderSourcing(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetAssignment(ctx, "fa-1")
	_, _ = c.ListAssignments(ctx, ports.AssignmentListQuery{})

	assertOnlyPinnedToolsCalled(t, up, wantOrderSourcingTools)
}

func TestOrderSourcing_UnreachableUpstream(t *testing.T) {
	up := newOrderSourcingTestUpstream(t)
	up.Close()
	c := NewOrderSourcing(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetAssignment(ctx, "fa-1"); err == nil {
		t.Error("GetAssignment: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListAssignments(ctx, ports.AssignmentListQuery{}); err == nil {
		t.Error("ListAssignments: an unreachable upstream must surface as an error")
	}
}

func TestOrderSourcing_RespectsCallerContext(t *testing.T) {
	up := newOrderSourcingTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewOrderSourcing(Config{Endpoint: up.URL}).GetAssignment(ctx, "fa-1"); err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
}
