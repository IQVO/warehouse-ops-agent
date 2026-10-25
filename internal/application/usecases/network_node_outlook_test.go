package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// --- fakes for the narrow node-outlook ports ---------------------------------

type fakeNetworkNodeReader struct {
	node    ports.NetworkNode
	nodeErr error
	lanes   map[string]ports.NetworkLaneList // "origin:<id>" / "destination:<id>"
	laneErr error
}

func (f *fakeNetworkNodeReader) GetNode(context.Context, string) (ports.NetworkNode, error) {
	return f.node, f.nodeErr
}

func (f *fakeNetworkNodeReader) ListLanes(_ context.Context, origin, destination string) (ports.NetworkLaneList, error) {
	if f.laneErr != nil {
		return ports.NetworkLaneList{}, f.laneErr
	}
	key := "origin:" + origin
	if origin == "" {
		key = "destination:" + destination
	}
	return f.lanes[key], nil
}

type fakeNodeScorecardReader struct {
	scorecards ports.NodeScorecards
	err        error
}

func (f *fakeNodeScorecardReader) GetScorecard(context.Context, string) (ports.NodeScorecards, error) {
	return f.scorecards, f.err
}

type fakeNodeListingReader struct {
	page    ports.ListingPage
	feed    ports.InventoryFeed
	err     error
	feedErr error
	calls   int
}

func (f *fakeNodeListingReader) ListListings(context.Context, ports.ListingListQuery) (ports.ListingPage, error) {
	f.calls++
	return f.page, f.err
}

func (f *fakeNodeListingReader) GetInventoryFeed(context.Context, string) (ports.InventoryFeed, error) {
	return f.feed, f.feedErr
}

type fakeVendorNodeReader struct {
	node     ports.VendorNode
	nodeErr  error
	scores   ports.VendorNodeScorecards
	scoreErr error
}

func (f *fakeVendorNodeReader) GetVendorNode(context.Context, string, string) (ports.VendorNode, error) {
	return f.node, f.nodeErr
}
func (f *fakeVendorNodeReader) GetNodeScorecard(context.Context, string, string) (ports.VendorNodeScorecards, error) {
	return f.scores, f.scoreErr
}

type fakeNodeDepartureReader struct {
	page ports.LinehaulDeparturePage
	err  error
	from string
}

func (f *fakeNodeDepartureReader) ListDepartures(_ context.Context, query ports.LinehaulDepartureQuery) (ports.LinehaulDeparturePage, error) {
	f.from = query.From
	return f.page, f.err
}

type fakeStationWaveReader struct {
	waves       ports.DeliveryWaves
	err         error
	serviceDate string
}

func (f *fakeStationWaveReader) ListWaves(_ context.Context, _, serviceDate string) (ports.DeliveryWaves, error) {
	f.serviceDate = serviceDate
	return f.waves, f.err
}

// wiredNodeFixture is one fully-wired node outlook over happy-path fakes:
// a vendor-registered Chicago delivery station.
func wiredNodeFixture() (*NetworkNodeOutlook, *fakeNetworkNodeReader, *fakeStationWaveReader) {
	topology := &fakeNetworkNodeReader{
		node: ports.NetworkNode{
			NodeID: "node-42", NodeType: "DELIVERY_STATION", Status: "ACTIVE",
			RegionID: "reg-1", Timezone: "America/Chicago",
			VendorRef: &ports.NodeVendorRef{VendorID: "ven-9", VendorNodeCode: "CHI-1"},
		},
		lanes: map[string]ports.NetworkLaneList{
			"origin:node-42":      {Lanes: []ports.NetworkLane{{LaneID: "lane-out", OriginNodeID: "node-42", DestinationNodeID: "node-7"}}},
			"destination:node-42": {Lanes: []ports.NetworkLane{{LaneID: "lane-in", OriginNodeID: "node-7", DestinationNodeID: "node-42"}}},
		},
	}
	waves := &fakeStationWaveReader{waves: ports.DeliveryWaves{Items: []ports.DeliveryWave{{
		WaveID: "wave-1", StationNodeID: "node-42", ServiceDate: "2026-10-24", State: "OPEN",
	}}}}
	uc := &NetworkNodeOutlook{
		Topology: topology,
		Performance: &fakeNodeScorecardReader{scorecards: ports.NodeScorecards{
			NodeID:     "node-42",
			Scorecards: []ports.NodeScorecard{{ScorecardID: "sc-7", NodeID: "node-42", Window: "7d", Grade: "A"}},
		}},
		Listings: &fakeNodeListingReader{
			page: ports.ListingPage{Listings: []ports.Listing{{
				ListingID: "list-1", NodeID: "node-42", State: "ACTIVE",
				Availability: ports.ListingAvailability{AvailableQty: 5, Basis: "FEED", AsOf: "2026-10-24T06:00:00Z", FeedID: "feed-2"},
			}, {
				ListingID: "list-2", NodeID: "node-42", State: "ACTIVE",
				Availability: ports.ListingAvailability{AvailableQty: 0, Basis: "FEED", AsOf: "2026-10-23T06:00:00Z", FeedID: "feed-1"},
			}}},
			feed: ports.InventoryFeed{FeedID: "feed-2", NodeID: "node-42", State: "APPLIED"},
		},
		Vendor: &fakeVendorNodeReader{
			node:   ports.VendorNode{NodeID: "node-42", VendorID: "ven-9", Status: "ACTIVE"},
			scores: ports.VendorNodeScorecards{NodeID: "node-42", Windows: []ports.VendorScorecardWindow{{Window: "7d", Grade: "A"}}},
		},
		Departures: &fakeNodeDepartureReader{page: ports.LinehaulDeparturePage{Departures: []ports.LinehaulDeparture{{
			DepartureID: "dep-1", LaneID: "lane-out", OriginNodeID: "node-42", State: "SCHEDULED",
		}}}},
		Waves: waves,
		Now:   func() time.Time { return time.Date(2026, 10, 24, 18, 30, 0, 0, time.UTC) },
	}
	return uc, topology, waves
}

