// Package ports (this file): the outbound port for the
// vendor-connectivity bounded context (retail network), the fleet's
// vendor-facing Open Host Service: purchase orders released to vendors,
// their acknowledgement and shipment transactions, inbound shipments
// and the vendor node registry.
//
// vendor-connectivity's MCP server is READ-ONLY (its own governance
// test pins its nine tools, every one annotated readOnlyHint). This
// port mirrors all nine: get_vendor, get_vendor_node,
// list_vendor_nodes, get_purchase_order, poll_purchase_orders,
// get_inbound_shipment, poll_inbound_shipments, get_transaction and
// get_node_scorecard. vendor-connectivity's writes (acknowledge an
// order, submit inventory, confirm a shipment) are asynchronous 202
// REST transactions upstream of it and have no method here. The
// zero-write fitness test (internal/architecture/zerowrite) fails the
// build if the mcpclient adapter ever names a write-verb tool, and the
// contract snapshot test in mcpclient pins the published tools against
// vendor-connectivity's published registry.
package ports

import (
	"context"
)

// VendorConnectivityClient is the outbound port for
// vendor-connectivity's nine published read tools.
type VendorConnectivityClient interface {
	// GetVendor calls get_vendor: one vendor account.
	GetVendor(ctx context.Context, vendorID string) (VendorAccount, error)
	// GetVendorNode calls get_vendor_node: one fulfilment node of a
	// vendor.
	GetVendorNode(ctx context.Context, vendorID, nodeID string) (VendorNode, error)
	// ListVendorNodes calls list_vendor_nodes: the nodes the network has
	// registered for one vendor.
	ListVendorNodes(ctx context.Context, vendorID string) (VendorNodeList, error)
	// GetPurchaseOrder calls get_purchase_order: one purchase order as
	// the vendor sees it.
	GetPurchaseOrder(ctx context.Context, vendorID, poNumber string) (VendorPurchaseOrder, error)
	// PollPurchaseOrders calls poll_purchase_orders: the purchase orders
	// released to one vendor, one page at a time. Beyond the required
	// vendor, zero query fields are omitted so vendor-connectivity
	// applies its own defaults.
	PollPurchaseOrders(ctx context.Context, query VendorPollQuery) (VendorPurchaseOrderPage, error)
	// GetInboundShipment calls get_inbound_shipment: one inbound
	// shipment with its pallets, cartons and item lines.
	GetInboundShipment(ctx context.Context, vendorID, shipmentID string) (VendorInboundShipment, error)
	// PollInboundShipments calls poll_inbound_shipments: the inbound
	// shipments bound for one vendor's nodes, one page at a time.
	PollInboundShipments(ctx context.Context, query VendorPollQuery) (VendorInboundShipmentPage, error)
	// GetTransaction calls get_transaction: the outcome of one
	// asynchronous vendor write.
	GetTransaction(ctx context.Context, vendorID, transactionID string) (VendorTransaction, error)
	// GetNodeScorecard calls get_node_scorecard: a vendor node's rolling
	// scorecards, relayed live from node-performance.
	GetNodeScorecard(ctx context.Context, vendorID, nodeID string) (VendorNodeScorecards, error)
}

// VendorPollQuery filters and pages the two poll tools: the required
// vendor, and the optional node, created-after instant, page size
// (1..200) and the previous page's nextToken.
type VendorPollQuery struct {
	VendorID     string
	NodeID       string
	CreatedAfter string
	NextToken    string
	Limit        int
}

// --- vendor-connectivity read-model DTOs -------------------------------------
//
// Field-for-field mirrors of vendor-connectivity's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/vendor_connectivity_tools.golden.json.

// VendorAccount mirrors vendorView (get_vendor). State is ACTIVE or
// SUSPENDED; reads stay allowed while suspended.
type VendorAccount struct {
	VendorID         string `json:"vendorId"`
	Name             string `json:"name"`
	AckWindowMinutes int    `json:"ackWindowMinutes"`
	State            string `json:"state"`
}

