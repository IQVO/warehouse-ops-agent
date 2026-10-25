// Package usecases (this file): NetworkNodeOutlook (ADR 0026) -- assembles
// one fulfilment node's retail-network picture across the ADR-0025
// clients: the node and its operational profile (node-topology), the lanes
// on either side of it, its rolling scorecards (node-performance), what it
// sells and the inventory feed behind that (offer-availability), its vendor
// registration and vendor-relayed scorecards (vendor-connectivity), the
// linehaul departures still ahead of it (linehaul-transportation) and — for
// a delivery station — today's last-mile waves (last-mile-delivery).
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// ErrNodeTopologyNotConfigured is returned when the node outlook has no
// node-topology port (NODE_TOPOLOGY_MCP_ENDPOINT unset): the composition
// root then wires no outlook at all.
var ErrNodeTopologyNotConfigured = errors.New("node-topology is not configured")

// deliveryStationNodeType is node-topology's published node type for a
// last-mile delivery station; only such a node has waves.
const deliveryStationNodeType = "DELIVERY_STATION"

// NetworkNodeOutlookResult is one node outlook. Each section fails
// independently (ADR 0023 fail-open semantics): an unconfigured upstream
// omits its section with "not-configured", a failing call omits it with
// the error, and the rest are still reported.
type NetworkNodeOutlookResult struct {
	NodeID      string
	GeneratedAt time.Time

	Node          OutlookSection[ports.NetworkNode]
	InboundLanes  OutlookSection[ports.NetworkLane]
	OutboundLanes OutlookSection[ports.NetworkLane]
	Scorecards    OutlookSection[ports.NodeScorecard]
	Listings      OutlookSection[ports.Listing]
	InventoryFeed OutlookSection[ports.InventoryFeed]
	Vendor        OutlookSection[ports.VendorNode]
	VendorScores  OutlookSection[ports.VendorScorecardWindow]
	Departures    OutlookSection[ports.LinehaulDeparture]
	Waves         OutlookSection[ports.DeliveryWave]
}

