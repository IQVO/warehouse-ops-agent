package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// NodePerformance implements ports.NodePerformanceClient by calling
// node-performance's two published READ tools get_scorecard and
// list_scorecards_by_grade.
//
// node-performance's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestNodePerformanceClientCallsOnlyPinnedTools allows exactly these
// two names. The argument keys and result shapes are pinned against
// node-performance's published registry in
// testdata/node_performance_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type NodePerformance struct {
	session *Session
}

// NewNodePerformance builds a NodePerformance client for the given
// connection config.
func NewNodePerformance(cfg Config) *NodePerformance {
	cfg.Name = "node-performance"
	return &NodePerformance{session: New(cfg)}
}

var _ ports.NodePerformanceClient = (*NodePerformance)(nil)

// GetScorecard calls get_scorecard for one node: both its 7-day and its
// 30-day scorecard.
func (c *NodePerformance) GetScorecard(ctx context.Context, nodeID string) (ports.NodeScorecards, error) {
	var out ports.NodeScorecards
	err := c.session.callTool(ctx, "get_scorecard", map[string]any{"nodeId": nodeID}, &out)
	return out, err
}

// ListScorecardsByGrade calls list_scorecards_by_grade. The grade is
// required; zero-valued optional fields are omitted so
// node-performance applies its own defaults (both windows, the first
// page, the default page size).
func (c *NodePerformance) ListScorecardsByGrade(ctx context.Context, query ports.ScorecardGradeQuery) (ports.ScorecardGradePage, error) {
	args := map[string]any{"grade": query.Grade}
	if query.Limit != 0 {
		args["limit"] = query.Limit
	}
	setIfNotEmpty(args, "window", query.Window)
	setIfNotEmpty(args, "after", query.After)
	var out ports.ScorecardGradePage
	err := c.session.callTool(ctx, "list_scorecards_by_grade", args, &out)
	return out, err
}
