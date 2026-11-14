package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/cucumber/godog"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Steps of the documentation-derived scenarios (features/*_rules.feature):
// the MCP surface of the agent, the stranded-reservation check (E2, MCP-only),
// the release-next-work act slice (ADR 0027) and the small extra Given/Then
// vocabulary the rule tables need. They reuse the shared world and fakes.

// docState is the per-scenario state of these steps; nil until first used.
type docState struct {
	inv *fakeInventory

	mcp         *httptest.Server
	session     *sdk.ClientSession
	toolIsError bool
	toolText    string
	tools       []*sdk.Tool

	releaser     *fakeReleaser
	releaseToken string
}

func (w *world) doc() *docState {
	if w.docs == nil {
		w.docs = &docState{inv: &fakeInventory{usable: map[string]int{}, bins: map[string]ports.BinOccupancy{}}}
	}
	return w.docs
}

// ----------------------------------------------------------------- fakes ---

// fakeInventory stands in for inventory-storage's two read tools the
// stranded-reservation check uses.
type fakeInventory struct {
	down   bool
	usable map[string]int
	bins   map[string]ports.BinOccupancy
	// binAsks records every get_bin_occupancy call.
	binAsks []string
}

func (f *fakeInventory) CheckAvailability(_ context.Context, sku string) (ports.Availability, error) {
	if f.down {
		return ports.Availability{}, errUnreachable
	}
	n, ok := f.usable[sku]
	if !ok {
		return ports.Availability{}, fmt.Errorf("inventory-storage: no availability for SKU %q", sku)
	}
	return ports.Availability{SKU: sku, Usable: n}, nil
}

func (f *fakeInventory) GetBinOccupancy(_ context.Context, binID string) (ports.BinOccupancy, error) {
	f.binAsks = append(f.binAsks, binID)
	if f.down {
		return ports.BinOccupancy{}, errUnreachable
	}
	b, ok := f.bins[binID]
	if !ok {
		return ports.BinOccupancy{}, fmt.Errorf("inventory-storage: no bin %q", binID)
	}
	return b, nil
}

// fakeReleaser stands in for wes-work-planning's release_next_work A2A skill.
type fakeReleaser struct {
	calls []string
	err   error
}

func (f *fakeReleaser) ReleaseNextWork(_ context.Context, pathID string) (ports.ReleasedWork, error) {
	f.calls = append(f.calls, pathID)
	if f.err != nil {
		return ports.ReleasedWork{}, f.err
	}
	return ports.ReleasedWork{WorkUnitId: "ORD-1042-line-1", CPT: "2026-01-01T16:00:00Z", Ref: "ORD-1042"}, nil
}

// ---------------------------------------------------------------- Given ----

