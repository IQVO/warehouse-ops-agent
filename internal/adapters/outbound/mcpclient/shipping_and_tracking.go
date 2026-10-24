package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// ShippingAndTracking implements ports.ShippingAndTrackingClient by
// calling shipping-and-tracking's single published READ tool
// get_journey.
//
// shipping-and-tracking's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestShippingAndTrackingClientCallsOnlyPinnedTools allows exactly this
// one name. The argument keys and result shapes are pinned against
// shipping-and-tracking's published registry in
// testdata/shipping_and_tracking_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type ShippingAndTracking struct {
	session *Session
}

// NewShippingAndTracking builds a ShippingAndTracking client for the
// given connection config.
func NewShippingAndTracking(cfg Config) *ShippingAndTracking {
	cfg.Name = "shipping-and-tracking"
	return &ShippingAndTracking{session: New(cfg)}
}

var _ ports.ShippingAndTrackingClient = (*ShippingAndTracking)(nil)

// GetJourney calls get_journey for one tracking number.
func (c *ShippingAndTracking) GetJourney(ctx context.Context, trackingNumber string) (ports.PackageJourney, error) {
	var out ports.PackageJourney
	err := c.session.callTool(ctx, "get_journey", map[string]any{"trackingNumber": trackingNumber}, &out)
	return out, err
}
