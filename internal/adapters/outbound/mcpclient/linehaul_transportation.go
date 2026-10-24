package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// LinehaulTransportation implements ports.LinehaulTransportationClient
// by calling linehaul-transportation's two published READ tools
// get_departure and list_departures.
//
// linehaul-transportation's MCP server is read-only by its own
// governance test; this client is additionally held read-only from this
// side: the zero-write fitness test (internal/architecture/zerowrite)
// scans this package's tool-name literals, and
// TestLinehaulTransportationClientCallsOnlyPinnedTools allows exactly
// these two names. The argument keys and result shapes are pinned
// against linehaul-transportation's published registry in
// testdata/linehaul_transportation_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type LinehaulTransportation struct {
	session *Session
}

// NewLinehaulTransportation builds a LinehaulTransportation client for
// the given connection config.
func NewLinehaulTransportation(cfg Config) *LinehaulTransportation {
	cfg.Name = "linehaul-transportation"
	return &LinehaulTransportation{session: New(cfg)}
}

var _ ports.LinehaulTransportationClient = (*LinehaulTransportation)(nil)

// GetDeparture calls get_departure for one departure id.
func (c *LinehaulTransportation) GetDeparture(ctx context.Context, departureID string) (ports.LinehaulDeparture, error) {
	var out ports.LinehaulDeparture
	err := c.session.callTool(ctx, "get_departure", map[string]any{"departureId": departureID}, &out)
	return out, err
}

// ListDepartures calls list_departures. Zero query fields are omitted so
// linehaul-transportation applies its own defaults (every lane, the
// default page size of 500).
func (c *LinehaulTransportation) ListDepartures(ctx context.Context, query ports.LinehaulDepartureQuery) (ports.LinehaulDeparturePage, error) {
	args := map[string]any{}
	if query.Limit != 0 {
		args["limit"] = query.Limit
	}
	setIfNotEmpty(args, "origin", query.Origin)
	setIfNotEmpty(args, "destination", query.Destination)
	setIfNotEmpty(args, "from", query.From)
	setIfNotEmpty(args, "to", query.To)
	var out ports.LinehaulDeparturePage
	err := c.session.callTool(ctx, "list_departures", args, &out)
	return out, err
}
