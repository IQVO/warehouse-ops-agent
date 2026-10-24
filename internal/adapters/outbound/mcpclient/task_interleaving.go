package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// TaskInterleaving implements ports.TaskInterleavingClient by calling
// task-interleaving's four published READ tools get_hint,
// get_site_policy, get_station and list_hints.
//
// task-interleaving's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestTaskInterleavingClientCallsOnlyPinnedTools allows exactly these
// four names. The argument keys and result shapes are pinned against
// task-interleaving's published registry in
// testdata/task_interleaving_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type TaskInterleaving struct {
	session *Session
}

// NewTaskInterleaving builds a TaskInterleaving client for the given
// connection config.
func NewTaskInterleaving(cfg Config) *TaskInterleaving {
	cfg.Name = "task-interleaving"
	return &TaskInterleaving{session: New(cfg)}
}

var _ ports.TaskInterleavingClient = (*TaskInterleaving)(nil)

// GetHint calls get_hint for one hint id.
func (c *TaskInterleaving) GetHint(ctx context.Context, hintID string) (ports.InterleavingHint, error) {
	var out ports.InterleavingHint
	err := c.session.callTool(ctx, "get_hint", map[string]any{"hintId": hintID}, &out)
	return out, err
}

// GetSitePolicy calls get_site_policy for one site.
func (c *TaskInterleaving) GetSitePolicy(ctx context.Context, siteID string) (ports.InterleavingPolicy, error) {
	var out ports.InterleavingPolicy
	err := c.session.callTool(ctx, "get_site_policy", map[string]any{"siteId": siteID}, &out)
	return out, err
}

// GetStation calls get_station for one station id.
func (c *TaskInterleaving) GetStation(ctx context.Context, stationID string) (ports.StationState, error) {
	var out ports.StationState
	err := c.session.callTool(ctx, "get_station", map[string]any{"stationId": stationID}, &out)
	return out, err
}

// ListHints calls list_hints. Zero query fields are omitted so
// task-interleaving applies its own defaults (every hint, the first page
// at the default page size).
func (c *TaskInterleaving) ListHints(ctx context.Context, query ports.HintListQuery) (ports.HintPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "stationId", query.StationID)
	setIfNotEmpty(args, "siteId", query.SiteID)
	setIfNotEmpty(args, "state", query.State)
	var out ports.HintPage
	err := c.session.callTool(ctx, "list_hints", args, &out)
	return out, err
}
