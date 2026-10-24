package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// LastMileDelivery implements ports.LastMileDeliveryClient by calling
// last-mile-delivery's three published READ tools list_waves,
// get_wave_stops and get_route.
//
// last-mile-delivery's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestLastMileDeliveryClientCallsOnlyPinnedTools allows exactly these
// three names. The argument keys and result shapes are pinned against
// last-mile-delivery's published registry in
// testdata/last_mile_delivery_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type LastMileDelivery struct {
	session *Session
}

// NewLastMileDelivery builds a LastMileDelivery client for the given
// connection config.
func NewLastMileDelivery(cfg Config) *LastMileDelivery {
	cfg.Name = "last-mile-delivery"
	return &LastMileDelivery{session: New(cfg)}
}

var _ ports.LastMileDeliveryClient = (*LastMileDelivery)(nil)

// ListWaves calls list_waves for one station's waves of one service
// date (YYYY-MM-DD, the station's local date).
func (c *LastMileDelivery) ListWaves(ctx context.Context, stationNodeID, serviceDate string) (ports.DeliveryWaves, error) {
	var out ports.DeliveryWaves
	err := c.session.callTool(ctx, "list_waves", map[string]any{"stationNodeId": stationNodeID, "serviceDate": serviceDate}, &out)
	return out, err
}

// GetWaveStops calls get_wave_stops for one wave.
func (c *LastMileDelivery) GetWaveStops(ctx context.Context, waveID string) (ports.WaveStops, error) {
	var out ports.WaveStops
	err := c.session.callTool(ctx, "get_wave_stops", map[string]any{"waveId": waveID}, &out)
	return out, err
}

// GetRoute calls get_route for one delivery-service-partner route.
func (c *LastMileDelivery) GetRoute(ctx context.Context, routeID string) (ports.DeliveryRoute, error) {
	var out ports.DeliveryRoute
	err := c.session.callTool(ctx, "get_route", map[string]any{"routeId": routeID}, &out)
	return out, err
}
