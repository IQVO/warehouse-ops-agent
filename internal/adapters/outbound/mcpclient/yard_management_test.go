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

// yardManagementGoldenPath is a VERBATIM copy of yard-management's own
// published tool registry golden
// (yard-management: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at yard-management origin/develop 418feea9). Refresh it with
//
//	git -C ../yard-management show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever yard-management amends its tool surface; the tests below then say
// exactly which consumer-side assumption broke.
const yardManagementGoldenPath = "testdata/yard_management_tools.golden.json"

// yardManagementWriteTools is every L3 write tool yard-management
// publishes. The client wraps none of them (ADR 0032, D-Y4); they are pinned
// here so a new tool upstream cannot slip in unclassified.
var yardManagementWriteTools = []string{
	"cancel_move", "complete_inspection", "open_inspection", "open_yard_check", "place_hold",
	"record_inspection_item", "record_seal", "release_hold", "release_securing", "request_move", "secure_trailer",
}

// --- the published contract ------------------------------------------------

func TestYardManagement_GoldenPinsTenReadToolsAndElevenWriteTools(t *testing.T) {
	assertPinnedReadWriteSplit(t, loadGoldenTools(t, yardManagementGoldenPath), "yard-management", YardManagementReadTools, yardManagementWriteTools)
}

func TestYardManagement_ReadToolsAreTheGoldenL1Set(t *testing.T) {
	var l1 []string
	for name, tool := range loadGoldenTools(t, yardManagementGoldenPath) {
		if tool.Meta["com.warehouse/authority"] == "L1" {
			l1 = append(l1, name)
		}
	}
	slices.Sort(l1)
	if !slices.Equal(l1, YardManagementReadTools) {
		t.Fatalf("YardManagementReadTools = %v, the golden's L1 tools are %v", YardManagementReadTools, l1)
	}
}

