// Package main_test hosts the godog (Cucumber for Go) acceptance suite. It
// drives the REAL chi router over HTTP -- the same inboundhttp.NewRouter and the
// same use cases cmd/agent wires in production -- but with fake outbound
// adapters in place of every MCP client, REST client, telemetry reader and
// model, so every scenario in features/*.feature is a black-box test of the
// REST API that never touches a real LLM, a bounded context or the network
// beyond loopback.
package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// TestFeatures runs every Gherkin feature under features/ against a freshly
// wired HTTP server.
func TestFeatures(t *testing.T) {
	// The CORS middleware reads this variable when the router is built; pin the
	// default (console) origin whatever the developer's shell exports.
	t.Setenv("CORS_ALLOWED_ORIGINS", "")

	// The use cases log a warning for every deliberately degraded upstream;
	// keep the acceptance output readable.
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// response is what the last HTTP call returned.
type response struct {
	status  int
	body    []byte
	headers http.Header
	sent    http.Header
	elapsed time.Duration
}

// world is the per-scenario state: one fake per upstream context, the
// capabilities that are deliberately left unwired, the running server and the
// last response.
type world struct {
	now time.Time

	// MCP upstreams of the decision-support use cases.
	wes      *fakeWes
	wfm      *fakeWFM
	fe       *fakeFE
	facility *fakeFacility
	lp       *fakeLP

	// Reasoner path (ADR 0004 / 0011).
	llmMode         policy.LLMMode
	reasoner        *fakeReasoner
	metrics         *fakeMetrics
	stand           *fakeAnthropic
	standReasoner   ports.Reasoner
	llmTimeout      time.Duration
	breakerCooldown time.Duration

	// Console BFF upstreams (REST).
	orders  *fakeOrderStack
	reports *fakeReports

	// Observability.
	telemetry       *fakeTelemetry
	logs            *fakeLogs
	runtimeServices []string

	// Optional MCP contexts.
	productMaster *fakeProductMaster
	nip           *fakeNIP
	inbound       *fakeInbound
	staleAge      time.Duration

	targets  []usecases.PathTarget
	bindings map[string]string
	unwired  map[string]bool

	server *httptest.Server
	response
}

func newWorld() *world {
	return &world{
		now:           time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
		wes:           newFakeWes(),
		wfm:           newFakeWFM(),
		fe:            &fakeFE{queues: map[string]int{}},
		facility:      newFakeFacility(),
		lp:            &fakeLP{util: map[string]ports.TaskTypeUtilization{}},
		llmMode:       policy.LLMOff,
		metrics:       &fakeMetrics{},
		orders:        newFakeOrderStack(),
		reports:       newFakeReports(),
		telemetry:     &fakeTelemetry{stats: map[string]serviceStats{}},
		logs:          &fakeLogs{},
		productMaster: &fakeProductMaster{},
		nip:           &fakeNIP{details: map[string]ports.TransferDetail{}},
		inbound:       &fakeInbound{},
		bindings:      map[string]string{},
		unwired:       map[string]bool{},
	}
}

// start builds the composition root the way cmd/agent does -- the same use
// cases, the same inboundhttp.NewRouter -- but over fakes, and serves it on a
// real loopback listener. It is lazy so Given steps can still configure the
// fakes and the wiring before the first request.
func (w *world) start() error {
	if w.server != nil {
		return nil
	}
	clock := func() time.Time { return w.now }
	h := &inboundhttp.Handlers{
		DailyBrief: &usecases.DailyBrief{Facility: w.facility, Wes: w.wes, Fe: w.fe, Wfm: w.wfm, Targets: w.targets, Now: clock},
	}
	if !w.unwired["flow-balance"] {
		advisory, err := w.flowBalance()
		if err != nil {
			return err
		}
		h.FlowBalanceAdvisory = advisory
	}
	if !w.unwired["explain-travel-factor"] {
		h.ExplainTravelFactor = &usecases.ExplainTravelFactor{Facility: w.facility}
	}
	if !w.unwired["order-lifecycle"] {
		h.OrderLifecycle = w.orderLifecycle()
	}
	if !w.unwired["console-reports"] {
		h.ConsoleReports = w.consoleReports(clock)
	}
	if !w.unwired["runtime-signals"] {
		h.RuntimeSignals = w.runtimeSignals(clock)
	}
	if !w.unwired["master-data-gaps"] {
		h.MasterDataGaps = &usecases.MasterDataGaps{ProductMaster: w.productMaster}
	}
	if !w.unwired["inbound-outlook"] {
		h.InboundOutlook = &usecases.InboundOutlook{Inbound: w.inbound, StaleReceiptAge: w.staleAge, Now: clock}
	}
	if !w.unwired["transfer-watch"] {
		h.TransferWatch = &usecases.TransferWatch{NIP: w.nip}
	}
	w.server = httptest.NewServer(inboundhttp.NewRouter(h, "warehouse-ops-agent"))
	return nil
}

func (w *world) stop() {
	if w.server != nil {
		w.server.Close()
		w.server = nil
	}
	if w.stand != nil {
		w.stand.close()
	}
}

func (w *world) pathTaskTypes() map[string]string {
	out := map[string]string{}
	for _, t := range w.targets {
		out[t.PathId] = t.ProcessPath
	}
	for path, processPath := range w.bindings {
		out[path] = processPath
	}
	return out
}

func (w *world) flowBalance() (*usecases.FlowBalanceAdvisory, error) {
	uc := &usecases.FlowBalanceAdvisory{
		Wes:           w.wes,
		WFM:           w.wfm,
		FE:            w.fe,
		LP:            w.lp,
		PathTaskTypes: w.pathTaskTypes(),
		LLMMode:       w.llmMode,
		Metrics:       w.metrics,
	}
	switch {
	case w.stand != nil:
		if w.standReasoner == nil {
			r, err := w.stand.newReasoner(w.llmTimeout, w.breakerCooldown)
			if err != nil {
				return nil, err
			}
			w.standReasoner = r
		}
		uc.Reasoner = w.standReasoner
	case w.reasoner != nil:
		uc.Reasoner = w.reasoner
	}
	return uc, nil
}

func (w *world) orderLifecycle() *usecases.OrderLifecycle {
	var orderManagement ports.OrderManagementClient = w.orders
	return &usecases.OrderLifecycle{OrderManagement: &orderManagement, Inventory: w.orders, WorkUnits: w.orders, Tasks: w.orders}
}

func (w *world) consoleReports(clock func() time.Time) *usecases.ConsoleReports {
	cr := &usecases.ConsoleReports{Now: clock}
	if !w.unwired["reports:order-management"] {
		cr.OrderFunnel = w.reports
	}
	if !w.unwired["reports:inventory-storage"] {
		cr.InventoryFlowAccuracy = w.reports
	}
	if !w.unwired["reports:facility-layout"] {
		cr.CatalogGrowth = w.reports
	}
	if !w.unwired["reports:wes-work-planning"] {
		cr.PlanningThroughput = w.reports
	}
	if !w.unwired["reports:fulfillment-execution"] {
		cr.FulfillmentThroughput = w.reports
	}
	if !w.unwired["reports:workforce-management"] {
		cr.Labor = w.reports
	}
	if !w.unwired["reports:labor-performance"] {
		cr.LaborPerformance = w.reports
	}
	return cr
}

func (w *world) runtimeSignals(clock func() time.Time) *usecases.RuntimeSignals {
	uc := &usecases.RuntimeSignals{Services: w.runtimeServices, Namespace: "warehouse-systems", WindowMinutes: 10, Now: clock}
	if !w.unwired["prometheus"] {
		uc.Telemetry = w.telemetry
	}
	if !w.unwired["loki"] {
		uc.Logs = w.logs
	}
	return uc
}

// do issues a real net/http request against the test server and remembers the
// response the Then steps assert against.
func (w *world) do(ctx context.Context, method, target string, header map[string]string) error {
	if err := w.start(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, w.server.URL+target, http.NoBody)
	if err != nil {
		return err
	}
	for name, value := range header {
		req.Header.Set(name, value)
	}
	started := time.Now()
	resp, err := w.server.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	w.response = response{status: resp.StatusCode, body: body, headers: resp.Header, sent: req.Header.Clone(), elapsed: time.Since(started)}
	return nil
}

func (w *world) get(ctx context.Context, target string) error {
	return w.do(ctx, http.MethodGet, target, nil)
}

// jsonAt resolves a dotted path ("sites.0.paths.0.backlog") in the last JSON
// response. found is false when any segment is missing; a JSON null is found
// with a nil value.
func (w *world) jsonAt(path string) (value any, found bool, err error) {
	var doc any
	if err := json.Unmarshal(w.body, &doc); err != nil {
		return nil, false, fmt.Errorf("response body is not valid JSON (%w): %s", err, string(w.body))
	}
	cur := doc
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false, nil
			}
			cur = next
		case []any:
			idx, convErr := strconv.Atoi(part)
			if convErr != nil || idx < 0 || idx >= len(node) {
				return nil, false, nil
			}
			cur = node[idx]
		default:
			return nil, false, nil
		}
	}
	return cur, true, nil
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return "null"
	default:
		raw, _ := json.Marshal(t)
		return string(raw)
	}
}

