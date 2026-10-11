package policy

import (
	"reflect"
	"testing"
	"time"
)

var inboundNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func TestAsnsAwaitingArrival_OnlyRegisteredEarliestFirstNoArrivalLast(t *testing.T) {
	asns := []InboundAsnFact{
		{AsnNumber: "ASN-4", State: "Registered"}, // no expected arrival
		{AsnNumber: "ASN-3", State: "Receiving", ExpectedArrival: inboundNow.Add(-time.Hour)},
		{AsnNumber: "ASN-2", State: "Registered", ExpectedArrival: inboundNow.Add(2 * time.Hour)},
		{AsnNumber: "ASN-1", State: "Registered", ExpectedArrival: inboundNow.Add(-3 * time.Hour)},
		{AsnNumber: "ASN-0", State: "Registered", ExpectedArrival: inboundNow.Add(2 * time.Hour)},
		{AsnNumber: "ASN-5", State: "Closed"},
		{AsnNumber: "ASN-6", State: "Cancelled"},
	}
	got := AsnsAwaitingArrival(asns, inboundNow)
	var order []string
	for _, a := range got {
		order = append(order, a.AsnNumber)
	}
	if want := []string{"ASN-1", "ASN-0", "ASN-2", "ASN-4"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	overdue := map[string]bool{}
	for _, a := range got {
		overdue[a.AsnNumber] = a.Overdue
	}
	if !overdue["ASN-1"] || overdue["ASN-2"] || overdue["ASN-4"] {
		t.Fatalf("overdue only when an expected arrival exists and is past: %v", overdue)
	}
}

func TestAsnsAwaitingArrival_ArrivalExactlyNowIsNotOverdueAndEmptyIsNonNil(t *testing.T) {
	got := AsnsAwaitingArrival([]InboundAsnFact{{AsnNumber: "A", State: "Registered", ExpectedArrival: inboundNow}}, inboundNow)
	if len(got) != 1 || got[0].Overdue {
		t.Fatalf("an ASN expected exactly now is not overdue: %+v", got)
	}
	if empty := AsnsAwaitingArrival(nil, inboundNow); empty == nil || len(empty) != 0 {
		t.Fatalf("no ASNs must give an empty, non-nil list, got %#v", empty)
	}
}

func TestAppointmentsInHorizon_WindowOverlapStatesAndOrder(t *testing.T) {
	h := func(n int) time.Time { return inboundNow.Add(time.Duration(n) * time.Hour) }
	appts := []InboundAppointmentFact{
		{AppointmentID: "ended", State: "Booked", WindowStart: h(-3), WindowEnd: h(-1)},
		{AppointmentID: "endsNow", State: "Booked", WindowStart: h(-2), WindowEnd: h(0)},
		{AppointmentID: "inProgress", State: "CheckedIn", WindowStart: h(-1), WindowEnd: h(1)},
		{AppointmentID: "b-later", State: "Booked", WindowStart: h(5), WindowEnd: h(6)},
		{AppointmentID: "a-later", State: "Booked", WindowStart: h(5), WindowEnd: h(7)},
		{AppointmentID: "startsAtHorizon", State: "Booked", WindowStart: h(24), WindowEnd: h(25)},
		{AppointmentID: "lastStart", State: "Booked", WindowStart: h(24).Add(-time.Minute), WindowEnd: h(25)},
		{AppointmentID: "cancelled", State: "Cancelled", WindowStart: h(2), WindowEnd: h(3)},
		{AppointmentID: "completed", State: "Completed", WindowStart: h(2), WindowEnd: h(3)},
	}
	var order []string
	for _, a := range AppointmentsInHorizon(appts, inboundNow) {
		order = append(order, a.AppointmentID)
	}
	if want := []string{"inProgress", "a-later", "b-later", "lastStart"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("appointments = %v, want %v", order, want)
	}
	if empty := AppointmentsInHorizon(nil, inboundNow); empty == nil || len(empty) != 0 {
		t.Fatalf("no appointments must give an empty, non-nil list, got %#v", empty)
	}
}

func TestStaleOpenReceipts_StrictlyOlderThanAgeOldestFirst(t *testing.T) {
	age := 4 * time.Hour
	receipts := []InboundReceiptFact{
		{ReceiptID: "exactly", State: "Open", OpenedAt: inboundNow.Add(-age)},
		{ReceiptID: "b-old", State: "Open", OpenedAt: inboundNow.Add(-6 * time.Hour)},
		{ReceiptID: "oldest", State: "Open", OpenedAt: inboundNow.Add(-9 * time.Hour)},
		{ReceiptID: "a-old", State: "Open", OpenedAt: inboundNow.Add(-6 * time.Hour)},
		{ReceiptID: "fresh", State: "Open", OpenedAt: inboundNow.Add(-time.Hour)},
		{ReceiptID: "closed", State: "Closed", OpenedAt: inboundNow.Add(-20 * time.Hour)},
		{ReceiptID: "noOpenedAt", State: "Open"},
	}
	got := StaleOpenReceipts(receipts, inboundNow, age)
	var order []string
	for _, r := range got {
		order = append(order, r.ReceiptID)
	}
	if want := []string{"oldest", "a-old", "b-old"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("stale = %v, want %v", order, want)
	}
	if got[0].OpenFor != 9*time.Hour {
		t.Fatalf("OpenFor = %v, want 9h", got[0].OpenFor)
	}
}

func TestStaleOpenReceipts_NonPositiveAgeIsNoThreshold(t *testing.T) {
	r := []InboundReceiptFact{{ReceiptID: "r", State: "Open", OpenedAt: inboundNow.Add(-100 * time.Hour)}}
	for _, age := range []time.Duration{0, -time.Hour} {
		if got := StaleOpenReceipts(r, inboundNow, age); got == nil || len(got) != 0 {
			t.Fatalf("age %v must flag nothing (and be non-nil), got %#v", age, got)
		}
	}
}

func TestDayBounds_UsesTheInstantsOwnLocation(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	from, to := DayBounds(time.Date(2026, 10, 8, 23, 30, 0, 0, brt))
	if !from.Equal(time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("day = [%v, %v)", from, to)
	}
}

func TestClosedWithDiscrepancies_TodayOnlyAndOnlyDiscrepant(t *testing.T) {
	from, to := DayBounds(inboundNow)
	d := []InboundDiscrepancyFact{{LineNo: 1, Sku: "S", Kind: "Short"}}
	receipts := []InboundReceiptFact{
		{ReceiptID: "late", State: "Closed", ClosedAt: from.Add(20 * time.Hour), Discrepancies: d},
		{ReceiptID: "b-early", State: "Closed", ClosedAt: from.Add(time.Hour), Discrepancies: d},
		{ReceiptID: "a-early", State: "Closed", ClosedAt: from.Add(time.Hour), Discrepancies: d},
		{ReceiptID: "startOfDay", State: "Closed", ClosedAt: from, Discrepancies: d},
		{ReceiptID: "yesterday", State: "Closed", ClosedAt: from.Add(-time.Second), Discrepancies: d},
		{ReceiptID: "tomorrow", State: "Closed", ClosedAt: to, Discrepancies: d},
		{ReceiptID: "clean", State: "Closed", ClosedAt: from.Add(2 * time.Hour)},
		{ReceiptID: "openWithDiscrepancy", State: "Open", ClosedAt: from.Add(2 * time.Hour), Discrepancies: d},
	}
	var order []string
	for _, r := range ClosedWithDiscrepancies(receipts, from, to) {
		order = append(order, r.ReceiptID)
	}
	if want := []string{"startOfDay", "a-early", "b-early", "late"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("closed-with-discrepancies = %v, want %v", order, want)
	}
	if empty := ClosedWithDiscrepancies(nil, from, to); empty == nil || len(empty) != 0 {
		t.Fatalf("no receipts must give an empty, non-nil list, got %#v", empty)
	}
}
