package mcp

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// --- get_network_order_outlook (ADR 0026) ------------------------------------
//
// The order outlook's DTOs carry the decision-relevant projection of each
// upstream's facts. Where an upstream publishes more detail than an
// outlook needs (a promise's revisions and witness path, a journey's full
// scan trail) the summary is carried and the drill-down stays behind that
// upstream's own tool — the outlook is one picture, not a data dump.

type networkOrderOutlookInput struct {
	OrderID string `json:"orderId" jsonschema:"the customer order id to assemble the retail-network picture for"`
}

type networkOrderOutlookOutput struct {
	OrderID     string `json:"orderId"`
	GeneratedAt string `json:"generatedAt"`

	Order    inboundSection[networkOrderDTO]      `json:"order"`
	Tracking inboundSection[networkTrackingDTO]   `json:"tracking"`
	ShipTo   inboundSection[networkShipToDTO]     `json:"shipTo"`
	Promises inboundSection[networkPromiseDTO]    `json:"promises"`
	Sourcing inboundSection[networkAssignmentDTO] `json:"sourcing"`
	Journeys inboundSection[networkJourneyDTO]    `json:"journeys"`
}

type networkOrderDTO struct {
	OrderID                 string                 `json:"orderId"`
	Status                  string                 `json:"status"`
	ShopperID               string                 `json:"shopperId"`
	DestinationPostalPrefix string                 `json:"destinationPostalPrefix"`
	PlacedAt                string                 `json:"placedAt"`
	CancelledAt             string                 `json:"cancelledAt,omitempty"`
	Lines                   []networkOrderLineDTO  `json:"lines"`
	Groups                  []networkOrderGroupDTO `json:"groups"`
}

type networkOrderLineDTO struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type networkOrderGroupDTO struct {
	GroupNo           int    `json:"groupNo"`
	PromiseID         string `json:"promiseId"`
	Tier              string `json:"tier"`
	ShipBy            string `json:"shipBy"`
	DeliverByEarliest string `json:"deliverByEarliest"`
	DeliverByLatest   string `json:"deliverByLatest"`
	LineNos           []int  `json:"lineNos"`
	Status            string `json:"status"`
	CancelReason      string `json:"cancelReason,omitempty"`
}

type networkTrackingDTO struct {
	OrderID string                   `json:"orderId"`
	Status  string                   `json:"status"`
	Groups  []networkTrackedGroupDTO `json:"groups"`
}

type networkTrackedGroupDTO struct {
	GroupNo            int                        `json:"groupNo"`
	PromiseID          string                     `json:"promiseId"`
	Status             string                     `json:"status"`
	PromiseVerdict     string                     `json:"promiseVerdict,omitempty"`
	AtRiskReason       string                     `json:"atRiskReason,omitempty"`
	LateByMinutes      *int                       `json:"lateByMinutes,omitempty"`
	ShippedAt          string                     `json:"shippedAt,omitempty"`
	LostAt             string                     `json:"lostAt,omitempty"`
	ReturnedAt         string                     `json:"returnedAt,omitempty"`
	OutcomePackage     string                     `json:"outcomePackage,omitempty"`
	UnsourceableReason string                     `json:"unsourceableReason,omitempty"`
	Packages           []networkTrackedPackageDTO `json:"packages"`
}

type networkTrackedPackageDTO struct {
	TrackingNumber  string `json:"trackingNumber"`
	State           string `json:"state,omitempty"`
	Carrier         string `json:"carrier,omitempty"`
	LastScanAt      string `json:"lastScanAt,omitempty"`
	LastScanNodeID  string `json:"lastScanNodeId,omitempty"`
	ExceptionReason string `json:"exceptionReason,omitempty"`
	DeliveredAt     string `json:"deliveredAt,omitempty"`
}

type networkShipToDTO struct {
	Name        string `json:"name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2,omitempty"`
	City        string `json:"city"`
	Region      string `json:"region"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone,omitempty"`
}

type networkPromiseDTO struct {
	PromiseID         string                      `json:"promiseId"`
	GroupNo           int                         `json:"groupNo"`
	State             string                      `json:"state"`
	Tier              string                      `json:"tier"`
	ShipBy            string                      `json:"shipBy"`
	DeliverByEarliest string                      `json:"deliverByEarliest"`
	DeliverByLatest   string                      `json:"deliverByLatest"`
	AtRiskReason      string                      `json:"atRiskReason,omitempty"`
	Judgement         *networkPromiseJudgementDTO `json:"judgement,omitempty"`
	Version           int                         `json:"version"`
}