func (w *world) arrayAt(path string) ([]any, error) {
	v, found, err := w.jsonAt(path)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("expected a JSON array at %q, but the field is absent: %s", path, string(w.body))
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON array at %q, got %s", path, scalar(v))
	}
	return arr, nil
}

// ---------------------------------------------------------------- Given ----

func (w *world) aWarehouseOpsAgentWiredToFakeUpstreams() error {
	if w.server != nil {
		return fmt.Errorf("expected a fresh agent, but one is already serving")
	}
	return nil
}

func (w *world) theClockIsFixedAt(instant string) error {
	t, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		return err
	}
	w.now = t
	return nil
}

// capabilityKeys are the optional use cases the composition root can leave
// unwired (cmd/agent wires them only when their endpoint is configured).
var capabilityKeys = map[string]bool{
	"flow-balance": true, "explain-travel-factor": true, "order-lifecycle": true,
	"console-reports": true, "runtime-signals": true, "master-data-gaps": true,
	"inbound-outlook": true, "transfer-watch": true,
}

func (w *world) capabilityIsNotConfigured(key string) error {
	if !capabilityKeys[key] {
		return fmt.Errorf("unknown capability %q", key)
	}
	w.unwired[key] = true
	return nil
}

// ----------------------------------------------------------------- When ----