func (w *world) theseUpstreamsAreUnreachable(list string) error {
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case "":
		case "inventory-storage":
			w.doc().inv.down = true
		default:
			if err := w.mcpToolsAreUnreachable(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *world) workforceFlagsUnderstaffedAlthough(path string, planned, active int) error {
	w.wfm.gaps[path] = ports.StaffingGap{PathId: path, PlannedHeads: planned, ActiveHeads: active, Understaffed: true}
	return nil
}

func (w *world) inventoryReportsUsable(n int, sku string) error {
	w.doc().inv.usable[sku] = n
	return nil
}

func (w *world) inventoryBinHolds(bin string, reservedA int, skuA string, reservedB int, skuB string) error {
	w.doc().inv.bins[bin] = ports.BinOccupancy{
		BinId: bin, UnitCount: 2, Reserved: reservedA + reservedB,
		Lines: []ports.BinOccupancyLine{
			{StockUnitId: "SU-1", SKU: skuA, OnHand: reservedA, Reserved: reservedA, State: "Reserved"},
			{StockUnitId: "SU-2", SKU: skuB, OnHand: reservedB, Reserved: reservedB, State: "Reserved"},
		},
	}
	return nil
}

func (w *world) inboundHoldsAppointmentInState(state, id, door, carrier, start, end string) error {
	w.inbound.appointments = append(w.inbound.appointments, ports.InboundAppointment{
		AppointmentID: id, DoorCode: door, Carrier: carrier, WindowStart: start, WindowEnd: end, State: state, Version: 1,
	})
	return nil
}

func (w *world) inboundHoldsClosedReceiptWithoutDiscrepancy(id, asn, closed string) error {
	closedAt, err := time.Parse(time.RFC3339, closed)
	if err != nil {
		return err
	}
	w.inbound.receipts = append(w.inbound.receipts, ports.InboundReceipt{
		ReceiptID: id, AsnNumber: asn, DoorCode: "D1", State: "Closed",
		OpenedAt: closedAt.Add(-time.Hour).Format(time.RFC3339), ClosedAt: closed, Version: 2,
	})
	return nil
}

func (w *world) inboundHoldsAsnPages(n int) error {
	w.inbound.asnPages = n
	return nil
}

func (w *world) productMasterHoldsUnclassifiedAndDisagreeing(sku, description string) error {
	if err := w.productMasterHoldsUnclassified(sku, description); err != nil {
		return err
	}
	p := &w.productMaster.products[len(w.productMaster.products)-1]
	p.PhysicalProfile = ports.ProductPhysicalProfile{
		Declared:        &ports.ProductDimensions{LengthMm: 100, WidthMm: 50, HeightMm: 30, WeightG: 200, VolumeMm3: 150000},
		Measured:        &ports.ProductMeasurement{LengthMm: 140, WidthMm: 50, HeightMm: 30, WeightG: 200, VolumeMm3: 210000, MeasuredAt: "2025-12-30T10:00:00Z", DeviceId: "scanner-7"},
		EffectiveSource: "measured", Discrepancy: true,
	}
	return nil
}

func (w *world) productMasterFailsAfterCursor(cursor string) error {
	w.productMaster.failCursor = cursor
	return nil
}

func (w *world) timePassesSeconds(n int) error {
	w.now = w.now.Add(time.Duration(n) * time.Second)
	return nil
}

// releaseSliceIsWired wires the ADR 0027 act slice the way cmd/agent does when
// WES_WORK_PLANNING_A2A_URL is set: the advisory inside, a signer on the
// scenario clock, and an in-memory stand-in for planning's release skill.
func (w *world) releaseSliceIsWired() error {
	d := w.doc()
	d.releaser = &fakeReleaser{}
	w.hooks = append(w.hooks, func(h *inboundhttp.Handlers) error {
		advisory, err := w.flowBalance()
		if err != nil {
			return err
		}
		signer, err := approval.NewSigner([]byte(laborSigningKey), 5*time.Minute, func() time.Time { return w.now })
		if err != nil {
			return err
		}
		uc, err := usecases.NewReleaseWork(advisory, signer, d.releaser, nil, func() time.Time { return w.now })
		if err != nil {
			return err
		}
		h.ReleaseWork = uc
		return nil
	})
	return nil
}

func (w *world) theReleaseAgentOutcome(outcome string) error {
	d := w.doc()
	if d.releaser == nil {
		return errors.New("the release-next-work act slice has not been wired")
	}
	switch outcome {
	case "has no pending work":
		d.releaser.err = fmt.Errorf("%w: the path queue is empty", ports.ErrNoPendingWork)
	case "does not know the path":
		d.releaser.err = fmt.Errorf("%w: pick-zone-z", ports.ErrUnknownWorkPlanningPath)
	default:
		d.releaser.err = errors.Join(ports.ErrWorkPlanningUnreachable, errors.New("connection refused"))
	}
	return nil
}

// ----------------------------------------------------------------- When ----

func (w *world) iAskToReleaseTheNextWork(ctx context.Context, path string) error {
	body := fmt.Sprintf(`{"pathId":%q,"buildingId":%q,"shiftId":%q}`, path, defaultBuilding, defaultShift)
	if err := w.postJSON(ctx, "/act/release-next-work/proposals", body); err != nil {
		return err
	}
	if w.status == http.StatusOK {
		var out struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(w.body, &out); err != nil {
			return err
		}
		w.doc().releaseToken = out.Token
	}
	return nil
}

func (w *world) iConfirmTheReleaseWith(ctx context.Context, token string) error {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return err
	}
	return w.postJSON(ctx, "/act/release-next-work/approvals", string(body))
}

func (w *world) iConfirmTheReleaseWithTheProposedToken(ctx context.Context) error {
	token := w.doc().releaseToken
	if token == "" {
		return errors.New("no release proposal has been made yet")
	}
	return w.iConfirmTheReleaseWith(ctx, token)
}

func (w *world) iConfirmTheReleaseWithALaborToken(ctx context.Context) error {
	signer, err := approval.NewSigner([]byte(laborSigningKey), 5*time.Minute, func() time.Time { return w.now })
	if err != nil {
		return err
	}
	token, _, err := signer.MintReallocation("a1", "pick", "pack")
	if err != nil {
		return err
	}
	return w.iConfirmTheReleaseWith(ctx, token)
}

