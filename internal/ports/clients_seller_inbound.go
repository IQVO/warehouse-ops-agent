package ports

import (
	"context"
	"time"
)

// SellerInboundClient is the outbound port for seller-inbound's published
// read tools (get_seller_scorecard, get_inbound_plan, get_shipment_labels,
// get_inbound_shipment, get_placement_plan, list_placement_plans).
// Wired but not yet consumed by any use case (ADR 0027): it exists so the
// partner-inbound read surface is reachable the moment a decision-support
// use case needs it.
type SellerInboundClient interface {
	// GetSellerScorecard answers a seller's rolling-window compliance
	// scorecard; an empty window means seller-inbound's own default (30d).
	GetSellerScorecard(ctx context.Context, sellerID, window string) (SellerScorecard, error)
	// GetInboundPlan reads one inbound plan with its lines.
	GetInboundPlan(ctx context.Context, planID string) (InboundPlan, error)
	// GetShipmentLabels reads the label data of a labelled shipment, one
	// entry per carton.
	GetShipmentLabels(ctx context.Context, shipmentID string) (ShipmentLabels, error)
	// GetInboundShipment reads one inbound shipment with its cartons.
	GetInboundShipment(ctx context.Context, shipmentID string) (InboundShipment, error)
	// GetPlacementPlan reads one placement plan (ADR 0012): the per-node
	// inbound placement suggestions of one seller's catalogue item.
	GetPlacementPlan(ctx context.Context, planID string) (PlacementPlan, error)
	// ListPlacementPlans lists the placement plans of one seller and
	// catalogue item, newest first; a zero limit is omitted so
	// seller-inbound applies its own default page size.
	ListPlacementPlans(ctx context.Context, sellerID, catalogItemID string, limit int) (PlacementPlans, error)
}

// SellerScorecard mirrors seller-inbound's get_seller_scorecard result
// (the REST body of GET /sellers/{sellerId}/scorecard) field-for-field. A
// rate whose denominator is zero is omitted, never null or NaN; the kind
// map is always present.
type SellerScorecard struct {
	SellerID                   string         `json:"sellerId"`
	Window                     string         `json:"window"`
	WindowStart                time.Time      `json:"windowStart"`
	WindowEnd                  time.Time      `json:"windowEnd"`
	ShipmentsReceived          int            `json:"shipmentsReceived"`
	CartonsReported            int            `json:"cartonsReported"`
	CartonsReceived            int            `json:"cartonsReceived"`
	ReceiptAccuracyRate        *float64       `json:"receiptAccuracyRate,omitempty"`
	DiscrepancyRatePerShipment *float64       `json:"discrepancyRatePerShipment,omitempty"`
	DiscrepanciesByKind        map[string]int `json:"discrepanciesByKind"`
	ComplianceViolations       int            `json:"complianceViolations"`
	ComplianceTier             string         `json:"complianceTier"`
}

// InboundPlanLine mirrors one line of seller-inbound's plan view.
type InboundPlanLine struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
}

// InboundPlan mirrors seller-inbound's get_inbound_plan result (the REST
// body of GET /inbound-plans/{planId}).
type InboundPlan struct {
	PlanID            string            `json:"planId"`
	SellerID          string            `json:"sellerId"`
	DestinationNodeID string            `json:"destinationNodeId"`
	PackingOption     string            `json:"packingOption"`
	State             string            `json:"state"`
	Lines             []InboundPlanLine `json:"lines"`
	CreatedAt         time.Time         `json:"createdAt"`
	ConfirmedAt       *time.Time        `json:"confirmedAt,omitempty"`
	CancelledAt       *time.Time        `json:"cancelledAt,omitempty"`
}

// CartonContent mirrors one content entry of a carton.
type CartonContent struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
	Lot           string `json:"lot,omitempty"`
	Expiry        string `json:"expiry,omitempty"`
}

// CartonDims mirrors a carton's millimetre dimensions.
type CartonDims struct {
	L int `json:"l"`
	W int `json:"w"`
	H int `json:"h"`
}