type networkPromiseJudgementDTO struct {
	Outcome       string `json:"outcome"`
	Cause         string `json:"cause,omitempty"`
	DeliveredAt   string `json:"deliveredAt,omitempty"`
	LateByMinutes *int   `json:"lateByMinutes,omitempty"`
}

type networkAssignmentDTO struct {
	AssignmentID      string   `json:"assignmentId"`
	GroupNo           int      `json:"groupNo"`
	State             string   `json:"state"`
	CandidateNodeID   string   `json:"candidateNodeId,omitempty"`
	PickupDepartureID string   `json:"pickupDepartureId,omitempty"`
	PickupCutoffAt    string   `json:"pickupCutoffAt,omitempty"`
	ETA               string   `json:"eta,omitempty"`
	TrackingNumbers   []string `json:"trackingNumbers"`
	LockReason        string   `json:"lockReason,omitempty"`
	CancelReason      string   `json:"cancelReason,omitempty"`
}

type networkJourneyDTO struct {
	TrackingNumber string `json:"trackingNumber"`
	State          string `json:"state,omitempty"`
	PoNumber       string `json:"poNumber,omitempty"`
	AssignmentID   string `json:"assignmentId,omitempty"`
	Tier           string `json:"tier,omitempty"`
	HandedOverAt   string `json:"handedOverAt,omitempty"`
	DeliveredAt    string `json:"deliveredAt,omitempty"`
	ScanCount      int    `json:"scanCount,omitempty"`
	LastScanAt     string `json:"lastScanAt,omitempty"`
	LastScanNodeID string `json:"lastScanNodeId,omitempty"`
	// Error is non-empty when this journey could not be read: unknown,
	// never "delivered".
	Error string `json:"error,omitempty"`
}

func (d Deps) getNetworkOrderOutlook(ctx context.Context, in networkOrderOutlookInput) (networkOrderOutlookOutput, error) {
	res, err := d.NetworkOrderOutlook.Execute(ctx, in.OrderID)
	if err != nil {
		return networkOrderOutlookOutput{}, err
	}
	out := networkOrderOutlookOutput{
		OrderID:     res.OrderID,
		GeneratedAt: inboundInstant(res.GeneratedAt),
	}

	orders := make([]networkOrderDTO, 0, len(res.Order.Items))
	for _, o := range res.Order.Items {
		orders = append(orders, toNetworkOrderDTO(o))
	}
	out.Order = newNetworkSection(res.Order.OutlookSectionStatus, orders)

	tracking := make([]networkTrackingDTO, 0, len(res.Tracking.Items))
	for _, t := range res.Tracking.Items {
		tracking = append(tracking, toNetworkTrackingDTO(t))
	}
	out.Tracking = newNetworkSection(res.Tracking.OutlookSectionStatus, tracking)

	shipTo := make([]networkShipToDTO, 0, len(res.ShipTo.Items))
	for _, s := range res.ShipTo.Items {
		shipTo = append(shipTo, networkShipToDTO{
			Name: s.Name, Line1: s.Line1, Line2: s.Line2, City: s.City,
			Region: s.Region, PostalCode: s.PostalCode, CountryCode: s.CountryCode, Phone: s.Phone,
		})
	}
	out.ShipTo = newNetworkSection(res.ShipTo.OutlookSectionStatus, shipTo)

	promises := make([]networkPromiseDTO, 0, len(res.Promises.Items))
	for _, p := range res.Promises.Items {
		promises = append(promises, toNetworkPromiseDTO(p))
	}
	out.Promises = newNetworkSection(res.Promises.OutlookSectionStatus, promises)

	sourcing := make([]networkAssignmentDTO, 0, len(res.Sourcing.Items))
	for _, a := range res.Sourcing.Items {
		sourcing = append(sourcing, toNetworkAssignmentDTO(a))
	}
	out.Sourcing = newNetworkSection(res.Sourcing.OutlookSectionStatus, sourcing)

	journeys := make([]networkJourneyDTO, 0, len(res.Journeys.Items))
	for _, j := range res.Journeys.Items {
		journeys = append(journeys, networkJourneyDTO{
			TrackingNumber: j.TrackingNumber, State: j.State, PoNumber: j.PoNumber,
			AssignmentID: j.AssignmentID, Tier: j.Tier,
			HandedOverAt: j.HandedOverAt, DeliveredAt: j.DeliveredAt,
			ScanCount: j.ScanCount, LastScanAt: j.LastScanAt, LastScanNodeID: j.LastScanNodeID,
			Error: j.Error,
		})
	}
	out.Journeys = newNetworkSection(res.Journeys.OutlookSectionStatus, journeys)
	return out, nil
}

