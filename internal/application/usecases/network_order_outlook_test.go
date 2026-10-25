package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// --- fakes for the narrow order-outlook ports --------------------------------

type fakePlacedOrderReader struct {
	order     ports.CustomerOrder
	tracking  ports.OrderTracking
	shipTo    ports.ShipToAddress
	orderErr  error
	trackErr  error
	shipToErr error
}

func (f *fakePlacedOrderReader) GetOrder(context.Context, string) (ports.CustomerOrder, error) {
	return f.order, f.orderErr
}
func (f *fakePlacedOrderReader) GetOrderTracking(context.Context, string) (ports.OrderTracking, error) {
	return f.tracking, f.trackErr
}
func (f *fakePlacedOrderReader) GetShipTo(context.Context, string) (ports.ShipToAddress, error) {
	return f.shipTo, f.shipToErr
}

type fakeOrderPromiseReader struct {
	listed     ports.PromisesByOrder
	listErr    error
	byID       map[string]ports.DeliveryPromise
	promiseErr error
}

func (f *fakeOrderPromiseReader) ListPromisesByOrder(context.Context, string) (ports.PromisesByOrder, error) {
	return f.listed, f.listErr
}
func (f *fakeOrderPromiseReader) GetPromise(_ context.Context, id string) (ports.DeliveryPromise, error) {
	if f.promiseErr != nil {
		return ports.DeliveryPromise{}, f.promiseErr
	}
	return f.byID[id], nil
}

type fakeOrderAssignmentReader struct {
	page ports.AssignmentPage
	err  error
}

func (f *fakeOrderAssignmentReader) ListAssignments(context.Context, ports.AssignmentListQuery) (ports.AssignmentPage, error) {
	return f.page, f.err
}

type fakePackageJourneyReader struct {
	byNumber map[string]ports.PackageJourney
	err      error
}

func (f *fakePackageJourneyReader) GetJourney(_ context.Context, number string) (ports.PackageJourney, error) {
	if f.err != nil {
		return ports.PackageJourney{}, f.err
	}
	return f.byNumber[number], nil
}

// wiredOrderFixture is one fully-wired order outlook over happy-path fakes.
func wiredOrderFixture() (*NetworkOrderOutlook, *fakePlacedOrderReader) {
	orders := &fakePlacedOrderReader{
		order: ports.CustomerOrder{
			OrderID: "ord-1", Status: "IN_FULFILLMENT", PlacedAt: "2026-10-24T08:00:00Z",
			Groups: []ports.CustomerOrderGroup{{GroupNo: 1, PromiseID: "prm-1", Tier: "STANDARD", Status: "PROMISED"}},
		},
		tracking: ports.OrderTracking{
			OrderID: "ord-1", Status: "IN_FULFILLMENT",
			Groups: []ports.TrackedOrderGroup{{
				GroupNo: 1, PromiseID: "prm-1", Status: "SHIPPED",
				Packages: []ports.TrackedPackage{{TrackingNumber: "RN001", State: "IN_TRANSIT"}},
			}},
		},
		shipTo: ports.ShipToAddress{Name: "A. Shopper", Line1: "1 Main St", City: "Austin"},
	}
	uc := &NetworkOrderOutlook{
		Orders: orders,
		Promises: &fakeOrderPromiseReader{
			listed: ports.PromisesByOrder{Items: []ports.DeliveryPromise{{
				PromiseID: "prm-1", State: "CONFIRMED", Terms: ports.PromiseTerms{Tier: "STANDARD"},
			}}},
		},
		Sourcing: &fakeOrderAssignmentReader{page: ports.AssignmentPage{Assignments: []ports.FulfillmentAssignment{{
			AssignmentID: "asg-1", OrderID: "ord-1", State: "LOCKED", TrackingNumbers: []string{"RN001"},
		}}}},
		Journeys: &fakePackageJourneyReader{byNumber: map[string]ports.PackageJourney{
			"RN001": {TrackingNumber: "RN001", State: "IN_TRANSIT", Tier: "STANDARD", Scans: []ports.JourneyScan{{ScanID: "s1", At: "2026-10-25T06:00:00Z", NodeID: "node-1"}}},
		}},
	}
	return uc, orders
}

func TestNetworkOrderOutlook_FullPicture(t *testing.T) {
	uc, _ := wiredOrderFixture()
	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertOrderOutlookCoreSections(t, res)
	assertOrderOutlookJourneySection(t, res)
}

