// Package usecases: InboundOutlook (ADR 0021) -- reads inbound-receiving
// through its list_asns, list_appointments and list_receipts read tools and
// reports the dock side's near-term picture: ASNs awaiting arrival,
// appointments in the next 24 hours, open receipts older than the configured
// age, and receipts closed with discrepancies today.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

const (
	// inboundPageSize is the list page size: inbound-receiving's own
	// maximum, so a scan needs as few calls as possible.
	inboundPageSize = 500
	// inboundMaxPages bounds one section's scan to 10 pages (5,000
	// records). It is a per-request work bound, not a business input: when
	// more records remain the section says Complete=false and its list is a
	// lower bound; it never pretends the rest holds nothing.
	inboundMaxPages = 10
)

// ErrInboundReceivingNotConfigured is returned when the use case has no
// inbound-receiving port (INBOUND_RECEIVING_MCP_ENDPOINT unset).
var ErrInboundReceivingNotConfigured = errors.New("inbound-receiving is not configured")

// InboundSectionStatus is the honesty envelope of one outlook section.
// Omitted is non-empty when the section could not be produced (an upstream
// failure, or an input the operator never configured) and says why; its
// list is then empty and MUST NOT be read as "nothing to report". Complete
// is false when the per-request page bound was reached before the last
// page, so the list is a lower bound.
type InboundSectionStatus struct {
	Omitted  string
	Complete bool
}

// InboundAwaitingAsns is the "ASNs awaiting arrival" section.
type InboundAwaitingAsns struct {
	InboundSectionStatus
	Items []policy.AwaitingAsn
}

// InboundUpcomingAppointments is the "appointments in the next 24 h"
// section.
type InboundUpcomingAppointments struct {
	InboundSectionStatus
	Items []policy.InboundAppointmentFact
}

// InboundStaleReceipts is the "open receipts older than the configured age"
// section.
type InboundStaleReceipts struct {
	InboundSectionStatus
	Items []policy.StaleReceipt
}

// InboundDiscrepantReceipts is the "receipts closed with discrepancies
// today" section.
type InboundDiscrepantReceipts struct {
	InboundSectionStatus
	Items []policy.InboundReceiptFact
}

// InboundOutlookResult is one outlook. The window fields echo exactly what
// each section was evaluated against, so a reader can see the inputs.
type InboundOutlookResult struct {
	GeneratedAt time.Time
	// AppointmentsUntil is GeneratedAt + 24 h.
	AppointmentsUntil time.Time
	// DayFrom / DayTo bound "today": the calendar day containing
	// GeneratedAt, in the agent's local time zone.
	DayFrom time.Time
	DayTo   time.Time
	// StaleAfter echoes INBOUND_STALE_RECEIPT_AGE (0 = not configured).
	StaleAfter time.Duration

	AwaitingAsns         InboundAwaitingAsns
	UpcomingAppointments InboundUpcomingAppointments
	StaleReceipts        InboundStaleReceipts
	DiscrepantReceipts   InboundDiscrepantReceipts
}

