package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakeInbound serves scripted pages and records every query. A page is
// looked up by cursor ("" = first page) per method/state.
type fakeInbound struct {
	asnPages   map[string]ports.InboundAsnPage
	apptPages  map[string]ports.InboundAppointmentPage
	openPages  map[string]ports.InboundReceiptPage
	closedPage map[string]ports.InboundReceiptPage

	asnErr, apptErr, openErr, closedErr error

	asnQueries   []ports.InboundAsnListQuery
	apptQueries  []ports.InboundAppointmentListQuery
	rcptQueries  []ports.InboundReceiptListQuery
	nonListCalls int
}

func (f *fakeInbound) ListAsns(_ context.Context, q ports.InboundAsnListQuery) (ports.InboundAsnPage, error) {
	f.asnQueries = append(f.asnQueries, q)
	return f.asnPages[q.Cursor], f.asnErr
}

func (f *fakeInbound) ListAppointments(_ context.Context, q ports.InboundAppointmentListQuery) (ports.InboundAppointmentPage, error) {
	f.apptQueries = append(f.apptQueries, q)
	return f.apptPages[q.Cursor], f.apptErr
}

func (f *fakeInbound) ListReceipts(_ context.Context, q ports.InboundReceiptListQuery) (ports.InboundReceiptPage, error) {
	f.rcptQueries = append(f.rcptQueries, q)
	if q.State == "Open" {
		return f.openPages[q.Cursor], f.openErr
	}
	return f.closedPage[q.Cursor], f.closedErr
}

func (f *fakeInbound) GetAsn(context.Context, string) (ports.InboundAsn, error) {
	f.nonListCalls++
	return ports.InboundAsn{}, errors.New("not used")
}

func (f *fakeInbound) GetAppointment(context.Context, string) (ports.InboundAppointment, error) {
	f.nonListCalls++
	return ports.InboundAppointment{}, errors.New("not used")
}

func (f *fakeInbound) GetReceipt(context.Context, string) (ports.InboundReceipt, error) {
	f.nonListCalls++
	return ports.InboundReceipt{}, errors.New("not used")
}

func (f *fakeInbound) ListDocks(context.Context) (ports.InboundDockList, error) {
	f.nonListCalls++
	return ports.InboundDockList{}, errors.New("not used")
}

var inboundOutlookNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func inboundOutlookUC(f *fakeInbound, staleAge time.Duration) *InboundOutlook {
	return &InboundOutlook{Inbound: f, StaleReceiptAge: staleAge, Now: func() time.Time { return inboundOutlookNow }}
}

func at(h int) string {
	return inboundOutlookNow.Add(time.Duration(h) * time.Hour).Format(time.RFC3339)
}

func healthyInbound() *fakeInbound {
	return &fakeInbound{
		asnPages: map[string]ports.InboundAsnPage{"": {Items: []ports.InboundAsn{
			{AsnNumber: "ASN-2", State: "Registered", ExpectedArrival: at(3)},
			{AsnNumber: "ASN-1", State: "Registered", ExpectedArrival: at(-2)},
			{AsnNumber: "ASN-3", State: "Registered"},
		}}},
		apptPages: map[string]ports.InboundAppointmentPage{"": {Items: []ports.InboundAppointment{
			{AppointmentID: "ap-2", DoorCode: "D2", State: "Booked", WindowStart: at(5), WindowEnd: at(6)},
			{AppointmentID: "ap-1", DoorCode: "D1", State: "CheckedIn", WindowStart: at(-1), WindowEnd: at(1)},
			{AppointmentID: "ap-x", DoorCode: "D1", State: "Cancelled", WindowStart: at(2), WindowEnd: at(3)},
		}}},
		openPages: map[string]ports.InboundReceiptPage{"": {Items: []ports.InboundReceipt{
			{ReceiptID: "r-old", State: "Open", OpenedAt: at(-9)},
			{ReceiptID: "r-new", State: "Open", OpenedAt: at(-1)},
		}}},
		closedPage: map[string]ports.InboundReceiptPage{"": {Items: []ports.InboundReceipt{
			{ReceiptID: "r-disc", AsnNumber: "ASN-9", State: "Closed", OpenedAt: at(-8), ClosedAt: at(-1),
				Discrepancies: []ports.InboundDiscrepancy{{LineNo: 1, Sku: "S", Kind: "Short", ExpectedQty: 10, ReceivedQty: 8}}},
			{ReceiptID: "r-clean", State: "Closed", OpenedAt: at(-8), ClosedAt: at(-1)},
			{ReceiptID: "r-yday", State: "Closed", OpenedAt: at(-40), ClosedAt: at(-30),
				Discrepancies: []ports.InboundDiscrepancy{{LineNo: 1, Sku: "S", Kind: "Over"}}},
		}}},
	}
}

