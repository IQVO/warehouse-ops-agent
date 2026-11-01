package mcp

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type networkNodeOutlookInput struct {
	NodeID string `json:"nodeId" jsonschema:"the network node id to assemble the retail-network picture for"`
}

type networkNodeOutlookOutput struct {
	NodeID      string `json:"nodeId"`
	GeneratedAt string `json:"generatedAt"`

	Node          inboundSection[networkNodeDTO]          `json:"node"`
	InboundLanes  inboundSection[networkLaneDTO]          `json:"inboundLanes"`
	OutboundLanes inboundSection[networkLaneDTO]          `json:"outboundLanes"`
	Scorecards    inboundSection[networkScorecardDTO]     `json:"scorecards"`
	Listings      inboundSection[networkListingDTO]       `json:"listings"`
	InventoryFeed inboundSection[networkInventoryFeedDTO] `json:"inventoryFeed"`
	Vendor        inboundSection[networkVendorNodeDTO]    `json:"vendor"`
	VendorScores  inboundSection[networkVendorScoreDTO]   `json:"vendorScores"`
	Departures    inboundSection[networkDepartureDTO]     `json:"departures"`
	Waves         inboundSection[networkWaveDTO]          `json:"waves"`
}

type networkNodeDTO struct {
	NodeID        string                     `json:"nodeId"`
	NodeType      string                     `json:"nodeType"`
	Status        string                     `json:"status"`
	RegionID      string                     `json:"regionId"`
	Operator      string                     `json:"operator"`
	Timezone      string                     `json:"timezone"`
	Subtype       string                     `json:"subtype,omitempty"`
	Capabilities  networkNodeCapabilitiesDTO `json:"capabilities"`
	Location      networkNodeLocationDTO     `json:"location"`
	ActivatedAt   string                     `json:"activatedAt,omitempty"`
	RetiredAt     string                     `json:"retiredAt,omitempty"`
	SuspendedAt   string                     `json:"suspendedAt,omitempty"`
	SuspendReason string                     `json:"suspendReason,omitempty"`
	VendorRef     *networkVendorRefDTO       `json:"vendorRef,omitempty"`
	// OperationalProfile is the operationally relevant summary of the
	// node's declared profile (handling time, cutoffs, throughput cap);
	// the full profile stays behind node-topology's get_node.
	OperationalProfile *networkNodeProfileDTO `json:"operationalProfile,omitempty"`
}

type networkNodeCapabilitiesDTO struct {
	Outbound bool `json:"outbound"`
	Inbound  bool `json:"inbound"`
	Returns  bool `json:"returns"`
	Sort     bool `json:"sort"`
	LastMile bool `json:"lastMile"`
	Air      bool `json:"air"`
}

type networkNodeLocationDTO struct {
	PostalCode  string   `json:"postalCode"`
	CountryCode string   `json:"countryCode"`
	Lat         *float64 `json:"lat,omitempty"`
	Lon         *float64 `json:"lon,omitempty"`
}

type networkVendorRefDTO struct {
	VendorID       string `json:"vendorId"`
	VendorNodeCode string `json:"vendorNodeCode"`
}

type networkNodeProfileDTO struct {
	ProfileRevision     int                             `json:"profileRevision"`
	Timezone            string                          `json:"timezone"`
	HandlingTimeMinutes int                             `json:"handlingTimeMinutes"`
	OperatingHoursByDay map[string]ports.OperatingHours `json:"operatingHoursByDay,omitempty"`
	Cutoffs             []networkNodeCutoffDTO          `json:"cutoffs"`
	ThroughputCap       networkThroughputCapDTO         `json:"throughputCap"`
	SortDwellMinutes    *int                            `json:"sortDwellMinutes,omitempty"`
}

type networkNodeCutoffDTO struct {
	LocalTime string   `json:"localTime"`
	Days      []string `json:"days,omitempty"`
	MaxOrders int      `json:"maxOrders,omitempty"`
}

type networkThroughputCapDTO struct {
	Value    int    `json:"value"`
	TimeUnit string `json:"timeUnit"`
	Unit     string `json:"unit"`
}

type networkLaneDTO struct {
	LaneID                string `json:"laneId"`
	OriginNodeID          string `json:"originNodeId"`
	DestinationNodeID     string `json:"destinationNodeId"`
	Mode                  string `json:"mode"`
	NominalTransitMinutes int    `json:"nominalTransitMinutes"`
	Status                string `json:"status"`
	ClosedAt              string `json:"closedAt,omitempty"`
}