// TestYardManagement_PortDTOsMatchPublishedOutputSchemas walks every port
// DTO against the published output schema.
func TestYardManagement_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadGoldenTools(t, yardManagementGoldenPath)
	cases := map[string]reflect.Type{
		"get_yard_board":           reflect.TypeFor[ports.YardBoard](),
		"list_trailers":            reflect.TypeFor[ports.YardTrailerPage](),
		"get_trailer":              reflect.TypeFor[ports.YardTrailer](),
		"list_moves":               reflect.TypeFor[ports.YardMovePage](),
		"list_visits":              reflect.TypeFor[ports.YardVisitPage](),
		"get_visit":                reflect.TypeFor[ports.YardVisit](),
		"get_yard_policy":          reflect.TypeFor[ports.YardPolicy](),
		"get_yard_dwell_report":    reflect.TypeFor[ports.YardDwellReport](),
		"get_inspection":           reflect.TypeFor[ports.YardInspection](),
		"list_trailer_inspections": reflect.TypeFor[ports.YardInspectionPage](),
	}
	if len(cases) != len(YardManagementReadTools) {
		t.Fatalf("every read tool needs a DTO walk: %d cases for %d tools", len(cases), len(YardManagementReadTools))
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

// --- a schema-faithful yard-management stand-in -----------------------------

const yardTrailerFixture = `{"appointmentId":"appt-0f3a91","arrivedAt":"2026-11-17T06:00:00Z","asnNumbers":["ASN-1"],"carrierScac":"ACME","departedAt":null,
  "doorWork":"UNLOADING","hazmatClass":null,"hold":null,"kind":"DRY_VAN","loadState":"LOADED","location":{"code":"D-07","kind":"DOOR"},
  "openMoveId":null,"outboundRef":null,"requiresTemperatureControl":false,"seal":{"class":"H","number":"SEAL-1","state":"INTACT"},
  "securedAt":"2026-11-17T06:20:00Z","securedBy":"hostler-1","securerRef":null,"siteId":"site-1","state":"DOCKED","trailerId":"trl-0f3a91",
  "unNumber":null,"unitNumber":"ABCU1234567","unitNumberScheme":"ISO6346","version":5,"visitId":"visit-9"}`

const yardMoveFixture = `{"actualLocation":null,"appointmentId":null,"claimedAt":null,"completedAt":null,"from":{"code":"S-12","kind":"SPOT"},
  "hostlerRef":null,"leaseExpiresAt":null,"moveId":"mv-1","performer":"HOSTLER","priority":{"band":3,"dueBy":"2026-11-17T08:00:00Z"},
  "reason":"TO_DOOR","requestedAt":"2026-11-17T06:30:00Z","siteId":"site-1","state":"REQUESTED","to":{"code":"D-07","kind":"DOOR"},
  "trailerId":"trl-0f3a91","version":1}`

const yardVisitFixture = `{"appointmentId":null,"arrivedAt":"2026-11-17T05:50:00Z","carrier":"Acme Freight","carrierFlag":null,"carrierScac":"ACME",
  "departedAt":null,"driverRef":"drv-1","purpose":"DELIVERY","rejectionReason":null,"siteId":"site-1","state":"CHECKED_IN","tractorPlate":"ABC1D23",
  "trailersIn":["trl-0f3a91"],"trailersOut":[],"version":2,"visitId":"visit-9"}`

const yardInspectionFixture = `{"completedAt":null,"conditionGrade":null,"gate":"PRE_DOCK","inspectionId":"insp-1",
  "items":[{"allowNotApplicable":false,"evidenceRefs":[],"holdReason":"DAMAGED","itemCode":"FLOOR","note":null,"required":true,"result":"PASS","text":"Floor intact"}],
  "openedAt":"2026-11-17T06:10:00Z","performerRef":"insp-user","siteId":"site-1","status":"OPEN","templateId":"tpl-1","templateVersion":2,
  "trailerId":"trl-0f3a91","version":1,"visitId":null}`

func newYardManagementTestUpstream(t *testing.T) *goldenUpstream {
	t.Helper()
	golden := loadGoldenTools(t, yardManagementGoldenPath)
	page := func(items string) goldenFixture {
		return goldenResult(goldenJSON(t, `{"items":[`+items+`],"nextCursor":"cur-2"}`))
	}
	return newGoldenUpstream(t, "yard-management-test", golden, map[string]goldenFixture{
		"get_yard_board": withID("siteId", "site-1", "site-not-found", goldenJSON(t, `{"asOf":"2026-11-17T07:00:00Z",
		  "doors":[{"closed":false,"code":"D-07","dockFlow":"INBOUND","kind":"DOOR","trailer":`+yardTrailerFixture+`}],
		  "inMove":[`+yardTrailerFixture+`],"openMoves":[`+yardMoveFixture+`],"siteId":"site-1",
		  "spots":[{"closed":true,"code":"S-12","dockFlow":null,"kind":"SPOT","trailer":null}]}`)),
		"list_trailers":  page(yardTrailerFixture),
		"get_trailer":    withID("trailerId", "trl-0f3a91", "trailer-not-found", goldenJSON(t, yardTrailerFixture)),
		"list_moves":     page(yardMoveFixture),
		"list_visits":    page(yardVisitFixture),
		"get_visit":      withID("visitId", "visit-9", "visit-not-found", goldenJSON(t, yardVisitFixture)),
		"get_inspection": withID("inspectionId", "insp-1", "inspection-not-found", goldenJSON(t, yardInspectionFixture)),
		"list_trailer_inspections": withID("trailerId", "trl-0f3a91", "trailer-not-found",
			goldenJSON(t, `{"items":[`+yardInspectionFixture+`],"nextCursor":"cur-2"}`)),
		"get_yard_policy": withID("siteId", "site-1", "site-not-found", goldenJSON(t, `{"allowHazmatOnUnclassifiedSpots":false,"declared":true,
		  "dwellThresholdMinutes":{"DRY_VAN":2880,"REEFER":720},"freeTimeMinutes":120,"holdingSpotCodes":["H-1"],"leaseSeconds":900,
		  "minimumSealClass":"H","parkingZoneIds":["zone-1"],"requireAppointment":true,"requireSecuringBeforeDoorWork":true,
		  "requiredInspections":[{"appliesTo":{"purposes":["DELIVERY"],"trailerKinds":["REEFER"]},"gate":"PRE_DOCK","templateId":"tpl-1"}],
		  "sealMismatchAction":"FLAG","siteId":"site-1","suspendedCarrierAction":"REJECT","version":3}`)),
		"get_yard_dwell_report": goldenResult(goldenJSON(t, `{"from":"2026-11-10T00:00:00Z","lastEventAt":"2026-11-17T06:59:00Z",
		  "rows":[{"avgDriverWaitMinutes":12.5,"avgDwellMinutes":340.2,"avgProcessingMinutes":null,"kind":"DRY_VAN","maxDwellMinutes":1500,
		    "p50DwellMinutes":300,"p90DwellMinutes":null,"trailers":42}],"siteId":"site-1","to":"2026-11-17T00:00:00Z"}`)),
	})
}

// --- the client against the stand-in ----------------------------------------

func TestYardManagement_CallsOnlyPinnedReadTools(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})
	ctx := context.Background()
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	calls := []func() error{
		func() error { _, err := c.GetYardBoard(ctx, "site-1"); return err },
		func() error { _, err := c.ListTrailers(ctx, ports.YardTrailerQuery{}); return err },
		func() error { _, err := c.GetTrailer(ctx, "trl-0f3a91"); return err },
		func() error { _, err := c.ListMoves(ctx, ports.YardMoveQuery{}); return err },
		func() error { _, err := c.ListVisits(ctx, ports.YardVisitQuery{}); return err },
		func() error { _, err := c.GetVisit(ctx, "visit-9"); return err },
		func() error { _, err := c.GetYardPolicy(ctx, "site-1"); return err },
		func() error { _, err := c.GetYardDwellReport(ctx, from, from.Add(7*24*time.Hour), ""); return err },
		func() error { _, err := c.GetInspection(ctx, "insp-1"); return err },
		func() error { _, err := c.ListTrailerInspections(ctx, "trl-0f3a91", 0, ""); return err },
	}
	for i, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	assertOnlyPinnedToolsCalled(t, up, YardManagementReadTools)
}

