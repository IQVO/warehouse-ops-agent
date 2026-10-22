package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// ReturnsProcessing implements ports.ReturnsProcessingClient by calling
// returns-processing's three published READ tools get_return,
// list_returns_by_order and get_disposition_policy.
//
// returns-processing's MCP server is read-only by its own governance test;
// this client is additionally held read-only from this side: the zero-write
// fitness test (internal/architecture/zerowrite) scans this package's
// tool-name literals, and TestReturnsProcessingClientCallsOnlyPinnedTools
// allows exactly these three names. The argument keys and result shapes
// are pinned against returns-processing's published registry in
// testdata/returns_processing_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput, a
// *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming use
// case decides what to do with it.
type ReturnsProcessing struct {
	session *Session
}

// NewReturnsProcessing builds a ReturnsProcessing client for the given
// connection config.
func NewReturnsProcessing(cfg Config) *ReturnsProcessing {
	cfg.Name = "returns-processing"
	return &ReturnsProcessing{session: New(cfg)}
}

var _ ports.ReturnsProcessingClient = (*ReturnsProcessing)(nil)

// GetReturn calls get_return for one return authorization id.
func (c *ReturnsProcessing) GetReturn(ctx context.Context, rmaID string) (ports.ReturnAuthorization, error) {
	var out ports.ReturnAuthorization
	err := c.session.callTool(ctx, "get_return", map[string]any{"rma_id": rmaID}, &out)
	return out, err
}

// ListReturnsByOrder calls list_returns_by_order for one original order.
// An order with no returns — or an unknown order — answers an empty list,
// never an error (returns-processing's own contract).
func (c *ReturnsProcessing) ListReturnsByOrder(ctx context.Context, orderID string) (ports.ReturnsByOrder, error) {
	var out ports.ReturnsByOrder
	err := c.session.callTool(ctx, "list_returns_by_order", map[string]any{"order_id": orderID}, &out)
	return out, err
}

// GetDispositionPolicy calls get_disposition_policy. Version 0 (or
// negative) omits the argument, so returns-processing answers the CURRENT
// policy version; a positive version reads that declared one.
func (c *ReturnsProcessing) GetDispositionPolicy(ctx context.Context, version int64) (ports.DispositionPolicy, error) {
	args := map[string]any{}
	if version > 0 {
		args["version"] = version
	}
	var out ports.DispositionPolicy
	err := c.session.callTool(ctx, "get_disposition_policy", args, &out)
	return out, err
}