// Carton mirrors one carton of seller-inbound's shipment view.
type Carton struct {
	SSCC          string          `json:"sscc"`
	ParentSSCC    string          `json:"parentSscc,omitempty"`
	Kind          string          `json:"kind"`
	Contents      []CartonContent `json:"contents"`
	GrossWeightG  int             `json:"grossWeightG"`
	DimsMm        CartonDims      `json:"dimsMm"`
	Sealed        bool            `json:"sealed"`
	ReceiptStatus string          `json:"receiptStatus,omitempty"`
}

// InboundShipment mirrors seller-inbound's get_inbound_shipment result
// (the REST body of GET /inbound-shipments/{shipmentId}).
type InboundShipment struct {
	ShipmentID string     `json:"shipmentId"`
	PlanID     string     `json:"planId"`
	SellerID   string     `json:"sellerId"`
	NodeID     string     `json:"nodeId"`
	State      string     `json:"state"`
	Cartons    []Carton   `json:"cartons"`
	CreatedAt  time.Time  `json:"createdAt"`
	LabelledAt *time.Time `json:"labelledAt,omitempty"`
	ShippedAt  *time.Time `json:"shippedAt,omitempty"`
	ReceivedAt *time.Time `json:"receivedAt,omitempty"`
}

// LabelEntry mirrors one carton label of seller-inbound's labels view.
type LabelEntry struct {
	LabelRef          string          `json:"labelRef"`
	SSCC              string          `json:"sscc"`
	GS1128            string          `json:"gs1128"`
	HumanReadable     string          `json:"humanReadable"`
	ParentSSCC        string          `json:"parentSscc,omitempty"`
	Kind              string          `json:"kind"`
	Contents          []CartonContent `json:"contents"`
	ContentSummary    string          `json:"contentSummary"`
	TotalEaches       int             `json:"totalEaches"`
	DestinationNodeID string          `json:"destinationNodeId"`
}

// ShipmentLabels mirrors seller-inbound's get_shipment_labels result (the
// REST body of GET /inbound-shipments/{shipmentId}/labels).
type ShipmentLabels struct {
	ShipmentID        string       `json:"shipmentId"`
	State             string       `json:"state"`
	DestinationNodeID string       `json:"destinationNodeId"`
	Labels            []LabelEntry `json:"labels"`
}

// PlacementSuggestion mirrors one per-node suggestion of a placement
// plan (ADR 0012): the units to place at a node and the evidence behind
// them (the region's demand and the node's availability over the same
// cover days).
type PlacementSuggestion struct {
	NodeID                string `json:"nodeId"`
	RegionID              string `json:"regionId"`
	SuggestedUnits        int    `json:"suggestedUnits"`
	RegionDemandUnits     int    `json:"regionDemandUnits"`
	NodeAvailabilityUnits int    `json:"nodeAvailabilityUnits"`
	CoverDays             int    `json:"coverDays"`
}

// PlacementPlan mirrors seller-inbound's get_placement_plan result and
// each list_placement_plans item (ADR 0012). State is SUGGESTED,
// ACCEPTED or SUPERSEDED; AcceptedAt and SupersededAt are nil until the
// state change happens.
type PlacementPlan struct {
	PlanID        string                `json:"planId"`
	SellerID      string                `json:"sellerId"`
	CatalogItemID string                `json:"catalogItemId"`
	CoverDays     int                   `json:"coverDays"`
	WindowDays    int                   `json:"windowDays"`
	Basis         string                `json:"basis"`
	State         string                `json:"state"`
	Suggestions   []PlacementSuggestion `json:"suggestions"`
	CreatedAt     time.Time             `json:"createdAt"`
	AcceptedAt    *time.Time            `json:"acceptedAt,omitempty"`
	SupersededAt  *time.Time            `json:"supersededAt,omitempty"`
}

// PlacementPlans mirrors seller-inbound's list_placement_plans result.
type PlacementPlans struct {
	Items []PlacementPlan `json:"items"`
}
