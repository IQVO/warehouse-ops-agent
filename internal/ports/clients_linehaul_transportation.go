// Package ports (this file): the outbound port for the
// linehaul-transportation bounded context (retail network), the fleet's
// owner of the departures that move packages between network nodes.
//
// linehaul-transportation's MCP server is READ-ONLY (its own governance
// test pins its two tools, every one annotated readOnlyHint). This port
// mirrors both: get_departure and list_departures. linehaul's writes
// (book a departure, cancel one) are upstream of it and have no method
// here. The zero-write fitness test (internal/architecture/zerowrite)
// fails the build if the mcpclient adapter ever names a write-verb tool,
// and the contract snapshot test in mcpclient pins the published tools
// against linehaul-transportation's published registry.
package ports

import (
	"context"
)

// LinehaulTransportationClient is the outbound port for
// linehaul-transportation's two published read tools.
type LinehaulTransportationClient interface {
	// GetDeparture calls get_departure: one departure by id.
	GetDeparture(ctx context.Context, departureID string) (LinehaulDeparture, error)
	// ListDepartures calls list_departures: the departure horizon by
	// cutoff, one page at a time. Zero query fields are omitted so
	// linehaul-transportation applies its own defaults (every lane, the
	// default page size).
	ListDepartures(ctx context.Context, query LinehaulDepartureQuery) (LinehaulDeparturePage, error)
}

// LinehaulDepartureQuery filters list_departures: an optional origin
// node, destination node and cutoff window, and an optional page size
// (1..1000; zero means the default of 500).
type LinehaulDepartureQuery struct {
	Origin      string
	Destination string
	From        string // earliest cutoffAt, inclusive
	To          string // latest cutoffAt, exclusive
	Limit       int
}

// --- linehaul-transportation read-model DTOs ---------------------------------
//
// Field-for-field mirrors of linehaul-transportation's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/linehaul_transportation_tools.golden.json.

// LinehaulDeparture mirrors departureView (get_departure and each
// list_departures item). Kind is PICKUP, LINEHAUL or AIR; state is as
// linehaul-transportation publishes it (SCHEDULED, DEPARTED, ARRIVED,
// CANCELLED).
type LinehaulDeparture struct {
	DepartureID       string `json:"departureId"`
	LaneID            string `json:"laneId"`
	Kind              string `json:"kind"`
	OriginNodeID      string `json:"originNodeId"`
	DestinationNodeID string `json:"destinationNodeId"`
	CutoffAt          string `json:"cutoffAt"`
	DepartsAt         string `json:"departsAt"`
	ArrivesAt         string `json:"arrivesAt"`
	CapacityPackages  int    `json:"capacityPackages"`
	BookedPackages    int    `json:"bookedPackages"`
	RemainingPackages int    `json:"remainingPackages"`
	State             string `json:"state"`
	DepartedAt        string `json:"departedAt,omitempty"`
	ArrivedAt         string `json:"arrivedAt,omitempty"`
	CancelledAt       string `json:"cancelledAt,omitempty"`
	CancelReason      string `json:"cancelReason,omitempty"`
}

// LinehaulDeparturePage mirrors the list_departures result.
type LinehaulDeparturePage struct {
	Departures []LinehaulDeparture `json:"departures"`
}
