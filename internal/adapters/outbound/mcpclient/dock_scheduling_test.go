package mcpclient

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// dockSchedulingGoldenPath is a VERBATIM copy of dock-scheduling's own
// published tool registry golden
// (dock-scheduling: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at dock-scheduling origin/develop eecbee17). Refresh it with
//
//	git -C ../dock-scheduling show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever dock-scheduling amends its tool surface; the tests below then say
// exactly which consumer-side assumption broke.
const dockSchedulingGoldenPath = "testdata/dock_scheduling_tools.golden.json"

// dockSchedulingWriteTools is every L3 write tool dock-scheduling publishes.
// The client wraps none of them (ADR 0032, D-Y4); they are pinned here so a
// new tool upstream cannot slip in unclassified.
var dockSchedulingWriteTools = []string{
	"book_appointment", "cancel_appointment", "register_carrier", "reinstate_carrier",
	"report_eta", "reschedule_appointment", "suspend_carrier",
}

// --- the published contract ------------------------------------------------

func TestDockScheduling_GoldenPinsSevenReadToolsAndSevenWriteTools(t *testing.T) {
	assertPinnedReadWriteSplit(t, loadGoldenTools(t, dockSchedulingGoldenPath), "dock-scheduling", DockSchedulingReadTools, dockSchedulingWriteTools)
}

func TestDockScheduling_ReadToolsAreTheGoldenL1Set(t *testing.T) {
	var l1 []string
	for name, tool := range loadGoldenTools(t, dockSchedulingGoldenPath) {
		if tool.Meta["com.warehouse/authority"] == "L1" {
			l1 = append(l1, name)
		}
	}
	slices.Sort(l1)
	if !slices.Equal(l1, DockSchedulingReadTools) {
		t.Fatalf("DockSchedulingReadTools = %v, the golden's L1 tools are %v", DockSchedulingReadTools, l1)
	}
}