func (w *world) iGET(ctx context.Context, target string) error { return w.get(ctx, target) }

func (w *world) iSendARequestTo(ctx context.Context, method, target string) error {
	return w.do(ctx, method, target, nil)
}

func (w *world) iSendACORSPreflight(ctx context.Context, origin, method, target string) error {
	return w.do(ctx, http.MethodOptions, target, map[string]string{
		"Origin":                         origin,
		"Access-Control-Request-Method":  method,
		"Access-Control-Request-Headers": "content-type",
	})
}

// ----------------------------------------------------------------- Then ----

func (w *world) theResponseStatusIs(expected int) error {
	if w.status != expected {
		return fmt.Errorf("expected status %d, got %d: %s", expected, w.status, string(w.body))
	}
	return nil
}

func (w *world) theResponseStatusIsNeither401Nor403() error {
	if w.status == http.StatusUnauthorized || w.status == http.StatusForbidden {
		return fmt.Errorf("expected no authentication challenge, got %d: %s", w.status, string(w.body))
	}
	return nil
}

func (w *world) theRequestCarriedNoAuthorizationHeader() error {
	if got := w.sent.Get("Authorization"); got != "" {
		return fmt.Errorf("the step itself sent an Authorization header %q", got)
	}
	return nil
}

func (w *world) theResponseIsJSON() error {
	if ct := w.headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return fmt.Errorf("expected a JSON Content-Type, got %q", ct)
	}
	var doc any
	return json.Unmarshal(w.body, &doc)
}

func (w *world) theResponseHeaderEquals(name, expected string) error {
	if got := w.headers.Get(name); got != expected {
		return fmt.Errorf("expected response header %s=%q, got %q", name, expected, got)
	}
	return nil
}

func (w *world) theResponseHeaderIsAbsent(name string) error {
	if got := w.headers.Get(name); got != "" {
		return fmt.Errorf("expected no %s response header, got %q", name, got)
	}
	return nil
}

func (w *world) theResponseErrorMentions(fragment string) error {
	return w.theJSONFieldContains("error", fragment)
}

func (w *world) theJSONFieldEquals(path, expected string) error {
	v, found, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("expected %q to equal %q, but the field is absent: %s", path, expected, string(w.body))
	}
	if got := scalar(v); got != expected {
		return fmt.Errorf("expected %q to equal %q, got %q", path, expected, got)
	}
	return nil
}

func (w *world) theJSONFieldContains(path, fragment string) error {
	v, found, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("expected %q to contain %q, but the field is absent: %s", path, fragment, string(w.body))
	}
	if got := scalar(v); !strings.Contains(got, fragment) {
		return fmt.Errorf("expected %q to contain %q, got %q", path, fragment, got)
	}
	return nil
}

func (w *world) theJSONFieldIsBool(path, expected string) error {
	return w.theJSONFieldEquals(path, expected)
}

