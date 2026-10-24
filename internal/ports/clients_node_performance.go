// Package ports (this file): the outbound port for the node-performance
// bounded context (retail network), the fleet's owner of the rolling
// fulfilment-node scorecards.
//
// node-performance's MCP server is READ-ONLY (its own governance test
// pins its two tools, every one annotated readOnlyHint). This port
// mirrors both: get_scorecard and list_scorecards_by_grade.
// node-performance's writes (grading a window) are upstream of it and
// have no method here. The zero-write fitness test
// (internal/architecture/zerowrite) fails the build if the mcpclient
// adapter ever names a write-verb tool, and the contract snapshot test
// in mcpclient pins the published tools against node-performance's
// published registry.
package ports

import (
	"context"
)

// NodePerformanceClient is the outbound port for node-performance's two
// published read tools.
type NodePerformanceClient interface {
	// GetScorecard calls get_scorecard: both the 7-day and the 30-day
	// scorecard of one node.
	GetScorecard(ctx context.Context, nodeID string) (NodeScorecards, error)
	// ListScorecardsByGrade calls list_scorecards_by_grade: every
	// scorecard whose current grade matches, one page at a time. Grade is
	// required (A, B, C, D or PROVISIONAL); zero-valued optional fields
	// are omitted so node-performance applies its own defaults.
	ListScorecardsByGrade(ctx context.Context, query ScorecardGradeQuery) (ScorecardGradePage, error)
}

// ScorecardGradeQuery filters list_scorecards_by_grade: the required
// grade, an optional window (7d or 30d) and the optional keyset
// pagination pair.
type ScorecardGradeQuery struct {
	Grade  string
	Window string
	After  string
	Limit  int
}

// --- node-performance read-model DTOs ----------------------------------------
//
// Field-for-field mirrors of node-performance's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/node_performance_tools.golden.json.

// NodeScorecards mirrors the get_scorecard result: the node's scorecard
// per window (7d then 30d), PROVISIONAL below the observation floor.
type NodeScorecards struct {
	NodeID     string          `json:"nodeId"`
	Scorecards []NodeScorecard `json:"scorecards"`
}

// NodeScorecard mirrors scorecardView: one graded window. State is
// PROVISIONAL or GRADED; grade is A, B, C, D or PROVISIONAL.
type NodeScorecard struct {
	ScorecardID string           `json:"scorecardId"`
	NodeID      string           `json:"nodeId"`
	Window      string           `json:"window"`
	State       string           `json:"state"`
	Grade       string           `json:"grade"`
	Metrics     ScorecardMetrics `json:"metrics"`
	ComputedAt  string           `json:"computedAt"`
}

// ScorecardMetrics mirrors the rate family of a graded window: handover,
// acknowledgement, rejection, expiry and promise-kept rates plus feed
// freshness and the observation count behind them.
type ScorecardMetrics struct {
	OnTimeHandoverRate      float64 `json:"onTimeHandoverRate"`
	LateAckRate             float64 `json:"lateAckRate"`
	RejectRate              float64 `json:"rejectRate"`
	ExpiryRate              float64 `json:"expiryRate"`
	PromiseKeptRate         float64 `json:"promiseKeptRate"`
	FeedFreshnessP95Minutes int     `json:"feedFreshnessP95Minutes"`
	Observations            int     `json:"observations"`
}

// ScorecardGradePage mirrors the list_scorecards_by_grade result.
type ScorecardGradePage struct {
	Items      []NodeScorecard `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}