// NetworkNodeOutlook is the ADR-0026 node-side decision-support use case.
// It is read-only and invents nothing: every grade, lane, departure and
// wave is an upstream's published fact; the only inputs are the caller's
// node id and the clock. node-topology is the anchor (it owns the node
// id); the outlook is wired only when it is configured, and every other
// upstream degrades per section.
//
// Only when EVERY attempted section failed does Execute return an error,
// so an unreachable node-topology is never rendered as an all-clear.
type NetworkNodeOutlook struct {
	Topology    ports.NetworkNodeReader
	Performance ports.NodeScorecardReader
	Listings    ports.NodeListingReader
	Vendor      ports.VendorNodeReader
	Departures  ports.NodeDepartureReader
	Waves       ports.StationWaveReader
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Execute builds the outlook for one node id.
func (uc *NetworkNodeOutlook) Execute(ctx context.Context, nodeID string) (NetworkNodeOutlookResult, error) {
	if uc == nil || uc.Topology == nil {
		return NetworkNodeOutlookResult{}, ErrNodeTopologyNotConfigured
	}
	if nodeID == "" {
		return NetworkNodeOutlookResult{}, errors.New("node id is required")
	}
	now := time.Now()
	if uc.Now != nil {
		now = uc.Now()
	}
	res := NetworkNodeOutlookResult{NodeID: nodeID, GeneratedAt: now}

	var firstErr error
	attempted, failed := 0, 0
	note := func(err error) string {
		attempted++
		if err == nil {
			return ""
		}
		failed++
		if firstErr == nil {
			firstErr = err
		}
		return err.Error()
	}

	res.Node = record(note, uc.nodeSection(ctx, nodeID))
	res.InboundLanes = record(note, uc.lanesSection(ctx, "", nodeID, "list_lanes (destination=%s)"))
	res.OutboundLanes = record(note, uc.lanesSection(ctx, nodeID, "", "list_lanes (origin=%s)"))
	res.Scorecards = record(note, uc.scorecardsSection(ctx, nodeID))

	res.Listings = uc.listingsSection(ctx, nodeID)
	res.InventoryFeed = uc.inventoryFeedSection(ctx, res.Listings)
	res.Vendor, res.VendorScores = uc.vendorSections(ctx, res.Node)
	res.Departures = uc.departuresSection(ctx, nodeID, now)
	res.Waves = uc.wavesSection(ctx, res.Node, now)

	if attempted > 0 && failed == attempted {
		return NetworkNodeOutlookResult{}, fmt.Errorf("network node outlook: every attempted section failed: %w", firstErr)
	}
	return res, nil
}

// nodeSection reads the node with its operational profile.
func (uc *NetworkNodeOutlook) nodeSection(ctx context.Context, nodeID string) sectionResult[ports.NetworkNode] {
	section := OutlookSection[ports.NetworkNode]{Items: []ports.NetworkNode{}}
	node, err := uc.Topology.GetNode(ctx, nodeID)
	if err != nil {
		return sectionResult[ports.NetworkNode]{section: section, attempted: true, err: fmt.Errorf("get_node: %w", err)}
	}
	section.Items = append(section.Items, node)
	section.Complete = true
	return sectionResult[ports.NetworkNode]{section: section, attempted: true}
}

// lanesSection reads the directed lanes on one side of the node: origin
// set for outbound, destination set for inbound.
func (uc *NetworkNodeOutlook) lanesSection(ctx context.Context, origin, destination, call string) sectionResult[ports.NetworkLane] {
	section := OutlookSection[ports.NetworkLane]{Items: []ports.NetworkLane{}}
	list, err := uc.Topology.ListLanes(ctx, origin, destination)
	if err != nil {
		node := origin
		if node == "" {
			node = destination
		}
		return sectionResult[ports.NetworkLane]{section: section, attempted: true, err: fmt.Errorf(call+": %w", node, err)}
	}
	section.Items = append(section.Items, list.Lanes...)
	section.Complete = true
	return sectionResult[ports.NetworkLane]{section: section, attempted: true}
}

// scorecardsSection reads the node's rolling 7-day/30-day scorecards.
func (uc *NetworkNodeOutlook) scorecardsSection(ctx context.Context, nodeID string) sectionResult[ports.NodeScorecard] {
	section := OutlookSection[ports.NodeScorecard]{Items: []ports.NodeScorecard{}}
	if uc.Performance == nil {
		section.Omitted = notConfiguredReason("node-performance")
		return sectionResult[ports.NodeScorecard]{section: section}
	}
	scorecards, err := uc.Performance.GetScorecard(ctx, nodeID)
	if err != nil {
		return sectionResult[ports.NodeScorecard]{section: section, attempted: true, err: fmt.Errorf("get_scorecard: %w", err)}
	}
	section.Items = append(section.Items, scorecards.Scorecards...)
	section.Complete = true
	return sectionResult[ports.NodeScorecard]{section: section, attempted: true}
}

// listingsSection scans what the node sells (list_listings filtered by
// node, keyset pages of 500, at most 10 pages).
func (uc *NetworkNodeOutlook) listingsSection(ctx context.Context, nodeID string) OutlookSection[ports.Listing] {
	section := OutlookSection[ports.Listing]{Items: []ports.Listing{}}
	if uc.Listings == nil {
		section.Omitted = notConfiguredReason("offer-availability")
		return section
	}
	items, complete, err := scanInboundPages(func(cursor string) ([]ports.Listing, string, error) {
		page, err := uc.Listings.ListListings(ctx, ports.ListingListQuery{NodeID: nodeID, After: cursor, Limit: networkPageSize})
		if err != nil {
			return nil, "", fmt.Errorf("list_listings: %w", err)
		}
		return page.Listings, page.NextCursor, nil
	})
	if err != nil {
		section.Omitted = err.Error()
		return section
	}
	section.Items = items
	section.Complete = complete
	return section
}

// inventoryFeedSection reads the vendor inventory feed that most recently
// backed the node's listings (each listing carries the feedId its
// availability came from). No feed-bearing listing means the honest
// omission "no feed"; an unreadable listings section means the feed id is
// unknown, which is a different omission.
func (uc *NetworkNodeOutlook) inventoryFeedSection(ctx context.Context, listings OutlookSection[ports.Listing]) OutlookSection[ports.InventoryFeed] {
	section := OutlookSection[ports.InventoryFeed]{Items: []ports.InventoryFeed{}}
	if uc.Listings == nil {
		section.Omitted = notConfiguredReason("offer-availability")
		return section
	}
	if listings.Omitted != "" {
		section.Omitted = "listings unavailable: no inventory feed id is known"
		return section
	}
	feedID := newestFeedID(listings.Items)
	if feedID == "" {
		section.Omitted = "no listing carries an inventory feed: nothing was ever fed for this node"
		return section
	}
	feed, err := uc.Listings.GetInventoryFeed(ctx, feedID)
	if err != nil {
		section.Omitted = fmt.Errorf("get_inventory_feed: %w", err).Error()
		return section
	}
	section.Items = append(section.Items, feed)
	section.Complete = true
	return section
}

// newestFeedID picks the feed id with the freshest asOf among the
// listings' feeds (ties broken by feed id for determinism); "" when no
// listing carries one.
func newestFeedID(listings []ports.Listing) string {
	newest := ""
	newestAsOf := ""
	for _, listing := range listings {
		feedID := listing.Availability.FeedID
		if feedID == "" {
			continue
		}
		asOf := listing.Availability.AsOf
		if newest == "" || asOf > newestAsOf || (asOf == newestAsOf && feedID > newest) {
			newest, newestAsOf = feedID, asOf
		}
	}
	return newest
}

// vendorSections read the node's vendor registration and the
// vendor-relayed scorecards. Both need the vendor id from node-topology's
// own vendorRef: a node that is not registered under a vendor — or a node
// that could not be read at all — omits both sections with why, never an
// error.
func (uc *NetworkNodeOutlook) vendorSections(ctx context.Context, node OutlookSection[ports.NetworkNode]) (OutlookSection[ports.VendorNode], OutlookSection[ports.VendorScorecardWindow]) {
	vendor := OutlookSection[ports.VendorNode]{Items: []ports.VendorNode{}}
	scores := OutlookSection[ports.VendorScorecardWindow]{Items: []ports.VendorScorecardWindow{}}
	if uc.Vendor == nil {
		vendor.Omitted = notConfiguredReason("vendor-connectivity")
		scores.Omitted = notConfiguredReason("vendor-connectivity")
		return vendor, scores
	}
	nodeID, vendorID := nodeVendorRef(node)
	if nodeID == "" {
		vendor.Omitted = "node unavailable: the vendor registration cannot be looked up"
		scores.Omitted = "node unavailable: the vendor scorecards cannot be looked up"
		return vendor, scores
	}
	if vendorID == "" {
		vendor.Omitted = "node is not registered under a vendor"
		scores.Omitted = "node is not registered under a vendor"
		return vendor, scores
	}
	registered, err := uc.Vendor.GetVendorNode(ctx, vendorID, nodeID)
	if err != nil {
		vendor.Omitted = fmt.Errorf("get_vendor_node: %w", err).Error()
	} else {
		vendor.Items = append(vendor.Items, registered)
		vendor.Complete = true
	}
	relayed, err := uc.Vendor.GetNodeScorecard(ctx, vendorID, nodeID)
	if err != nil {
		scores.Omitted = fmt.Errorf("get_node_scorecard: %w", err).Error()
	} else {
		scores.Items = append(scores.Items, relayed.Windows...)
		scores.Complete = true
	}
	return vendor, scores
}

// nodeVendorRef returns the node id and the vendor id node-topology
// published for it; both empty when the node section did not produce a
// node, vendor id empty when the node carries no vendorRef.
func nodeVendorRef(node OutlookSection[ports.NetworkNode]) (nodeID, vendorID string) {
	if len(node.Items) == 0 {
		return "", ""
	}
	item := node.Items[0]
	if item.VendorRef != nil {
		return item.NodeID, item.VendorRef.VendorID
	}
	return item.NodeID, ""
}

// departuresSection reads the linehaul departures whose cutoff is still
// ahead, by origin = the node.
func (uc *NetworkNodeOutlook) departuresSection(ctx context.Context, nodeID string, now time.Time) OutlookSection[ports.LinehaulDeparture] {
	section := OutlookSection[ports.LinehaulDeparture]{Items: []ports.LinehaulDeparture{}}
	if uc.Departures == nil {
		section.Omitted = notConfiguredReason("linehaul-transportation")
		return section
	}
	page, err := uc.Departures.ListDepartures(ctx, ports.LinehaulDepartureQuery{Origin: nodeID, From: now.UTC().Format(time.RFC3339), Limit: networkPageSize})
	if err != nil {
		section.Omitted = fmt.Errorf("list_departures: %w", err).Error()
		return section
	}
	section.Items = append(section.Items, page.Departures...)
	section.Complete = true
	return section
}

// wavesSection reads a delivery station's waves for today's service date
// in the node's own timezone — the last-mile context around the node. A
// node that is not a delivery station omits the section with why; a node
// that could not be read omits it too. list_waves answers an empty list
// for a station and date with no wave, which is the honest "none today".
func (uc *NetworkNodeOutlook) wavesSection(ctx context.Context, node OutlookSection[ports.NetworkNode], now time.Time) OutlookSection[ports.DeliveryWave] {
	section := OutlookSection[ports.DeliveryWave]{Items: []ports.DeliveryWave{}}
	if uc.Waves == nil {
		section.Omitted = notConfiguredReason("last-mile-delivery")
		return section
	}
	nodeID, nodeType, location := nodeWaveContext(node)
	if nodeID == "" {
		section.Omitted = "node unavailable: whether the node is a delivery station is unknown"
		return section
	}
	if nodeType != deliveryStationNodeType {
		section.Omitted = "node is not a delivery station: no last-mile waves serve it"
		return section
	}
	waves, err := uc.Waves.ListWaves(ctx, nodeID, localServiceDate(now, location))
	if err != nil {
		section.Omitted = fmt.Errorf("list_waves: %w", err).Error()
		return section
	}
	section.Items = append(section.Items, waves.Items...)
	section.Complete = true
	return section
}

// nodeWaveContext returns the node id, type and timezone the wave lookup
// keys on; empty id when the node section did not produce a node.
func nodeWaveContext(node OutlookSection[ports.NetworkNode]) (nodeID, nodeType, timezone string) {
	if len(node.Items) == 0 {
		return "", "", ""
	}
	item := node.Items[0]
	return item.NodeID, item.NodeType, item.Timezone
}

// localServiceDate renders now in the node's own timezone as the
// YYYY-MM-DD service date list_waves expects; an unknown timezone falls
// back to UTC (the node outlook never guesses a different calendar).
func localServiceDate(now time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" {
		loc = time.UTC
	}
	return now.In(loc).Format("2006-01-02")
}
