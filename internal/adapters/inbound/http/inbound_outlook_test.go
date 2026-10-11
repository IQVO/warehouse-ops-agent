package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakeInbound answers the three list tools the outlook uses; the other four
// published tools are never called by it.
type fakeInbound struct {
	asns  ports.InboundAsnPage
	appts ports.InboundAppointmentPage
	open  ports.InboundReceiptPage
	done  ports.InboundReceiptPage
	err   error
}

func (f *fakeInbound) ListAsns(context.Context, ports.InboundAsnListQuery) (ports.InboundAsnPage, error) {
	return f.asns, f.err
}

func (f *fakeInbound) ListAppointments(context.Context, ports.InboundAppointmentListQuery) (ports.InboundAppointmentPage, error) {
	return f.appts, f.err
}

func (f *fakeInbound) ListReceipts(_ context.Context, q ports.InboundReceiptListQuery) (ports.InboundReceiptPage, error) {
	if q.State == "Open" {
		return f.open, f.err
	}
	return f.done, f.err
}

func (f *fakeInbound) GetAsn(context.Context, string) (ports.InboundAsn, error) {
	return ports.InboundAsn{}, errors.New("not used")
}

func (f *fakeInbound) GetAppointment(context.Context, string) (ports.InboundAppointment, error) {
	return ports.InboundAppointment{}, errors.New("not used")
}

func (f *fakeInbound) GetReceipt(context.Context, string) (ports.InboundReceipt, error) {
	return ports.InboundReceipt{}, errors.New("not used")
}

func (f *fakeInbound) ListDocks(context.Context) (ports.InboundDockList, error) {
	return ports.InboundDockList{}, errors.New("not used")
}

var outlookNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func hrs(n int) string { return outlookNow.Add(time.Duration(n) * time.Hour).Format(time.RFC3339) }

func healthyOutlookInbound() *fakeInbound {
	return &fakeInbound{
		asns: ports.InboundAsnPage{Items: []ports.InboundAsn{
			{AsnNumber: "ASN-1", SupplierRef: "SUP-1", State: "Registered", ExpectedArrival: hrs(-2)},
			{AsnNumber: "ASN-2", SupplierRef: "SUP-2", State: "Registered"},
		}},
		appts: ports.InboundAppointmentPage{Items: []ports.InboundAppointment{
			{AppointmentID: "ap-1", DoorCode: "D1", Carrier: "ACME", State: "Booked", WindowStart: hrs(2), WindowEnd: hrs(3), AsnNumbers: []string{"ASN-1"}},
		}},
		open: ports.InboundReceiptPage{Items: []ports.InboundReceipt{
			{ReceiptID: "r-old", AsnNumber: "ASN-7", State: "Open", OpenedAt: hrs(-9)},
		}},
		done: ports.InboundReceiptPage{Items: []ports.InboundReceipt{
			{ReceiptID: "r-disc", AsnNumber: "ASN-8", DoorCode: "D2", State: "Closed", OpenedAt: hrs(-6), ClosedAt: hrs(-1),
				Discrepancies: []ports.InboundDiscrepancy{{LineNo: 2, Sku: "SKU-9", Kind: "Short", ExpectedQty: 10, ReceivedQty: 7, DamagedQty: 1}}},
		}},
	}
}

func outlookUC(f *fakeInbound, staleAge time.Duration) *usecases.InboundOutlook {
	return &usecases.InboundOutlook{Inbound: f, StaleReceiptAge: staleAge, Now: func() time.Time { return outlookNow }}
}

