package http

import (
	"errors"
	nethttp "net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The transfer-watch routes (ADR 0019) are read-only views of
// network-inventory-planning. Nil TransferWatch (not wired because
// NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT is unset) answers 503, the same
// convention as every other optional use case in this router.

const transferWatchNotConfigured = "transfer watch not configured (network-inventory-planning endpoint unset)"

type transferDTO struct {
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

type transferTriageDTO struct {
	Cause     string `json:"cause"`
	Summary   string `json:"summary"`
	NextCheck string `json:"nextCheck,omitempty"`
}

type triagedTransferDTO struct {
	transferDTO
	Triage transferTriageDTO `json:"triage"`
}

type causeCountDTO struct {
	Cause string `json:"cause"`
	Count int    `json:"count"`
}

type stuckTransfersDTO struct {
	Total      int                  `json:"total"`
	Transfers  []triagedTransferDTO `json:"transfers"`
	CauseCount []causeCountDTO      `json:"causeCount"`
}

type auditEntryDTO struct {
	Seq        int64  `json:"seq"`
	From       string `json:"from,omitempty"`
	To         string `json:"to"`
	Event      string `json:"event"`
	Cause      string `json:"cause"`
	OccurredAt string `json:"occurredAt"`
}

type transferStatusDTO struct {
	transferDTO
	Audit  []auditEntryDTO   `json:"audit"`
	Triage transferTriageDTO `json:"triage"`
}

type siteImbalanceDTO struct {
	Site               string  `json:"site"`
	Balance            string  `json:"balance"`
	OriginEnabled      bool    `json:"originEnabled"`
	DestinationEnabled bool    `json:"destinationEnabled"`
	TotalDemand        int     `json:"totalDemand"`
	CapacityOverWindow float64 `json:"capacityOverWindow"`
	CapacityHeadroom   int     `json:"capacityHeadroom"`
	Explanation        string  `json:"explanation"`
}

type networkImbalanceDTO struct {
	Advisory   bool               `json:"advisory"`
	AsOf       string             `json:"asOf"`
	Imbalanced bool               `json:"imbalanced"`
	Sites      []siteImbalanceDTO `json:"sites"`
	Summary    string             `json:"summary"`
	NextCheck  string             `json:"nextCheck,omitempty"`
}

func toTransferDTO(t ports.Transfer) transferDTO {
	return transferDTO{
		Id: t.Id, State: t.State, SKU: t.SKU, Quantity: t.Quantity, PickedQuantity: t.PickedQuantity,
		OriginSiteId: t.OriginSiteId, DestinationSiteId: t.DestinationSiteId,
		ReservationId: t.ReservationId, RejectionReason: t.RejectionReason,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

func toTransferTriageDTO(t policy.TransferTriage) transferTriageDTO {
	return transferTriageDTO{Cause: string(t.Cause), Summary: t.Summary, NextCheck: t.NextCheck}
}

func toStuckTransfersDTO(r usecases.StuckTransfersResult) stuckTransfersDTO {
	out := stuckTransfersDTO{
		Total:      r.Total,
		Transfers:  make([]triagedTransferDTO, 0, len(r.Transfers)),
		CauseCount: make([]causeCountDTO, 0, len(r.CauseCount)),
	}
	for _, t := range r.Transfers {
		out.Transfers = append(out.Transfers, triagedTransferDTO{transferDTO: toTransferDTO(t.Transfer), Triage: toTransferTriageDTO(t.Triage)})
	}
	for _, c := range r.CauseCount {
		out.CauseCount = append(out.CauseCount, causeCountDTO{Cause: string(c.Cause), Count: c.Count})
	}
	return out
}

func toTransferStatusDTO(r usecases.TransferStatusResult) transferStatusDTO {
	out := transferStatusDTO{
		transferDTO: toTransferDTO(r.Detail.Transfer),
		Audit:       make([]auditEntryDTO, 0, len(r.Detail.Audit)),
		Triage:      toTransferTriageDTO(r.Triage),
	}
	for _, a := range r.Detail.Audit {
		out.Audit = append(out.Audit, auditEntryDTO{Seq: a.Seq, From: a.From, To: a.To, Event: a.Event, Cause: a.Cause, OccurredAt: a.OccurredAt})
	}
	return out
}

func toNetworkImbalanceDTO(n policy.NetworkImbalance) networkImbalanceDTO {
	out := networkImbalanceDTO{
		Advisory: n.Advisory, AsOf: n.AsOf, Imbalanced: n.Imbalanced,
		Sites: make([]siteImbalanceDTO, 0, len(n.Sites)), Summary: n.Summary, NextCheck: n.NextCheck,
	}
	for _, s := range n.Sites {
		out.Sites = append(out.Sites, siteImbalanceDTO{
			Site: s.Site, Balance: string(s.Balance), OriginEnabled: s.OriginEnabled, DestinationEnabled: s.DestinationEnabled,
			TotalDemand: s.TotalDemand, CapacityOverWindow: s.CapacityOverWindow, CapacityHeadroom: s.CapacityHeadroom,
			Explanation: s.Explanation,
		})
	}
	return out
}

// transferWatchStatus maps a use-case error onto an HTTP status: the caller's
// own input is a 400, an unknown transfer a 404, anything else is NIP being
// unreachable or fail-closed -- an upstream degradation (502).
func transferWatchStatus(err error) int {
	switch {
	case errors.Is(err, usecases.ErrInvalidInput):
		return nethttp.StatusBadRequest
	case errors.Is(err, usecases.ErrTransferNotFound):
		return nethttp.StatusNotFound
	default:
		return nethttp.StatusBadGateway
	}
}

// getStuckTransfers handles GET /transfer-watch/stuck?olderThanMinutes=N
// [&state=S][&limit=L]. olderThanMinutes is required and must be positive: the
// agent never picks a staleness threshold on the operator's behalf.
func (h *Handlers) getStuckTransfers(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.TransferWatch == nil {
		writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"error": transferWatchNotConfigured})
		return
	}
	q := r.URL.Query()
	minutes, err := strconv.Atoi(q.Get("olderThanMinutes"))
	if err != nil {
		writeJSON(w, nethttp.StatusBadRequest, map[string]string{"error": "olderThanMinutes is required and must be an integer"})
		return
	}
	limit := 0
	if raw := q.Get("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil {
			writeJSON(w, nethttp.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
			return
		}
	}
	result, err := h.TransferWatch.StuckTransfers(r.Context(), minutes, q.Get("state"), limit)
	if err != nil {
		writeJSON(w, transferWatchStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, nethttp.StatusOK, toStuckTransfersDTO(result))
}

// getTransferStatus handles GET /transfer-watch/transfers/{id}.
func (h *Handlers) getTransferStatus(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.TransferWatch == nil {
		writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"error": transferWatchNotConfigured})
		return
	}
	result, err := h.TransferWatch.TransferStatus(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, transferWatchStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, nethttp.StatusOK, toTransferStatusDTO(result))
}

// getNetworkImbalance handles GET /transfer-watch/imbalance.
func (h *Handlers) getNetworkImbalance(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.TransferWatch == nil {
		writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"error": transferWatchNotConfigured})
		return
	}
	result, err := h.TransferWatch.NetworkImbalance(r.Context())
	if err != nil {
		writeJSON(w, transferWatchStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, nethttp.StatusOK, toNetworkImbalanceDTO(result))
}