// mcpSession lazily serves the agent's own MCP server over a real loopback
// listener, wired from the same fakes as the REST router.
func (w *world) mcpSession(ctx context.Context) (*sdk.ClientSession, error) {
	d := w.doc()
	if d.session != nil {
		return d.session, nil
	}
	clock := func() time.Time { return w.now }
	deps := inboundmcp.Deps{
		DailyBrief:          &usecases.DailyBrief{Facility: w.facility, Wes: w.wes, Fe: w.fe, Wfm: w.wfm, Targets: w.targets, Now: clock},
		StrandedReservation: &usecases.DetectStrandedReservation{FulfillmentExecution: w.fe, InventoryStorage: d.inv},
	}
	if !w.unwired["flow-balance"] {
		advisory, err := w.flowBalance()
		if err != nil {
			return nil, err
		}
		deps.FlowBalanceAdvisory = advisory
	}
	if !w.unwired["explain-travel-factor"] {
		deps.ExplainTravelFactor = &usecases.ExplainTravelFactor{Facility: w.facility}
	}
	d.mcp = httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(deps)))
	client := sdk.NewClient(&sdk.Implementation{Name: "bdd-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: d.mcp.URL}, nil)
	if err != nil {
		return nil, err
	}
	d.session = session
	return session, nil
}

func (w *world) iCallTheMCPTool(ctx context.Context, name string, args map[string]any) error {
	session, err := w.mcpSession(ctx)
	if err != nil {
		return err
	}
	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return fmt.Errorf("calling %s: %w", name, err)
	}
	d := w.doc()
	d.toolIsError, d.toolText = res.IsError, ""
	for _, c := range res.Content {
		if text, ok := c.(*sdk.TextContent); ok {
			d.toolText += text.Text
		}
	}
	if res.IsError {
		return nil
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return err
	}
	// Reuse the JSON assertions of the REST steps on the structured result.
	w.response = response{status: http.StatusOK, body: raw}
	return nil
}

func (w *world) iCallTheMCPToolWithArguments(ctx context.Context, name, raw string) error {
	args := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return fmt.Errorf("arguments %q: %w", raw, err)
	}
	return w.iCallTheMCPTool(ctx, name, args)
}

func (w *world) iAskForAStrandedReservationCheck(ctx context.Context, taskType, sku string, threshold int, reservation, bin string) error {
	args := map[string]any{"taskType": taskType, "sku": sku, "minUsableThreshold": threshold}
	if reservation != "" {
		args["reservationId"] = reservation
	}
	if bin != "" {
		args["binId"] = bin
	}
	return w.iCallTheMCPTool(ctx, "detect_stranded_reservation", args)
}

func (w *world) iListTheMCPTools(ctx context.Context) error {
	session, err := w.mcpSession(ctx)
	if err != nil {
		return err
	}
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	w.doc().tools = res.Tools
	return nil
}

// ----------------------------------------------------------------- Then ----

func (w *world) theMCPToolCallSucceeds() error {
	if d := w.doc(); d.toolIsError {
		return fmt.Errorf("expected the tool call to succeed, it failed: %s", d.toolText)
	}
	return nil
}

func (w *world) theMCPToolCallFailsMentioning(fragment string) error {
	d := w.doc()
	if !d.toolIsError {
		return fmt.Errorf("expected the tool call to fail, it answered %s", string(w.body))
	}
	if !strings.Contains(d.toolText, fragment) {
		return fmt.Errorf("expected the tool error to mention %q, got %q", fragment, d.toolText)
	}
	return nil
}

func (w *world) theMCPToolsInclude(name string) error {
	for _, t := range w.doc().tools {
		if t.Name == name {
			return nil
		}
	}
	return fmt.Errorf("expected the MCP tool %q to be advertised, got %s", name, w.toolNames())
}

func (w *world) theMCPToolsDoNotInclude(name string) error {
	for _, t := range w.doc().tools {
		if t.Name == name {
			return fmt.Errorf("expected the MCP tool %q not to be advertised, got %s", name, w.toolNames())
		}
	}
	return nil
}

func (w *world) everyMCPToolIsReadOnly() error {
	tools := w.doc().tools
	if len(tools) == 0 {
		return errors.New("no MCP tool was listed")
	}
	for _, t := range tools {
		if t.Annotations == nil || !t.Annotations.ReadOnlyHint {
			return fmt.Errorf("MCP tool %q is not marked read-only", t.Name)
		}
	}
	return nil
}