// InboundOutlook is the ADR 0021 decision-support use case. It is read-only
// (list tools only) and invents nothing: every state, discrepancy and
// timestamp is inbound-receiving's published fact, "now" comes from the
// clock, and the only operator input is the stale-receipt age, which has no
// default -- unset, that section is omitted with the reason, not guessed.
//
// Sections fail independently: one failing list call omits that section
// (with the error) and the others are still reported. Only when EVERY
// attempted section failed does Execute return an error, so an unreachable
// inbound-receiving is a 502 rather than an all-empty "all clear".
type InboundOutlook struct {
	Inbound ports.InboundReceivingClient
	// StaleReceiptAge is INBOUND_STALE_RECEIPT_AGE; 0 means not configured.
	StaleReceiptAge time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Execute builds the outlook as of now.
func (uc *InboundOutlook) Execute(ctx context.Context) (InboundOutlookResult, error) {
	if uc == nil || uc.Inbound == nil {
		return InboundOutlookResult{}, ErrInboundReceivingNotConfigured
	}
	now := time.Now()
	if uc.Now != nil {
		now = uc.Now()
	}
	res := InboundOutlookResult{
		GeneratedAt:       now,
		AppointmentsUntil: now.Add(policy.InboundAppointmentHorizon),
		StaleAfter:        uc.StaleReceiptAge,
	}
	res.DayFrom, res.DayTo = policy.DayBounds(now)

	var firstErr error
	attempted, failed := 0, 0
	note := func(err error) string {
		attempted++
		if err == nil {
			return ""
		}
		failed++
		if firstErr == nil {
			firstErr = err
		}
		return err.Error()
	}

	var err error
	res.AwaitingAsns, err = uc.awaitingAsns(ctx, now)
	res.AwaitingAsns.Omitted = note(err)

	res.UpcomingAppointments, err = uc.upcomingAppointments(ctx, now)
	res.UpcomingAppointments.Omitted = note(err)

	if uc.StaleReceiptAge > 0 {
		res.StaleReceipts, err = uc.staleReceipts(ctx, now)
		res.StaleReceipts.Omitted = note(err)
	} else {
		res.StaleReceipts = InboundStaleReceipts{Items: []policy.StaleReceipt{}}
		res.StaleReceipts.Omitted = "INBOUND_STALE_RECEIPT_AGE is not configured: no stale-receipt threshold is assumed"
	}

	res.DiscrepantReceipts, err = uc.discrepantReceipts(ctx, res.DayFrom, res.DayTo)
	res.DiscrepantReceipts.Omitted = note(err)

	if attempted > 0 && failed == attempted {
		return InboundOutlookResult{}, fmt.Errorf("inbound-receiving: every outlook section failed: %w", firstErr)
	}
	return res, nil
}

func (uc *InboundOutlook) awaitingAsns(ctx context.Context, now time.Time) (InboundAwaitingAsns, error) {
	facts, complete, err := scanInboundPages(func(cursor string) ([]policy.InboundAsnFact, string, error) {
		page, err := uc.Inbound.ListAsns(ctx, ports.InboundAsnListQuery{Limit: inboundPageSize, Cursor: cursor, State: policy.InboundAsnRegistered})
		if err != nil {
			return nil, "", fmt.Errorf("list_asns: %w", err)
		}
		out := make([]policy.InboundAsnFact, 0, len(page.Items))
		for _, a := range page.Items {
			arrival, err := parseInboundInstant("expected_arrival of ASN "+a.AsnNumber, a.ExpectedArrival, false)
			if err != nil {
				return nil, "", err
			}
			out = append(out, policy.InboundAsnFact{AsnNumber: a.AsnNumber, SupplierRef: a.SupplierRef, ExpectedArrival: arrival, State: a.State})
		}
		return out, page.NextCursor, nil
	})
	if err != nil {
		return InboundAwaitingAsns{Items: []policy.AwaitingAsn{}}, err
	}
	return InboundAwaitingAsns{
		InboundSectionStatus: InboundSectionStatus{Complete: complete},
		Items:                policy.AsnsAwaitingArrival(facts, now),
	}, nil
}

func (uc *InboundOutlook) upcomingAppointments(ctx context.Context, now time.Time) (InboundUpcomingAppointments, error) {
	until := now.Add(policy.InboundAppointmentHorizon)
	facts, complete, err := scanInboundPages(func(cursor string) ([]policy.InboundAppointmentFact, string, error) {
		page, err := uc.Inbound.ListAppointments(ctx, ports.InboundAppointmentListQuery{Limit: inboundPageSize, Cursor: cursor, From: now, To: until})
		if err != nil {
			return nil, "", fmt.Errorf("list_appointments: %w", err)
		}
		out := make([]policy.InboundAppointmentFact, 0, len(page.Items))
		for _, a := range page.Items {
			start, err := parseInboundInstant("window_start of appointment "+a.AppointmentID, a.WindowStart, true)
			if err != nil {
				return nil, "", err
			}
			end, err := parseInboundInstant("window_end of appointment "+a.AppointmentID, a.WindowEnd, true)
			if err != nil {
				return nil, "", err
			}
			out = append(out, policy.InboundAppointmentFact{
				AppointmentID: a.AppointmentID, DoorCode: a.DoorCode, Carrier: a.Carrier,
				WindowStart: start, WindowEnd: end, AsnNumbers: a.AsnNumbers, State: a.State,
			})
		}
		return out, page.NextCursor, nil
	})
	if err != nil {
		return InboundUpcomingAppointments{Items: []policy.InboundAppointmentFact{}}, err
	}
	return InboundUpcomingAppointments{
		InboundSectionStatus: InboundSectionStatus{Complete: complete},
		Items:                policy.AppointmentsInHorizon(facts, now),
	}, nil
}

func (uc *InboundOutlook) staleReceipts(ctx context.Context, now time.Time) (InboundStaleReceipts, error) {
	facts, complete, err := uc.scanReceipts(ctx, policy.InboundReceiptOpen)
	if err != nil {
		return InboundStaleReceipts{Items: []policy.StaleReceipt{}}, err
	}
	return InboundStaleReceipts{
		InboundSectionStatus: InboundSectionStatus{Complete: complete},
		Items:                policy.StaleOpenReceipts(facts, now, uc.StaleReceiptAge),
	}, nil
}

func (uc *InboundOutlook) discrepantReceipts(ctx context.Context, from, to time.Time) (InboundDiscrepantReceipts, error) {
	facts, complete, err := uc.scanReceipts(ctx, policy.InboundReceiptClosed)
	if err != nil {
		return InboundDiscrepantReceipts{Items: []policy.InboundReceiptFact{}}, err
	}
	return InboundDiscrepantReceipts{
		InboundSectionStatus: InboundSectionStatus{Complete: complete},
		Items:                policy.ClosedWithDiscrepancies(facts, from, to),
	}, nil
}

// scanReceipts pages list_receipts filtered to one state. list_receipts has
// no time filter, so the scan covers every receipt in that state up to the
// page bound.
func (uc *InboundOutlook) scanReceipts(ctx context.Context, state string) ([]policy.InboundReceiptFact, bool, error) {
	return scanInboundPages(func(cursor string) ([]policy.InboundReceiptFact, string, error) {
		page, err := uc.Inbound.ListReceipts(ctx, ports.InboundReceiptListQuery{Limit: inboundPageSize, Cursor: cursor, State: state})
		if err != nil {
			return nil, "", fmt.Errorf("list_receipts (%s): %w", state, err)
		}
		out := make([]policy.InboundReceiptFact, 0, len(page.Items))
		for _, r := range page.Items {
			fact, err := toInboundReceiptFact(r)
			if err != nil {
				return nil, "", err
			}
			out = append(out, fact)
		}
		return out, page.NextCursor, nil
	})
}

func toInboundReceiptFact(r ports.InboundReceipt) (policy.InboundReceiptFact, error) {
	opened, err := parseInboundInstant("opened_at of receipt "+r.ReceiptID, r.OpenedAt, true)
	if err != nil {
		return policy.InboundReceiptFact{}, err
	}
	// closed_at is published only on a Closed receipt; a Closed receipt
	// without one is a contract violation, not "closed at the epoch".
	closed, err := parseInboundInstant("closed_at of receipt "+r.ReceiptID, r.ClosedAt, r.State == policy.InboundReceiptClosed)
	if err != nil {
		return policy.InboundReceiptFact{}, err
	}
	discrepancies := make([]policy.InboundDiscrepancyFact, 0, len(r.Discrepancies))
	for _, d := range r.Discrepancies {
		discrepancies = append(discrepancies, policy.InboundDiscrepancyFact{
			LineNo: d.LineNo, Sku: d.Sku, Kind: d.Kind,
			ExpectedQty: d.ExpectedQty, ReceivedQty: d.ReceivedQty, DamagedQty: d.DamagedQty,
		})
	}
	return policy.InboundReceiptFact{
		ReceiptID: r.ReceiptID, AsnNumber: r.AsnNumber, DoorCode: r.DoorCode, State: r.State,
		OpenedAt: opened, ClosedAt: closed, Discrepancies: discrepancies,
	}, nil
}

// parseInboundInstant parses an RFC 3339 instant inbound-receiving
// published. An empty value is the zero time when optional and an error when
// required; a malformed value is always an error.
func parseInboundInstant(what, raw string, required bool) (time.Time, error) {
	if raw == "" {
		if required {
			return time.Time{}, fmt.Errorf("inbound-receiving published no %s", what)
		}
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("inbound-receiving published an unparseable %s %q: %w", what, raw, err)
	}
	return t, nil
}

// scanInboundPages follows next_cursor from the first page for at most
// inboundMaxPages pages. complete is false when the bound was reached with a
// cursor still pending.
func scanInboundPages[T any](fetch func(cursor string) ([]T, string, error)) ([]T, bool, error) {
	var all []T
	cursor := ""
	for page := 0; page < inboundMaxPages; page++ {
		items, next, err := fetch(cursor)
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		if next == "" {
			return all, true, nil
		}
		cursor = next
	}
	return all, false, nil
}
