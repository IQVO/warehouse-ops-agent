package mcpclient

import (
	"context"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// OrderManagement implements ports.OrderManagementMCPClient by calling
// order-management's published read tool get_promise_health (ADR 0024).
// order-management also publishes get_order; this client does not name it
// because no use case of this agent calls it — a wired-but-unused method
// is the bug class this change closes (gap A8).
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error; the
// consuming use case decides what to do with it.
type OrderManagement struct {
	session *Session
}

// NewOrderManagement builds an OrderManagement client for the given
// connection config.
func NewOrderManagement(cfg Config) *OrderManagement {
	cfg.Name = "order-management"
	return &OrderManagement{session: New(cfg)}
}

var _ ports.OrderManagementMCPClient = (*OrderManagement)(nil)

// GetPromiseHealth calls get_promise_health for the window [query.From,
// query.To), both sent as UTC RFC 3339 instants under the tool's published
// from/to keys. An empty PathId is omitted so no path filter applies.
func (c *OrderManagement) GetPromiseHealth(ctx context.Context, query ports.PromiseHealthQuery) (ports.PromiseHealth, error) {
	args := map[string]any{
		"from": query.From.UTC().Format(time.RFC3339Nano),
		"to":   query.To.UTC().Format(time.RFC3339Nano),
	}
	setIfNotEmpty(args, "pathId", query.PathId)
	var out ports.PromiseHealth
	err := c.session.callTool(ctx, "get_promise_health", args, &out)
	return out, err
}
