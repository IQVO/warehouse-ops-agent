package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// ProcessPathManagement implements ports.ProcessPathManagementClient by
// calling process-path-management's three published READ tools
// get_process_path, list_process_paths and get_cpt_schedule.
//
// process-path-management's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestNetworkFleetClientsCallOnlyPinnedTools (whose allow-list this
// client's file is part of) allows exactly these
// three names. The argument keys and result shapes are pinned against
// process-path-management's published registry in
// testdata/process_path_management_tools.golden.json (converted from that
// repo's TSV registry golden into this repo's JSON convention, ADR 0026).
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// *-not-found tool error also matches ports.ErrUpstreamNotFound, ADR
// 0018); the consuming use case decides what to do with it.
type ProcessPathManagement struct {
	session *Session
}

// NewProcessPathManagement builds a ProcessPathManagement client for the
// given connection config.
func NewProcessPathManagement(cfg Config) *ProcessPathManagement {
	cfg.Name = "process-path-management"
	return &ProcessPathManagement{session: New(cfg)}
}

var _ ports.ProcessPathManagementClient = (*ProcessPathManagement)(nil)

// GetProcessPath calls get_process_path for one canonical path id.
func (c *ProcessPathManagement) GetProcessPath(ctx context.Context, pathID string) (ports.ProcessPathEntry, error) {
	var out ports.ProcessPathEntry
	err := c.session.callTool(ctx, "get_process_path", map[string]any{"pathId": pathID}, &out)
	return out, err
}

// ListProcessPaths calls list_process_paths; activeOnly=false asks for the
// deactivated paths too (the tool's own default is activeOnly=true).
func (c *ProcessPathManagement) ListProcessPaths(ctx context.Context, activeOnly bool) (ports.ProcessPathList, error) {
	var out ports.ProcessPathList
	err := c.session.callTool(ctx, "list_process_paths", map[string]any{"activeOnly": activeOnly}, &out)
	return out, err
}

// GetCPTSchedule calls get_cpt_schedule for one site id.
func (c *ProcessPathManagement) GetCPTSchedule(ctx context.Context, siteID string) (ports.CPTSchedule, error) {
	var out ports.CPTSchedule
	err := c.session.callTool(ctx, "get_cpt_schedule", map[string]any{"siteId": siteID}, &out)
	return out, err
}