func toNetworkOrderDTO(o ports.CustomerOrder) networkOrderDTO {
	lines := make([]networkOrderLineDTO, 0, len(o.Lines))
	for _, l := range o.Lines {
		lines = append(lines, networkOrderLineDTO{LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
	}
	groups := make([]networkOrderGroupDTO, 0, len(o.Groups))
	for _, g := range o.Groups {
		lineNos := g.LineNos
		if lineNos == nil {
			lineNos = []int{}
		}
		groups = append(groups, networkOrderGroupDTO{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: g.Tier,
			ShipBy: g.ShipBy, DeliverByEarliest: g.DeliverByEarliest, DeliverByLatest: g.DeliverByLatest,
			LineNos: lineNos, Status: g.Status, CancelReason: g.CancelReason,
		})
	}
	return networkOrderDTO{
		OrderID: o.OrderID, Status: o.Status, ShopperID: o.ShopperID,
		DestinationPostalPrefix: o.DestinationPostalPrefix, PlacedAt: o.PlacedAt, CancelledAt: o.CancelledAt,
		Lines: lines, Groups: groups,
	}
}

func toNetworkTrackingDTO(t ports.OrderTracking) networkTrackingDTO {
	groups := make([]networkTrackedGroupDTO, 0, len(t.Groups))
	for _, g := range t.Groups {
		packages := make([]networkTrackedPackageDTO, 0, len(g.Packages))
		for _, p := range g.Packages {
			packages = append(packages, networkTrackedPackageDTO{
				TrackingNumber: p.TrackingNumber, State: p.State, Carrier: p.Carrier,
				LastScanAt: p.LastScanAt, LastScanNodeID: p.LastScanNodeID,
				ExceptionReason: p.ExceptionReason, DeliveredAt: p.DeliveredAt,
			})
		}
		groups = append(groups, networkTrackedGroupDTO{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Status: g.Status,
			PromiseVerdict: g.PromiseVerdict, AtRiskReason: g.AtRiskReason, LateByMinutes: g.LateByMinutes,
			ShippedAt: g.ShippedAt, LostAt: g.LostAt, ReturnedAt: g.ReturnedAt,
			OutcomePackage: g.OutcomePackage, UnsourceableReason: g.UnsourceableReason,
			Packages: packages,
		})
	}
	return networkTrackingDTO{OrderID: t.OrderID, Status: t.Status, Groups: groups}
}

func toNetworkPromiseDTO(p ports.DeliveryPromise) networkPromiseDTO {
	var judgement *networkPromiseJudgementDTO
	if p.Judgement != nil {
		judgement = &networkPromiseJudgementDTO{
			Outcome: p.Judgement.Outcome, Cause: p.Judgement.Cause,
			DeliveredAt: p.Judgement.DeliveredAt, LateByMinutes: p.Judgement.LateByMinutes,
		}
	}
	return networkPromiseDTO{
		PromiseID: p.PromiseID, GroupNo: p.GroupNo, State: p.State,
		Tier: p.Terms.Tier, ShipBy: p.Terms.ShipBy,
		DeliverByEarliest: p.Terms.DeliverByEarliest, DeliverByLatest: p.Terms.DeliverByLatest,
		AtRiskReason: p.AtRiskReason, Judgement: judgement, Version: p.Version,
	}
}

func toNetworkAssignmentDTO(a ports.FulfillmentAssignment) networkAssignmentDTO {
	trackingNumbers := a.TrackingNumbers
	if trackingNumbers == nil {
		trackingNumbers = []string{}
	}
	dto := networkAssignmentDTO{
		AssignmentID: a.AssignmentID, GroupNo: a.GroupNo, State: a.State,
		PickupDepartureID: a.PickupDepartureID, TrackingNumbers: trackingNumbers,
		LockReason: a.LockReason, CancelReason: a.CancelReason,
	}
	if a.Candidate != nil {
		dto.CandidateNodeID = a.Candidate.NodeID
		dto.PickupCutoffAt = a.Candidate.PickupCutoffAt
		dto.ETA = a.Candidate.ETA
		if a.Candidate.PickupDepartureID != "" {
			dto.PickupDepartureID = a.Candidate.PickupDepartureID
		}
	}
	return dto
}

// --- get_network_node_outlook (ADR 0026) -------------------------------------

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

type networkScorecardDTO struct {
	ScorecardID string                     `json:"scorecardId"`
	Window      string                     `json:"window"`
	State       string                     `json:"state"`
	Grade       string                     `json:"grade"`
	Metrics     networkScorecardMetricsDTO `json:"metrics"`
	ComputedAt  string                     `json:"computedAt"`
}

type networkScorecardMetricsDTO struct {
	OnTimeHandoverRate      float64 `json:"onTimeHandoverRate"`
	LateAckRate             float64 `json:"lateAckRate"`
	RejectRate              float64 `json:"rejectRate"`
	ExpiryRate              float64 `json:"expiryRate"`
	PromiseKeptRate         float64 `json:"promiseKeptRate"`
	FeedFreshnessP95Minutes int     `json:"feedFreshnessP95Minutes"`
	Observations            int     `json:"observations"`
}

type networkListingDTO struct {
	ListingID         string `json:"listingId"`
	CatalogItemID     string `json:"catalogItemId"`
	State             string `json:"state"`
	AvailableQty      int    `json:"availableQty"`
	Basis             string `json:"basis"`
	AsOf              string `json:"asOf,omitempty"`
	FeedID            string `json:"feedId,omitempty"`
	SuppressionReason string `json:"suppressionReason,omitempty"`
}

type networkInventoryFeedDTO struct {
	FeedID       string `json:"feedId"`
	NodeID       string `json:"nodeId"`
	FullUpdate   bool   `json:"fullUpdate"`
	AsOf         string `json:"asOf"`
	State        string `json:"state"`
	LineCount    int    `json:"lineCount"`
	AppliedCount int    `json:"appliedCount"`
	StaleCount   int    `json:"staleCount"`
	ZeroedCount  int    `json:"zeroedCount"`
	ReceivedAt   string `json:"receivedAt"`
	AppliedAt    string `json:"appliedAt,omitempty"`
	FailureCode  string `json:"failureCode,omitempty"`
}

type networkVendorNodeDTO struct {
	NodeID              string `json:"nodeId"`
	VendorID            string `json:"vendorId"`
	VendorNodeCode      string `json:"vendorNodeCode"`
	NodeType            string `json:"nodeType"`
	Status              string `json:"status"`
	Subtype             string `json:"subtype,omitempty"`
	RegionID            string `json:"regionId,omitempty"`
	Timezone            string `json:"timezone,omitempty"`
	HandlingTimeMinutes int    `json:"handlingTimeMinutes,omitempty"`
	ProfileRevision     int    `json:"profileRevision,omitempty"`
}

type networkVendorScoreDTO struct {
	Window                  string   `json:"window"`
	Grade                   string   `json:"grade"`
	Observations            int      `json:"observations"`
	ComputedAt              string   `json:"computedAt"`
	OnTimeHandoverRate      *float64 `json:"onTimeHandoverRate,omitempty"`
	LateAckRate             *float64 `json:"lateAckRate,omitempty"`
	RejectRate              *float64 `json:"rejectRate,omitempty"`
	ExpiryRate              *float64 `json:"expiryRate,omitempty"`
	PromiseKeptRate         *float64 `json:"promiseKeptRate,omitempty"`
	FeedFreshnessP95Minutes *float64 `json:"feedFreshnessP95Minutes,omitempty"`
}

type networkDepartureDTO struct {
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

type networkWaveDTO struct {
	WaveID         string `json:"waveId"`
	ServiceDate    string `json:"serviceDate"`
	InductCutoffAt string `json:"inductCutoffAt"`
	WindowStart    string `json:"windowStart"`
	WindowEnd      string `json:"windowEnd"`
	StopCapacity   int    `json:"stopCapacity"`
	AssignedStops  int    `json:"assignedStops"`
	RemainingStops int    `json:"remainingStops"`
	State          string `json:"state"`
	CancelReason   string `json:"cancelReason,omitempty"`
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

func toNetworkScorecardDTOs(scorecards []ports.NodeScorecard) []networkScorecardDTO {
	out := make([]networkScorecardDTO, 0, len(scorecards))
	for _, s := range scorecards {
		out = append(out, networkScorecardDTO{
			ScorecardID: s.ScorecardID, Window: s.Window, State: s.State, Grade: s.Grade,
			Metrics: networkScorecardMetricsDTO{
				OnTimeHandoverRate:      s.Metrics.OnTimeHandoverRate,
				LateAckRate:             s.Metrics.LateAckRate,
				RejectRate:              s.Metrics.RejectRate,
				ExpiryRate:              s.Metrics.ExpiryRate,
				PromiseKeptRate:         s.Metrics.PromiseKeptRate,
				FeedFreshnessP95Minutes: s.Metrics.FeedFreshnessP95Minutes,
				Observations:            s.Metrics.Observations,
			},
			ComputedAt: s.ComputedAt,
		})
	}
	return out
}

func toNetworkListingDTOs(listings []ports.Listing) []networkListingDTO {
	out := make([]networkListingDTO, 0, len(listings))
	for _, l := range listings {
		out = append(out, networkListingDTO{
			ListingID: l.ListingID, CatalogItemID: l.CatalogItemID, State: l.State,
			AvailableQty:      l.Availability.AvailableQty,
			Basis:             l.Availability.Basis,
			AsOf:              l.Availability.AsOf,
			FeedID:            l.Availability.FeedID,
			SuppressionReason: l.SuppressionReason,
		})
	}
	return out
}

func toNetworkInventoryFeedDTOs(feeds []ports.InventoryFeed) []networkInventoryFeedDTO {
	out := make([]networkInventoryFeedDTO, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, networkInventoryFeedDTO{
			FeedID: f.FeedID, NodeID: f.NodeID, FullUpdate: f.FullUpdate, AsOf: f.AsOf,
			State: f.State, LineCount: f.LineCount, AppliedCount: f.AppliedCount,
			StaleCount: f.StaleCount, ZeroedCount: f.ZeroedCount, ReceivedAt: f.ReceivedAt,
			AppliedAt: f.AppliedAt, FailureCode: f.FailureCode,
		})
	}
	return out
}

func toNetworkVendorNodeDTOs(vendors []ports.VendorNode) []networkVendorNodeDTO {
	out := make([]networkVendorNodeDTO, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, networkVendorNodeDTO{
			NodeID: v.NodeID, VendorID: v.VendorID, VendorNodeCode: v.VendorNodeCode,
			NodeType: v.NodeType, Status: v.Status, Subtype: v.Subtype, RegionID: v.RegionID,
			Timezone: v.Timezone, HandlingTimeMinutes: v.HandlingTimeMinutes, ProfileRevision: v.ProfileRevision,
		})
	}
	return out
}

func toNetworkVendorScoreDTOs(windows []ports.VendorScorecardWindow) []networkVendorScoreDTO {
	out := make([]networkVendorScoreDTO, 0, len(windows))
	for _, w := range windows {
		out = append(out, networkVendorScoreDTO{
			Window: w.Window, Grade: w.Grade, Observations: w.Observations, ComputedAt: w.ComputedAt,
			OnTimeHandoverRate:      w.OnTimeHandoverRate,
			LateAckRate:             w.LateAckRate,
			RejectRate:              w.RejectRate,
			ExpiryRate:              w.ExpiryRate,
			PromiseKeptRate:         w.PromiseKeptRate,
			FeedFreshnessP95Minutes: w.FeedFreshnessP95Minutes,
		})
	}
	return out
}

func toNetworkDepartureDTOs(departures []ports.LinehaulDeparture) []networkDepartureDTO {
	out := make([]networkDepartureDTO, 0, len(departures))
	for _, dep := range departures {
		out = append(out, networkDepartureDTO{
			DepartureID: dep.DepartureID, LaneID: dep.LaneID, Kind: dep.Kind,
			OriginNodeID: dep.OriginNodeID, DestinationNodeID: dep.DestinationNodeID,
			CutoffAt: dep.CutoffAt, DepartsAt: dep.DepartsAt, ArrivesAt: dep.ArrivesAt,
			CapacityPackages: dep.CapacityPackages, BookedPackages: dep.BookedPackages,
			RemainingPackages: dep.RemainingPackages, State: dep.State,
			DepartedAt: dep.DepartedAt, ArrivedAt: dep.ArrivedAt,
			CancelledAt: dep.CancelledAt, CancelReason: dep.CancelReason,
		})
	}
	return out
}

func toNetworkWaveDTOs(waves []ports.DeliveryWave) []networkWaveDTO {
	out := make([]networkWaveDTO, 0, len(waves))
	for _, w := range waves {
		out = append(out, networkWaveDTO{
			WaveID: w.WaveID, ServiceDate: w.ServiceDate, InductCutoffAt: w.InductCutoffAt,
			WindowStart: w.WindowStart, WindowEnd: w.WindowEnd,
			StopCapacity: w.StopCapacity, AssignedStops: w.AssignedStops, RemainingStops: w.RemainingStops,
			State: w.State, CancelReason: w.CancelReason,
		})
	}
	return out
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

// newNetworkSection maps the outlook envelope onto the wire section the
// inbound tools already publish (same fields, same meaning as
// newInboundSection in inbound_outlook.go).
func newNetworkSection[T any](status usecases.OutlookSectionStatus, items []T) inboundSection[T] {
	return inboundSection[T]{Omitted: status.Omitted, Complete: status.Complete, Count: len(items), Items: items}
}