func TestInboundOutlook_ReportsAllFourSections(t *testing.T) {
	f := healthyInbound()
	res, err := inboundOutlookUC(f, 4*time.Hour).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.GeneratedAt.Equal(inboundOutlookNow) || !res.AppointmentsUntil.Equal(inboundOutlookNow.Add(24*time.Hour)) || res.StaleAfter != 4*time.Hour {
		t.Fatalf("echoed inputs wrong: %+v", res)
	}
	if !res.DayFrom.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) || !res.DayTo.Equal(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("day window = [%v, %v)", res.DayFrom, res.DayTo)
	}

	assertAwaitingAsns(t, res.AwaitingAsns)
	assertUpcomingAppointments(t, res.UpcomingAppointments)
	assertStaleReceipts(t, res.StaleReceipts)
	assertDiscrepantReceipts(t, res.DiscrepantReceipts)
	if f.nonListCalls != 0 {
		t.Errorf("the outlook must only use list tools, got %d other calls", f.nonListCalls)
	}
}

func assertAwaitingAsns(t *testing.T, s InboundAwaitingAsns) {
	t.Helper()
	a := s.Items
	if s.Omitted != "" || !s.Complete || len(a) != 3 || a[0].AsnNumber != "ASN-1" || !a[0].Overdue || a[1].AsnNumber != "ASN-2" || a[2].AsnNumber != "ASN-3" {
		t.Errorf("awaiting ASNs: %+v", s)
	}
}

func assertUpcomingAppointments(t *testing.T, s InboundUpcomingAppointments) {
	t.Helper()
	ap := s.Items
	if s.Omitted != "" || len(ap) != 2 || ap[0].AppointmentID != "ap-1" || ap[1].AppointmentID != "ap-2" {
		t.Errorf("upcoming appointments: %+v", s)
	}
}

func assertStaleReceipts(t *testing.T, s InboundStaleReceipts) {
	t.Helper()
	st := s.Items
	if s.Omitted != "" || len(st) != 1 || st[0].ReceiptID != "r-old" || st[0].OpenFor != 9*time.Hour {
		t.Errorf("stale receipts: %+v", s)
	}
}

func assertDiscrepantReceipts(t *testing.T, s InboundDiscrepantReceipts) {
	t.Helper()
	dr := s.Items
	if s.Omitted != "" || len(dr) != 1 || dr[0].ReceiptID != "r-disc" || dr[0].Discrepancies[0].Kind != "Short" || dr[0].Discrepancies[0].ExpectedQty != 10 {
		t.Errorf("discrepant receipts: %+v", s)
	}
}

func TestInboundOutlook_QueriesCarryOnlyDerivedInputs(t *testing.T) {
	f := healthyInbound()
	if _, err := inboundOutlookUC(f, time.Hour).Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.asnQueries) != 1 || f.asnQueries[0].State != "Registered" || f.asnQueries[0].Limit != 500 {
		t.Errorf("ASN query: %+v", f.asnQueries)
	}
	if len(f.apptQueries) != 1 || !f.apptQueries[0].From.Equal(inboundOutlookNow) || !f.apptQueries[0].To.Equal(inboundOutlookNow.Add(24*time.Hour)) ||
		f.apptQueries[0].State != "" || f.apptQueries[0].Door != "" {
		t.Errorf("appointment query must window on [now, now+24h) only: %+v", f.apptQueries)
	}
	if len(f.rcptQueries) != 2 || f.rcptQueries[0].State != "Open" || f.rcptQueries[1].State != "Closed" || f.rcptQueries[0].AsnNumber != "" {
		t.Errorf("receipt queries: %+v", f.rcptQueries)
	}
}