func TestNetworkNodeOutlook_FullPicture(t *testing.T) {
	uc, _, waves := wiredNodeFixture()
	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNodeTopologySections(t, res)
	assertNodeSupplySections(t, res)
	assertNodeLogisticsSections(t, res)
	// 18:30 UTC on 2026-10-24 is 13:30 in Chicago: the same calendar day.
	if waves.serviceDate != "2026-10-24" {
		t.Errorf("wave service date = %q, want the node's local 2026-10-24", waves.serviceDate)
	}
}

func assertNodeTopologySections(t *testing.T, res NetworkNodeOutlookResult) {
	t.Helper()
	if res.Node.Omitted != "" || !res.Node.Complete {
		t.Errorf("node section = %+v", res.Node)
	}
	if res.InboundLanes.Omitted != "" || len(res.InboundLanes.Items) != 1 || res.InboundLanes.Items[0].LaneID != "lane-in" {
		t.Errorf("inboundLanes section = %+v", res.InboundLanes)
	}
	if res.OutboundLanes.Omitted != "" || len(res.OutboundLanes.Items) != 1 || res.OutboundLanes.Items[0].LaneID != "lane-out" {
		t.Errorf("outboundLanes section = %+v", res.OutboundLanes)
	}
	if res.Scorecards.Omitted != "" || len(res.Scorecards.Items) != 1 {
		t.Errorf("scorecards section = %+v", res.Scorecards)
	}
}

func assertNodeSupplySections(t *testing.T, res NetworkNodeOutlookResult) {
	t.Helper()
	if res.Listings.Omitted != "" || !res.Listings.Complete || len(res.Listings.Items) != 2 {
		t.Errorf("listings section = %+v", res.Listings)
	}
	if res.InventoryFeed.Omitted != "" || len(res.InventoryFeed.Items) != 1 || res.InventoryFeed.Items[0].FeedID != "feed-2" {
		t.Errorf("inventoryFeed section = %+v", res.InventoryFeed)
	}
	if res.Vendor.Omitted != "" || len(res.Vendor.Items) != 1 {
		t.Errorf("vendor section = %+v", res.Vendor)
	}
	if res.VendorScores.Omitted != "" || len(res.VendorScores.Items) != 1 {
		t.Errorf("vendorScores section = %+v", res.VendorScores)
	}
}

func assertNodeLogisticsSections(t *testing.T, res NetworkNodeOutlookResult) {
	t.Helper()
	if res.Departures.Omitted != "" || len(res.Departures.Items) != 1 {
		t.Errorf("departures section = %+v", res.Departures)
	}
	if res.Waves.Omitted != "" || len(res.Waves.Items) != 1 {
		t.Errorf("waves section = %+v", res.Waves)
	}
}

