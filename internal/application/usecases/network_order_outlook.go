// Package usecases (this file): NetworkOrderOutlook (ADR 0026) -- assembles
// one customer order's retail-network picture across the ADR-0025 clients:
// the placed order and its shipment groups (customer-ordering), how each
// group is travelling (get_order_tracking), the ship-to address, the
// delivery promises against its groups (delivery-promise), the fulfillment
// assignments sourcing it (order-sourcing) and the customer-visible package
// journeys behind its tracking numbers (shipping-and-tracking).
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// ErrCustomerOrderingNotConfigured is returned when the order outlook has
// no customer-ordering port (CUSTOMER_ORDERING_MCP_ENDPOINT unset): the
// composition root then wires no outlook at all.
var ErrCustomerOrderingNotConfigured = errors.New("customer-ordering is not configured")

// JourneyFact is one package journey of the order, or why it could not be
// read. The outlook carries the journey's summary (state, tier, the
// assignment it rides, scan count and last scan); the full scan trail stays
// behind shipping-and-tracking's get_journey.
type JourneyFact struct {
	TrackingNumber string
	State          string
	PoNumber       string
	AssignmentID   string
	Tier           string
	HandedOverAt   string
	DeliveredAt    string
	ScanCount      int
	LastScanAt     string
	LastScanNodeID string
	// Error is non-empty when this journey could not be read; every other
	// field is then empty and the journey is unknown, not absent.
	Error string
}

// NetworkOrderOutlookResult is one order outlook. Each section fails
// independently (ADR 0023 fail-open semantics): an unconfigured upstream
// omits its section with "not-configured", a failing call omits it with
// the error, and the rest are still reported.
type NetworkOrderOutlookResult struct {
	OrderID     string
	GeneratedAt time.Time

	Order    OutlookSection[ports.CustomerOrder]
	Tracking OutlookSection[ports.OrderTracking]
	ShipTo   OutlookSection[ports.ShipToAddress]
	Promises OutlookSection[ports.DeliveryPromise]
	Sourcing OutlookSection[ports.FulfillmentAssignment]
	Journeys OutlookSection[JourneyFact]
}