// VendorNode mirrors vendorNodeView (get_vendor_node and each
// list_vendor_nodes item). Status is PROPOSED, ACTIVE, SUSPENDED or
// RETIRED.
type VendorNode struct {
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

// VendorNodeList mirrors the list_vendor_nodes result.
type VendorNodeList struct {
	Nodes []VendorNode `json:"nodes"`
}

// VendorAcknowledgement mirrors a purchase order's acknowledgement once
// its transaction succeeded.
type VendorAcknowledgement struct {
	Decision      string `json:"decision"`
	TransactionID string `json:"transactionId"`
	ReasonCode    string `json:"reasonCode,omitempty"`
}

// VendorPackLevel mirrors one level of a PO line's packaging hierarchy.
type VendorPackLevel struct {
	Level        string `json:"level"`
	GTIN         string `json:"gtin"`
	UnitsPerPack int    `json:"unitsPerPack"`
	ChildGtin    string `json:"childGtin,omitempty"`
}

// VendorPOLine mirrors one purchase-order line: the listing it was
// released against, the quantity, and the packaging hierarchy.
type VendorPOLine struct {
	LineNo    int               `json:"lineNo"`
	ListingID string            `json:"listingId"`
	Quantity  int               `json:"quantity"`
	Packaging []VendorPackLevel `json:"packaging"`
}

// VendorPOLabel mirrors one shipping label of a purchase order.
type VendorPOLabel struct {
	LabelRef       string `json:"labelRef"`
	TrackingNumber string `json:"trackingNumber"`
	Carrier        string `json:"carrier"`
	PackageNo      int    `json:"packageNo,omitempty"`
}

// VendorPurchaseOrder mirrors purchaseOrderView (get_purchase_order and
// each poll_purchase_orders item). State is ISSUED, ACK_PROCESSING,
// ACCEPTED, REJECTED, EXPIRED, SHIPMENT_PROCESSING, SHIPPED or
// CLOSED.
type VendorPurchaseOrder struct {
	PoNumber        string                 `json:"poNumber"`
	VendorID        string                 `json:"vendorId"`
	NodeID          string                 `json:"nodeId"`
	State           string                 `json:"state"`
	IssuedAt        string                 `json:"issuedAt"`
	RequiredShipBy  string                 `json:"requiredShipBy"`
	AckBy           string                 `json:"ackBy"`
	Lines           []VendorPOLine         `json:"lines"`
	Labels          []VendorPOLabel        `json:"labels"`
	TrackingNumbers []string               `json:"trackingNumbers"`
	Acknowledgement *VendorAcknowledgement `json:"acknowledgement,omitempty"`
	ShippedAt       string                 `json:"shippedAt,omitempty"`
}

// VendorPurchaseOrderPage mirrors the poll_purchase_orders result.
type VendorPurchaseOrderPage struct {
	PurchaseOrders []VendorPurchaseOrder `json:"purchaseOrders"`
	NextToken      string                `json:"nextToken,omitempty"`
}

// VendorUnitContent mirrors one item line in eaches inside a handling
// unit: gtin and quantity, plus lot and expiry when declared.
type VendorUnitContent struct {
	GTIN      string `json:"gtin"`
	QtyEaches int    `json:"qtyEaches"`
	SKU       string `json:"sku,omitempty"`
	Lot       string `json:"lot,omitempty"`
	Expiry    string `json:"expiry,omitempty"`
}

// VendorUnitDims mirrors a handling unit's dimensions in mm.
type VendorUnitDims struct {
	L int `json:"l"`
	W int `json:"w"`
	H int `json:"h"`
}

// VendorHandlingUnit mirrors one carton of a shipment: its SSCC, kind,
// contents, and dims/weight once declared. Top-level cartons carry
// ParentSscc when stacked on a pallet.
type VendorHandlingUnit struct {
	SSCC         string              `json:"sscc"`
	Kind         string              `json:"kind"`
	Contents     []VendorUnitContent `json:"contents"`
	ParentSscc   string              `json:"parentSscc,omitempty"`
	GrossWeightG int                 `json:"grossWeightG,omitempty"`
	DimsMm       *VendorUnitDims     `json:"dimsMm,omitempty"`
}

// VendorPallet mirrors one pallet of a shipment: its SSCC and the
// cartons stacked on it.
type VendorPallet struct {
	SSCC    string               `json:"sscc"`
	Cartons []VendorHandlingUnit `json:"cartons"`
}

// VendorInboundShipment mirrors shipmentView (get_inbound_shipment and
// each poll_inbound_shipments item). Status is SHIPPED or RECEIVED.
type VendorInboundShipment struct {
	ShipmentID string               `json:"shipmentId"`
	NodeID     string               `json:"nodeId"`
	ShippedAt  string               `json:"shippedAt"`
	Status     string               `json:"status"`
	Pallets    []VendorPallet       `json:"pallets"`
	Cartons    []VendorHandlingUnit `json:"cartons"`
	SellerID   string               `json:"sellerId,omitempty"`
}

// VendorInboundShipmentPage mirrors the poll_inbound_shipments result.
type VendorInboundShipmentPage struct {
	InboundShipments []VendorInboundShipment `json:"inboundShipments"`
	NextToken        string                  `json:"nextToken,omitempty"`
}

// VendorTransactionError mirrors one error of a failed transaction.
type VendorTransactionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

// VendorTransaction mirrors transactionView (get_transaction): the
// outcome of one asynchronous vendor write. Kind is ACKNOWLEDGEMENT,
// INVENTORY, NODE_REGISTRATION, NODE_PROFILE or SHIPMENT_CONFIRMATION;
// status is PENDING, APPLIED or FAILED.
type VendorTransaction struct {
	TransactionID string                   `json:"transactionId"`
	VendorID      string                   `json:"vendorId"`
	Kind          string                   `json:"kind"`
	Status        string                   `json:"status"`
	ReceivedAt    string                   `json:"receivedAt"`
	Errors        []VendorTransactionError `json:"errors"`
	NodeID        string                   `json:"nodeId,omitempty"`
	PoNumber      string                   `json:"poNumber,omitempty"`
	ProcessedAt   string                   `json:"processedAt,omitempty"`
}

// VendorScorecardWindow mirrors one window of a vendor node's scorecard:
// grade, observations and the rate family, null until observed.
type VendorScorecardWindow struct {
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

// VendorNodeScorecards mirrors the get_node_scorecard result.
type VendorNodeScorecards struct {
	NodeID  string                  `json:"nodeId"`
	Windows []VendorScorecardWindow `json:"windows"`
}
