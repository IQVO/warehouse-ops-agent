// Package ports (this file): the outbound port for the node-topology
// bounded context (retail network), the fleet's owner of the network's
// nodes, regions, lanes and postal-prefix service areas.
//
// node-topology's MCP server is READ-ONLY (its own governance test pins
// its six tools, every one annotated readOnlyHint). This port mirrors
// all six: get_node, get_region, list_nodes, list_regions, list_lanes
// and resolve_service_area. node-topology's writes (register a node,
// declare a profile, open a lane) are upstream of it and have no method
// here. The zero-write fitness test (internal/architecture/zerowrite)
// fails the build if the mcpclient adapter ever names a write-verb tool,
// and the contract snapshot test in mcpclient pins the published tools
// against node-topology's published registry.
package ports

import (
	"context"
)

// NodeTopologyClient is the outbound port for node-topology's six
// published read tools.
type NodeTopologyClient interface {
	// GetNode calls get_node: one node with its current operational
	// profile.
	GetNode(ctx context.Context, nodeID string) (NetworkNode, error)
	// GetRegion calls get_region: one region by id.
	GetRegion(ctx context.Context, regionID string) (NetworkRegion, error)
	// ListNodes calls list_nodes: every node by id ascending, optionally
	// filtered. Zero query fields are omitted so node-topology applies
	// its own default (every node).
	ListNodes(ctx context.Context, query NodeListQuery) (NetworkNodeList, error)
	// ListRegions calls list_regions: every region by id ascending.
	ListRegions(ctx context.Context) (NetworkRegionList, error)
	// ListLanes calls list_lanes: the directed lanes by id ascending,
	// optionally filtered by origin and/or destination. Empty filters
	// mean every lane.
	ListLanes(ctx context.Context, origin, destination string) (NetworkLaneList, error)
	// ResolveServiceArea calls resolve_service_area: which delivery
	// station serves one postal prefix.
	ResolveServiceArea(ctx context.Context, postalPrefix string) (ServiceArea, error)
}

// NodeListQuery filters list_nodes: an optional node type, region and
// status. The zero value means every node.
type NodeListQuery struct {
	Type     string
	RegionID string
	Status   string
}

// --- node-topology read-model DTOs -------------------------------------------
//
// Field-for-field mirrors of node-topology's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/node_topology_tools.golden.json.

// NetworkNode mirrors nodeView (get_node and each list_nodes item).
// NodeType is FULFILLMENT_CENTER, INBOUND_CROSS_DOCK, RETURNS_CENTER,
// SORTATION_CENTER, DELIVERY_STATION or AIR_HUB. OperationalProfile is
// null until the network declares one; Lat/Lon are null until the
// geometry is known.
type NetworkNode struct {
	NodeID             string                  `json:"nodeId"`
	NodeType           string                  `json:"nodeType"`
	Status             string                  `json:"status"`
	RegionID           string                  `json:"regionId"`
	Operator           string                  `json:"operator"`
	Timezone           string                  `json:"timezone"`
	Capabilities       NodeCapabilities        `json:"capabilities"`
	Location           NodeLocation            `json:"location"`
	Subtype            string                  `json:"subtype,omitempty"`
	ActivatedAt        string                  `json:"activatedAt,omitempty"`
	RetiredAt          string                  `json:"retiredAt,omitempty"`
	SuspendedAt        string                  `json:"suspendedAt,omitempty"`
	SuspendReason      string                  `json:"suspendReason,omitempty"`
	OperationalProfile *NodeOperationalProfile `json:"operationalProfile,omitempty"`
	VendorRef          *NodeVendorRef          `json:"vendorRef,omitempty"`
}

// NodeCapabilities mirrors the node's capability flags.
type NodeCapabilities struct {
	Outbound bool `json:"outbound"`
	Inbound  bool `json:"inbound"`
	Returns  bool `json:"returns"`
	Sort     bool `json:"sort"`
	LastMile bool `json:"lastMile"`
	Air      bool `json:"air"`
}

// NodeLocation mirrors the node's location: postal code and country,
// with the optional geographic point.
type NodeLocation struct {
	PostalCode  string   `json:"postalCode"`
	CountryCode string   `json:"countryCode"`
	Lat         *float64 `json:"lat,omitempty"`
	Lon         *float64 `json:"lon,omitempty"`
}

// OperatingHours mirrors one day's operating window (local times).
type OperatingHours struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// NodeCutoff mirrors one order cutoff of a profile: the local time, the
// days it applies to and the optional order cap.
type NodeCutoff struct {
	LocalTime string   `json:"localTime"`
	Days      []string `json:"days,omitempty"`
	MaxOrders int      `json:"maxOrders,omitempty"`
}

// ThroughputCap mirrors the profile's throughput cap: value, unit and
// time unit (e.g. 1200 units per hour).
type ThroughputCap struct {
	Value    int    `json:"value"`
	TimeUnit string `json:"timeUnit"`
	Unit     string `json:"unit"`
}

// NodeOperationalProfile mirrors the node's declared operational
// profile. SortDwellMinutes is null until measured.
type NodeOperationalProfile struct {
	ProfileRevision     int                       `json:"profileRevision"`
	Timezone            string                    `json:"timezone"`
	OperatingHoursByDay map[string]OperatingHours `json:"operatingHoursByDay"`
	Holidays            []string                  `json:"holidays"`
	HandlingTimeMinutes int                       `json:"handlingTimeMinutes"`
	Cutoffs             []NodeCutoff              `json:"cutoffs"`
	ThroughputCap       ThroughputCap             `json:"throughputCap"`
	DeclaredBy          string                    `json:"declaredBy"`
	DeclaredAt          string                    `json:"declaredAt"`
	SortDwellMinutes    *int                      `json:"sortDwellMinutes,omitempty"`
}

// NodeVendorRef mirrors the vendor a network node is registered under.
type NodeVendorRef struct {
	VendorID       string `json:"vendorId"`
	VendorNodeCode string `json:"vendorNodeCode"`
}

// NetworkNodeList mirrors the list_nodes result.
type NetworkNodeList struct {
	Nodes []NetworkNode `json:"nodes"`
}

// NetworkRegion mirrors regionView (get_region and each list_regions
// item).
type NetworkRegion struct {
	RegionID string `json:"regionId"`
	Code     string `json:"code"`
	Name     string `json:"name"`
}

// NetworkRegionList mirrors the list_regions result.
type NetworkRegionList struct {
	Regions []NetworkRegion `json:"regions"`
}

// NetworkLane mirrors laneView (one list_lanes item): a directed lane
// between two nodes and its nominal transit.
type NetworkLane struct {
	LaneID                string `json:"laneId"`
	OriginNodeID          string `json:"originNodeId"`
	DestinationNodeID     string `json:"destinationNodeId"`
	Mode                  string `json:"mode"`
	NominalTransitMinutes int    `json:"nominalTransitMinutes"`
	Status                string `json:"status"`
	ClosedAt              string `json:"closedAt,omitempty"`
}

// NetworkLaneList mirrors the list_lanes result.
type NetworkLaneList struct {
	Lanes []NetworkLane `json:"lanes"`
}

// ServiceArea mirrors the resolve_service_area result: the exact-match
// postal prefix, its delivery station and region, and the assignment
// version.
type ServiceArea struct {
	PostalPrefix          string `json:"postalPrefix"`
	DeliveryStationNodeID string `json:"deliveryStationNodeId"`
	RegionID              string `json:"regionId"`
	AssignmentVersion     int    `json:"assignmentVersion"`
}