func TestYardManagement_ZeroFiltersAreOmittedSoUpstreamDefaultsApply(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.ListTrailers(ctx, ports.YardTrailerQuery{}); err != nil {
		t.Fatalf("trailers: %v", err)
	}
	wantEq(t, "len(args) of an empty trailer query (an omitted state means not DEPARTED)", len(up.lastArgs()), 0)
	if _, err := c.ListMoves(ctx, ports.YardMoveQuery{}); err != nil {
		t.Fatalf("moves: %v", err)
	}
	wantEq(t, "len(args) of an empty move query", len(up.lastArgs()), 0)
	if _, err := c.ListVisits(ctx, ports.YardVisitQuery{}); err != nil {
		t.Fatalf("visits: %v", err)
	}
	wantEq(t, "len(args) of an empty visit query", len(up.lastArgs()), 0)
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if _, err := c.GetYardDwellReport(ctx, from, from.Add(time.Hour), ""); err != nil {
		t.Fatalf("dwell: %v", err)
	}
	if got := up.lastArgs(); len(got) != 2 {
		t.Fatalf("an empty siteID must be omitted so the report covers every site, sent %v", got)
	}
	if _, err := c.ListTrailerInspections(ctx, "trl-0f3a91", 0, ""); err != nil {
		t.Fatalf("inspections: %v", err)
	}
	if got := up.lastArgs(); len(got) != 1 || got["trailerId"] != "trl-0f3a91" {
		t.Fatalf("a zero limit and empty cursor must be omitted, sent %v", got)
	}
}

