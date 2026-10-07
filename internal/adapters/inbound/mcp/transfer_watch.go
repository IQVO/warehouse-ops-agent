package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// --- transfer watch (ADR 0019) -------------------------------------------
//
// Three read-only tools over network-inventory-planning (NIP). They are
// registered only when the TransferWatch use case is wired, i.e. when
// NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT is set. None of them moves,
// approves or cancels a transfer: that stays an operator action in NIP.

type transferViewDTO struct {
	Id                string `json:"id"`
	State             string `json:"state"`
	SKU               string `json:"sku"`
	Quantity          int    `json:"quantity"`
	PickedQuantity    int    `json:"pickedQuantity,omitempty"`
	OriginSiteId      string `json:"originSiteId"`
	DestinationSiteId string `json:"destinationSiteId"`
	ReservationId     string `json:"reservationId,omitempty"`
	RejectionReason   string `json:"rejectionReason,omitempty"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

type transferTriageViewDTO struct {
	Cause     string `json:"cause"`
	Summary   string `json:"summary"`
	NextCheck string `json:"nextCheck,omitempty"`
}

func toTransferViewDTO(t ports.Transfer) transferViewDTO {
	return transferViewDTO{
		Id: t.Id, State: t.State, SKU: t.SKU, Quantity: t.Quantity, PickedQuantity: t.PickedQuantity,
		OriginSiteId: t.OriginSiteId, DestinationSiteId: t.DestinationSiteId,
		ReservationId: t.ReservationId, RejectionReason: t.RejectionReason,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

func toTransferTriageViewDTO(t policy.TransferTriage) transferTriageViewDTO {
	return transferTriageViewDTO{Cause: string(t.Cause), Summary: t.Summary, NextCheck: t.NextCheck}
}

// --- find_stuck_transfers_triage -----------------------------------------

type stuckTransfersInput struct {
	OlderThanMinutes int    `json:"olderThanMinutes" jsonschema:"required: a transfer is stuck when its state has not changed for longer than this many minutes; the agent never picks a threshold itself"`
	State            string `json:"state,omitempty" jsonschema:"optional: only transfers in this non-terminal state, e.g. ALLOCATING, ALLOCATED, PICKED, IN_TRANSIT, ARRIVED"`
	Limit            int    `json:"limit,omitempty" jsonschema:"optional page size, 1-200; omit for the network-inventory-planning default"`
}

type triagedTransferViewDTO struct {
	transferViewDTO
	Triage transferTriageViewDTO `json:"triage"`
}

type causeCountViewDTO struct {
	Cause string `json:"cause"`
	Count int    `json:"count"`
}

type stuckTransfersOutput struct {
	Total      int                      `json:"total"`
	Transfers  []triagedTransferViewDTO `json:"transfers"`
	CauseCount []causeCountViewDTO      `json:"causeCount"`
}

func (d Deps) triageStuckTransfers(ctx context.Context, in stuckTransfersInput) (stuckTransfersOutput, error) {
	result, err := d.TransferWatch.StuckTransfers(ctx, in.OlderThanMinutes, in.State, in.Limit)
	if err != nil {
		return stuckTransfersOutput{}, err
	}
	out := stuckTransfersOutput{
		Total:      result.Total,
		Transfers:  make([]triagedTransferViewDTO, 0, len(result.Transfers)),
		CauseCount: make([]causeCountViewDTO, 0, len(result.CauseCount)),
	}
	for _, t := range result.Transfers {
		out.Transfers = append(out.Transfers, triagedTransferViewDTO{transferViewDTO: toTransferViewDTO(t.Transfer), Triage: toTransferTriageViewDTO(t.Triage)})
	}
	for _, c := range result.CauseCount {
		out.CauseCount = append(out.CauseCount, causeCountViewDTO{Cause: string(c.Cause), Count: c.Count})
	}
	return out, nil
}

// --- get_transfer_status --------------------------------------------------

type transferStatusInput struct {
	TransferId string `json:"transferId" jsonschema:"the inter-warehouse transfer id"`
}

type transferAuditViewDTO struct {
	Seq        int64  `json:"seq"`
	From       string `json:"from,omitempty"`
	To         string `json:"to"`
	Event      string `json:"event"`
	Cause      string `json:"cause"`
	OccurredAt string `json:"occurredAt"`
}

type transferStatusOutput struct {
	transferViewDTO
	Audit  []transferAuditViewDTO `json:"audit"`
	Triage transferTriageViewDTO  `json:"triage"`
}

func (d Deps) getTransferStatus(ctx context.Context, in transferStatusInput) (transferStatusOutput, error) {
	result, err := d.TransferWatch.TransferStatus(ctx, in.TransferId)
	if err != nil {
		return transferStatusOutput{}, err
	}
	out := transferStatusOutput{
		transferViewDTO: toTransferViewDTO(result.Detail.Transfer),
		Audit:           make([]transferAuditViewDTO, 0, len(result.Detail.Audit)),
		Triage:          toTransferTriageViewDTO(result.Triage),
	}
	for _, a := range result.Detail.Audit {
		out.Audit = append(out.Audit, transferAuditViewDTO{Seq: a.Seq, From: a.From, To: a.To, Event: a.Event, Cause: a.Cause, OccurredAt: a.OccurredAt})
	}
	return out, nil
}

// --- explain_network_imbalance --------------------------------------------

type networkImbalanceInput struct{}

type siteImbalanceViewDTO struct {
	Site               string  `json:"site"`
	Balance            string  `json:"balance"`
	OriginEnabled      bool    `json:"originEnabled"`
	DestinationEnabled bool    `json:"destinationEnabled"`
	TotalDemand        int     `json:"totalDemand"`
	CapacityOverWindow float64 `json:"capacityOverWindow"`
	CapacityHeadroom   int     `json:"capacityHeadroom"`
	Explanation        string  `json:"explanation"`
}

type networkImbalanceOutput struct {
	Advisory   bool                   `json:"advisory"`
	AsOf       string                 `json:"asOf"`
	Imbalanced bool                   `json:"imbalanced"`
	Sites      []siteImbalanceViewDTO `json:"sites"`
	Summary    string                 `json:"summary"`
	NextCheck  string                 `json:"nextCheck,omitempty"`
}

func (d Deps) explainNetworkImbalance(ctx context.Context, _ networkImbalanceInput) (networkImbalanceOutput, error) {
	n, err := d.TransferWatch.NetworkImbalance(ctx)
	if err != nil {
		return networkImbalanceOutput{}, err
	}
	out := networkImbalanceOutput{
		Advisory: n.Advisory, AsOf: n.AsOf, Imbalanced: n.Imbalanced,
		Sites: make([]siteImbalanceViewDTO, 0, len(n.Sites)), Summary: n.Summary, NextCheck: n.NextCheck,
	}
	for _, s := range n.Sites {
		out.Sites = append(out.Sites, siteImbalanceViewDTO{
			Site: s.Site, Balance: string(s.Balance), OriginEnabled: s.OriginEnabled, DestinationEnabled: s.DestinationEnabled,
			TotalDemand: s.TotalDemand, CapacityOverWindow: s.CapacityOverWindow, CapacityHeadroom: s.CapacityHeadroom,
			Explanation: s.Explanation,
		})
	}
	return out, nil
}

// registerTransferWatchTools adds the three read-only transfer-watch tools.
func (d Deps) registerTransferWatchTools(server *mcp.Server) {
	readOnly := true

	addTool(server, &mcp.Tool{
		Name:        "triage_stuck_transfers",
		Description: "List inter-warehouse transfers (from network-inventory-planning) whose state has not changed for longer than the caller-supplied number of minutes, stalest first, each classified by where the saga is waiting (inventory reply missing, floor work not progressing, destination receipt missing) with the next read-only check an operator can make, plus a per-cause tally. Advisory only: it never moves, approves or cancels a transfer.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.triageStuckTransfers)

	addTool(server, &mcp.Tool{
		Name:        "get_transfer_status",
		Description: "Read one inter-warehouse transfer from network-inventory-planning with its full audit trail (every state transition) and an advisory reading of where it is waiting. An unknown transfer id is an error.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.getTransferStatus)

	addTool(server, &mcp.Tool{
		Name:        "explain_network_imbalance",
		Description: "Explain network-inventory-planning's advisory simulation: which sites are short of capacity over the plan window, which have headroom and may originate a transfer, and what an operator can check next. It never proposes a quantity or a route. Fails when network-inventory-planning's read models are not fresh, rather than answering with an empty reading.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.explainNetworkImbalance)
}