func TestInboundOutlook_StaleAgeUnsetOmitsSectionWithoutCalling(t *testing.T) {
	f := healthyInbound()
	res, err := inboundOutlookUC(f, 0).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.StaleReceipts.Omitted, "INBOUND_STALE_RECEIPT_AGE") || res.StaleReceipts.Items == nil || len(res.StaleReceipts.Items) != 0 {
		t.Errorf("an unset age must omit the section with the reason: %+v", res.StaleReceipts)
	}
	for _, q := range f.rcptQueries {
		if q.State == "Open" {
			t.Errorf("no Open receipts must be listed without a configured age: %+v", f.rcptQueries)
		}
	}
	if res.AwaitingAsns.Omitted != "" || res.DiscrepantReceipts.Omitted != "" {
		t.Errorf("the other sections are unaffected: %+v", res)
	}
}

func TestInboundOutlook_OneFailingSectionIsOmittedOthersSurvive(t *testing.T) {
	f := healthyInbound()
	f.apptErr = errors.New("boom")
	res, err := inboundOutlookUC(f, 4*time.Hour).Execute(context.Background())
	if err != nil {
		t.Fatalf("a single failing section must not fail the outlook: %v", err)
	}
	if !strings.Contains(res.UpcomingAppointments.Omitted, "boom") || !strings.Contains(res.UpcomingAppointments.Omitted, "list_appointments") || len(res.UpcomingAppointments.Items) != 0 || res.UpcomingAppointments.Items == nil {
		t.Errorf("failed section: %+v", res.UpcomingAppointments)
	}
	if res.UpcomingAppointments.Complete {
		t.Error("an omitted section is not complete")
	}
	if len(res.AwaitingAsns.Items) != 3 || len(res.StaleReceipts.Items) != 1 || len(res.DiscrepantReceipts.Items) != 1 {
		t.Errorf("the other sections must still report: %+v", res)
	}
}

func TestInboundOutlook_EverySectionFailingIsAnError(t *testing.T) {
	f := healthyInbound()
	boom := errors.New("unreachable")
	f.asnErr, f.apptErr, f.openErr, f.closedErr = boom, boom, boom, boom
	_, err := inboundOutlookUC(f, 4*time.Hour).Execute(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("want the upstream error, got %v", err)
	}
}

func TestInboundOutlook_StaleUnsetAndRemainingSectionsFailingIsAnError(t *testing.T) {
	f := healthyInbound()
	boom := errors.New("unreachable")
	f.asnErr, f.apptErr, f.closedErr = boom, boom, boom
	if _, err := inboundOutlookUC(f, 0).Execute(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("with the stale section not configured, three failed of three attempted is an error, got %v", err)
	}
}

func TestInboundOutlook_FollowsCursorsAndFlagsAnIncompleteScan(t *testing.T) {
	f := healthyInbound()
	f.asnPages = map[string]ports.InboundAsnPage{
		"":   {Items: []ports.InboundAsn{{AsnNumber: "ASN-1", State: "Registered"}}, NextCursor: "p2"},
		"p2": {Items: []ports.InboundAsn{{AsnNumber: "ASN-2", State: "Registered"}}},
	}
	// Receipts: a cursor that never ends -> bounded at inboundMaxPages.
	endless := &endlessReceipts{fakeInbound: f}
	uc := &InboundOutlook{Inbound: endless, StaleReceiptAge: time.Hour, Now: func() time.Time { return inboundOutlookNow }}
	res, err := uc.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.AwaitingAsns.Items) != 2 || !res.AwaitingAsns.Complete {
		t.Errorf("both ASN pages must be read: %+v", res.AwaitingAsns)
	}
	if len(f.asnQueries) != 2 || f.asnQueries[1].Cursor != "p2" {
		t.Errorf("ASN cursor not followed: %+v", f.asnQueries)
	}
	if res.DiscrepantReceipts.Complete || res.StaleReceipts.Complete {
		t.Errorf("a scan stopped by the page bound must say it is incomplete: %+v / %+v", res.StaleReceipts, res.DiscrepantReceipts)
	}
	if endless.closedCalls != inboundMaxPages || endless.openCalls != inboundMaxPages {
		t.Errorf("page bound = %d, got open=%d closed=%d calls", inboundMaxPages, endless.openCalls, endless.closedCalls)
	}
}

// endlessReceipts always returns a next cursor for list_receipts.
type endlessReceipts struct {
	*fakeInbound
	openCalls, closedCalls int
}