func TestYardManagement_FiltersAreSentUnderTheirPublishedNames(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
		want map[string]any
	}{
		{"list_trailers", func() error {
			_, err := c.ListTrailers(ctx, ports.YardTrailerQuery{SiteID: "site-1", State: "DOCKED", Kind: "REEFER", LocationCode: "D-07", UnitNumber: "ABCU1234567", VisitID: "visit-9", Limit: 25, Cursor: "cur-1"})
			return err
		}, map[string]any{"siteId": "site-1", "state": "DOCKED", "kind": "REEFER", "locationCode": "D-07", "unitNumber": "ABCU1234567", "visitId": "visit-9", "limit": float64(25), "cursor": "cur-1"}},
		{"list_moves", func() error {
			_, err := c.ListMoves(ctx, ports.YardMoveQuery{SiteID: "site-1", State: "CLAIMED", TrailerID: "trl-0f3a91", HostlerRef: "h-1", Limit: 25, Cursor: "cur-1"})
			return err
		}, map[string]any{"siteId": "site-1", "state": "CLAIMED", "trailerId": "trl-0f3a91", "hostlerRef": "h-1", "limit": float64(25), "cursor": "cur-1"}},
		{"list_visits", func() error {
			_, err := c.ListVisits(ctx, ports.YardVisitQuery{SiteID: "site-1", State: "CHECKED_IN", TractorPlate: "ABC1D23", Limit: 25, Cursor: "cur-1"})
			return err
		}, map[string]any{"siteId": "site-1", "state": "CHECKED_IN", "tractorPlate": "ABC1D23", "limit": float64(25), "cursor": "cur-1"}},
		{"get_yard_dwell_report", func() error {
			from := time.Date(2026, 11, 10, 0, 0, 0, 0, time.FixedZone("BRT", -3*3600))
			_, err := c.GetYardDwellReport(ctx, from, from.Add(24*time.Hour), "site-1")
			return err
		}, map[string]any{"from": "2026-11-10T03:00:00Z", "to": "2026-11-11T03:00:00Z", "siteId": "site-1"}},
		{"list_trailer_inspections", func() error {
			_, err := c.ListTrailerInspections(ctx, "trl-0f3a91", 10, "cur-1")
			return err
		}, map[string]any{"trailerId": "trl-0f3a91", "limit": float64(10), "cursor": "cur-1"}},
	}
	for _, tc := range cases {
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := up.lastArgs(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s args = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestYardManagement_DecodesTheBoard(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})

	board, err := c.GetYardBoard(context.Background(), "site-1")
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	wantEq(t, "len(doors)", len(board.Doors), 1)
	wantEq(t, "len(spots)", len(board.Spots), 1)
	wantEq(t, "len(inMove)", len(board.InMove), 1)
	wantEq(t, "len(openMoves)", len(board.OpenMoves), 1)
	if board.Doors[0].Trailer == nil || board.Doors[0].Trailer.TrailerID != "trl-0f3a91" || board.Spots[0].Trailer != nil {
		t.Fatalf("a slot's trailer must decode, an empty one stay nil: %+v %+v", board.Doors[0], board.Spots[0])
	}
	if board.Doors[0].DockFlow == nil || *board.Doors[0].DockFlow != "INBOUND" || board.Spots[0].DockFlow != nil || !board.Spots[0].Closed {
		t.Fatalf("dockFlow/closed decoded wrong: %+v %+v", board.Doors[0], board.Spots[0])
	}
}

func TestYardManagement_DecodesATrailer(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})
	ctx := context.Background()

	trailer, err := c.GetTrailer(ctx, "trl-0f3a91")
	if err != nil {
		t.Fatalf("trailer: %v", err)
	}
	wantEq(t, "state", trailer.State, "DOCKED")
	wantEq(t, "seal.state", trailer.Seal.State, "INTACT")
	wantEq(t, "version", trailer.Version, 5)
	if trailer.Location == nil || trailer.Location.Code != "D-07" || trailer.Seal.Class == nil || *trailer.Seal.Class != "H" {
		t.Fatalf("location and seal class must decode: %+v", trailer)
	}
	if trailer.SecuredAt == nil || trailer.DepartedAt != nil || trailer.Hold != nil || trailer.OpenMoveID != nil {
		t.Fatalf("optional instants and refs decoded wrong: %+v", trailer)
	}
	page, err := c.ListTrailers(ctx, ports.YardTrailerQuery{})
	if err != nil || len(page.Items) != 1 || page.NextCursor != "cur-2" {
		t.Fatalf("trailer page: %+v %v", page, err)
	}
}

