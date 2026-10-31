package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// DeliveryPromise implements ports.DeliveryPromiseClient by calling
// delivery-promise's five published READ tools get_current_policy,
// get_delivery_offer, get_promise, list_promise_books and
// list_promises_by_order.
//
// delivery-promise's MCP server is read-only by its own governance test;
// this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestDeliveryPromiseClientCallsOnlyPinnedTools allows exactly these
// five names. The argument keys and result shapes are pinned against
// delivery-promise's published registry in
// testdata/delivery_promise_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type DeliveryPromise struct {
	session *Session
}

// NewDeliveryPromise builds a DeliveryPromise client for the given
// connection config.
func NewDeliveryPromise(cfg Config) *DeliveryPromise {
	cfg.Name = "delivery-promise"
	return &DeliveryPromise{session: New(cfg)}
}

var _ ports.DeliveryPromiseClient = (*DeliveryPromise)(nil)

// GetCurrentPolicy calls get_current_policy. It takes no arguments;
// delivery-promise answers the policy in force.
func (c *DeliveryPromise) GetCurrentPolicy(ctx context.Context) (ports.PromisePolicy, error) {
	var out ports.PromisePolicy
	err := c.session.callTool(ctx, "get_current_policy", map[string]any{}, &out)
	return out, err
}

// GetDeliveryOffer calls get_delivery_offer for one offer id.
func (c *DeliveryPromise) GetDeliveryOffer(ctx context.Context, offerID string) (ports.DeliveryOffer, error) {
	var out ports.DeliveryOffer
	err := c.session.callTool(ctx, "get_delivery_offer", map[string]any{"offerId": offerID}, &out)
	return out, err
}

// GetPromise calls get_promise for one promise id.
func (c *DeliveryPromise) GetPromise(ctx context.Context, promiseID string) (ports.DeliveryPromise, error) {
	var out ports.DeliveryPromise
	err := c.session.callTool(ctx, "get_promise", map[string]any{"promiseId": promiseID}, &out)
	return out, err
}

// ListPromiseBooks calls list_promise_books for one node's books whose
// pickup cutoff falls on one date (YYYY-MM-DD in the node's own time
// zone).
func (c *DeliveryPromise) ListPromiseBooks(ctx context.Context, nodeID, date string) (ports.PromiseBooks, error) {
	var out ports.PromiseBooks
	err := c.session.callTool(ctx, "list_promise_books", map[string]any{"nodeId": nodeID, "date": date}, &out)
	return out, err
}

// ListPromisesByOrder calls list_promises_by_order for one customer
// order.
func (c *DeliveryPromise) ListPromisesByOrder(ctx context.Context, orderID string) (ports.PromisesByOrder, error) {
	var out ports.PromisesByOrder
	err := c.session.callTool(ctx, "list_promises_by_order", map[string]any{"orderId": orderID}, &out)
	return out, err
}