func getOutlook(t *testing.T, uc *usecases.InboundOutlook) (int, map[string]any) {
	t.Helper()
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{InboundOutlook: uc}, "warehouse-ops-agent-test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/inbound-outlook", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func section(t *testing.T, body map[string]any, name string) map[string]any {
	t.Helper()
	s, ok := body[name].(map[string]any)
	if !ok {
		t.Fatalf("section %q missing from %v", name, body)
	}
	return s
}

func TestGetInboundOutlook_ReportsAllFourSections(t *testing.T) {
	code, body := getOutlook(t, outlookUC(healthyOutlookInbound(), 4*time.Hour))
	if code != http.StatusOK {
		t.Fatalf("status = %d: %v", code, body)
	}
	if body["generatedAt"] != "2026-10-08T12:00:00Z" || body["appointmentsUntil"] != "2026-10-09T12:00:00Z" || body["staleAfterSeconds"] != float64(4*3600) {
		t.Fatalf("echoed inputs: %v", body)
	}

	assertOutlookAsns(t, section(t, body, "awaitingAsns"))
	assertOutlookAppointment(t, section(t, body, "upcomingAppointments")["items"].([]any)[0].(map[string]any))
	assertOutlookStale(t, section(t, body, "staleReceipts")["items"].([]any)[0].(map[string]any))
	assertOutlookDiscrepant(t, section(t, body, "discrepantReceipts")["items"].([]any)[0].(map[string]any))
}

func assertOutlookAsns(t *testing.T, asns map[string]any) {
	t.Helper()
	items := asns["items"].([]any)
	first := items[0].(map[string]any)
	if asns["count"] != float64(2) || asns["complete"] != true || first["asnNumber"] != "ASN-1" || first["overdue"] != true || first["expectedArrival"] != hrs(-2) {
		t.Errorf("awaitingAsns: %v", asns)
	}
	if _, has := items[1].(map[string]any)["expectedArrival"]; has {
		t.Errorf("an ASN with no expected arrival must omit the field: %v", items[1])
	}
}

func assertOutlookAppointment(t *testing.T, appt map[string]any) {
	t.Helper()
	if appt["appointmentId"] != "ap-1" || appt["doorCode"] != "D1" || appt["windowStart"] != hrs(2) || len(appt["asnNumbers"].([]any)) != 1 {
		t.Errorf("upcomingAppointments: %v", appt)
	}
}

func assertOutlookStale(t *testing.T, stale map[string]any) {
	t.Helper()
	if stale["receiptId"] != "r-old" || stale["openForSeconds"] != float64(9*3600) {
		t.Errorf("staleReceipts: %v", stale)
	}
	if _, has := stale["doorCode"]; has {
		t.Errorf("a walk-in receipt must omit doorCode: %v", stale)
	}
}

func assertOutlookDiscrepant(t *testing.T, disc map[string]any) {
	t.Helper()
	line := disc["discrepancies"].([]any)[0].(map[string]any)
	if disc["receiptId"] != "r-disc" || disc["doorCode"] != "D2" || disc["closedAt"] != hrs(-1) || line["sku"] != "SKU-9" || line["kind"] != "Short" || line["receivedQty"] != float64(7) || line["damagedQty"] != float64(1) {
		t.Errorf("discrepantReceipts: %v", disc)
	}
}

func TestGetInboundOutlook_EmptySectionsAreEmptyArraysAndAnUnsetAgeSaysWhy(t *testing.T) {
	code, body := getOutlook(t, outlookUC(&fakeInbound{}, 0))
	if code != http.StatusOK {
		t.Fatalf("status = %d: %v", code, body)
	}
	for _, name := range []string{"awaitingAsns", "upcomingAppointments", "staleReceipts", "discrepantReceipts"} {
		s := section(t, body, name)
		if s["items"] == nil || len(s["items"].([]any)) != 0 || s["count"] != float64(0) {
			t.Errorf("%s must be an empty array (never null): %v", name, s)
		}
	}
	if _, has := body["staleAfterSeconds"]; has {
		t.Errorf("an unset age must omit staleAfterSeconds: %v", body)
	}
	stale := section(t, body, "staleReceipts")
	if msg, _ := stale["omitted"].(string); !strings.Contains(msg, "INBOUND_STALE_RECEIPT_AGE") || stale["complete"] != false {
		t.Errorf("an unset age must omit the stale section with the reason: %v", stale)
	}
	if _, has := section(t, body, "awaitingAsns")["omitted"]; has {
		t.Error("a healthy section carries no omitted field")
	}
}

func TestGetInboundOutlook_StatusMapping(t *testing.T) {
	cases := []struct {
		name string
		uc   *usecases.InboundOutlook
		want int
	}{
		{"not wired", nil, http.StatusServiceUnavailable},
		{"wired without a port", &usecases.InboundOutlook{}, http.StatusServiceUnavailable},
		{"upstream down", outlookUC(&fakeInbound{err: errors.New("inbound-receiving: connect: refused")}, time.Hour), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getOutlook(t, tc.uc)
			if msg, _ := body["error"].(string); code != tc.want || strings.TrimSpace(msg) == "" {
				t.Fatalf("status = %d (%v), want %d with an error body", code, body, tc.want)
			}
		})
	}
}
