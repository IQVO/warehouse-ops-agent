// Package ports (this file): the outbound port for the
// network-inventory-planning (NIP) bounded context (ADR 0019), the fourth
// wave of upstreams after clients.go, clients_phase2.go and
// clients_planning.go.
//
// NIP owns inter-warehouse transfers (a saga from DRAFT to RECEIVED) and an
// advisory network simulation. Its MCP server publishes READ tools only
// today, but this port is read-only by construction regardless: it exposes
// exactly get_transfer, list_transfers, find_stuck_transfers and
// simulate_transfer_options. Approving, cancelling or otherwise moving a
// transfer is an operator action in NIP itself; no method for it exists
// here, and the zero-write fitness test
// (internal/architecture/zerowrite) fails the build if the mcpclient adapter
// ever names a write tool.
package ports

import "context"

// NetworkInventoryPlanningClient is the outbound port for NIP's published
// READ tools.
type NetworkInventoryPlanningClient interface {
	// GetTransfer calls get_transfer: one transfer with its audit trail.
	// An unknown id yields an error satisfying
	// errors.Is(err, ErrUpstreamNotFound).
	GetTransfer(ctx context.Context, transferID string) (TransferDetail, error)
	// ListTransfers calls list_transfers (newest first).
	ListTransfers(ctx context.Context, req ListTransfersRequest) (TransferPage, error)
	// FindStuckTransfers calls find_stuck_transfers: NON-terminal transfers
	// whose state has not changed for longer than req.OlderThanMinutes,
	// stalest first.
	FindStuckTransfers(ctx context.Context, req FindStuckTransfersRequest) (TransferPage, error)
	// SimulateTransferOptions calls simulate_transfer_options: NIP's
	// advisory, fail-closed network simulation (it takes no argument).
	SimulateTransferOptions(ctx context.Context) (TransferSimulation, error)
}

// ListTransfersRequest is the argument set of list_transfers. Empty State /
// Site and a zero Limit are OMITTED from the call (NIP applies its own
// defaults: no filter, page size 50).
type ListTransfersRequest struct {
	State string
	Site  string
	Limit int
}

// FindStuckTransfersRequest is the argument set of find_stuck_transfers.
// OlderThanMinutes is always sent (NIP requires it to be positive); an empty
// State and a zero Limit are omitted.
type FindStuckTransfersRequest struct {
	OlderThanMinutes int
	State            string
	Limit            int
}

// --- NIP read-model DTOs ---------------------------------------------------
//
// Hand-mirrored from network-inventory-planning's
// internal/adapters/inbound/mcp/tools.go (transferView, auditView,
// transferPageOutput, simulateOutput); this repo never imports its Go code.

// Transfer mirrors one transferView. Timestamps are RFC 3339 strings;
// PickedQuantity, ReservationId and RejectionReason are omitted upstream
// until the saga has produced them.
type Transfer struct {
	Id                string `json:"id"`
	State             string `json:"state"`
	SKU               string `json:"sku"`
	Quantity          int    `json:"quantity"`
	PickedQuantity    int    `json:"picked_quantity,omitempty"`
	OriginSiteId      string `json:"origin_site_id"`
	DestinationSiteId string `json:"destination_site_id"`
	PolicyVersion     string `json:"policy_version"`
	ReservationId     string `json:"reservation_id,omitempty"`
	RejectionReason   string `json:"rejection_reason,omitempty"`
	ExpiresAt         string `json:"expires_at"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	Version           int64  `json:"version"`
}

// TransferAuditEntry mirrors one auditView: one state transition.
type TransferAuditEntry struct {
	Seq        int64  `json:"seq"`
	From       string `json:"from,omitempty"`
	To         string `json:"to"`
	Event      string `json:"event"`
	Cause      string `json:"cause"`
	OccurredAt string `json:"occurred_at"`
}

// TransferDetail mirrors get_transfer's output: the transfer's fields plus
// its audit trail.
type TransferDetail struct {
	Transfer
	Audit []TransferAuditEntry `json:"audit"`
}

// TransferPage mirrors transferPageOutput. Total counts every match before
// paging, so Total > len(Transfers) means there is more.
type TransferPage struct {
	Transfers []Transfer `json:"transfers"`
	Total     int        `json:"total"`
}

// SiteSimulation mirrors one siteSimulationView. CapacityHeadroom is
// negative when the site is short over the plan window.
type SiteSimulation struct {
	Site               string  `json:"site"`
	OriginEnabled      bool    `json:"origin_enabled"`
	DestinationEnabled bool    `json:"destination_enabled"`
	TotalDemand        int     `json:"total_demand"`
	CapacityOverWindow float64 `json:"capacity_over_window"`
	CapacityHeadroom   int     `json:"capacity_headroom"`
	WindowStart        string  `json:"window_start"`
	WindowEnd          string  `json:"window_end"`
}

// TransferSimulation mirrors simulateOutput. Advisory is true: the
// simulation reserves and moves nothing.
type TransferSimulation struct {
	Advisory bool             `json:"advisory"`
	AsOf     string           `json:"as_of"`
	Sites    []SiteSimulation `json:"sites"`
}
