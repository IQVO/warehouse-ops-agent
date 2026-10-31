package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// OrderSourcing implements ports.OrderSourcingClient by calling
// order-sourcing's two published READ tools get_assignment and
// list_assignments.
//
// order-sourcing's MCP server is read-only by its own governance test;
// this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestOrderSourcingClientCallsOnlyPinnedTools allows exactly these two
// names. The argument keys and result shapes are pinned against
// order-sourcing's published registry in
// testdata/order_sourcing_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type OrderSourcing struct {
	session *Session
}

// NewOrderSourcing builds an OrderSourcing client for the given
// connection config.
func NewOrderSourcing(cfg Config) *OrderSourcing {
	cfg.Name = "order-sourcing"
	return &OrderSourcing{session: New(cfg)}
}

var _ ports.OrderSourcingClient = (*OrderSourcing)(nil)

// GetAssignment calls get_assignment for one fulfillment assignment id.
func (c *OrderSourcing) GetAssignment(ctx context.Context, assignmentID string) (ports.FulfillmentAssignment, error) {
	var out ports.FulfillmentAssignment
	err := c.session.callTool(ctx, "get_assignment", map[string]any{"assignmentId": assignmentID}, &out)
	return out, err
}

// ListAssignments calls list_assignments. Zero query fields are omitted
// so order-sourcing applies its own defaults (every assignment, the
// default page size of 100).
func (c *OrderSourcing) ListAssignments(ctx context.Context, query ports.AssignmentListQuery) (ports.AssignmentPage, error) {
	args := map[string]any{}
	if query.Limit != 0 {
		args["limit"] = query.Limit
	}
	setIfNotEmpty(args, "orderId", query.OrderID)
	setIfNotEmpty(args, "nodeId", query.NodeID)
	setIfNotEmpty(args, "state", query.State)
	var out ports.AssignmentPage
	err := c.session.callTool(ctx, "list_assignments", args, &out)
	return out, err
}