func assertOrderOutlookCoreSections(t *testing.T, res NetworkOrderOutlookResult) {
	t.Helper()
	if res.Order.Omitted != "" || !res.Order.Complete || len(res.Order.Items) != 1 {
		t.Fatalf("order section = %+v", res.Order)
	}
	if res.Tracking.Omitted != "" || !res.Tracking.Complete {
		t.Fatalf("tracking section = %+v", res.Tracking)
	}
	if res.ShipTo.Omitted != "" || len(res.ShipTo.Items) != 1 || res.ShipTo.Items[0].City != "Austin" {
		t.Fatalf("shipTo section = %+v", res.ShipTo)
	}
	if res.Promises.Omitted != "" || !res.Promises.Complete || len(res.Promises.Items) != 1 {
		t.Fatalf("promises section = %+v", res.Promises)
	}
	if res.Sourcing.Omitted != "" || !res.Sourcing.Complete || len(res.Sourcing.Items) != 1 {
		t.Fatalf("sourcing section = %+v", res.Sourcing)
	}
}

func assertOrderOutlookJourneySection(t *testing.T, res NetworkOrderOutlookResult) {
	t.Helper()
	if res.Journeys.Omitted != "" || !res.Journeys.Complete || len(res.Journeys.Items) != 1 {
		t.Fatalf("journeys section = %+v", res.Journeys)
	}
	journey := res.Journeys.Items[0]
	if journey.State != "IN_TRANSIT" || journey.ScanCount != 1 || journey.LastScanNodeID != "node-1" || journey.Error != "" {
		t.Fatalf("journey fact = %+v", journey)
	}
}

// TestNetworkOrderOutlook_UnconfiguredClientOmitsNotConfigured is the ADR
// 0023 table: a section whose client is unconfigured is omitted with
// "not-configured", never an error, and every other section still reports.
func TestNetworkOrderOutlook_UnconfiguredClientOmitsNotConfigured(t *testing.T) {
	uc, _ := wiredOrderFixture()
	uc.Promises = nil
	uc.Sourcing = nil
	uc.Journeys = nil

	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("an unconfigured non-anchor upstream must never fail the outlook: %v", err)
	}
	for name, section := range map[string]struct {
		omitted string
	}{
		"promises": {res.Promises.Omitted},
		"sourcing": {res.Sourcing.Omitted},
		"journeys": {res.Journeys.Omitted},
	} {
		if !strings.Contains(section.omitted, "not-configured") {
			t.Errorf("%s section omitted = %q, want the not-configured reason", name, section.omitted)
		}
	}
	if res.Order.Omitted != "" {
		t.Errorf("the anchor section must still report: %q", res.Order.Omitted)
	}
}

// TestNetworkOrderOutlook_FailingCallOmitsWithReason is the fail-open
// table: one failing upstream call omits its section with the reason and
// the others still report.
func TestNetworkOrderOutlook_FailingCallOmitsWithReason(t *testing.T) {
	uc, orders := wiredOrderFixture()
	orders.trackErr = errors.New("customer-ordering unreachable")
	uc.Promises = &fakeOrderPromiseReader{listErr: errors.New("delivery-promise unreachable"), promiseErr: errors.New("delivery-promise unreachable")}

	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("one failing section must not fail the outlook: %v", err)
	}
	if !strings.Contains(res.Tracking.Omitted, "get_order_tracking") || !strings.Contains(res.Tracking.Omitted, "unreachable") {
		t.Errorf("tracking omitted = %q, want the failing call's reason", res.Tracking.Omitted)
	}
	if !strings.Contains(res.Promises.Omitted, "list_promises_by_order") {
		t.Errorf("promises omitted = %q, want the failing call's reason", res.Promises.Omitted)
	}
	if res.Order.Omitted != "" {
		t.Errorf("the healthy sections must still report: order omitted %q", res.Order.Omitted)
	}
}

// TestNetworkOrderOutlook_EveryAttemptedSectionFailedIsAnError is the
// all-or-nothing edge: the outlook is never an all-clear when everything
// it tried failed.
func TestNetworkOrderOutlook_EveryAttemptedSectionFailedIsAnError(t *testing.T) {
	uc, orders := wiredOrderFixture()
	orders.orderErr = errors.New("down")
	orders.trackErr = errors.New("down")
	orders.shipToErr = errors.New("down")
	uc.Promises = &fakeOrderPromiseReader{listErr: errors.New("down")}
	uc.Sourcing = &fakeOrderAssignmentReader{err: errors.New("down")}

	if _, err := uc.Execute(context.Background(), "ord-1"); err == nil {
		t.Fatal("every attempted section failed: Execute must return an error, not an empty all-clear")
	}
}

