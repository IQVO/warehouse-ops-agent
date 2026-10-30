package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// SellerInbound implements ports.SellerInboundClient by calling
// seller-inbound's four published READ tools get_seller_scorecard,
// get_inbound_plan, get_shipment_labels and get_inbound_shipment.
//
// seller-inbound's MCP server is read-only by its own governance test;
// this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestSellerInbound_CallsOnlyPinnedTools allows exactly these four
// names. The argument keys and result shapes are pinned against
// seller-inbound's published registry in
// testdata/seller_inbound_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput, a
// *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming use
// case decides what to do with it. No use case consumes this client yet
// (ADR 0027).
type SellerInbound struct {
	session *Session
}

// NewSellerInbound builds a SellerInbound client for the given connection
// config.
func NewSellerInbound(cfg Config) *SellerInbound {
	cfg.Name = "seller-inbound"
	return &SellerInbound{session: New(cfg)}
}

var _ ports.SellerInboundClient = (*SellerInbound)(nil)

// GetSellerScorecard calls get_seller_scorecard. An empty window is
// omitted so seller-inbound applies its own default (30d).
func (c *SellerInbound) GetSellerScorecard(ctx context.Context, sellerID, window string) (ports.SellerScorecard, error) {
	args := map[string]any{"sellerId": sellerID}
	setIfNotEmpty(args, "window", window)
	var out ports.SellerScorecard
	err := c.session.callTool(ctx, "get_seller_scorecard", args, &out)
	return out, err
}

// GetInboundPlan calls get_inbound_plan for one inbound plan id.
func (c *SellerInbound) GetInboundPlan(ctx context.Context, planID string) (ports.InboundPlan, error) {
	var out ports.InboundPlan
	err := c.session.callTool(ctx, "get_inbound_plan", map[string]any{"planId": planID}, &out)
	return out, err
}

// GetShipmentLabels calls get_shipment_labels for one labelled shipment.
func (c *SellerInbound) GetShipmentLabels(ctx context.Context, shipmentID string) (ports.ShipmentLabels, error) {
	var out ports.ShipmentLabels
	err := c.session.callTool(ctx, "get_shipment_labels", map[string]any{"shipmentId": shipmentID}, &out)
	return out, err
}

// GetInboundShipment calls get_inbound_shipment for one shipment id.
func (c *SellerInbound) GetInboundShipment(ctx context.Context, shipmentID string) (ports.InboundShipment, error) {
	var out ports.InboundShipment
	err := c.session.callTool(ctx, "get_inbound_shipment", map[string]any{"shipmentId": shipmentID}, &out)
	return out, err
}