func (w *world) toolNames() string {
	var names []string
	for _, t := range w.doc().tools {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}

func (w *world) feWasAskedForStuckTasksWithin(seconds int) error {
	if len(w.fe.windows) == 0 {
		return errors.New("fulfillment-execution was never asked for stuck tasks")
	}
	for _, got := range w.fe.windows {
		if got != seconds {
			return fmt.Errorf("expected every stuck-task lookup to use a window of %d s, got %v", seconds, w.fe.windows)
		}
	}
	return nil
}

func (w *world) lpWasAskedForTaskType(taskType string) error {
	for _, asked := range w.lp.asked {
		if asked == taskType {
			return nil
		}
	}
	return fmt.Errorf("expected labor-performance to be asked for %q, got %v", taskType, w.lp.asked)
}

func (w *world) lpWasNotAsked() error {
	if len(w.lp.asked) != 0 {
		return fmt.Errorf("expected labor-performance not to be asked, got %v", w.lp.asked)
	}
	return nil
}

func (w *world) theUtilizationOverlayIs(kind string) error {
	if kind == "none" {
		return w.theJSONFieldIsAbsent("utilization")
	}
	return w.theJSONFieldEquals("utilization.kind", kind)
}

func (w *world) lokiWasAskedForAtMost(limit int) error {
	if len(w.logs.limits) == 0 {
		return errors.New("Loki was never asked")
	}
	for _, got := range w.logs.limits {
		if got < 1 || got > limit {
			return fmt.Errorf("expected Loki to be asked for at most %d lines, got %v", limit, w.logs.limits)
		}
	}
	return nil
}

func (w *world) productMasterWasAskedForEveryProduct() error {
	if c := w.productMaster.lastQuery.Classified; c != nil {
		return fmt.Errorf("expected product-master to be asked for every product, got classified=%t", *c)
	}
	return nil
}

func (w *world) inboundWasAskedForAppointmentsFromTo(from, to string) error {
	q := w.inbound.apptQuery
	if got := q.From.UTC().Format(time.RFC3339); got != from {
		return fmt.Errorf("expected the appointment lookup to start at %s, got %s", from, got)
	}
	if got := q.To.UTC().Format(time.RFC3339); got != to {
		return fmt.Errorf("expected the appointment lookup to end at %s, got %s", to, got)
	}
	return nil
}

func (w *world) inventoryWasAskedForBinOccupancy(state string) error {
	negation := ""
	if state == "not asked" {
		negation = "not"
	}
	asked := len(w.doc().inv.binAsks) > 0
	if negation == "" && !asked {
		return errors.New("expected inventory-storage to be asked for the bin occupancy, it was not")
	}
	if negation != "" && asked {
		return fmt.Errorf("expected inventory-storage not to be asked for the bin occupancy, got %v", w.doc().inv.binAsks)
	}
	return nil
}

func (w *world) fulfillmentWasNotAskedForTasks() error {
	if len(w.orders.taskAsks) != 0 {
		return fmt.Errorf("expected fulfillment-execution not to be asked for tasks, got %v", w.orders.taskAsks)
	}
	return nil
}

func (w *world) theReleaseAgentReceived(n int) error {
	d := w.doc()
	if d.releaser == nil {
		return errors.New("the release-next-work act slice has not been wired")
	}
	if len(d.releaser.calls) != n {
		return fmt.Errorf("expected the release agent to receive %d request(s), got %d (%v)", n, len(d.releaser.calls), d.releaser.calls)
	}
	return nil
}

func (w *world) theReleaseAgentWasAskedFor(path string) error {
	d := w.doc()
	if d.releaser == nil || len(d.releaser.calls) == 0 || d.releaser.calls[len(d.releaser.calls)-1] != path {
		return fmt.Errorf("expected the release agent to be asked to release path %q, got %v", path, d.releaser.calls)
	}
	return nil
}

func (w *world) theResponseCarriesAConfirmationToken(negation string) error {
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.body, &out)
	if negation == "" && out.Token == "" {
		return fmt.Errorf("expected a confirmation token, got %s", string(w.body))
	}
	if negation != "" && out.Token != "" {
		return fmt.Errorf("expected no confirmation token, got one in %s", string(w.body))
	}
	return nil
}

