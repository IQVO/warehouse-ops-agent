// Package ports (this file): the outbound port for the delivery-promise
// bounded context (retail network), the fleet's owner of delivery
// offers, promises and the per-cutoff capacity ledger books.
//
// delivery-promise's MCP server is READ-ONLY (its own governance test
// pins its six tools, every one annotated readOnlyHint). This port
// mirrors all six: estimate_delivery, get_current_policy,
// get_delivery_offer, get_promise, list_promise_books and
// list_promises_by_order. delivery-promise's
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

// EstimateDeliveryQuery is the argument set of estimate_delivery: one
// destination postal prefix, up to 50 items (each catalogue item once)
// and an optional subset of tiers. An empty Tiers slice is omitted so
// delivery-promise quotes all four of SAME_DAY, NEXT_DAY, TWO_DAY and
// STANDARD (CONSOLIDATED is quoted on REST and refused by the tool).
type EstimateDeliveryQuery struct {
	DestinationPostalPrefix string
	Items                   []EstimateDeliveryItem
	Tiers                   []string
}

// EstimateDeliveryItem is one item of an estimate: a catalogue item and
// the quantity to estimate (1 to 999).
type EstimateDeliveryItem struct {
	CatalogItemID string
	Quantity      int
}

// DeliveryPromiseClient is the outbound port for delivery-promise's six
// published read tools.
type DeliveryPromiseClient interface {
	// EstimateDelivery calls estimate_delivery: the advisory delivery
	// options of up to 50 catalogue items for one destination postal
	// prefix, per item and tier. Nothing is stored: no offer, no hold.
	EstimateDelivery(ctx context.Context, query EstimateDeliveryQuery) (DeliveryEstimates, error)
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
// TTLs, the package cap, the network's capacity share, the calibration
// buffer parameters and the order deadlines per tier and region.
type PromisePolicy struct {
	PolicyVersion                  string                 `json:"policyVersion"`
	Version                        int                    `json:"version"`
	OfferTtlMinutes                int                    `json:"offerTtlMinutes"`
	HoldTtlMinutes                 int                    `json:"holdTtlMinutes"`
	PackagesPerOrder               int                    `json:"packagesPerOrder"`
	CapacitySharePercent           int                    `json:"capacitySharePercent"`
	TransitWindowDays              int                    `json:"transitWindowDays"`
	TransitServiceLevelPercent     int                    `json:"transitServiceLevelPercent"`
	MinTransitObservations         int                    `json:"minTransitObservations"`
	TargetOnTimeProbabilityPercent int                    `json:"targetOnTimeProbabilityPercent"`
	MinCalibrationObservations     int                    `json:"minCalibrationObservations"`
	CalibrationWindowDays          int                    `json:"calibrationWindowDays"`
	MaxCalibrationBufferMinutes    int                    `json:"maxCalibrationBufferMinutes"`
	OrderDeadlines                 []PromiseOrderDeadline `json:"orderDeadlines"`
	PublishedAt                    string                 `json:"publishedAt"`
}

// PromiseOrderDeadline mirrors one tier's order deadline: the local
// time-of-day an order must be placed by, optionally per region.
type PromiseOrderDeadline struct {
	Tier      string `json:"tier"`
	LocalTime string `json:"localTime"`
	RegionID  string `json:"regionId,omitempty"`
}

// PromiseLine mirrors one line of an offer or promise: a catalogue item,
// the quantity promised and the owning seller's listing reference when
// the line is owned by a seller.
type PromiseLine struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
	OwnerRef      string `json:"ownerRef,omitempty"`
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
// EXPIRED or CONSUMED. Confidence is nil while delivery-promise has too
// few judgements of the segment to calibrate (unknown, not zero), and
// ConsolidationEligible is nil unless the tier is CONSOLIDATED;
// capacityBand is a quote-time reading and is never stored.
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
	Confidence              *float64           `json:"confidence,omitempty"`
	ConsolidationEligible   *bool              `json:"consolidationEligible,omitempty"`
	CapacityBand            string             `json:"capacityBand,omitempty"`
	PolicyVersion           string             `json:"policyVersion"`
	QuotedAt                string             `json:"quotedAt"`
	ExpiresAt               string             `json:"expiresAt"`
	OrderByAt               string             `json:"orderByAt"`
}

// PromiseTerms mirrors the terms of a promise: tier, shipBy, the
// deliver-by window, basis, the policy version they were quoted under,
// and the optional calibrated confidence and consolidation flag.
// Confidence is nil while unknown (not zero); ConsolidationEligible is
// nil unless the tier is CONSOLIDATED.
type PromiseTerms struct {
	Tier                  string   `json:"tier"`
	ShipBy                string   `json:"shipBy"`
	DeliverByEarliest     string   `json:"deliverByEarliest"`
	DeliverByLatest       string   `json:"deliverByLatest"`
	Basis                 string   `json:"basis"`
	Confidence            *float64 `json:"confidence,omitempty"`
	ConsolidationEligible *bool    `json:"consolidationEligible,omitempty"`
	PolicyVersion         string   `json:"policyVersion"`
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

// DeliveryEstimateWindow mirrors one estimate option's delivery window:
// the start and end instants and the delivery station's IANA time zone.
type DeliveryEstimateWindow struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	TimeZone string `json:"timeZone"`
}

// DeliveryEstimateOption mirrors one tier's best option of an estimate:
// the tier, its window, the basis, the optional calibrated confidence
// (nil means unknown, not zero), the latest order-by instant and the
// optional capacityBand (AMPLE, TIGHT or SCARCE; omitted when unknown).
type DeliveryEstimateOption struct {
	Tier         string                 `json:"tier"`
	Window       DeliveryEstimateWindow `json:"window"`
	Basis        string                 `json:"basis"`
	Confidence   *float64               `json:"confidence,omitempty"`
	OrderByAt    string                 `json:"orderByAt,omitempty"`
	CapacityBand string                 `json:"capacityBand,omitempty"`
}

// DeliveryEstimateConstraint mirrors one tier without an option: the
// constraint code (DESTINATION_NOT_SERVED, ITEM_QUANTITY_NOT_AVAILABLE,
// DELIVERY_SLA_UNAVAILABLE, NODE_CAPACITY_EXHAUSTED or
// PAST_ORDER_DEADLINE) and the tier it applies to when it is per-tier.
type DeliveryEstimateConstraint struct {
	Code string `json:"code"`
	Tier string `json:"tier,omitempty"`
}

// DeliveryEstimate mirrors one item's estimate (estimate_delivery), in
// request order: the best option of every requested tier and the
// constraints of the tiers without one.
type DeliveryEstimate struct {
	CatalogItemID string                       `json:"catalogItemId"`
	Quantity      int                          `json:"quantity"`
	Options       []DeliveryEstimateOption     `json:"options"`
	Constraints   []DeliveryEstimateConstraint `json:"constraints"`
}

// DeliveryEstimates mirrors the estimate_delivery result: advisory
// only — nothing was stored and no capacity was reserved.
type DeliveryEstimates struct {
	ComputedAt              string             `json:"computedAt"`
	DestinationPostalPrefix string             `json:"destinationPostalPrefix"`
	Estimates               []DeliveryEstimate `json:"estimates"`
}
