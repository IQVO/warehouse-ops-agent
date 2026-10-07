// Package usecases: TransferWatch (ADR 0019) -- the agent's read-only view of
// network-inventory-planning (NIP): where an inter-warehouse transfer stands,
// which transfers are stuck and why, and which sites of the network are short.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// maxStuckLimit bounds one stuck-transfer page. NIP applies its own cap; this
// keeps an untrusted caller (an LLM through MCP) from asking for an
// unbounded read.
const maxStuckLimit = 200

// ErrTransferNotFound marks a transfer id NIP does not know. The inbound
// adapters map it to 404.
var ErrTransferNotFound = errors.New("transfer not found")

// TransferWatch asks NIP (READ tools only: get_transfer, find_stuck_transfers,
// simulate_transfer_options) and hands the facts to the pure policy functions
// that classify them. It never moves, approves or cancels a transfer -- those
// stay operator actions in NIP itself -- and it never proposes a quantity or a
// route.
//
// A nil *TransferWatch, or one with a nil NIP port, is "not configured": the
// inbound adapters answer 503 (HTTP) or do not register the tools (MCP), the
// same convention as every other optional use case in this agent.
type TransferWatch struct {
	NIP ports.NetworkInventoryPlanningClient

	// Logger receives a warning when the upstream call fails. Defaults to
	// slog.Default() when nil.
	Logger *slog.Logger
}

// TriagedTransfer is one stuck transfer with its advisory reading.
type TriagedTransfer struct {
	Transfer ports.Transfer
	Triage   policy.TransferTriage
}

// StuckTransfersResult is the triaged page. Total counts every match before
// paging, so Total > len(Transfers) means there is more.
type StuckTransfersResult struct {
	Total      int
	Transfers  []TriagedTransfer
	CauseCount []policy.CauseCount
}

// TransferStatusResult is one transfer, its audit trail and its triage.
type TransferStatusResult struct {
	Detail ports.TransferDetail
	Triage policy.TransferTriage
}

func (uc *TransferWatch) logger() *slog.Logger {
	if uc.Logger != nil {
		return uc.Logger
	}
	return slog.Default()
}

// StuckTransfers lists non-terminal transfers whose state has not changed for
// longer than olderThanMinutes (stalest first, as NIP returns them), each
// triaged by its state, plus a per-cause tally. state, when non-empty, must be
// a stuck-able (non-terminal) transfer state; limit 0 leaves NIP's default.
func (uc *TransferWatch) StuckTransfers(ctx context.Context, olderThanMinutes int, state string, limit int) (StuckTransfersResult, error) {
	if uc == nil || uc.NIP == nil {
		return StuckTransfersResult{}, nil
	}
	if olderThanMinutes <= 0 {
		return StuckTransfersResult{}, fmt.Errorf("%w: find stuck transfers: olderThanMinutes must be positive", ErrInvalidInput)
	}
	if limit < 0 || limit > maxStuckLimit {
		return StuckTransfersResult{}, fmt.Errorf("%w: find stuck transfers: limit must be between 0 and %d", ErrInvalidInput, maxStuckLimit)
	}
	if state != "" {
		parsed, err := policy.ParseStuckTransferState(state)
		if err != nil {
			return StuckTransfersResult{}, fmt.Errorf("%w: find stuck transfers: %w", ErrInvalidInput, err)
		}
		state = string(parsed)
	}

	page, err := uc.NIP.FindStuckTransfers(ctx, ports.FindStuckTransfersRequest{
		OlderThanMinutes: olderThanMinutes,
		State:            state,
		Limit:            limit,
	})
	if err != nil {
		return StuckTransfersResult{}, uc.upstreamError("find_stuck_transfers", err)
	}

	out := StuckTransfersResult{Total: page.Total, Transfers: make([]TriagedTransfer, 0, len(page.Transfers))}
	causes := make([]policy.TransferCause, 0, len(page.Transfers))
	for _, t := range page.Transfers {
		triage := policy.TriageTransfer(signalOf(t))
		out.Transfers = append(out.Transfers, TriagedTransfer{Transfer: t, Triage: triage})
		causes = append(causes, triage.Cause)
	}
	out.CauseCount = policy.TallyCauses(causes)
	return out, nil
}

// TransferStatus reads one transfer with its audit trail and triages it. An
// unknown id yields an error satisfying errors.Is(err, ErrTransferNotFound).
func (uc *TransferWatch) TransferStatus(ctx context.Context, transferID string) (TransferStatusResult, error) {
	if uc == nil || uc.NIP == nil {
		return TransferStatusResult{}, nil
	}
	if transferID == "" {
		return TransferStatusResult{}, fmt.Errorf("%w: transfer status: transferId is required", ErrInvalidInput)
	}
	detail, err := uc.NIP.GetTransfer(ctx, transferID)
	if err != nil {
		if errors.Is(err, ports.ErrUpstreamNotFound) {
			return TransferStatusResult{}, fmt.Errorf("%w: %s", ErrTransferNotFound, transferID)
		}
		return TransferStatusResult{}, uc.upstreamError("get_transfer", err)
	}
	return TransferStatusResult{Detail: detail, Triage: policy.TriageTransfer(signalOf(detail.Transfer))}, nil
}

// NetworkImbalance explains NIP's advisory simulation: which sites are short
// over the plan window and which have headroom. NIP's simulation is
// fail-closed (it answers a tool error until its read models are fresh); that
// error is returned as an upstream failure, never papered over with an empty
// reading.
func (uc *TransferWatch) NetworkImbalance(ctx context.Context) (policy.NetworkImbalance, error) {
	if uc == nil || uc.NIP == nil {
		return policy.NetworkImbalance{}, nil
	}
	sim, err := uc.NIP.SimulateTransferOptions(ctx)
	if err != nil {
		return policy.NetworkImbalance{}, uc.upstreamError("simulate_transfer_options", err)
	}
	sites := make([]policy.SiteSimulationSignal, 0, len(sim.Sites))
	for _, s := range sim.Sites {
		sites = append(sites, policy.SiteSimulationSignal{
			Site:               s.Site,
			OriginEnabled:      s.OriginEnabled,
			DestinationEnabled: s.DestinationEnabled,
			TotalDemand:        s.TotalDemand,
			CapacityOverWindow: s.CapacityOverWindow,
			CapacityHeadroom:   s.CapacityHeadroom,
			WindowStart:        s.WindowStart,
			WindowEnd:          s.WindowEnd,
		})
	}
	return policy.ExplainImbalance(sim.Advisory, sim.AsOf, sites), nil
}

// upstreamError logs a failed NIP call and returns it, classifying a
// rejected-input slug as the caller's own input error (ADR 0018) and leaving
// everything else a plain upstream error.
func (uc *TransferWatch) upstreamError(tool string, err error) error {
	uc.logger().Warn("transfer_watch: network-inventory-planning call failed",
		"tool", tool, "error", sanitizeForLog(err.Error()))
	if errors.Is(err, ports.ErrUpstreamInvalidInput) {
		return fmt.Errorf("%w: %s: %w", ErrInvalidInput, tool, err)
	}
	return err
}

func signalOf(t ports.Transfer) policy.TransferSignal {
	return policy.TransferSignal{
		Id:                t.Id,
		State:             t.State,
		SKU:               t.SKU,
		Quantity:          t.Quantity,
		PickedQuantity:    t.PickedQuantity,
		OriginSiteId:      t.OriginSiteId,
		DestinationSiteId: t.DestinationSiteId,
		ReservationId:     t.ReservationId,
		RejectionReason:   t.RejectionReason,
	}
}
