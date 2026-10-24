// Package ports (this file): the outbound port for the order-sourcing
// bounded context (retail network), the fleet's owner of the fulfillment
// assignments that decide which vendor node and path fulfils each
// shipment group of a customer order.
//
// order-sourcing's MCP server is READ-ONLY (its own governance test pins
// its two tools, every one annotated readOnlyHint). This port mirrors
// both: get_assignment and list_assignments. order-sourcing's writes
// (queue, release, lock or ship an assignment) are upstream of it and
// have no method here. The zero-write fitness test
// (internal/architecture/zerowrite) fails the build if the mcpclient
// adapter ever names a write-verb tool, and the contract snapshot test
// in mcpclient pins the published tools against order-sourcing's
// published registry.
package ports

import (
	"context"
)

// OrderSourcingClient is the outbound port for order-sourcing's two
// published read tools.
type OrderSourcingClient interface {
	// GetAssignment calls get_assignment: one fulfillment assignment in
	// full by id.
	GetAssignment(ctx context.Context, assignmentID string) (FulfillmentAssignment, error)
	// ListAssignments calls list_assignments: the assignments by id
	// ascending, one page at a time. Zero query fields are omitted so
	// order-sourcing applies its own defaults (every assignment, the
	// default page size).
	ListAssignments(ctx context.Context, query AssignmentListQuery) (AssignmentPage, error)
}

// AssignmentListQuery filters list_assignments: an optional order, node
// and state, and an optional page size (1..500).
type AssignmentListQuery struct {
	OrderID string
	NodeID  string
	State   string
	Limit   int
}

// --- order-sourcing read-model DTOs ------------------------------------------
//
// Field-for-field mirrors of order-sourcing's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/order_sourcing_tools.golden.json.

// AssignmentPromise mirrors the promise constraint an assignment must
// keep: the promise id, tier, deliver-by-latest, the witness node it was
// quoted against and the revision at quote time.
type AssignmentPromise struct {
	PromiseID       string `json:"promiseId"`
	Tier            string `json:"tier"`
	DeliverByLatest string `json:"deliverByLatest"`
	WitnessNodeID   string `json:"witnessNodeId"`
	RevisionNo      int    `json:"revisionNo"`
}

// AssignmentLine mirrors one line of an assignment.
type AssignmentLine struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
	ListingID     string `json:"listingId,omitempty"`
}

// AssignmentCandidate mirrors the current candidate node and path: the
// node, the pickup departure and its cutoff, the leg ids, the delivery
// wave, the ETA and the cost.
type AssignmentCandidate struct {
	NodeID            string   `json:"nodeId"`
	PickupDepartureID string   `json:"pickupDepartureId"`
	PickupCutoffAt    string   `json:"pickupCutoffAt"`
	Legs              []string `json:"legs"`
	DeliveryWaveID    string   `json:"deliveryWaveId"`
	ETA               string   `json:"eta"`
	Cost              int      `json:"cost"`
}

// AssignmentExclusion mirrors one node excluded from candidacy, with the
// reason.
type AssignmentExclusion struct {
	NodeID string `json:"nodeId"`
	Reason string `json:"reason"`
}

// AssignmentRelease mirrors one release of an assignment to a node: the
// release number, node, when, the required-ship-by it was released
// against, and the outcome once known.
type AssignmentRelease struct {
	ReleaseNo      int    `json:"releaseNo"`
	NodeID         string `json:"nodeId"`
	ReleasedAt     string `json:"releasedAt"`
	RequiredShipBy string `json:"requiredShipBy"`
	Outcome        string `json:"outcome"`
	ReasonCode     string `json:"reasonCode,omitempty"`
}

// FulfillmentAssignment mirrors assignmentView (get_assignment and each
// list_assignments item). State is QUEUED, RELEASED, LOCKED, SHIPPED,
// DELIVERED, UNSOURCEABLE or CANCELLED; Candidate is null once the
// assignment has left QUEUED.
type FulfillmentAssignment struct {
	AssignmentID            string                `json:"assignmentId"`
	OrderID                 string                `json:"orderId"`
	GroupNo                 int                   `json:"groupNo"`
	DestinationPostalPrefix string                `json:"destinationPostalPrefix"`
	State                   string                `json:"state"`
	AssignmentVersion       int                   `json:"assignmentVersion"`
	Promise                 AssignmentPromise     `json:"promise"`
	Lines                   []AssignmentLine      `json:"lines"`
	Releases                []AssignmentRelease   `json:"releases"`
	ExcludedNodes           []AssignmentExclusion `json:"excludedNodes"`
	TrackingNumbers         []string              `json:"trackingNumbers"`
	Candidate               *AssignmentCandidate  `json:"candidate,omitempty"`
	PickupDepartureID       string                `json:"pickupDepartureId,omitempty"`
	LockedAt                string                `json:"lockedAt,omitempty"`
	LockReason              string                `json:"lockReason,omitempty"`
	ReleaseAt               string                `json:"releaseAt,omitempty"`
	ShippedAt               string                `json:"shippedAt,omitempty"`
	DeliveredAt             string                `json:"deliveredAt,omitempty"`
	CancelReason            string                `json:"cancelReason,omitempty"`
}

// AssignmentPage mirrors the list_assignments result.
type AssignmentPage struct {
	Assignments []FulfillmentAssignment `json:"assignments"`
}