// TestDockScheduling_PortDTOsMatchPublishedOutputSchemas walks every port
// DTO against the published output schema.
func TestDockScheduling_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, dockSchedulingGoldenPath)
	cases := map[string]reflect.Type{
		"get_appointment":           reflect.TypeFor[ports.DockAppointment](),
		"list_appointments":         reflect.TypeFor[ports.DockAppointmentPage](),
		"list_at_risk_appointments": reflect.TypeFor[ports.DockAppointmentPage](),
		"get_door_availability":     reflect.TypeFor[ports.DockDoorAvailability](),
		"get_dock_calendar":         reflect.TypeFor[ports.DockCalendar](),
		"list_carriers":             reflect.TypeFor[ports.DockCarrierPage](),
		"get_carrier":               reflect.TypeFor[ports.DockCarrier](),
	}
	if len(cases) != len(DockSchedulingReadTools) {
		t.Fatalf("every read tool needs a DTO walk: %d cases for %d tools", len(cases), len(DockSchedulingReadTools))
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful dock-scheduling stand-in -----------------------------

const dockApptFixture = `{"appointmentId":"appt-0f3a91","arrival":{"checkedInAt":"2026-11-17T08:05:00Z","implicit":false,"status":"CHECKED_IN","visitId":"visit-9"},
  "asnNumbers":["ASN-1","ASN-2"],"atRisk":true,"atRiskCause":"LATE_ETA","cancellationReason":null,"carrier":"Acme Freight","carrierScac":"ACME",
  "completedAt":null,"cptId":null,"createdAt":"2026-11-16T10:00:00Z","direction":"INBOUND","doorCode":"D-07",
  "eta":{"etaAt":"2026-11-17T09:30:00Z","reportedAt":"2026-11-17T07:00:00Z","source":"mcp"},"expectedSealNumber":"SEAL-1","provenance":"api",
  "shipmentRefs":[],"siteId":"site-1","state":"BOOKED","updatedAt":"2026-11-17T07:00:00Z","version":3,
  "windowEnd":"2026-11-17T09:00:00Z","windowStart":"2026-11-17T08:00:00Z"}`

const dockCarrierFixture = `{"createdAt":"2026-10-01T00:00:00Z","mc":"MC123","name":"Acme Freight","scac":"ACME","status":"SUSPENDED",
  "suspendedAt":"2026-11-10T12:00:00Z","suspensionReason":"insurance lapsed","updatedAt":"2026-11-10T12:00:00Z","usdot":"1234567","version":2}`

const dockSpanFixture = `{"end":"2026-11-17T10:00:00Z","kind":"APPOINTMENT","ref":"appt-0f3a91","start":"2026-11-17T08:00:00Z"}`

func newDockSchedulingTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, dockSchedulingGoldenPath)
	page := func(items string) goldenFixture {
		return goldenResult(goldenJSON(t, `{"items":[`+items+`],"nextCursor":"cur-2"}`))
	}
	return newGoldenUpstream(t, "dock-scheduling-test", golden, map[string]goldenFixture{
		"get_appointment":           withID("appointmentId", "appt-0f3a91", "appointment-not-found", goldenJSON(t, dockApptFixture)),
		"list_appointments":         page(dockApptFixture),
		"list_at_risk_appointments": page(dockApptFixture),
		"get_carrier":               withID("scac", "ACME", "carrier-not-found", goldenJSON(t, dockCarrierFixture)),
		"list_carriers":             page(dockCarrierFixture),
		"get_door_availability": goldenResult(goldenJSON(t, `{"busy":[`+dockSpanFixture+`],"doorCode":"D-07","freeWindows":[{"end":"2026-11-17T12:00:00Z","kind":null,"ref":null,"start":"2026-11-17T10:00:00Z"}],
		  "from":"2026-11-17T08:00:00Z","granularityMinutes":30,"siteId":"site-1","to":"2026-11-17T12:00:00Z"}`)),
		"get_dock_calendar": withID("siteId", "site-1", "site-not-found", goldenJSON(t, `{"closures":[{"closureId":"clo-1","doorCode":"D-07","from":"2026-11-20T00:00:00Z","lifted":false,"reason":"maintenance","siteId":"site-1","to":"2026-11-21T00:00:00Z"}],
		  "operatingHours":[{"closesAt":"18:00","doorCode":null,"opensAt":"06:00","timezone":"America/Sao_Paulo","weekday":"MONDAY"}],
		  "settings":{"arrivalToleranceMinutes":15,"maxWindowMinutes":120,"noShowGraceMinutes":30,"slotGranularityMinutes":30},"siteId":"site-1","version":4}`)),
	})
}

// --- the client against the stand-in ----------------------------------------

func TestDockScheduling_CallsOnlyPinnedReadTools(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})
	ctx := context.Background()
	from := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	calls := []func() error{
		func() error { _, err := c.ListAppointments(ctx, ports.DockAppointmentQuery{}); return err },
		func() error { _, err := c.GetAppointment(ctx, "appt-0f3a91"); return err },
		func() error { _, err := c.GetDoorAvailability(ctx, "D-07", from, from.Add(4*time.Hour), 0); return err },
		func() error { _, err := c.GetDockCalendar(ctx, "site-1"); return err },
		func() error { _, err := c.ListCarriers(ctx, "", 0, ""); return err },
		func() error { _, err := c.GetCarrier(ctx, "ACME"); return err },
		func() error { _, err := c.ListAtRiskAppointments(ctx, "", 0, ""); return err },
	}
	for i, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	assertOnlyPinnedToolsCalled(t, up, DockSchedulingReadTools)
}