func TestNetworkOrderOutlook_PromisesFallBackPerGroup(t *testing.T) {
	uc, _ := wiredOrderFixture()
	uc.Promises = &fakeOrderPromiseReader{
		listErr: errors.New("list broken"),
		byID: map[string]ports.DeliveryPromise{
			"prm-1": {PromiseID: "prm-1", State: "CONFIRMED"},
		},
	}
	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Promises.Omitted != "" {
		t.Fatalf("promises omitted = %q, want the per-group fallback to report", res.Promises.Omitted)
	}
	if len(res.Promises.Items) != 1 || res.Promises.Items[0].PromiseID != "prm-1" {
		t.Fatalf("promises items = %+v", res.Promises.Items)
	}
	if res.Promises.Complete {
		t.Fatal("a fallback promise list is a lower bound: Complete must be false")
	}
}

func TestNetworkOrderOutlook_UncoveredGroupPromiseIsFetched(t *testing.T) {
	uc, orders := wiredOrderFixture()
	orders.order.Groups = []ports.CustomerOrderGroup{
		{GroupNo: 1, PromiseID: "prm-1"},
		{GroupNo: 2, PromiseID: "prm-2"},
	}
	uc.Promises = &fakeOrderPromiseReader{
		listed: ports.PromisesByOrder{Items: []ports.DeliveryPromise{{PromiseID: "prm-1", State: "CONFIRMED"}}},
		byID:   map[string]ports.DeliveryPromise{"prm-2": {PromiseID: "prm-2", State: "PROVISIONAL"}},
	}
	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Promises.Items) != 2 {
		t.Fatalf("promises items = %+v, want the uncovered group's promise fetched by id", res.Promises.Items)
	}
}

func TestNetworkOrderOutlook_JourneyFailureKeepsItsSlot(t *testing.T) {
	uc, _ := wiredOrderFixture()
	uc.Journeys = &fakePackageJourneyReader{err: errors.New("shipping-and-tracking unreachable")}
	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Journeys.Items) != 1 || res.Journeys.Items[0].Error == "" {
		t.Fatalf("journeys items = %+v, want RN001 kept with its error", res.Journeys.Items)
	}
	if res.Journeys.Omitted != "" {
		t.Fatalf("the section itself is not omitted for one failed journey: %q", res.Journeys.Omitted)
	}
}

func TestNetworkOrderOutlook_NoReadableTrackingSourceOmitsJourneys(t *testing.T) {
	uc, orders := wiredOrderFixture()
	orders.trackErr = errors.New("down")
	uc.Sourcing = &fakeOrderAssignmentReader{err: errors.New("down")}
	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Journeys.Omitted, "no tracking numbers could be read") {
		t.Fatalf("journeys omitted = %q", res.Journeys.Omitted)
	}
}

func TestNetworkOrderOutlook_NothingLabelledIsTheHonestEmpty(t *testing.T) {
	uc, orders := wiredOrderFixture()
	orders.tracking.Groups[0].Packages = nil
	uc.Sourcing = &fakeOrderAssignmentReader{}
	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Journeys.Omitted != "" || len(res.Journeys.Items) != 0 || !res.Journeys.Complete {
		t.Fatalf("journeys section = %+v, want the honest empty list", res.Journeys)
	}
}

func TestNetworkOrderOutlook_JourneyReadsAreCapped(t *testing.T) {
	uc, orders := wiredOrderFixture()
	packages := make([]ports.TrackedPackage, networkMaxJourneys+5)
	for i := range packages {
		packages[i] = ports.TrackedPackage{TrackingNumber: "RN" + string(rune('A'+i))}
	}
	orders.tracking.Groups[0].Packages = packages
	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Journeys.Items) != networkMaxJourneys {
		t.Fatalf("journeys items = %d, want the cap %d", len(res.Journeys.Items), networkMaxJourneys)
	}
	if res.Journeys.Complete {
		t.Fatal("a capped journeys list is a lower bound: Complete must be false")
	}
}

func TestNetworkOrderOutlook_InputValidation(t *testing.T) {
	uc, _ := wiredOrderFixture()
	if _, err := uc.Execute(context.Background(), ""); err == nil {
		t.Fatal("an empty order id must be rejected")
	}
	var nilUC *NetworkOrderOutlook
	if _, err := nilUC.Execute(context.Background(), "ord-1"); !errors.Is(err, ErrCustomerOrderingNotConfigured) {
		t.Fatalf("nil use case error = %v, want ErrCustomerOrderingNotConfigured", err)
	}
	if _, err := (&NetworkOrderOutlook{}).Execute(context.Background(), "ord-1"); !errors.Is(err, ErrCustomerOrderingNotConfigured) {
		t.Fatalf("no anchor error = %v, want ErrCustomerOrderingNotConfigured", err)
	}
}

func TestNetworkOrderOutlook_NowDefaultsToTimeNow(t *testing.T) {
	uc, _ := wiredOrderFixture()
	before := time.Now()
	res, err := uc.Execute(context.Background(), "ord-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.GeneratedAt.Before(before) {
		t.Fatalf("GeneratedAt = %v, want the clock", res.GeneratedAt)
	}
}
