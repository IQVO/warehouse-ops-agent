// Package policy: inbound outlook rules (ADR 0021).
//
// The inbound outlook answers four operator questions about the dock side,
// each read straight off inbound-receiving's published facts -- these rules
// never recompute a state, a discrepancy or an expected quantity:
//
//   - which ASNs are still awaiting arrival (state Registered), and which of
//     those are already past their own expected arrival;
//   - which dock appointments fall in the next 24 hours (Booked or CheckedIn,
//     window overlapping [now, now+24h));
//   - which receipts have been Open longer than a configured age;
//   - which receipts were closed today with discrepancies.
//
// "Now" and the day boundary are parameters, never read from a clock here,
// and the stale-receipt age is an operator input with no default. Pure
// values and functions only (no ports, no I/O).
package policy

import (
	"sort"
	"time"
)

// inbound-receiving's published state names, as the rules compare them.
const (
	// InboundAsnRegistered is the ASN state "awaiting arrival".
	InboundAsnRegistered = "Registered"
	// InboundReceiptOpen and InboundReceiptClosed are the receipt states.
	InboundReceiptOpen   = "Open"
	InboundReceiptClosed = "Closed"

	inboundApptBooked    = "Booked"
	inboundApptCheckedIn = "CheckedIn"
)

// InboundAppointmentHorizon is how far ahead "the next 24 h" looks. It is
// part of the use case's definition (the brief fixes it), not a tunable.
const InboundAppointmentHorizon = 24 * time.Hour

// InboundAsnFact is the slice of one ASN the outlook reads. ExpectedArrival
// is the zero time when the supplier gave none.
type InboundAsnFact struct {
	AsnNumber       string
	SupplierRef     string
	ExpectedArrival time.Time
	State           string
}

// InboundAppointmentFact is the slice of one dock appointment the outlook
// reads.
type InboundAppointmentFact struct {
	AppointmentID string
	DoorCode      string
	Carrier       string
	WindowStart   time.Time
	WindowEnd     time.Time
	AsnNumbers    []string
	State         string
}

// InboundDiscrepancyFact is one discrepancy exactly as inbound-receiving
// judged it (Kind is Short, Over or Damaged).
type InboundDiscrepancyFact struct {
	LineNo      int
	Sku         string
	Kind        string
	ExpectedQty int64
	ReceivedQty int64
	DamagedQty  int64
}

// InboundReceiptFact is the slice of one receipt the outlook reads. ClosedAt
// is the zero time while the receipt is Open; DoorCode is empty for a
// walk-in.
type InboundReceiptFact struct {
	ReceiptID     string
	AsnNumber     string
	DoorCode      string
	State         string
	OpenedAt      time.Time
	ClosedAt      time.Time
	Discrepancies []InboundDiscrepancyFact
}

// AwaitingAsn is a Registered ASN. Overdue is true only when the supplier
// gave an expected arrival and it is already in the past; an ASN with no
// expected arrival is never called overdue.
type AwaitingAsn struct {
	InboundAsnFact
	Overdue bool
}

// StaleReceipt is an Open receipt that has been open longer than the
// configured age. OpenFor is now minus its opening instant.
type StaleReceipt struct {
	InboundReceiptFact
	OpenFor time.Duration
}

// AsnsAwaitingArrival returns the Registered ASNs, earliest expected arrival
// first (ASNs with no expected arrival last), ties by ASN number.
func AsnsAwaitingArrival(asns []InboundAsnFact, now time.Time) []AwaitingAsn {
	out := []AwaitingAsn{}
	for _, a := range asns {
		if a.State != InboundAsnRegistered {
			continue
		}
		out = append(out, AwaitingAsn{
			InboundAsnFact: a,
			Overdue:        !a.ExpectedArrival.IsZero() && a.ExpectedArrival.Before(now),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ExpectedArrival.IsZero() != b.ExpectedArrival.IsZero() {
			return !a.ExpectedArrival.IsZero()
		}
		if !a.ExpectedArrival.Equal(b.ExpectedArrival) {
			return a.ExpectedArrival.Before(b.ExpectedArrival)
		}
		return a.AsnNumber < b.AsnNumber
	})
	return out
}

// AppointmentsInHorizon returns the Booked or CheckedIn appointments whose
// window overlaps [now, now+InboundAppointmentHorizon), earliest window
// first, ties by appointment id. A window that has already ended, or that
// starts at or after the horizon, is excluded.
func AppointmentsInHorizon(appts []InboundAppointmentFact, now time.Time) []InboundAppointmentFact {
	until := now.Add(InboundAppointmentHorizon)
	out := []InboundAppointmentFact{}
	for _, a := range appts {
		if a.State != inboundApptBooked && a.State != inboundApptCheckedIn {
			continue
		}
		if a.WindowEnd.After(now) && a.WindowStart.Before(until) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].WindowStart.Equal(out[j].WindowStart) {
			return out[i].WindowStart.Before(out[j].WindowStart)
		}
		return out[i].AppointmentID < out[j].AppointmentID
	})
	return out
}

// StaleOpenReceipts returns the Open receipts opened strictly more than age
// ago, oldest first. A non-positive age is not a threshold: nothing is
// stale (callers report the threshold as not configured instead).
func StaleOpenReceipts(receipts []InboundReceiptFact, now time.Time, age time.Duration) []StaleReceipt {
	out := []StaleReceipt{}
	if age <= 0 {
		return out
	}
	for _, r := range receipts {
		if r.State != InboundReceiptOpen || r.OpenedAt.IsZero() {
			continue
		}
		if openFor := now.Sub(r.OpenedAt); openFor > age {
			out = append(out, StaleReceipt{InboundReceiptFact: r, OpenFor: openFor})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.Before(out[j].OpenedAt)
		}
		return out[i].ReceiptID < out[j].ReceiptID
	})
	return out
}

// DayBounds returns the calendar day containing now, in now's own location:
// [from, to) with to = from + one calendar day.
func DayBounds(now time.Time) (from, to time.Time) {
	y, m, d := now.Date()
	from = time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	return from, from.AddDate(0, 0, 1)
}

// ClosedWithDiscrepancies returns the Closed receipts with at least one
// discrepancy whose close instant lies in [from, to), earliest close first,
// ties by receipt id.
func ClosedWithDiscrepancies(receipts []InboundReceiptFact, from, to time.Time) []InboundReceiptFact {
	out := []InboundReceiptFact{}
	for _, r := range receipts {
		if r.State != InboundReceiptClosed || len(r.Discrepancies) == 0 {
			continue
		}
		if !r.ClosedAt.Before(from) && r.ClosedAt.Before(to) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ClosedAt.Equal(out[j].ClosedAt) {
			return out[i].ClosedAt.Before(out[j].ClosedAt)
		}
		return out[i].ReceiptID < out[j].ReceiptID
	})
	return out
}