func (w *world) theJSONFieldIsNull(path string) error {
	v, found, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	if !found || v != nil {
		return fmt.Errorf("expected %q to be an explicit JSON null, found=%t value=%s", path, found, scalar(v))
	}
	return nil
}

func (w *world) theJSONFieldIsAbsent(path string) error {
	v, found, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("expected %q to be absent, got %s", path, scalar(v))
	}
	return nil
}

func (w *world) theJSONArrayHasItems(path string, n int) error {
	arr, err := w.arrayAt(path)
	if err != nil {
		return err
	}
	if len(arr) != n {
		return fmt.Errorf("expected %q to have %d item(s), got %d: %s", path, n, len(arr), scalar(arr))
	}
	return nil
}

func (w *world) theJSONArrayIsEmpty(path string) error { return w.theJSONArrayHasItems(path, 0) }

func (w *world) theJSONArrayHasTheItem(path, item string) error {
	arr, err := w.arrayAt(path)
	if err != nil {
		return err
	}
	for _, v := range arr {
		if scalar(v) == item {
			return nil
		}
	}
	return fmt.Errorf("expected %q to have the item %q, got %s", path, item, scalar(arr))
}

func (w *world) theJSONArrayHasAnItemContaining(path, fragment string) error {
	arr, err := w.arrayAt(path)
	if err != nil {
		return err
	}
	for _, v := range arr {
		if strings.Contains(scalar(v), fragment) {
			return nil
		}
	}
	return fmt.Errorf("expected %q to have an item containing %q, got %s", path, fragment, scalar(arr))
}

// ------------------------------------------------------------- wiring ------

// InitializeScenario registers the step definitions and gives every scenario
// its own fakes, its own server and its own in-memory state.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = *newWorld()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.stop()
		return ctx, nil
	})

	registerCommonSteps(sc, w)
	registerAdvisorySteps(sc, w)
	registerReasonerSteps(sc, w)
	registerConsoleSteps(sc, w)
	registerOptionalContextSteps(sc, w)
}

func registerCommonSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a warehouse-ops-agent wired to fake upstream contexts$`, w.aWarehouseOpsAgentWiredToFakeUpstreams)
	sc.Step(`^the clock is fixed at "([^"]*)"$`, w.theClockIsFixedAt)
	sc.Step(`^the "([^"]*)" capability is not configured$`, w.capabilityIsNotConfigured)

	sc.Step(`^I GET "([^"]*)"$`, w.iGET)
	sc.Step(`^I send a (POST|PUT|DELETE|PATCH) request to "([^"]*)"$`, w.iSendARequestTo)
	sc.Step(`^I send a CORS preflight from origin "([^"]*)" for method "([^"]*)" on "([^"]*)"$`, w.iSendACORSPreflight)

	sc.Step(`^the response status is (\d+)$`, w.theResponseStatusIs)
	sc.Step(`^the response status is neither 401 nor 403$`, w.theResponseStatusIsNeither401Nor403)
	sc.Step(`^the request carried no Authorization header$`, w.theRequestCarriedNoAuthorizationHeader)
	sc.Step(`^the response is JSON$`, w.theResponseIsJSON)
	sc.Step(`^the response header "([^"]*)" equals "([^"]*)"$`, w.theResponseHeaderEquals)
	sc.Step(`^the response header "([^"]*)" is absent$`, w.theResponseHeaderIsAbsent)
	sc.Step(`^the response error mentions "([^"]*)"$`, w.theResponseErrorMentions)

	sc.Step(`^the JSON field "([^"]*)" equals "([^"]*)"$`, w.theJSONFieldEquals)
	sc.Step(`^the JSON field "([^"]*)" contains "([^"]*)"$`, w.theJSONFieldContains)
	sc.Step(`^the JSON field "([^"]*)" is (true|false)$`, w.theJSONFieldIsBool)
	sc.Step(`^the JSON field "([^"]*)" is null$`, w.theJSONFieldIsNull)
	sc.Step(`^the JSON field "([^"]*)" is absent$`, w.theJSONFieldIsAbsent)
	sc.Step(`^the JSON array "([^"]*)" has (\d+) items?$`, w.theJSONArrayHasItems)
	sc.Step(`^the JSON array "([^"]*)" is empty$`, w.theJSONArrayIsEmpty)
	sc.Step(`^the JSON array "([^"]*)" has the item "([^"]*)"$`, w.theJSONArrayHasTheItem)
	sc.Step(`^the JSON array "([^"]*)" has an item containing "([^"]*)"$`, w.theJSONArrayHasAnItemContaining)
}