func TestNetworkNodeOutlook_DeparturesWindowStartsAtNow(t *testing.T) {
	uc, _, _ := wiredNodeFixture()
	departures := &fakeNodeDepartureReader{}
	uc.Departures = departures
	if _, err := uc.Execute(context.Background(), "node-42"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if departures.from != "2026-10-24T18:30:00Z" {
		t.Fatalf("list_departures from = %q, want the outlook's now as UTC RFC 3339", departures.from)
	}
}

// TestNetworkNodeOutlook_UnconfiguredClientOmitsNotConfigured is the ADR
// 0023 table for the node side.
func TestNetworkNodeOutlook_UnconfiguredClientOmitsNotConfigured(t *testing.T) {
	uc, _, _ := wiredNodeFixture()
	uc.Performance = nil
	uc.Listings = nil
	uc.Vendor = nil
	uc.Departures = nil
	uc.Waves = nil

	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("unconfigured non-anchor upstreams must never fail the outlook: %v", err)
	}
	for name, omitted := range map[string]string{
		"scorecards":    res.Scorecards.Omitted,
		"listings":      res.Listings.Omitted,
		"inventoryFeed": res.InventoryFeed.Omitted,
		"vendor":        res.Vendor.Omitted,
		"vendorScores":  res.VendorScores.Omitted,
		"departures":    res.Departures.Omitted,
		"waves":         res.Waves.Omitted,
	} {
		if !strings.Contains(omitted, "not-configured") {
			t.Errorf("%s section omitted = %q, want the not-configured reason", name, omitted)
		}
	}
	if res.Node.Omitted != "" {
		t.Errorf("the anchor section must still report: %q", res.Node.Omitted)
	}
}

// TestNetworkNodeOutlook_FailingCallOmitsWithReason is the fail-open table
// for the node side.
func TestNetworkNodeOutlook_FailingCallOmitsWithReason(t *testing.T) {
	uc, _, _ := wiredNodeFixture()
	uc.Performance = &fakeNodeScorecardReader{err: errors.New("node-performance unreachable")}

	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("one failing section must not fail the outlook: %v", err)
	}
	if !strings.Contains(res.Scorecards.Omitted, "get_scorecard") || !strings.Contains(res.Scorecards.Omitted, "unreachable") {
		t.Errorf("scorecards omitted = %q, want the failing call's reason", res.Scorecards.Omitted)
	}
	if res.Node.Omitted != "" {
		t.Errorf("the healthy sections must still report: node omitted %q", res.Node.Omitted)
	}
}

func TestNetworkNodeOutlook_NodeWithoutVendorRefOmitsVendorSections(t *testing.T) {
	uc, topology, _ := wiredNodeFixture()
	topology.node.VendorRef = nil
	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Vendor.Omitted, "not registered under a vendor") {
		t.Errorf("vendor omitted = %q", res.Vendor.Omitted)
	}
	if !strings.Contains(res.VendorScores.Omitted, "not registered under a vendor") {
		t.Errorf("vendorScores omitted = %q", res.VendorScores.Omitted)
	}
}

func TestNetworkNodeOutlook_VendorLookupFailureOmitsWithReason(t *testing.T) {
	uc, _, _ := wiredNodeFixture()
	uc.Vendor = &fakeVendorNodeReader{nodeErr: errors.New("vendor-connectivity down")}
	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Vendor.Omitted, "get_vendor_node") {
		t.Errorf("vendor omitted = %q", res.Vendor.Omitted)
	}
	if res.VendorScores.Omitted != "" {
		t.Errorf("the scorecard lookup is independent: omitted %q", res.VendorScores.Omitted)
	}
}

func TestNetworkNodeOutlook_NonStationNodeHasNoWaves(t *testing.T) {
	uc, topology, waves := wiredNodeFixture()
	topology.node.NodeType = "FULFILLMENT_CENTER"
	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Waves.Omitted, "not a delivery station") {
		t.Fatalf("waves omitted = %q", res.Waves.Omitted)
	}
	if waves.serviceDate != "" {
		t.Fatalf("list_waves must not be called for a non-station node, got service date %q", waves.serviceDate)
	}
}

func TestNetworkNodeOutlook_UnknownNodeOmitsDependentSections(t *testing.T) {
	uc, topology, _ := wiredNodeFixture()
	topology.nodeErr = errors.New("node-topology down")
	topology.laneErr = errors.New("node-topology down")
	uc.Performance = nil
	uc.Listings = nil
	uc.Vendor = nil
	uc.Departures = nil
	uc.Waves = nil

	if _, err := uc.Execute(context.Background(), "node-42"); err == nil {
		t.Fatal("node and lanes all failed plus every optional section unavailable: Execute must error rather than all-clear")
	}
}

