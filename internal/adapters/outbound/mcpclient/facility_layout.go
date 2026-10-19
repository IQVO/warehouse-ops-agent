package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// FacilityLayout implements ports.FacilityLayoutClient by calling
// facility-layout's published list_sites and
// estimate_travel_distance MCP tools.
type FacilityLayout struct {
	session *Session
}

// NewFacilityLayout builds a FacilityLayout client for the given connection
// config.
func NewFacilityLayout(cfg Config) *FacilityLayout {
	cfg.Name = "facility-layout"
	return &FacilityLayout{session: New(cfg)}
}

var _ ports.FacilityLayoutClient = (*FacilityLayout)(nil)

func (c *FacilityLayout) ListSites(ctx context.Context) (ports.SitesResult, error) {
	var out ports.SitesResult
	err := c.session.callTool(ctx, "list_sites", map[string]any{}, &out)
	return out, err
}

func (c *FacilityLayout) EstimateTravelDistance(ctx context.Context, from, to string) (ports.TravelDistance, error) {
	var out ports.TravelDistance
	err := c.session.callTool(ctx, "estimate_travel_distance", map[string]any{"from": from, "to": to}, &out)
	return out, err
}
