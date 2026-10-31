// Package ports (this file): the NARROW outbound ports the ADR-0026
// network outlooks depend on. Each interface names exactly the methods of
// one already-wired ADR-0025 client that an outlook use case calls; the
// full client interfaces (ports.CustomerOrderingClient &c.) remain the
// adapters' contract — the mcpclient types satisfy both, because these are
// method subsets. The subset keeps the use cases' own test fakes small and
// states, per upstream, which slice of the read surface the outlooks
// actually consume (everything else the clients wire stays available for
// the next use case, ADR 0025's "wired, not yet consumed" posture).
package ports

import (
	"context"
)

// --- the network ORDER outlook (ADR 0026) ------------------------------------

// PlacedOrderReader is the customer-ordering slice the order outlook
// reads: the order itself, its per-group tracking view and its ship-to
// address. Satisfied by the full CustomerOrderingClient.
type PlacedOrderReader interface {
	GetOrder(ctx context.Context, orderID string) (CustomerOrder, error)
	GetOrderTracking(ctx context.Context, orderID string) (OrderTracking, error)
	GetShipTo(ctx context.Context, orderID string) (ShipToAddress, error)
}

// OrderPromiseReader is the delivery-promise slice the order outlook
// reads: the order's promises per shipment group, plus one promise by id
// for a group the order list did not cover. Satisfied by the full
// DeliveryPromiseClient.
type OrderPromiseReader interface {
	GetPromise(ctx context.Context, promiseID string) (DeliveryPromise, error)
	ListPromisesByOrder(ctx context.Context, orderID string) (PromisesByOrder, error)
}

// OrderAssignmentReader is the order-sourcing slice the order outlook
// reads: the fulfillment assignments of the order (list_assignments
// filtered by order id). get_assignment stays unconsumed by the outlook —
// list_assignments already returns the full assignment view. Satisfied by
// the full OrderSourcingClient.
type OrderAssignmentReader interface {
	ListAssignments(ctx context.Context, query AssignmentListQuery) (AssignmentPage, error)
}

// PackageJourneyReader is the shipping-and-tracking slice the order
// outlook reads: the customer-visible journey of one package by tracking
// number. Satisfied by the full ShippingAndTrackingClient.
type PackageJourneyReader interface {
	GetJourney(ctx context.Context, trackingNumber string) (PackageJourney, error)
}

// --- the network NODE outlook (ADR 0026) -------------------------------------

// NetworkNodeReader is the node-topology slice the node outlook reads:
// the node itself and the directed lanes on either side of it. Satisfied
// by the full NodeTopologyClient.
type NetworkNodeReader interface {
	GetNode(ctx context.Context, nodeID string) (NetworkNode, error)
	ListLanes(ctx context.Context, origin, destination string) (NetworkLaneList, error)
}

// NodeScorecardReader is the node-performance slice the node outlook
// reads: the node's rolling 7-day/30-day scorecards. Satisfied by the full
// NodePerformanceClient.
type NodeScorecardReader interface {
	GetScorecard(ctx context.Context, nodeID string) (NodeScorecards, error)
}

// NodeListingReader is the offer-availability slice the node outlook
// reads: what the node sells (list_listings filtered by node) and the
// vendor inventory feed that last backed those listings. Satisfied by the
// full OfferAvailabilityClient.
type NodeListingReader interface {
	ListListings(ctx context.Context, query ListingListQuery) (ListingPage, error)
	GetInventoryFeed(ctx context.Context, feedID string) (InventoryFeed, error)
}

// VendorNodeReader is the vendor-connectivity slice the node outlook
// reads: the node's vendor registration and the vendor-relayed scorecards.
// Satisfied by the full VendorConnectivityClient.
type VendorNodeReader interface {
	GetVendorNode(ctx context.Context, vendorID, nodeID string) (VendorNode, error)
	GetNodeScorecard(ctx context.Context, vendorID, nodeID string) (VendorNodeScorecards, error)
}

// NodeDepartureReader is the linehaul-transportation slice the node
// outlook reads: the departures whose cutoff is still ahead, by origin.
// get_departure stays unconsumed by the outlook. Satisfied by the full
// LinehaulTransportationClient.
type NodeDepartureReader interface {
	ListDepartures(ctx context.Context, query LinehaulDepartureQuery) (LinehaulDeparturePage, error)
}

// StationWaveReader is the last-mile-delivery slice the node outlook
// reads: a delivery station's waves for one service date (the node
// outlook's last-mile context around a DELIVERY_STATION node).
// get_wave_stops and get_route stay unconsumed by the outlook. Satisfied
// by the full LastMileDeliveryClient.
type StationWaveReader interface {
	ListWaves(ctx context.Context, stationNodeID, serviceDate string) (DeliveryWaves, error)
}
