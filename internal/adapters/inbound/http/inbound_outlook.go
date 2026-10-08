package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// getInboundOutlook handles GET /inbound-outlook (ADR 0021): the inbound
// dock side's near-term picture read from inbound-receiving -- ASNs awaiting
// arrival, appointments in the next 24 hours, open receipts older than
// INBOUND_STALE_RECEIPT_AGE and receipts closed with discrepancies today. It
// takes no parameters (nothing is ever supplied by the caller). Each section
// carries its own omitted reason and complete flag; inbound-receiving being
// unreachable for every section is a 502, and an unwired use case
// (INBOUND_RECEIVING_MCP_ENDPOINT unset) is a 503 -- same convention as
// every optional route here.
func (h *Handlers) getInboundOutlook(w http.ResponseWriter, r *http.Request) {
	if h.InboundOutlook == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "inbound-receiving not configured"})
		return
	}
	res, err := h.InboundOutlook.Execute(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, usecases.ErrInboundReceivingNotConfigured) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toInboundOutlookDTO(res))
}

// inboundOutlookDTO is the GET /inbound-outlook body. staleAfterSeconds is
// omitted when INBOUND_STALE_RECEIPT_AGE is not configured.
type inboundOutlookDTO struct {
	GeneratedAt       string `json:"generatedAt"`
	AppointmentsUntil string `json:"appointmentsUntil"`
	DayFrom           string `json:"dayFrom"`
	DayTo             string `json:"dayTo"`
	StaleAfterSeconds int64  `json:"staleAfterSeconds,omitempty"`

	AwaitingAsns         inboundSectionDTO[inboundAsnDTO]            `json:"awaitingAsns"`
	UpcomingAppointments inboundSectionDTO[inboundAppointmentDTO]    `json:"upcomingAppointments"`
	StaleReceipts        inboundSectionDTO[inboundStaleReceiptDTO]   `json:"staleReceipts"`
	DiscrepantReceipts   inboundSectionDTO[inboundDiscrepantReceipt] `json:"discrepantReceipts"`
}

// inboundSectionDTO is one outlook section. omitted, when present, is why the
// section could not be produced and means items is NOT "nothing to report";
// complete=false means the per-request scan bound was reached and items is a
// lower bound.
type inboundSectionDTO[T any] struct {
	Omitted  string `json:"omitted,omitempty"`
	Complete bool   `json:"complete"`
	Count    int    `json:"count"`
	Items    []T    `json:"items"`
}

type inboundAsnDTO struct {
	AsnNumber       string `json:"asnNumber"`
	SupplierRef     string `json:"supplierRef"`
	ExpectedArrival string `json:"expectedArrival,omitempty"`
	State           string `json:"state"`
	Overdue         bool   `json:"overdue"`
}

type inboundAppointmentDTO struct {
	AppointmentID string   `json:"appointmentId"`
	DoorCode      string   `json:"doorCode"`
	Carrier       string   `json:"carrier"`
	WindowStart   string   `json:"windowStart"`
	WindowEnd     string   `json:"windowEnd"`
	AsnNumbers    []string `json:"asnNumbers"`
	State         string   `json:"state"`
}

type inboundStaleReceiptDTO struct {
	ReceiptID      string `json:"receiptId"`
	AsnNumber      string `json:"asnNumber"`
	DoorCode       string `json:"doorCode,omitempty"`
	OpenedAt       string `json:"openedAt"`
	OpenForSeconds int64  `json:"openForSeconds"`
}

type inboundDiscrepancyDTO struct {
	LineNo      int    `json:"lineNo"`
	Sku         string `json:"sku"`
	Kind        string `json:"kind"`
	ExpectedQty int64  `json:"expectedQty"`
	ReceivedQty int64  `json:"receivedQty"`
	DamagedQty  int64  `json:"damagedQty"`
}

type inboundDiscrepantReceipt struct {
	ReceiptID     string                  `json:"receiptId"`
	AsnNumber     string                  `json:"asnNumber"`
	DoorCode      string                  `json:"doorCode,omitempty"`
	OpenedAt      string                  `json:"openedAt"`
	ClosedAt      string                  `json:"closedAt"`
	Discrepancies []inboundDiscrepancyDTO `json:"discrepancies"`
}

func instantUTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func toInboundOutlookDTO(res usecases.InboundOutlookResult) inboundOutlookDTO {
	out := inboundOutlookDTO{
		GeneratedAt:       instantUTC(res.GeneratedAt),
		AppointmentsUntil: instantUTC(res.AppointmentsUntil),
		DayFrom:           res.DayFrom.Format(time.RFC3339),
		DayTo:             res.DayTo.Format(time.RFC3339),
		StaleAfterSeconds: int64(res.StaleAfter / time.Second),
	}

	asns := make([]inboundAsnDTO, 0, len(res.AwaitingAsns.Items))
	for _, a := range res.AwaitingAsns.Items {
		asns = append(asns, inboundAsnDTO{
			AsnNumber: a.AsnNumber, SupplierRef: a.SupplierRef, ExpectedArrival: instantUTC(a.ExpectedArrival),
			State: a.State, Overdue: a.Overdue,
		})
	}
	out.AwaitingAsns = newInboundSection(res.AwaitingAsns.InboundSectionStatus, asns)

	appts := make([]inboundAppointmentDTO, 0, len(res.UpcomingAppointments.Items))
	for _, a := range res.UpcomingAppointments.Items {
		appts = append(appts, toInboundAppointmentDTO(a))
	}
	out.UpcomingAppointments = newInboundSection(res.UpcomingAppointments.InboundSectionStatus, appts)

	stale := make([]inboundStaleReceiptDTO, 0, len(res.StaleReceipts.Items))
	for _, s := range res.StaleReceipts.Items {
		stale = append(stale, inboundStaleReceiptDTO{
			ReceiptID: s.ReceiptID, AsnNumber: s.AsnNumber, DoorCode: s.DoorCode,
			OpenedAt: instantUTC(s.OpenedAt), OpenForSeconds: int64(s.OpenFor / time.Second),
		})
	}
	out.StaleReceipts = newInboundSection(res.StaleReceipts.InboundSectionStatus, stale)

	disc := make([]inboundDiscrepantReceipt, 0, len(res.DiscrepantReceipts.Items))
	for _, r := range res.DiscrepantReceipts.Items {
		disc = append(disc, toInboundDiscrepantReceipt(r))
	}
	out.DiscrepantReceipts = newInboundSection(res.DiscrepantReceipts.InboundSectionStatus, disc)
	return out
}

func newInboundSection[T any](status usecases.InboundSectionStatus, items []T) inboundSectionDTO[T] {
	return inboundSectionDTO[T]{Omitted: status.Omitted, Complete: status.Complete, Count: len(items), Items: items}
}

func toInboundAppointmentDTO(a policy.InboundAppointmentFact) inboundAppointmentDTO {
	asns := a.AsnNumbers
	if asns == nil {
		asns = []string{}
	}
	return inboundAppointmentDTO{
		AppointmentID: a.AppointmentID, DoorCode: a.DoorCode, Carrier: a.Carrier,
		WindowStart: instantUTC(a.WindowStart), WindowEnd: instantUTC(a.WindowEnd),
		AsnNumbers: asns, State: a.State,
	}
}

func toInboundDiscrepantReceipt(r policy.InboundReceiptFact) inboundDiscrepantReceipt {
	ds := make([]inboundDiscrepancyDTO, 0, len(r.Discrepancies))
	for _, d := range r.Discrepancies {
		ds = append(ds, inboundDiscrepancyDTO{
			LineNo: d.LineNo, Sku: d.Sku, Kind: d.Kind,
			ExpectedQty: d.ExpectedQty, ReceivedQty: d.ReceivedQty, DamagedQty: d.DamagedQty,
		})
	}
	return inboundDiscrepantReceipt{
		ReceiptID: r.ReceiptID, AsnNumber: r.AsnNumber, DoorCode: r.DoorCode,
		OpenedAt: instantUTC(r.OpenedAt), ClosedAt: instantUTC(r.ClosedAt), Discrepancies: ds,
	}
}
