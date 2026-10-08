package mcp_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeInbound struct {
	asns ports.InboundAsnPage
	done ports.InboundReceiptPage
	err  error
}

func (f *fakeInbound) ListAsns(context.Context, ports.InboundAsnListQuery) (ports.InboundAsnPage, error) {
	return f.asns, f.err
}

func (f *fakeInbound) ListAppointments(context.Context, ports.InboundAppointmentListQuery) (ports.InboundAppointmentPage, error) {
	return ports.InboundAppointmentPage{Items: []ports.InboundAppointment{
		{AppointmentID: "ap-1", DoorCode: "D1", Carrier: "ACME", State: "Booked",
			WindowStart: "2026-10-08T14:00:00Z", WindowEnd: "2026-10-08T15:00:00Z"},
	}}, f.err
}

func (f *fakeInbound) ListReceipts(_ context.Context, q ports.InboundReceiptListQuery) (ports.InboundReceiptPage, error) {
	if q.State == "Open" {
		return ports.InboundReceiptPage{}, f.err
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

func newServerWithInboundOutlook(t *testing.T, f *fakeInbound, staleAge time.Duration) string {
	t.Helper()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	server := inboundmcp.NewServer(inboundmcp.Deps{
		DailyBrief:     &usecases.DailyBrief{},
		InboundOutlook: &usecases.InboundOutlook{Inbound: f, StaleReceiptAge: staleAge, Now: func() time.Time { return now }},
	})
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

func healthyInboundForMCP() *fakeInbound {
	return &fakeInbound{
		asns: ports.InboundAsnPage{Items: []ports.InboundAsn{{AsnNumber: "ASN-1", SupplierRef: "SUP", State: "Registered", ExpectedArrival: "2026-10-08T10:00:00Z"}}},
		done: ports.InboundReceiptPage{Items: []ports.InboundReceipt{
			{ReceiptID: "r-1", AsnNumber: "ASN-2", State: "Closed", OpenedAt: "2026-10-08T06:00:00Z", ClosedAt: "2026-10-08T09:00:00Z",
				Discrepancies: []ports.InboundDiscrepancy{{LineNo: 1, Sku: "SKU-1", Kind: "Over", ExpectedQty: 5, ReceivedQty: 6}}},
		}},
	}
}

func TestServer_GetInboundOutlook_OnlyAdvertisedWhenWiredAndReadOnly(t *testing.T) {
	if advertised(t, newServer(t), "get_inbound_outlook") != nil {
		t.Fatal("get_inbound_outlook must not be advertised when inbound-receiving is not configured")
	}
	tool := advertised(t, newServerWithInboundOutlook(t, healthyInboundForMCP(), time.Hour), "get_inbound_outlook")
	if tool == nil {
		t.Fatal("get_inbound_outlook not advertised when InboundOutlook is wired")
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("get_inbound_outlook must be read-only: %+v", tool.Annotations)
	}
}

func TestServer_GetInboundOutlook_OverTheWire(t *testing.T) {
	session := connect(t, newServerWithInboundOutlook(t, healthyInboundForMCP(), time.Hour))
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "get_inbound_outlook", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	if out["generatedAt"] != "2026-10-08T12:00:00Z" || out["appointmentsUntil"] != "2026-10-09T12:00:00Z" || out["staleAfterSeconds"] != float64(3600) {
		t.Fatalf("echoed inputs: %v", out)
	}

	assertWireSections(t, out)
}

func assertWireSections(t *testing.T, out map[string]any) {
	t.Helper()
	asns := out["awaitingAsns"].(map[string]any)
	asn := asns["items"].([]any)[0].(map[string]any)
	if asns["count"] != float64(1) || asn["asnNumber"] != "ASN-1" || asn["overdue"] != true {
		t.Errorf("awaitingAsns: %v", asns)
	}
	appt := out["upcomingAppointments"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if appt["appointmentId"] != "ap-1" || len(appt["asnNumbers"].([]any)) != 0 {
		t.Errorf("upcomingAppointments: %v", appt)
	}
	stale := out["staleReceipts"].(map[string]any)
	if stale["count"] != float64(0) || stale["complete"] != true {
		t.Errorf("staleReceipts: %v", stale)
	}
	disc := out["discrepantReceipts"].(map[string]any)["items"].([]any)[0].(map[string]any)
	line := disc["discrepancies"].([]any)[0].(map[string]any)
	if disc["receiptId"] != "r-1" || line["kind"] != "Over" || line["receivedQty"] != float64(6) {
		t.Errorf("discrepantReceipts: %v", disc)
	}
}

func TestServer_GetInboundOutlook_UnsetAgeOmitsTheStaleSectionWithAReason(t *testing.T) {
	session := connect(t, newServerWithInboundOutlook(t, healthyInboundForMCP(), 0))
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "get_inbound_outlook", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("call tool: %v %+v", err, res)
	}
	out := res.StructuredContent.(map[string]any)
	stale := out["staleReceipts"].(map[string]any)
	if msg, _ := stale["omitted"].(string); msg == "" {
		t.Errorf("an unset age must say why the section is omitted: %v", stale)
	}
	if _, has := out["staleAfterSeconds"]; has {
		t.Errorf("an unset age must omit staleAfterSeconds: %v", out)
	}
}

func TestServer_GetInboundOutlook_UpstreamDownIsAToolError(t *testing.T) {
	f := healthyInboundForMCP()
	f.err = errors.New("inbound-receiving unreachable")
	session := connect(t, newServerWithInboundOutlook(t, f, time.Hour))
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "get_inbound_outlook", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call tool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("every section failing must be a tool error, never an all-clear")
	}
}
