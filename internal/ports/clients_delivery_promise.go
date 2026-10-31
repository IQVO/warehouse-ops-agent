// Package ports (this file): the outbound port for the delivery-promise
// bounded context (retail network), the fleet's owner of delivery
// offers, promises and the per-cutoff capacity ledger books.
//
// delivery-promise's MCP server is READ-ONLY (its own governance test
// pins its five tools, every one annotated readOnlyHint). This port
// mirrors all five: get_current_policy, get_delivery_offer, get_promise,
// list_promise_books and list_promises_by_order. delivery-promise's
// writes (quote an offer, confirm/revise/release a promise, publish a
// policy) are upstream of it and have no method here. The zero-write
// fitness test (internal/architecture/zerowrite) fails the build if the
// mcpclient adapter ever names a write-verb tool, and the contract
// snapshot test in mcpclient pins the published tools against
// delivery-promise's published registry.
package ports

import (
	"context"
)

// DeliveryPromiseClient is the outbound port for delivery-promise's five
// published read tools.
type DeliveryPromiseClient interface {
	// GetCurrentPolicy calls get_current_policy: the promise policy in
	// force (v0 is the built-in default when nothing was published).
	GetCurrentPolicy(ctx context.Context) (PromisePolicy, error)
	// GetDeliveryOffer calls get_delivery_offer: one offer in any state.
	GetDeliveryOffer(ctx context.Context, offerID string) (DeliveryOffer, error)
	// GetPromise calls get_promise: one promise in any state, with its
	// terms, witness path, revisions and judgement.
	GetPromise(ctx context.Context, promiseID string) (DeliveryPromise, error)
	// ListPromiseBooks calls list_promise_books: the capacity ledger
	// books of one node whose pickup cutoff falls on one date.
	ListPromiseBooks(ctx context.Context, nodeID, date string) (PromiseBooks, error)
	// ListPromisesByOrder calls list_promises_by_order: the promises of
	// one customer order, one per shipment group.
	ListPromisesByOrder(ctx context.Context, orderID string) (PromisesByOrder, error)
}

// --- delivery-promise read-model DTOs ----------------------------------------
//
// Field-for-field mirrors of delivery-promise's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/delivery_promise_tools.golden.json.

// PromisePolicy mirrors policyView (get_current_policy): offer and hold
// TTLs, the package cap, the network's capacity share and the order
// deadlines per tier and region.
type PromisePolicy struct {
	PolicyVersion        string                 `json:"policyVersion"`
	Version              int                    `json:"version"`
	OfferTtlMinutes      int                    `json:"offerTtlMinutes"`
	HoldTtlMinutes       int                    `json:"holdTtlMinutes"`
	PackagesPerOrder     int                    `json:"packagesPerOrder"`
	CapacitySharePercent int                    `json:"capacitySharePercent"`
	OrderDeadlines       []PromiseOrderDeadline `json:"orderDeadlines"`
	PublishedAt          string                 `json:"publishedAt"`
}

// PromiseOrderDeadline mirrors one tier's order deadline: the local
// time-of-day an order must be placed by, optionally per region.
type PromiseOrderDeadline struct {
	Tier      string `json:"tier"`
	LocalTime string `json:"localTime"`
	RegionID  string `json:"regionId,omitempty"`
}

// PromiseLine mirrors one line of an offer or promise: a catalogue item
// and the quantity promised.
type PromiseLine struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

// DeliveryWindow mirrors the promised delivery window.
type DeliveryWindow struct {
	Earliest string `json:"earliest"`
	Latest   string `json:"latest"`
}

// PromiseWitnessPath mirrors the witness path that proves an offer or
// promise feasible: the fulfilment node, the leg ids in order, and the
// optional pickup departure and delivery wave it rides on.
type PromiseWitnessPath struct {
	NodeID            string   `json:"nodeId"`
	Legs              []string `json:"legs"`
	DeliveryWaveID    string   `json:"deliveryWaveId,omitempty"`
	PickupDepartureID string   `json:"pickupDepartureId,omitempty"`
}