func (d Deps) getNetworkNodeOutlook(ctx context.Context, in networkNodeOutlookInput) (networkNodeOutlookOutput, error) {
	res, err := d.NetworkNodeOutlook.Execute(ctx, in.NodeID)
	if err != nil {
		return networkNodeOutlookOutput{}, err
	}
	out := networkNodeOutlookOutput{
		NodeID:      res.NodeID,
		GeneratedAt: inboundInstant(res.GeneratedAt),
	}

	nodes := make([]networkNodeDTO, 0, len(res.Node.Items))
	for _, n := range res.Node.Items {
		nodes = append(nodes, toNetworkNodeDTO(n))
	}
	out.Node = newNetworkSection(res.Node.OutlookSectionStatus, nodes)

	out.InboundLanes = newNetworkSection(res.InboundLanes.OutlookSectionStatus, toNetworkLaneDTOs(res.InboundLanes.Items))
	out.OutboundLanes = newNetworkSection(res.OutboundLanes.OutlookSectionStatus, toNetworkLaneDTOs(res.OutboundLanes.Items))

	out.Scorecards = newNetworkSection(res.Scorecards.OutlookSectionStatus, toNetworkScorecardDTOs(res.Scorecards.Items))
	out.Listings = newNetworkSection(res.Listings.OutlookSectionStatus, toNetworkListingDTOs(res.Listings.Items))
	out.InventoryFeed = newNetworkSection(res.InventoryFeed.OutlookSectionStatus, toNetworkInventoryFeedDTOs(res.InventoryFeed.Items))
	out.Vendor = newNetworkSection(res.Vendor.OutlookSectionStatus, toNetworkVendorNodeDTOs(res.Vendor.Items))
	out.VendorScores = newNetworkSection(res.VendorScores.OutlookSectionStatus, toNetworkVendorScoreDTOs(res.VendorScores.Items))
	out.Departures = newNetworkSection(res.Departures.OutlookSectionStatus, toNetworkDepartureDTOs(res.Departures.Items))
	out.Waves = newNetworkSection(res.Waves.OutlookSectionStatus, toNetworkWaveDTOs(res.Waves.Items))
	return out, nil
}

func toNetworkNodeDTO(n ports.NetworkNode) networkNodeDTO {
	dto := networkNodeDTO{
		NodeID: n.NodeID, NodeType: n.NodeType, Status: n.Status, RegionID: n.RegionID,
		Operator: n.Operator, Timezone: n.Timezone, Subtype: n.Subtype,
		Capabilities: networkNodeCapabilitiesDTO{
			Outbound: n.Capabilities.Outbound, Inbound: n.Capabilities.Inbound, Returns: n.Capabilities.Returns,
			Sort: n.Capabilities.Sort, LastMile: n.Capabilities.LastMile, Air: n.Capabilities.Air,
		},
		Location:    networkNodeLocationDTO{PostalCode: n.Location.PostalCode, CountryCode: n.Location.CountryCode, Lat: n.Location.Lat, Lon: n.Location.Lon},
		ActivatedAt: n.ActivatedAt, RetiredAt: n.RetiredAt, SuspendedAt: n.SuspendedAt, SuspendReason: n.SuspendReason,
	}
	if n.VendorRef != nil {
		dto.VendorRef = &networkVendorRefDTO{VendorID: n.VendorRef.VendorID, VendorNodeCode: n.VendorRef.VendorNodeCode}
	}
	if n.OperationalProfile != nil {
		cutoffs := make([]networkNodeCutoffDTO, 0, len(n.OperationalProfile.Cutoffs))
		for _, c := range n.OperationalProfile.Cutoffs {
			cutoffs = append(cutoffs, networkNodeCutoffDTO{LocalTime: c.LocalTime, Days: c.Days, MaxOrders: c.MaxOrders})
		}
		dto.OperationalProfile = &networkNodeProfileDTO{
			ProfileRevision: n.OperationalProfile.ProfileRevision, Timezone: n.OperationalProfile.Timezone,
			HandlingTimeMinutes: n.OperationalProfile.HandlingTimeMinutes,
			OperatingHoursByDay: n.OperationalProfile.OperatingHoursByDay,
			Cutoffs:             cutoffs,
			ThroughputCap: networkThroughputCapDTO{
				Value:    n.OperationalProfile.ThroughputCap.Value,
				TimeUnit: n.OperationalProfile.ThroughputCap.TimeUnit,
				Unit:     n.OperationalProfile.ThroughputCap.Unit,
			},
			SortDwellMinutes: n.OperationalProfile.SortDwellMinutes,
		}
	}
	return dto
}

func toNetworkLaneDTOs(lanes []ports.NetworkLane) []networkLaneDTO {
	out := make([]networkLaneDTO, 0, len(lanes))
	for _, l := range lanes {
		out = append(out, networkLaneDTO{
			LaneID: l.LaneID, OriginNodeID: l.OriginNodeID, DestinationNodeID: l.DestinationNodeID,
			Mode: l.Mode, NominalTransitMinutes: l.NominalTransitMinutes, Status: l.Status, ClosedAt: l.ClosedAt,
		})
	}
	return out
}