func registerDocRulesSteps(sc *godog.ScenarioContext, w *world) {
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if d := w.docs; d != nil {
			if d.session != nil {
				_ = d.session.Close()
			}
			if d.mcp != nil {
				d.mcp.Close()
			}
		}
		return ctx, nil
	})

	sc.Step(`^these upstreams are unreachable: "([^"]*)"$`, w.theseUpstreamsAreUnreachable)
	sc.Step(`^workforce-management flags path "([^"]*)" understaffed although (\d+) planned and (\d+) active heads$`, w.workforceFlagsUnderstaffedAlthough)
	sc.Step(`^inventory-storage reports (\d+) usable units of SKU "([^"]*)"$`, w.inventoryReportsUsable)
	sc.Step(`^inventory-storage bin "([^"]*)" holds (\d+) reserved units of SKU "([^"]*)" and (\d+) reserved units of SKU "([^"]*)"$`, w.inventoryBinHolds)
	sc.Step(`^inbound-receiving holds an? "([^"]*)" appointment "([^"]*)" at door "([^"]*)" for carrier "([^"]*)" from "([^"]*)" to "([^"]*)"$`, w.inboundHoldsAppointmentInState)
	sc.Step(`^inbound-receiving holds a Closed receipt "([^"]*)" for ASN "([^"]*)" closed at "([^"]*)" with no discrepancy$`, w.inboundHoldsClosedReceiptWithoutDiscrepancy)
	sc.Step(`^inbound-receiving holds (\d+) pages of one Registered ASN each$`, w.inboundHoldsAsnPages)
	sc.Step(`^product-master holds product "([^"]*)" described as "([^"]*)" with no classification and measured dimensions that disagree with the declared ones$`, w.productMasterHoldsUnclassifiedAndDisagreeing)
	sc.Step(`^product-master fails on the page after the cursor "([^"]*)"$`, w.productMasterFailsAfterCursor)
	sc.Step(`^(\d+) seconds pass$`, w.timePassesSeconds)
	sc.Step(`^the release-next-work act slice is wired to wes-work-planning$`, w.releaseSliceIsWired)
	sc.Step(`^wes-work-planning's release agent (has no pending work|does not know the path|is unreachable)$`, w.theReleaseAgentOutcome)

	sc.Step(`^I ask to release the next work on path "([^"]*)"$`, w.iAskToReleaseTheNextWork)
	sc.Step(`^I confirm the release with the proposed token$`, w.iConfirmTheReleaseWithTheProposedToken)
	sc.Step(`^I confirm the release with the token "([^"]*)"$`, w.iConfirmTheReleaseWith)
	sc.Step(`^I confirm the release with a labor-reallocation token$`, w.iConfirmTheReleaseWithALaborToken)
	sc.Step(`^I call the MCP tool "([^"]*)" with the arguments (\{.*\})$`, w.iCallTheMCPToolWithArguments)
	sc.Step(`^I ask for a stranded-reservation check on task type "([^"]*)" for SKU "([^"]*)" with shortfall threshold (\d+), reservation "([^"]*)" in bin "([^"]*)"$`, w.iAskForAStrandedReservationCheck)
	sc.Step(`^I list the MCP tools$`, w.iListTheMCPTools)

	sc.Step(`^the MCP tool call succeeds$`, w.theMCPToolCallSucceeds)
	sc.Step(`^the MCP tool call fails mentioning "([^"]*)"$`, w.theMCPToolCallFailsMentioning)
	sc.Step(`^the MCP tools include "([^"]*)"$`, w.theMCPToolsInclude)
	sc.Step(`^the MCP tools do not include "([^"]*)"$`, w.theMCPToolsDoNotInclude)
	sc.Step(`^every MCP tool is read-only$`, w.everyMCPToolIsReadOnly)
	sc.Step(`^fulfillment-execution was asked for stuck tasks within (\d+) seconds$`, w.feWasAskedForStuckTasksWithin)
	sc.Step(`^labor-performance was asked for task type "([^"]*)"$`, w.lpWasAskedForTaskType)
	sc.Step(`^labor-performance was not asked$`, w.lpWasNotAsked)
	sc.Step(`^the utilization overlay is "([^"]*)"$`, w.theUtilizationOverlayIs)
	sc.Step(`^Loki was asked for at most (\d+) lines$`, w.lokiWasAskedForAtMost)
	sc.Step(`^product-master was asked for every product$`, w.productMasterWasAskedForEveryProduct)
	sc.Step(`^fulfillment-execution was not asked for any tasks$`, w.fulfillmentWasNotAskedForTasks)
	sc.Step(`^inventory-storage was (asked|not asked) for the bin occupancy$`, w.inventoryWasAskedForBinOccupancy)
	sc.Step(`^inbound-receiving was asked for appointments from "([^"]*)" to "([^"]*)"$`, w.inboundWasAskedForAppointmentsFromTo)
	sc.Step(`^wes-work-planning's release agent received (\d+) requests?$`, w.theReleaseAgentReceived)
	sc.Step(`^wes-work-planning's release agent was asked to release the next work for path "([^"]*)"$`, w.theReleaseAgentWasAskedFor)
	sc.Step(`^the response carries (a|no) confirmation token$`, func(article string) error {
		if article == "a" {
			return w.theResponseCarriesAConfirmationToken("")
		}
		return w.theResponseCarriesAConfirmationToken("no")
	})
}