// DeliveryOffer mirrors offerView (get_delivery_offer). State is QUOTED,
// EXPIRED or CONSUMED.
type DeliveryOffer struct {
	OfferID                 string             `json:"offerId"`
	State                   string             `json:"state"`
	GroupNo                 int                `json:"groupNo"`
	DestinationPostalPrefix string             `json:"destinationPostalPrefix"`
	Tier                    string             `json:"tier"`
	Lines                   []PromiseLine      `json:"lines"`
	Window                  DeliveryWindow     `json:"window"`
	WitnessPath             PromiseWitnessPath `json:"witnessPath"`
	Basis                   string             `json:"basis"`
	PolicyVersion           string             `json:"policyVersion"`
	QuotedAt                string             `json:"quotedAt"`
	ExpiresAt               string             `json:"expiresAt"`
}

// PromiseTerms mirrors the terms of a promise: tier, shipBy, the
// deliver-by window, basis and the policy version they were quoted
// under.
type PromiseTerms struct {
	Tier              string `json:"tier"`
	ShipBy            string `json:"shipBy"`
	DeliverByEarliest string `json:"deliverByEarliest"`
	DeliverByLatest   string `json:"deliverByLatest"`
	Basis             string `json:"basis"`
	PolicyVersion     string `json:"policyVersion"`
}

// PromiseRevision mirrors one revision of a promise: the previous terms,
// why they were revised and when.
type PromiseRevision struct {
	RevisionNo    int          `json:"revisionNo"`
	PreviousTerms PromiseTerms `json:"previousTerms"`
	Reason        string       `json:"reason"`
	RevisedAt     string       `json:"revisedAt"`
}

// PromiseJudgement mirrors a promise's judgement: its outcome and, once
// applicable, the cause, the delivery instant and how late it was.
// LateByMinutes is null until the promise is judged late.
type PromiseJudgement struct {
	Outcome       string `json:"outcome"`
	Cause         string `json:"cause,omitempty"`
	DeliveredAt   string `json:"deliveredAt,omitempty"`
	LateByMinutes *int   `json:"lateByMinutes,omitempty"`
}

// DeliveryPromise mirrors promiseView (get_promise and each
// list_promises_by_order item). State is PROVISIONAL, CONFIRMED,
// AT_RISK, KEPT, BROKEN, RELEASED or CANCELLED.
type DeliveryPromise struct {
	PromiseID               string             `json:"promiseId"`
	OrderID                 string             `json:"orderId,omitempty"`
	OfferID                 string             `json:"offerId"`
	State                   string             `json:"state"`
	GroupNo                 int                `json:"groupNo,omitempty"`
	DestinationPostalPrefix string             `json:"destinationPostalPrefix"`
	HoldExpiresAt           string             `json:"holdExpiresAt"`
	Lines                   []PromiseLine      `json:"lines"`
	Terms                   PromiseTerms       `json:"terms"`
	WitnessPath             PromiseWitnessPath `json:"witnessPath"`
	Revisions               []PromiseRevision  `json:"revisions"`
	Judgement               *PromiseJudgement  `json:"judgement,omitempty"`
	Version                 int                `json:"version"`
	AtRiskReason            string             `json:"atRiskReason,omitempty"`
	CancelReason            string             `json:"cancelReason,omitempty"`
	ReleaseReason           string             `json:"releaseReason,omitempty"`
}

// PromiseBookCapacity mirrors one book's capacity ledger: the node's
// share, the provisional and the confirmed entries against it.
type PromiseBookCapacity struct {
	Share       int `json:"share"`
	Provisional int `json:"provisional"`
	Confirmed   int `json:"confirmed"`
}

// PromiseBook mirrors bookView (one list_promise_books item): one cutoff
// of one node's capacity ledger.
type PromiseBook struct {
	BookID            string              `json:"bookId"`
	NodeID            string              `json:"nodeId"`
	PickupDepartureID string              `json:"pickupDepartureId"`
	CutoffAt          string              `json:"cutoffAt"`
	State             string              `json:"state"`
	Capacity          PromiseBookCapacity `json:"capacity"`
	Version           int                 `json:"version"`
}

// PromiseBooks mirrors the list_promise_books result.
type PromiseBooks struct {
	Items []PromiseBook `json:"items"`
}

// PromisesByOrder mirrors the list_promises_by_order result.
type PromisesByOrder struct {
	Items []DeliveryPromise `json:"items"`
}