func TestDockScheduling_ZeroFiltersAreOmittedSoUpstreamDefaultsApply(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.ListAppointments(ctx, ports.DockAppointmentQuery{}); err != nil {
		t.Fatalf("list: %v", err)
	}
	wantEq(t, "len(args) of an empty query", len(up.lastArgs()), 0)
	if _, err := c.ListCarriers(ctx, "", 0, ""); err != nil {
		t.Fatalf("carriers: %v", err)
	}
	wantEq(t, "len(args) of an empty carrier list", len(up.lastArgs()), 0)
	if _, err := c.ListAtRiskAppointments(ctx, "", 0, ""); err != nil {
		t.Fatalf("at-risk: %v", err)
	}
	wantEq(t, "len(args) of an empty at-risk list", len(up.lastArgs()), 0)
	from := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	if _, err := c.GetDoorAvailability(ctx, "D-07", from, from.Add(time.Hour), 0); err != nil {
		t.Fatalf("availability: %v", err)
	}
	if got := up.lastArgs(); len(got) != 3 || got["doorCode"] != "D-07" {
		t.Fatalf("zero minutes must be omitted and the door sent under its published name, sent %v", got)
	}
}

func TestDockScheduling_FiltersAreSentUnderTheirPublishedNames(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})
	ctx := context.Background()
	from := time.Date(2026, 11, 17, 8, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	to := from.Add(6 * time.Hour)

	_, err := c.ListAppointments(ctx, ports.DockAppointmentQuery{
		SiteID: "site-1", DoorCode: "D-07", State: "BOOKED", Direction: "INBOUND", CarrierSCAC: "ACME",
		AtRisk: true, From: from, To: to, Limit: 50, Cursor: "cur-1",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := map[string]any{
		"siteId": "site-1", "doorCode": "D-07", "state": "BOOKED", "direction": "INBOUND", "carrierScac": "ACME",
		"atRisk": true, "from": "2026-11-17T11:00:00Z", "to": "2026-11-17T17:00:00Z", "limit": float64(50), "cursor": "cur-1",
	}
	if got := up.lastArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("list_appointments args = %v, want %v", got, want)
	}

	if _, err := c.GetDoorAvailability(ctx, "D-07", from, to, 45); err != nil {
		t.Fatalf("availability: %v", err)
	}
	want = map[string]any{"doorCode": "D-07", "from": "2026-11-17T11:00:00Z", "to": "2026-11-17T17:00:00Z", "minutes": float64(45)}
	if got := up.lastArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("get_door_availability args = %v, want %v", got, want)
	}

	if _, err := c.ListCarriers(ctx, "SUSPENDED", 10, "cur-1"); err != nil {
		t.Fatalf("carriers: %v", err)
	}
	want = map[string]any{"status": "SUSPENDED", "limit": float64(10), "cursor": "cur-1"}
	if got := up.lastArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("list_carriers args = %v, want %v", got, want)
	}

	if _, err := c.ListAtRiskAppointments(ctx, "site-1", 5, "cur-1"); err != nil {
		t.Fatalf("at-risk: %v", err)
	}
	want = map[string]any{"siteId": "site-1", "limit": float64(5), "cursor": "cur-1"}
	if got := up.lastArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("list_at_risk_appointments args = %v, want %v", got, want)
	}
}

func TestDockScheduling_DecodesAnAppointment(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})

	appt, err := c.GetAppointment(context.Background(), "appt-0f3a91")
	if err != nil {
		t.Fatalf("appointment: %v", err)
	}
	wantEq(t, "state", appt.State, "BOOKED")
	wantEq(t, "doorCode", appt.DoorCode, "D-07")
	wantEq(t, "version", appt.Version, 3)
	wantEq(t, "atRisk", appt.AtRisk, true)
	wantEq(t, "len(asnNumbers)", len(appt.ASNNumbers), 2)
	if !appt.WindowStart.Equal(time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("windowStart = %v", appt.WindowStart)
	}
	if appt.AtRiskCause == nil || *appt.AtRiskCause != "LATE_ETA" || appt.CarrierSCAC == nil || *appt.CarrierSCAC != "ACME" {
		t.Fatalf("optional strings decoded wrong: %+v", appt)
	}
	if appt.CompletedAt != nil || appt.CPTID != nil || appt.CancellationReason != nil {
		t.Fatalf("null optionals must decode to nil: %+v", appt)
	}
	if appt.Arrival == nil || appt.Arrival.CheckedInAt == nil || appt.Arrival.Status != "CHECKED_IN" || appt.ETA == nil || appt.ETA.Source != "mcp" {
		t.Fatalf("arrival and eta must decode: %+v %+v", appt.Arrival, appt.ETA)
	}
}

