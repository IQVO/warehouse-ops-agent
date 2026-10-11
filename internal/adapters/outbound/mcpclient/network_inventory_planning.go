package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// NetworkInventoryPlanning implements ports.NetworkInventoryPlanningClient by
// calling network-inventory-planning's published READ tools: get_transfer,
// list_transfers, find_stuck_transfers and simulate_transfer_options.
//
// NIP's MCP server publishes read tools only today, and its governance test
// pins that set. This client is read-only regardless: it never names a
// mutating tool, and the zero-write fitness test
// (internal/architecture/zerowrite) scans this package's tool-name literals
// and fails the build if a write-prefixed tool appears (ADR 0019). Approving
// or cancelling a transfer stays with the operator in NIP.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker (those exist only on the Anthropic reasoner path, ADR
// 0011). Every failure is returned as an ordinary error; the consuming use
// case decides what to do with it. NIP's tool errors follow the fleet slug
// convention ("<slug>: <detail>", ADR 0018): invalid-query classifies as
// ports.ErrUpstreamInvalidInput and transfer-not-found as
// ports.ErrUpstreamNotFound.
type NetworkInventoryPlanning struct {
	session *Session
}

// NewNetworkInventoryPlanning builds a NetworkInventoryPlanning client for
// the given connection config.
func NewNetworkInventoryPlanning(cfg Config) *NetworkInventoryPlanning {
	cfg.Name = "network-inventory-planning"
	return &NetworkInventoryPlanning{session: New(cfg)}
}

var _ ports.NetworkInventoryPlanningClient = (*NetworkInventoryPlanning)(nil)

// GetTransfer calls get_transfer.
func (c *NetworkInventoryPlanning) GetTransfer(ctx context.Context, transferID string) (ports.TransferDetail, error) {
	var out ports.TransferDetail
	err := c.session.callTool(ctx, "get_transfer", map[string]any{"transfer_id": transferID}, &out)
	return out, err
}

// ListTransfers calls list_transfers. An empty State/Site and a zero Limit
// are omitted so NIP applies its own defaults.
func (c *NetworkInventoryPlanning) ListTransfers(ctx context.Context, req ports.ListTransfersRequest) (ports.TransferPage, error) {
	args := map[string]any{}
	if req.State != "" {
		args["state"] = req.State
	}
	if req.Site != "" {
		args["site"] = req.Site
	}
	if req.Limit != 0 {
		args["limit"] = req.Limit
	}
	var out ports.TransferPage
	err := c.session.callTool(ctx, "list_transfers", args, &out)
	return out, err
}

// FindStuckTransfers calls find_stuck_transfers. older_than_minutes is
// always sent exactly as given (NIP rejects a non-positive value with
// invalid-query; this client never defaults or repairs it).
func (c *NetworkInventoryPlanning) FindStuckTransfers(ctx context.Context, req ports.FindStuckTransfersRequest) (ports.TransferPage, error) {
	args := map[string]any{"older_than_minutes": req.OlderThanMinutes}
	if req.State != "" {
		args["state"] = req.State
	}
	if req.Limit != 0 {
		args["limit"] = req.Limit
	}
	var out ports.TransferPage
	err := c.session.callTool(ctx, "find_stuck_transfers", args, &out)
	return out, err
}

// SimulateTransferOptions calls simulate_transfer_options (no arguments).
func (c *NetworkInventoryPlanning) SimulateTransferOptions(ctx context.Context) (ports.TransferSimulation, error) {
	var out ports.TransferSimulation
	err := c.session.callTool(ctx, "simulate_transfer_options", map[string]any{}, &out)
	return out, err
}