func TestNetworkNodeOutlook_FailingNodeStillReportsIndependents(t *testing.T) {
	uc, topology, _ := wiredNodeFixture()
	topology.nodeErr = errors.New("get_node down") // lanes still answer
	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Node.Omitted, "get_node") {
		t.Errorf("node omitted = %q", res.Node.Omitted)
	}
	if !strings.Contains(res.Vendor.Omitted, "node unavailable") {
		t.Errorf("vendor omitted = %q, want the unknown-node reason", res.Vendor.Omitted)
	}
	if !strings.Contains(res.Waves.Omitted, "node unavailable") {
		t.Errorf("waves omitted = %q, want the unknown-node reason", res.Waves.Omitted)
	}
	if res.Scorecards.Omitted != "" {
		t.Errorf("scorecards key off the caller's node id, not the node payload: omitted %q", res.Scorecards.Omitted)
	}
}

func TestNetworkNodeOutlook_NoFeedBearingListingOmitsFeed(t *testing.T) {
	uc, _, _ := wiredNodeFixture()
	uc.Listings = &fakeNodeListingReader{page: ports.ListingPage{Listings: []ports.Listing{{
		ListingID: "list-1", NodeID: "node-42",
		Availability: ports.ListingAvailability{AvailableQty: 7, Basis: "MANUAL"},
	}}}}
	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.InventoryFeed.Omitted, "no listing carries an inventory feed") {
		t.Fatalf("inventoryFeed omitted = %q", res.InventoryFeed.Omitted)
	}
}

func TestNetworkNodeOutlook_FailedListingsOmitsFeedAsUnknown(t *testing.T) {
	uc, _, _ := wiredNodeFixture()
	uc.Listings = &fakeNodeListingReader{err: errors.New("offer-availability down")}
	res, err := uc.Execute(context.Background(), "node-42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Listings.Omitted, "list_listings") {
		t.Fatalf("listings omitted = %q", res.Listings.Omitted)
	}
	if !strings.Contains(res.InventoryFeed.Omitted, "listings unavailable") {
		t.Fatalf("inventoryFeed omitted = %q, want the unknown-feed-id reason", res.InventoryFeed.Omitted)
	}
}

func TestNewestFeedID_PicksFreshestAsOf(t *testing.T) {
	listings := []ports.Listing{
		{Availability: ports.ListingAvailability{FeedID: "feed-old", AsOf: "2026-10-22T06:00:00Z"}},
		{Availability: ports.ListingAvailability{FeedID: "feed-new", AsOf: "2026-10-24T06:00:00Z"}},
		{Availability: ports.ListingAvailability{FeedID: "feed-mid", AsOf: "2026-10-23T06:00:00Z"}},
		{Availability: ports.ListingAvailability{FeedID: "", AsOf: "2026-10-25T06:00:00Z"}},
	}
	if got := newestFeedID(listings); got != "feed-new" {
		t.Fatalf("newestFeedID = %q, want feed-new", got)
	}
	if got := newestFeedID(nil); got != "" {
		t.Fatalf("newestFeedID(nil) = %q, want \"\"", got)
	}
}

func TestLocalServiceDate_FallsBackToUTCForUnknownTimezone(t *testing.T) {
	now := time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)
	if got := localServiceDate(now, "Not/AZone"); got != "2026-10-24" {
		t.Fatalf("localServiceDate (unknown zone) = %q, want the UTC date", got)
	}
	// 23:30 UTC is already the next day in Tokyo.
	if got := localServiceDate(now, "Asia/Tokyo"); got != "2026-10-25" {
		t.Fatalf("localServiceDate (Tokyo) = %q, want 2026-10-25", got)
	}
}

func TestNetworkNodeOutlook_InputValidation(t *testing.T) {
	uc, _, _ := wiredNodeFixture()
	if _, err := uc.Execute(context.Background(), ""); err == nil {
		t.Fatal("an empty node id must be rejected")
	}
	var nilUC *NetworkNodeOutlook
	if _, err := nilUC.Execute(context.Background(), "node-42"); !errors.Is(err, ErrNodeTopologyNotConfigured) {
		t.Fatalf("nil use case error = %v, want ErrNodeTopologyNotConfigured", err)
	}
	if _, err := (&NetworkNodeOutlook{}).Execute(context.Background(), "node-42"); !errors.Is(err, ErrNodeTopologyNotConfigured) {
		t.Fatalf("no anchor error = %v, want ErrNodeTopologyNotConfigured", err)
	}
}
