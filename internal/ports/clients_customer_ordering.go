// Package ports (this file): the outbound port for the customer-ordering
// bounded context (retail network), the fleet's owner of the placed
// customer order and its shopper-facing delivery address.
//
// customer-ordering's MCP server is READ-ONLY (its own governance test
// pins its three tools, every one annotated readOnlyHint). This port
// mirrors all three: get_order, get_order_tracking and get_ship_to.
// customer-ordering's writes (place an order, cancel an order or one of
// its groups) happen upstream of it and have no method here. The
// zero-write fitness test (internal/architecture/zerowrite) fails the
// build if the mcpclient adapter ever names a write-verb tool, and the
// contract snapshot test in mcpclient pins the published tools against
// customer-ordering's published registry.
package ports

import (
	"context"
)

// CustomerOrderingClient is the outbound port for customer-ordering's
// three published read tools.
type CustomerOrderingClient interface {
	// GetOrder calls get_order: one placed order with its lines and
	// shipment groups.
	GetOrder(ctx context.Context, orderID string) (CustomerOrder, error)
	// GetOrderTracking calls get_order_tracking: how one order is
	// travelling, per shipment group.
	GetOrderTracking(ctx context.Context, orderID string) (OrderTracking, error)
	// GetShipTo calls get_ship_to: the ship-to delivery address of one
	// order (synthetic PII held only by customer-ordering).
	GetShipTo(ctx context.Context, orderID string) (ShipToAddress, error)
}

// --- customer-ordering read-model DTOs ---------------------------------------
//
// Field-for-field mirrors of customer-ordering's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/customer_ordering_tools.golden.json.

// CustomerOrder mirrors orderView (get_order). Status is PLACED,
// IN_FULFILLMENT, PARTIALLY_SHIPPED, SHIPPED, PARTIALLY_DELIVERED,
// DELIVERED, LOST, RETURNED or CANCELLED.
type CustomerOrder struct {
	OrderID                 string               `json:"orderId"`
	ShopperID               string               `json:"shopperId"`
	Status                  string               `json:"status"`
	DestinationPostalPrefix string               `json:"destinationPostalPrefix"`
	PlacedAt                string               `json:"placedAt"`
	CancelledAt             string               `json:"cancelledAt,omitempty"`
	Lines                   []CustomerOrderLine  `json:"lines"`
	Groups                  []CustomerOrderGroup `json:"groups"`
}

// CustomerOrderLine mirrors one order line: a catalogue item and the
// quantity ordered.
type CustomerOrderLine struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

// CustomerOrderGroup mirrors one shipment group of an order: the promise
// it must keep (promiseId, tier, shipBy and the deliver-by window), the
// lines it carries and its own status.
type CustomerOrderGroup struct {
	GroupNo             int    `json:"groupNo"`
	PromiseID           string `json:"promiseId"`
	Tier                string `json:"tier"`
	ShipBy              string `json:"shipBy"`
	DeliverByEarliest   string `json:"deliverByEarliest"`
	DeliverByLatest     string `json:"deliverByLatest"`
	LineNos             []int  `json:"lineNos"`
	Status              string `json:"status"`
	RevisionNo          int    `json:"revisionNo,omitempty"`
	CancelRequestedAt   string `json:"cancelRequestedAt,omitempty"`
	CancelReason        string `json:"cancelReason,omitempty"`
	CancelRefusedReason string `json:"cancelRefusedReason,omitempty"`
}

// OrderTracking mirrors trackingView (get_order_tracking): one entry per
// shipment group with its promise verdict and its packages.
type OrderTracking struct {
	OrderID string              `json:"orderId"`
	Status  string              `json:"status"`
	Groups  []TrackedOrderGroup `json:"groups"`
}

// TrackedOrderGroup mirrors one tracked shipment group. LateByMinutes is
// null until the promise is judged; the optional timestamps are empty
// until the event they name has happened.
type TrackedOrderGroup struct {
	GroupNo             int              `json:"groupNo"`
	PromiseID           string           `json:"promiseId"`
	Status              string           `json:"status"`
	DeliverByEarliest   string           `json:"deliverByEarliest"`
	DeliverByLatest     string           `json:"deliverByLatest"`
	Packages            []TrackedPackage `json:"packages"`
	PromiseVerdict      string           `json:"promiseVerdict,omitempty"`
	AtRiskReason        string           `json:"atRiskReason,omitempty"`
	LateByMinutes       *int             `json:"lateByMinutes,omitempty"`
	LockedAt            string           `json:"lockedAt,omitempty"`
	ShippedAt           string           `json:"shippedAt,omitempty"`
	LostAt              string           `json:"lostAt,omitempty"`
	ReturnedAt          string           `json:"returnedAt,omitempty"`
	OutcomePackage      string           `json:"outcomePackage,omitempty"`
	UnsourceableReason  string           `json:"unsourceableReason,omitempty"`
	CancelRefusedReason string           `json:"cancelRefusedReason,omitempty"`
}

// TrackedPackage mirrors one package of a tracked group: its carrier,
// state and last scan.
type TrackedPackage struct {
	TrackingNumber  string `json:"trackingNumber"`
	State           string `json:"state,omitempty"`
	Carrier         string `json:"carrier,omitempty"`
	DeliveredAt     string `json:"deliveredAt,omitempty"`
	LastScanAt      string `json:"lastScanAt,omitempty"`
	LastScanNodeID  string `json:"lastScanNodeId,omitempty"`
	ExceptionReason string `json:"exceptionReason,omitempty"`
}

// ShipToAddress mirrors shipToView (get_ship_to): the delivery address of
// one order. It is synthetic PII served only by this tool.
type ShipToAddress struct {
	Name        string `json:"name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2,omitempty"`
	City        string `json:"city"`
	Region      string `json:"region"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone,omitempty"`
}
