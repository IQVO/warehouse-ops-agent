package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// CustomerOrdering implements ports.CustomerOrderingClient by calling
// customer-ordering's three published READ tools get_order,
// get_order_tracking and get_ship_to.
//
// customer-ordering's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and TestCustomerOrderingClientCallsOnlyPinnedTools
// allows exactly these three names. The argument keys and result shapes
// are pinned against customer-ordering's published registry in
// testdata/customer_ordering_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type CustomerOrdering struct {
	session *Session
}

// NewCustomerOrdering builds a CustomerOrdering client for the given
// connection config.
func NewCustomerOrdering(cfg Config) *CustomerOrdering {
	cfg.Name = "customer-ordering"
	return &CustomerOrdering{session: New(cfg)}
}

var _ ports.CustomerOrderingClient = (*CustomerOrdering)(nil)

// GetOrder calls get_order for one order id.
func (c *CustomerOrdering) GetOrder(ctx context.Context, orderID string) (ports.CustomerOrder, error) {
	var out ports.CustomerOrder
	err := c.session.callTool(ctx, "get_order", map[string]any{"orderId": orderID}, &out)
	return out, err
}

// GetOrderTracking calls get_order_tracking for one order id.
func (c *CustomerOrdering) GetOrderTracking(ctx context.Context, orderID string) (ports.OrderTracking, error) {
	var out ports.OrderTracking
	err := c.session.callTool(ctx, "get_order_tracking", map[string]any{"orderId": orderID}, &out)
	return out, err
}

// GetShipTo calls get_ship_to for one order id. The address is synthetic
// PII held only by customer-ordering and served only by this tool.
func (c *CustomerOrdering) GetShipTo(ctx context.Context, orderID string) (ports.ShipToAddress, error) {
	var out ports.ShipToAddress
	err := c.session.callTool(ctx, "get_ship_to", map[string]any{"orderId": orderID}, &out)
	return out, err
}
