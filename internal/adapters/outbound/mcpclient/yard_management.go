package mcpclient

import (
	"context"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// YardManagementReadTools is the exact set of yard-management tools this
// agent may call or offer the model: the ten L1 read tools of the pinned
// registry, sorted. yard-management's eleven L3 write tools are deliberately
// absent (ADR 0032). TestYardManagement_ReadToolsAreTheGoldenL1Set pins this
// list to the golden.
var YardManagementReadTools = []string{
	"get_inspection", "get_trailer", "get_visit", "get_yard_board", "get_yard_dwell_report",
	"get_yard_policy", "list_moves", "list_trailer_inspections", "list_trailers", "list_visits",
}

// YardManagement implements ports.YardManagementClient by calling
// yard-management's ten published READ tools get_yard_board, list_trailers,
// get_trailer, list_moves, list_visits, get_visit, get_yard_policy,
// get_yard_dwell_report, get_inspection and list_trailer_inspections (every
// one L1, readOnlyHint).
//
// yard-management's MCP server also publishes eleven L3 write tools. This
// client never names any of them: the zero-write fitness test
// (internal/architecture/zerowrite) scans this file's tool-name literals
// against the exact read allow-list, and
// TestYardManagement_CallsOnlyPinnedReadTools proves it on the wire. The
// argument keys and result shapes are pinned against yard-management's
// published registry in testdata/yard_management_tools.golden.json (ADR
// 0032).
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput, a
// *-not-found one ports.ErrUpstreamNotFound, ADR 0018). No use case
// consumes this client yet.
type YardManagement struct {
	session *Session
}

// NewYardManagement builds a YardManagement client for the given connection
// config.
func NewYardManagement(cfg Config) *YardManagement {
	return &YardManagement{session: NewNamedSession("yard-management", cfg)}
}

// GetYardBoard calls get_yard_board for one site.
func (c *YardManagement) GetYardBoard(ctx context.Context, siteID string) (ports.YardBoard, error) {
	var out ports.YardBoard
	err := c.session.callTool(ctx, "get_yard_board", map[string]any{argSiteID: siteID}, &out)
	return out, err
}

// ListTrailers calls list_trailers; every zero filter is omitted (an
// omitted state means not DEPARTED upstream).
func (c *YardManagement) ListTrailers(ctx context.Context, q ports.YardTrailerQuery) (ports.YardTrailerPage, error) {
	args := pageArgs(q.Limit, q.Cursor)
	setIfNotEmpty(args, argSiteID, q.SiteID)
	setIfNotEmpty(args, argState, q.State)
	setIfNotEmpty(args, "kind", q.Kind)
	setIfNotEmpty(args, "locationCode", q.LocationCode)
	setIfNotEmpty(args, "unitNumber", q.UnitNumber)
	setIfNotEmpty(args, "visitId", q.VisitID)
	var out ports.YardTrailerPage
	err := c.session.callTool(ctx, "list_trailers", args, &out)
	return out, err
}

// GetTrailer calls get_trailer for one trailer id.
func (c *YardManagement) GetTrailer(ctx context.Context, trailerID string) (ports.YardTrailer, error) {
	var out ports.YardTrailer
	err := c.session.callTool(ctx, "get_trailer", map[string]any{argTrailerID: trailerID}, &out)
	return out, err
}

// ListMoves calls list_moves; every zero filter is omitted.
func (c *YardManagement) ListMoves(ctx context.Context, q ports.YardMoveQuery) (ports.YardMovePage, error) {
	args := pageArgs(q.Limit, q.Cursor)
	setIfNotEmpty(args, argSiteID, q.SiteID)
	setIfNotEmpty(args, argState, q.State)
	setIfNotEmpty(args, argTrailerID, q.TrailerID)
	setIfNotEmpty(args, "hostlerRef", q.HostlerRef)
	var out ports.YardMovePage
	err := c.session.callTool(ctx, "list_moves", args, &out)
	return out, err
}

// ListVisits calls list_visits; every zero filter is omitted.
func (c *YardManagement) ListVisits(ctx context.Context, q ports.YardVisitQuery) (ports.YardVisitPage, error) {
	args := pageArgs(q.Limit, q.Cursor)
	setIfNotEmpty(args, argSiteID, q.SiteID)
	setIfNotEmpty(args, argState, q.State)
	setIfNotEmpty(args, "tractorPlate", q.TractorPlate)
	var out ports.YardVisitPage
	err := c.session.callTool(ctx, "list_visits", args, &out)
	return out, err
}

// GetVisit calls get_visit for one visit id.
func (c *YardManagement) GetVisit(ctx context.Context, visitID string) (ports.YardVisit, error) {
	var out ports.YardVisit
	err := c.session.callTool(ctx, "get_visit", map[string]any{"visitId": visitID}, &out)
	return out, err
}

// GetYardPolicy calls get_yard_policy for one site.
func (c *YardManagement) GetYardPolicy(ctx context.Context, siteID string) (ports.YardPolicy, error) {
	var out ports.YardPolicy
	err := c.session.callTool(ctx, "get_yard_policy", map[string]any{argSiteID: siteID}, &out)
	return out, err
}

// GetYardDwellReport calls get_yard_dwell_report for [from, to); an empty
// siteID is omitted so the report covers every site.
func (c *YardManagement) GetYardDwellReport(ctx context.Context, from, to time.Time, siteID string) (ports.YardDwellReport, error) {
	args := map[string]any{argFrom: formatInstant(from), argTo: formatInstant(to)}
	setIfNotEmpty(args, argSiteID, siteID)
	var out ports.YardDwellReport
	err := c.session.callTool(ctx, "get_yard_dwell_report", args, &out)
	return out, err
}

// GetInspection calls get_inspection for one inspection id.
func (c *YardManagement) GetInspection(ctx context.Context, inspectionID string) (ports.YardInspection, error) {
	var out ports.YardInspection
	err := c.session.callTool(ctx, "get_inspection", map[string]any{"inspectionId": inspectionID}, &out)
	return out, err
}

// ListTrailerInspections calls list_trailer_inspections for one trailer.
func (c *YardManagement) ListTrailerInspections(ctx context.Context, trailerID string, limit int, cursor string) (ports.YardInspectionPage, error) {
	args := pageArgs(limit, cursor)
	args[argTrailerID] = trailerID
	var out ports.YardInspectionPage
	err := c.session.callTool(ctx, "list_trailer_inspections", args, &out)
	return out, err
}