func (e *endlessReceipts) ListReceipts(_ context.Context, q ports.InboundReceiptListQuery) (ports.InboundReceiptPage, error) {
	if q.State == "Open" {
		e.openCalls++
	} else {
		e.closedCalls++
	}
	return ports.InboundReceiptPage{NextCursor: q.Cursor + "x"}, nil
}

func TestInboundOutlook_ContractViolationsOmitTheSection(t *testing.T) {
	cases := map[string]func(f *fakeInbound){
		"unparseable expected_arrival": func(f *fakeInbound) {
			f.asnPages[""] = ports.InboundAsnPage{Items: []ports.InboundAsn{{AsnNumber: "A", State: "Registered", ExpectedArrival: "tomorrow"}}}
		},
		"missing window_start": func(f *fakeInbound) {
			f.apptPages[""] = ports.InboundAppointmentPage{Items: []ports.InboundAppointment{{AppointmentID: "p", State: "Booked", WindowEnd: at(1)}}}
		},
		"unparseable window_end": func(f *fakeInbound) {
			f.apptPages[""] = ports.InboundAppointmentPage{Items: []ports.InboundAppointment{{AppointmentID: "p", State: "Booked", WindowStart: at(1), WindowEnd: "later"}}}
		},
		"missing opened_at": func(f *fakeInbound) {
			f.openPages[""] = ports.InboundReceiptPage{Items: []ports.InboundReceipt{{ReceiptID: "r", State: "Open"}}}
		},
		"closed receipt without closed_at": func(f *fakeInbound) {
			f.closedPage[""] = ports.InboundReceiptPage{Items: []ports.InboundReceipt{{ReceiptID: "r", State: "Closed", OpenedAt: at(-1)}}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := healthyInbound()
			mutate(f)
			res, err := inboundOutlookUC(f, 4*time.Hour).Execute(context.Background())
			if err != nil {
				t.Fatalf("one bad section must not fail the outlook: %v", err)
			}
			omitted := res.AwaitingAsns.Omitted + res.UpcomingAppointments.Omitted + res.StaleReceipts.Omitted + res.DiscrepantReceipts.Omitted
			if omitted == "" {
				t.Fatalf("a published fact that breaks the contract must omit its section: %+v", res)
			}
		})
	}
}

func TestInboundOutlook_NotConfigured(t *testing.T) {
	var nilUC *InboundOutlook
	if _, err := nilUC.Execute(context.Background()); !errors.Is(err, ErrInboundReceivingNotConfigured) {
		t.Errorf("nil use case: %v", err)
	}
	if _, err := (&InboundOutlook{}).Execute(context.Background()); !errors.Is(err, ErrInboundReceivingNotConfigured) {
		t.Errorf("nil port: %v", err)
	}
}

func TestInboundOutlook_DefaultClockIsNow(t *testing.T) {
	f := &fakeInbound{}
	before := time.Now()
	res, err := (&InboundOutlook{Inbound: f}).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.GeneratedAt.Before(before) || res.GeneratedAt.After(time.Now()) {
		t.Errorf("GeneratedAt = %v, want about now", res.GeneratedAt)
	}
}

func TestInboundOutlook_TodayIsTheCallersLocalDay(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	now := time.Date(2026, 10, 8, 22, 0, 0, 0, brt) // already Oct 9 in UTC
	f := healthyInbound()
	f.closedPage[""] = ports.InboundReceiptPage{Items: []ports.InboundReceipt{
		{ReceiptID: "local-today", State: "Closed", OpenedAt: "2026-10-08T05:00:00-03:00", ClosedAt: "2026-10-08T10:00:00-03:00",
			Discrepancies: []ports.InboundDiscrepancy{{LineNo: 1, Sku: "S", Kind: "Short"}}},
		{ReceiptID: "local-tomorrow", State: "Closed", OpenedAt: "2026-10-08T05:00:00-03:00", ClosedAt: "2026-10-09T00:30:00-03:00",
			Discrepancies: []ports.InboundDiscrepancy{{LineNo: 1, Sku: "S", Kind: "Short"}}},
	}}
	uc := &InboundOutlook{Inbound: f, Now: func() time.Time { return now }}
	res, err := uc.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DiscrepantReceipts.Items) != 1 || res.DiscrepantReceipts.Items[0].ReceiptID != "local-today" {
		t.Errorf("today must follow the clock's own calendar day: %+v", res.DiscrepantReceipts.Items)
	}
}