// NetworkOrderOutlook is the ADR-0026 order-side decision-support use
// case. It is read-only and invents nothing: every state, window and scan
// is an upstream's published fact; the only inputs are the caller's order
// id and the clock. customer-ordering is the anchor (it owns the order
// id); the outlook is wired only when it is configured, and every other
// upstream degrades per section.
//
// Only when EVERY attempted section failed does Execute return an error,
// so an unreachable customer-ordering is never rendered as an all-clear.
type NetworkOrderOutlook struct {
	Orders   ports.PlacedOrderReader
	Promises ports.OrderPromiseReader
	Sourcing ports.OrderAssignmentReader
	Journeys ports.PackageJourneyReader
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Execute builds the outlook for one order id.
func (uc *NetworkOrderOutlook) Execute(ctx context.Context, orderID string) (NetworkOrderOutlookResult, error) {
	if uc == nil || uc.Orders == nil {
		return NetworkOrderOutlookResult{}, ErrCustomerOrderingNotConfigured
	}
	if orderID == "" {
		return NetworkOrderOutlookResult{}, errors.New("order id is required")
	}
	now := time.Now()
	if uc.Now != nil {
		now = uc.Now()
	}
	res := NetworkOrderOutlookResult{OrderID: orderID, GeneratedAt: now}

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

	res.Order = record(note, uc.orderSection(ctx, orderID))
	res.Tracking = record(note, uc.trackingSection(ctx, orderID))
	res.ShipTo = record(note, uc.shipToSection(ctx, orderID))
	res.Promises = record(note, uc.promisesSection(ctx, orderID, groupPromiseIDs(res.Order)))
	res.Sourcing = record(note, uc.sourcingSection(ctx, orderID))
	res.Journeys = uc.journeysSection(ctx, res.Tracking, res.Sourcing)

	if attempted > 0 && failed == attempted {
		return NetworkOrderOutlookResult{}, fmt.Errorf("network order outlook: every attempted section failed: %w", firstErr)
	}
	return res, nil
}

// sectionResult is one section builder's outcome: the section, whether it
// ran against a configured upstream (a declined builder carries its own
// not-configured reason and counts as neither attempted nor failed), and
// the error it hit when it did run.
type sectionResult[T any] struct {
	section   OutlookSection[T]
	attempted bool
	err       error
}

// record folds one builder outcome into the honesty envelope: an
// attempted section's omitted reason is the error (empty on success); a
// declined section keeps the reason its builder set.
func record[T any](note func(error) string, r sectionResult[T]) OutlookSection[T] {
	if r.attempted {
		r.section.Omitted = note(r.err)
	}
	return r.section
}

// orderSection reads the placed order itself. A failed read leaves the
// section omitted; a not-found order is an upstream error like any other.
func (uc *NetworkOrderOutlook) orderSection(ctx context.Context, orderID string) sectionResult[ports.CustomerOrder] {
	section := OutlookSection[ports.CustomerOrder]{Items: []ports.CustomerOrder{}}
	order, err := uc.Orders.GetOrder(ctx, orderID)
	if err != nil {
		return sectionResult[ports.CustomerOrder]{section: section, attempted: true, err: fmt.Errorf("get_order: %w", err)}
	}
	section.Items = append(section.Items, order)
	section.Complete = true
	return sectionResult[ports.CustomerOrder]{section: section, attempted: true}
}

func (uc *NetworkOrderOutlook) trackingSection(ctx context.Context, orderID string) sectionResult[ports.OrderTracking] {
	section := OutlookSection[ports.OrderTracking]{Items: []ports.OrderTracking{}}
	tracking, err := uc.Orders.GetOrderTracking(ctx, orderID)
	if err != nil {
		return sectionResult[ports.OrderTracking]{section: section, attempted: true, err: fmt.Errorf("get_order_tracking: %w", err)}
	}
	section.Items = append(section.Items, tracking)
	section.Complete = true
	return sectionResult[ports.OrderTracking]{section: section, attempted: true}
}

func (uc *NetworkOrderOutlook) shipToSection(ctx context.Context, orderID string) sectionResult[ports.ShipToAddress] {
	section := OutlookSection[ports.ShipToAddress]{Items: []ports.ShipToAddress{}}
	shipTo, err := uc.Orders.GetShipTo(ctx, orderID)
	if err != nil {
		return sectionResult[ports.ShipToAddress]{section: section, attempted: true, err: fmt.Errorf("get_ship_to: %w", err)}
	}
	section.Items = append(section.Items, shipTo)
	section.Complete = true
	return sectionResult[ports.ShipToAddress]{section: section, attempted: true}
}

// promisesSection reads the order's promises. list_promises_by_order is
// the primary read; get_promise covers two genuine gaps: a group whose
// promiseId the order list did not return, and — when the whole list call
// failed — every group's promise, one by one (bounded by the group count).
// Complete is false when any group's promise stayed unknown.
func (uc *NetworkOrderOutlook) promisesSection(ctx context.Context, orderID string, groupIDs []string) sectionResult[ports.DeliveryPromise] {
	section := OutlookSection[ports.DeliveryPromise]{Items: []ports.DeliveryPromise{}}
	if uc.Promises == nil {
		section.Omitted = notConfiguredReason("delivery-promise")
		return sectionResult[ports.DeliveryPromise]{section: section}
	}
	page, err := uc.Promises.ListPromisesByOrder(ctx, orderID)
	if err != nil {
		return uc.promisesPerGroup(ctx, groupIDs, fmt.Errorf("list_promises_by_order: %w", err))
	}
	section.Items = append(section.Items, page.Items...)
	section.Complete = true
	missing := missingPromiseIDs(page.Items, groupIDs)
	for _, id := range missing {
		promise, err := uc.Promises.GetPromise(ctx, id)
		if err != nil {
			section.Complete = false
			continue
		}
		section.Items = append(section.Items, promise)
	}
	return sectionResult[ports.DeliveryPromise]{section: section, attempted: true}
}

// promisesPerGroup is the get_promise fallback: one call per group
// promiseId. It runs only when the order list could not be read at all;
// when at least one promise is read the section reports it with
// Complete=false (the others are unknown), else the section is omitted
// with both reasons.
func (uc *NetworkOrderOutlook) promisesPerGroup(ctx context.Context, groupIDs []string, listErr error) sectionResult[ports.DeliveryPromise] {
	section := OutlookSection[ports.DeliveryPromise]{Items: []ports.DeliveryPromise{}}
	read := 0
	var firstErr error
	for _, id := range groupIDs {
		promise, err := uc.Promises.GetPromise(ctx, id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		read++
		section.Items = append(section.Items, promise)
	}
	if read > 0 {
		return sectionResult[ports.DeliveryPromise]{section: section, attempted: true}
	}
	if len(groupIDs) == 0 {
		return sectionResult[ports.DeliveryPromise]{section: section, attempted: true, err: listErr}
	}
	return sectionResult[ports.DeliveryPromise]{section: section, attempted: true, err: fmt.Errorf("list_promises_by_order and get_promise both failed: %w; get_promise: %v", listErr, firstErr)}
}

// sourcingSection reads the assignments sourcing the order. list_assignments
// returns the full assignment view, so one filtered call is the whole
// section; get_assignment stays the per-record drill-down, unconsumed here.
func (uc *NetworkOrderOutlook) sourcingSection(ctx context.Context, orderID string) sectionResult[ports.FulfillmentAssignment] {
	section := OutlookSection[ports.FulfillmentAssignment]{Items: []ports.FulfillmentAssignment{}}
	if uc.Sourcing == nil {
		section.Omitted = notConfiguredReason("order-sourcing")
		return sectionResult[ports.FulfillmentAssignment]{section: section}
	}
	page, err := uc.Sourcing.ListAssignments(ctx, ports.AssignmentListQuery{OrderID: orderID, Limit: networkPageSize})
	if err != nil {
		return sectionResult[ports.FulfillmentAssignment]{section: section, attempted: true, err: fmt.Errorf("list_assignments: %w", err)}
	}
	section.Items = append(section.Items, page.Assignments...)
	section.Complete = true
	return sectionResult[ports.FulfillmentAssignment]{section: section, attempted: true}
}

// journeysSection reads one package journey per tracking number behind the
// order. The numbers come from the tracking view's packages and the
// sourcing assignments (union, de-duplicated, capped at
// networkMaxJourneys reads). A failed get_journey keeps its slot with an
// Error, so an unknown journey is never mistaken for a delivered one. When
// no source of tracking numbers could be read at all the section is
// omitted with why; a read source with no numbers is the honest empty
// list (nothing is labelled yet).
func (uc *NetworkOrderOutlook) journeysSection(ctx context.Context, tracking OutlookSection[ports.OrderTracking], sourcing OutlookSection[ports.FulfillmentAssignment]) OutlookSection[JourneyFact] {
	section := OutlookSection[JourneyFact]{Items: []JourneyFact{}}
	if uc.Journeys == nil {
		section.Omitted = notConfiguredReason("shipping-and-tracking")
		return section
	}
	numbers, known := orderTrackingNumbers(tracking, sourcing)
	if !known {
		section.Omitted = "no tracking numbers could be read: customer-ordering tracking and order-sourcing sections are both unavailable"
		return section
	}
	reads := numbers
	if len(reads) > networkMaxJourneys {
		reads = reads[:networkMaxJourneys]
	}
	for _, number := range reads {
		fact := JourneyFact{TrackingNumber: number}
		journey, err := uc.Journeys.GetJourney(ctx, number)
		if err != nil {
			fact.Error = err.Error()
			section.Items = append(section.Items, fact)
			continue
		}
		section.Items = append(section.Items, journeyFact(journey))
	}
	section.Complete = len(numbers) <= networkMaxJourneys
	return section
}

// journeyFact summarizes one decoded journey.
func journeyFact(j ports.PackageJourney) JourneyFact {
	lastAt, lastNode := "", ""
	if n := len(j.Scans); n > 0 {
		last := j.Scans[n-1]
		lastAt, lastNode = last.At, last.NodeID
	}
	return JourneyFact{
		TrackingNumber: j.TrackingNumber, State: j.State, PoNumber: j.PoNumber,
		AssignmentID: j.AssignmentID, Tier: j.Tier,
		HandedOverAt: j.HandedOverAt, DeliveredAt: j.DeliveredAt,
		ScanCount: len(j.Scans), LastScanAt: lastAt, LastScanNodeID: lastNode,
	}
}

// orderTrackingNumbers unions the tracking numbers of the tracking view's
// packages and the sourcing assignments in first-seen order (uncapped: the
// caller bounds its reads). known is false when neither source produced a
// readable list (both sections omitted): the caller then cannot claim "no
// packages".
func orderTrackingNumbers(tracking OutlookSection[ports.OrderTracking], sourcing OutlookSection[ports.FulfillmentAssignment]) ([]string, bool) {
	anySource := tracking.Omitted == "" || sourcing.Omitted == ""
	if !anySource {
		return nil, false
	}
	seen := make(map[string]bool)
	var numbers []string
	add := func(number string) {
		if number == "" || seen[number] {
			return
		}
		seen[number] = true
		numbers = append(numbers, number)
	}
	for _, item := range tracking.Items {
		for _, group := range item.Groups {
			for _, pkg := range group.Packages {
				add(pkg.TrackingNumber)
			}
		}
	}
	for _, assignment := range sourcing.Items {
		for _, number := range assignment.TrackingNumbers {
			add(number)
		}
	}
	return numbers, true
}

// groupPromiseIDs returns the promise ids of the order's shipment groups,
// in group order; empty when the order itself could not be read.
func groupPromiseIDs(order OutlookSection[ports.CustomerOrder]) []string {
	var ids []string
	for _, item := range order.Items {
		for _, group := range item.Groups {
			if group.PromiseID != "" {
				ids = append(ids, group.PromiseID)
			}
		}
	}
	return ids
}

// missingPromiseIDs returns the group promise ids the list did not cover.
func missingPromiseIDs(listed []ports.DeliveryPromise, groupIDs []string) []string {
	have := make(map[string]bool, len(listed))
	for _, promise := range listed {
		have[promise.PromiseID] = true
	}
	var missing []string
	for _, id := range groupIDs {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	return missing
}