func TestDockScheduling_DecodesAppointmentPages(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})
	ctx := context.Background()

	page, err := c.ListAppointments(ctx, ports.DockAppointmentQuery{})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	wantEq(t, "len(items)", len(page.Items), 1)
	wantEq(t, "nextCursor", page.NextCursor, "cur-2")
	atRisk, err := c.ListAtRiskAppointments(ctx, "", 0, "")
	if err != nil || len(atRisk.Items) != 1 {
		t.Fatalf("at-risk page: %+v %v", atRisk, err)
	}
}

func TestDockScheduling_DecodesTheCalendar(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})

	cal, err := c.GetDockCalendar(context.Background(), "site-1")
	if err != nil {
		t.Fatalf("calendar: %v", err)
	}
	wantEq(t, "settings.slotGranularityMinutes", cal.Settings.SlotGranularityMinutes, 30)
	wantEq(t, "len(closures)", len(cal.Closures), 1)
	wantEq(t, "closures[0].reason", cal.Closures[0].Reason, "maintenance")
	wantEq(t, "len(operatingHours)", len(cal.OperatingHours), 1)
	wantEq(t, "operatingHours[0].opensAt", cal.OperatingHours[0].OpensAt, "06:00")
	if cal.OperatingHours[0].DoorCode != nil {
		t.Fatalf("a site-wide entry has a nil door code, got %v", *cal.OperatingHours[0].DoorCode)
	}
}

func TestDockScheduling_DecodesDoorAvailability(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})

	from := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	avail, err := c.GetDoorAvailability(context.Background(), "D-07", from, from.Add(4*time.Hour), 0)
	if err != nil {
		t.Fatalf("availability: %v", err)
	}
	wantEq(t, "granularityMinutes", avail.GranularityMinutes, 30)
	wantEq(t, "len(busy)", len(avail.Busy), 1)
	wantEq(t, "len(freeWindows)", len(avail.FreeWindows), 1)
	if avail.Busy[0].Ref == nil || *avail.Busy[0].Ref != "appt-0f3a91" || avail.FreeWindows[0].Ref != nil {
		t.Fatalf("span refs decoded wrong: %+v %+v", avail.Busy[0], avail.FreeWindows[0])
	}
}

func TestDockScheduling_DecodesCarriers(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})
	ctx := context.Background()

	carrier, err := c.GetCarrier(ctx, "ACME")
	if err != nil {
		t.Fatalf("carrier: %v", err)
	}
	wantEq(t, "status", carrier.Status, "SUSPENDED")
	if carrier.SuspendedAt == nil || carrier.SuspensionReason == nil || carrier.USDOT == nil || carrier.MC == nil {
		t.Fatalf("suspended carrier optionals must decode: %+v", carrier)
	}
	carriers, err := c.ListCarriers(ctx, "SUSPENDED", 0, "")
	if err != nil || len(carriers.Items) != 1 || carriers.NextCursor != "cur-2" {
		t.Fatalf("carrier page: %+v %v", carriers, err)
	}
}

func TestDockScheduling_NotFoundMapsToErrUpstreamNotFound(t *testing.T) {
	up := newDockSchedulingTestUpstream(t)
	c := NewDockScheduling(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetAppointment(ctx, "appt-nope"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("appointment-not-found must map to ports.ErrUpstreamNotFound, got %v", err)
	}
	if _, err := c.GetCarrier(ctx, "NOPE"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("carrier-not-found must map to ports.ErrUpstreamNotFound, got %v", err)
	}
	if _, err := c.GetDockCalendar(ctx, "site-nope"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("site-not-found must map to ports.ErrUpstreamNotFound, got %v", err)
	}
}
