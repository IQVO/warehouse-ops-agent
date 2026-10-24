// Package ports (this file): the outbound port for the
// shipping-and-tracking bounded context (retail network), the fleet's
// owner of the customer-visible package journey.
//
// shipping-and-tracking's MCP server is READ-ONLY (its own governance
// test pins its one tool, annotated readOnlyHint). This port mirrors
// it: get_journey. shipping-and-tracking's writes (label a package,
// hand it over) are upstream of it and have no method here. The
// zero-write fitness test (internal/architecture/zerowrite) fails the
// build if the mcpclient adapter ever names a write-verb tool, and the
// contract snapshot test in mcpclient pins the published tool against
// shipping-and-tracking's published registry.
package ports

import (
	"context"
)

// ShippingAndTrackingClient is the outbound port for
// shipping-and-tracking's single published read tool.
type ShippingAndTrackingClient interface {
	// GetJourney calls get_journey: the customer-visible journey of one
	// package by tracking number.
	GetJourney(ctx context.Context, trackingNumber string) (PackageJourney, error)
}

// --- shipping-and-tracking read-model DTOs -----------------------------------
//
// Field-for-field mirrors of shipping-and-tracking's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/shipping_and_tracking_tools.golden.json.

// JourneyScan mirrors one scan of the journey: where and when it
// happened, its type, and whether it was actually applied (a scan seen
// but rolled back keeps applied false).
type JourneyScan struct {
	ScanID   string `json:"scanId"`
	NodeID   string `json:"nodeId"`
	ScanType string `json:"scanType"`
	At       string `json:"at"`
	Applied  bool   `json:"applied"`
}

// PackageJourney mirrors journeyView (get_journey). State is LABELLED,
// HANDED_OVER, IN_TRANSIT, AT_STATION, OUT_FOR_DELIVERY, DELIVERED,
// RETURNED, LOST or VOIDED.
type PackageJourney struct {
	TrackingNumber string        `json:"trackingNumber"`
	State          string        `json:"state"`
	PoNumber       string        `json:"poNumber"`
	AssignmentID   string        `json:"assignmentId"`
	OrderID        string        `json:"orderId"`
	Tier           string        `json:"tier"`
	Scans          []JourneyScan `json:"scans"`
	HandedOverAt   string        `json:"handedOverAt,omitempty"`
	DeliveredAt    string        `json:"deliveredAt,omitempty"`
}
