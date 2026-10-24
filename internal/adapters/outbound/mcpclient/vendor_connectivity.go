package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// VendorConnectivity implements ports.VendorConnectivityClient by
// calling vendor-connectivity's nine published READ tools get_vendor,
// get_vendor_node, list_vendor_nodes, get_purchase_order,
// poll_purchase_orders, get_inbound_shipment, poll_inbound_shipments,
// get_transaction and get_node_scorecard.
//
// vendor-connectivity's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestVendorConnectivityClientCallsOnlyPinnedTools allows exactly these
// nine names. The argument keys and result shapes are pinned against
// vendor-connectivity's published registry in
// testdata/vendor_connectivity_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type VendorConnectivity struct {
	session *Session
}

// NewVendorConnectivity builds a VendorConnectivity client for the
// given connection config.
func NewVendorConnectivity(cfg Config) *VendorConnectivity {
	cfg.Name = "vendor-connectivity"
	return &VendorConnectivity{session: New(cfg)}
}

var _ ports.VendorConnectivityClient = (*VendorConnectivity)(nil)

// GetVendor calls get_vendor for one vendor account.
func (c *VendorConnectivity) GetVendor(ctx context.Context, vendorID string) (ports.VendorAccount, error) {
	var out ports.VendorAccount
	err := c.session.callTool(ctx, "get_vendor", map[string]any{"vendorId": vendorID}, &out)
	return out, err
}

// GetVendorNode calls get_vendor_node for one node of one vendor.
func (c *VendorConnectivity) GetVendorNode(ctx context.Context, vendorID, nodeID string) (ports.VendorNode, error) {
	var out ports.VendorNode
	err := c.session.callTool(ctx, "get_vendor_node", map[string]any{"vendorId": vendorID, "nodeId": nodeID}, &out)
	return out, err
}

// ListVendorNodes calls list_vendor_nodes for one vendor.
func (c *VendorConnectivity) ListVendorNodes(ctx context.Context, vendorID string) (ports.VendorNodeList, error) {
	var out ports.VendorNodeList
	err := c.session.callTool(ctx, "list_vendor_nodes", map[string]any{"vendorId": vendorID}, &out)
	return out, err
}

// GetPurchaseOrder calls get_purchase_order for one purchase order as
// the vendor sees it.
func (c *VendorConnectivity) GetPurchaseOrder(ctx context.Context, vendorID, poNumber string) (ports.VendorPurchaseOrder, error) {
	var out ports.VendorPurchaseOrder
	err := c.session.callTool(ctx, "get_purchase_order", map[string]any{"vendorId": vendorID, "poNumber": poNumber}, &out)
	return out, err
}

// PollPurchaseOrders calls poll_purchase_orders. Beyond the required
// vendor, zero query fields are omitted so vendor-connectivity applies
// its own defaults (every node and issue date, the first page at the
// service default of 50).
func (c *VendorConnectivity) PollPurchaseOrders(ctx context.Context, query ports.VendorPollQuery) (ports.VendorPurchaseOrderPage, error) {
	args := vendorPollArgs(query)
	var out ports.VendorPurchaseOrderPage
	err := c.session.callTool(ctx, "poll_purchase_orders", args, &out)
	return out, err
}

// GetInboundShipment calls get_inbound_shipment for one inbound
// shipment of one vendor.
func (c *VendorConnectivity) GetInboundShipment(ctx context.Context, vendorID, shipmentID string) (ports.VendorInboundShipment, error) {
	var out ports.VendorInboundShipment
	err := c.session.callTool(ctx, "get_inbound_shipment", map[string]any{"vendorId": vendorID, "shipmentId": shipmentID}, &out)
	return out, err
}

// PollInboundShipments calls poll_inbound_shipments. Beyond the
// required vendor, zero query fields are omitted so vendor-connectivity
// applies its own defaults.
func (c *VendorConnectivity) PollInboundShipments(ctx context.Context, query ports.VendorPollQuery) (ports.VendorInboundShipmentPage, error) {
	args := vendorPollArgs(query)
	var out ports.VendorInboundShipmentPage
	err := c.session.callTool(ctx, "poll_inbound_shipments", args, &out)
	return out, err
}

// GetTransaction calls get_transaction to follow one asynchronous vendor
// write by the transactionId its 202 answer returned.
func (c *VendorConnectivity) GetTransaction(ctx context.Context, vendorID, transactionID string) (ports.VendorTransaction, error) {
	var out ports.VendorTransaction
	err := c.session.callTool(ctx, "get_transaction", map[string]any{"vendorId": vendorID, "transactionId": transactionID}, &out)
	return out, err
}

// GetNodeScorecard calls get_node_scorecard for one vendor node: the
// rolling scorecards node-performance computes, relayed live.
func (c *VendorConnectivity) GetNodeScorecard(ctx context.Context, vendorID, nodeID string) (ports.VendorNodeScorecards, error) {
	var out ports.VendorNodeScorecards
	err := c.session.callTool(ctx, "get_node_scorecard", map[string]any{"vendorId": vendorID, "nodeId": nodeID}, &out)
	return out, err
}

// vendorPollArgs starts a poll call's argument map: the required vendor
// plus whichever optional filters are set, so vendor-connectivity
// applies its own defaults for the rest.
func vendorPollArgs(query ports.VendorPollQuery) map[string]any {
	args := map[string]any{"vendorId": query.VendorID}
	if query.Limit != 0 {
		args["limit"] = query.Limit
	}
	setIfNotEmpty(args, "nodeId", query.NodeID)
	setIfNotEmpty(args, "createdAfter", query.CreatedAfter)
	setIfNotEmpty(args, "nextToken", query.NextToken)
	return args
}