func TestYardManagement_DecodesMoves(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})

	moves, err := c.ListMoves(context.Background(), ports.YardMoveQuery{})
	if err != nil {
		t.Fatalf("moves: %v", err)
	}
	wantEq(t, "len(items)", len(moves.Items), 1)
	wantEq(t, "nextCursor", moves.NextCursor, "cur-2")
	mv := moves.Items[0]
	wantEq(t, "priority.band", mv.Priority.Band, 3)
	wantEq(t, "to.code", mv.To.Code, "D-07")
	if mv.From == nil || mv.From.Code != "S-12" || mv.Priority.DueBy == nil || mv.ActualLocation != nil || mv.ClaimedAt != nil {
		t.Fatalf("move optionals decoded wrong: %+v", mv)
	}
}

func TestYardManagement_DecodesVisits(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})
	ctx := context.Background()

	visit, err := c.GetVisit(ctx, "visit-9")
	if err != nil {
		t.Fatalf("visit: %v", err)
	}
	wantEq(t, "tractorPlate", visit.TractorPlate, "ABC1D23")
	wantEq(t, "len(trailersIn)", len(visit.TrailersIn), 1)
	visits, err := c.ListVisits(ctx, ports.YardVisitQuery{})
	if err != nil || len(visits.Items) != 1 || visits.NextCursor != "cur-2" {
		t.Fatalf("visit page: %+v %v", visits, err)
	}
}

func TestYardManagement_DecodesThePolicy(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})

	policy, err := c.GetYardPolicy(context.Background(), "site-1")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	wantEq(t, "declared", policy.Declared, true)
	wantEq(t, "dwellThresholdMinutes[REEFER]", policy.DwellThresholdMinutes["REEFER"], 720)
	wantEq(t, "len(requiredInspections)", len(policy.RequiredInspections), 1)
	wantEq(t, "requiredInspections[0].gate", policy.RequiredInspections[0].Gate, "PRE_DOCK")
	if policy.FreeTimeMinutes == nil || *policy.FreeTimeMinutes != 120 || policy.MinimumSealClass == nil || *policy.MinimumSealClass != "H" {
		t.Fatalf("policy optionals decoded wrong: %+v", policy)
	}
}

func TestYardManagement_DecodesTheDwellReport(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})

	from := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	dwell, err := c.GetYardDwellReport(context.Background(), from, from.Add(7*24*time.Hour), "site-1")
	if err != nil {
		t.Fatalf("dwell: %v", err)
	}
	wantEq(t, "len(rows)", len(dwell.Rows), 1)
	wantEq(t, "rows[0].trailers", dwell.Rows[0].Trailers, 42)
	if dwell.LastEventAt == nil || dwell.Rows[0].AvgDriverWaitMinutes == nil || dwell.Rows[0].AvgProcessingMinutes != nil || dwell.SiteID == nil {
		t.Fatalf("dwell optionals decoded wrong: %+v", dwell)
	}
}

func TestYardManagement_DecodesInspections(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})
	ctx := context.Background()

	insp, err := c.GetInspection(ctx, "insp-1")
	if err != nil {
		t.Fatalf("inspection: %v", err)
	}
	wantEq(t, "status", insp.Status, "OPEN")
	wantEq(t, "len(items)", len(insp.Items), 1)
	if insp.Items[0].Result == nil || *insp.Items[0].Result != "PASS" || insp.CompletedAt != nil || insp.VisitID != nil {
		t.Fatalf("inspection optionals decoded wrong: %+v", insp)
	}
	page, err := c.ListTrailerInspections(ctx, "trl-0f3a91", 0, "")
	if err != nil || len(page.Items) != 1 || page.NextCursor != "cur-2" {
		t.Fatalf("inspection page: %+v %v", page, err)
	}
}

func TestYardManagement_NotFoundMapsToErrUpstreamNotFound(t *testing.T) {
	up := newYardManagementTestUpstream(t)
	c := NewYardManagement(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetTrailer(ctx, "trl-nope"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("trailer-not-found must map to ports.ErrUpstreamNotFound, got %v", err)
	}
	if _, err := c.GetVisit(ctx, "visit-nope"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("visit-not-found must map to ports.ErrUpstreamNotFound, got %v", err)
	}
	if _, err := c.GetYardBoard(ctx, "site-nope"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("site-not-found must map to ports.ErrUpstreamNotFound, got %v", err)
	}
	if _, err := c.GetInspection(ctx, "insp-nope"); !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Errorf("inspection-not-found must map to ports.ErrUpstreamNotFound, got %v", err)
	}
}
