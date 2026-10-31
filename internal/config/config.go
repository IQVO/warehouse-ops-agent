// Package config loads warehouse-ops-agent's runtime configuration from the
// environment: one Streamable-HTTP endpoint per upstream bounded context
// (REST and MCP are unauthenticated fleet-wide — ADR 0006 — so there is no
// bearer-key pair to configure), plus this agent's own listen address and
// the set of process paths the daily brief (E3) monitors. It is
// deliberately dumb (env-var reads, defaults, no validation beyond
// presence, except that a malformed or empty DAILY_BRIEF_PATH_TARGETS is a
// load error) — the composition root (cmd/agent) decides what to do with a
// missing value.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// UpstreamConfig is one upstream context's MCP connection info.
type UpstreamConfig struct {
	Endpoint string
}

// PathTarget identifies one process path the daily brief monitors, binding
// together each upstream context's own naming for "the same" path (see
// internal/domain/policy.PathTarget for why those names are not guaranteed
// equal across contexts). This is deployment-time configuration: which wes
// PathId corresponds to which fulfillment-execution queue and which
// workforce-management (building, shift, path) is an operational fact
// about the fleet, not something the agent can infer.
type PathTarget struct {
	SiteCode    string `json:"siteCode"`
	PathId      string `json:"pathId"`
	ProcessPath string `json:"processPath"`
	BuildingId  string `json:"buildingId"`
	ShiftId     string `json:"shiftId"`

	// PlanningPathId, UnitsPerOrder and PackagesPerOrder are OPTIONAL and
	// only used by the warehouse-planning capacity outlook (ADR 0013).
	// PlanningPathId is the process-path id registered in
	// warehouse-planning (a multi-step path such as PICK→REBIN→PACK; it is
	// not wes-work-planning's PathId, and the site code above is what
	// warehouse-planning calls `location`). UnitsPerOrder / PackagesPerOrder
	// are the workload conversion factors planning needs for UNIT / PACKAGE
	// steps. They are operational facts only the operator knows: there is
	// deliberately NO default for any of the three. An absent factor is not
	// sent, and planning's own missing-conversion-factor error then shows up
	// as the outlook's omitted reason. A pointer distinguishes "unset" from
	// an explicit (invalid) 0, which planning rejects.
	PlanningPathId   string   `json:"planningPathId,omitempty"`
	UnitsPerOrder    *float64 `json:"unitsPerOrder,omitempty"`
	PackagesPerOrder *float64 `json:"packagesPerOrder,omitempty"`
}

// defaultPathTargets mirrors the e2s-tests bootstrap scenario's single
// seeded path (see e2s-tests/features/bootstrap.feature), so the daily
// brief has a sensible default to synthesize against out of the box.
var defaultPathTargets = []PathTarget{
	{SiteCode: "WH1", PathId: "pick-zone-a", ProcessPath: "PICK", BuildingId: "wh1", ShiftId: "shift-1"},
}

// Config is warehouse-ops-agent's full runtime configuration.
type Config struct {
	// Addr is this agent's own listen address, for the daily-brief HTTP
	// and MCP inbound adapters (T4).
	Addr string

	WesWorkPlanning      UpstreamConfig
	FulfillmentExecution UpstreamConfig
	InventoryStorage     UpstreamConfig
	WorkforceManagement  UpstreamConfig
	FacilityLayout       UpstreamConfig

	// OrderManagement and LaborPerformance are the second-wave upstream
	// contexts whose MCP servers came online in a later fleet-wide wiring
	// pass (ADR 0007). Like the five above, an empty endpoint means
	// "skip this client". LaborPerformance has since graduated to consumed
	// by FlowBalanceAdvisory's labor-utilization correlation overlay
	// (ADR 0008 Phase 3); OrderManagement is consumed by the daily brief's
	// promise-health section (ADR 0024). ProcessPathManagement was removed
	// by ADR 0024 (gap A9) and RE-ADDED by ADR 0026, pinned to the
	// current three-tool registry (its surface grew get_cpt_schedule, an
	// actual ops signal): an empty endpoint means "not configured" and the
	// agent boots fail-open exactly as before.
	OrderManagement       UpstreamConfig
	LaborPerformance      UpstreamConfig
	ProcessPathManagement UpstreamConfig

	// WarehousePlanning is the third-wave upstream (ADR 0013). An empty
	// endpoint means "not configured": the composition root then builds no
	// client and no capacity outlook, and the daily brief is unchanged.
	WarehousePlanning UpstreamConfig

	// ProductMaster is product-master's read-only MCP server (ADR 0020),
	// PRODUCT_MASTER_MCP_ENDPOINT. An empty endpoint means "not configured":
	// the composition root builds no client and no master-data-gaps use
	// case (GET /master-data-gaps answers 503, find_master_data_gaps is not
	// registered) and nothing else changes -- the agent boots fail-open.
	ProductMaster UpstreamConfig

	// InboundReceiving is inbound-receiving's read-only MCP server (ADR
	// 0021), INBOUND_RECEIVING_MCP_ENDPOINT. An empty endpoint means "not
	// configured": the composition root builds no client and no inbound
	// outlook (GET /inbound-outlook answers 503, get_inbound_outlook is not
	// registered) and nothing else changes -- the agent boots fail-open.
	InboundReceiving UpstreamConfig

	// InboundStaleReceiptAge is the age past which an OPEN receipt counts as
	// stale in the inbound outlook (INBOUND_STALE_RECEIPT_AGE, a Go
	// duration). It is an operational fact only the operator knows, so
	// there is deliberately NO default: unset (or unparseable / non-positive)
	// is 0 and the outlook then reports the stale-receipt section as
	// "not configured" instead of inventing a threshold.
	InboundStaleReceiptAge time.Duration

	// NetworkInventoryPlanning is the fourth-wave upstream (ADR 0019). An
	// empty endpoint means "not configured": the composition root then
	// builds no client and no transfer watch, the three /transfer-watch
	// routes answer 503 and the three MCP tools are not registered.
	NetworkInventoryPlanning UpstreamConfig

	// SlottingOptimization is slotting-optimization's read-only MCP server
	// (ADR 0024), SLOTTING_OPTIMIZATION_MCP_ENDPOINT. An empty endpoint
	// means "not configured": the composition root builds no client and no
	// slot-plan watch, and the daily brief carries no slot-plan section --
	// the agent boots fail-open.
	SlottingOptimization UpstreamConfig

	// ReturnsProcessing is returns-processing's read-only MCP server,
	// RETURNS_PROCESSING_MCP_ENDPOINT. An empty endpoint means "not
	// configured": the composition root builds no client (fail-open); no
	// use case consumes it yet.
	ReturnsProcessing UpstreamConfig

	// ExceptionManagement is exception-management's read-only MCP server,
	// EXCEPTION_MANAGEMENT_MCP_ENDPOINT. An empty endpoint means "not
	// configured": the composition root builds no client (fail-open); no
	// use case consumes it yet.
	ExceptionManagement UpstreamConfig

	// The twelve ADR-0025 upstreams: the ten retail-network contexts
	// (customer-ordering, delivery-promise, last-mile-delivery,
	// linehaul-transportation, node-performance, node-topology,
	// offer-availability, order-sourcing, shipping-and-tracking,
	// vendor-connectivity) plus task-interleaving and
	// pick-face-replenishment on the WMS side. Each is a read-only MCP
	// server whose client is pinned to its published registry golden. An
	// empty endpoint means "not configured": the composition root builds
	// no client (fail-open, ADR 0023) and nothing else changes -- no use
	// case consumes any of them yet.
	CustomerOrdering       UpstreamConfig
	DeliveryPromise        UpstreamConfig
	LastMileDelivery       UpstreamConfig
	LinehaulTransportation UpstreamConfig
	NodePerformance        UpstreamConfig
	NodeTopology           UpstreamConfig
	OfferAvailability      UpstreamConfig
	OrderSourcing          UpstreamConfig
	ShippingAndTracking    UpstreamConfig
	VendorConnectivity     UpstreamConfig

	// SellerInbound is seller-inbound's read-only MCP server (its four
	// read tools), SELLER_INBOUND_MCP_ENDPOINT. Wired but not consumed by
	// any use case yet (ADR 0027); an empty endpoint means "not
	// configured" and no client is built (fail-open, ADR 0023).
	SellerInbound         UpstreamConfig
	TaskInterleaving      UpstreamConfig
	PickFaceReplenishment UpstreamConfig

	// SlotPlanStaleAge is the approval age past which an Approved slot
	// plan with unexecuted moves is flagged in the daily brief's
	// slot-plan section (SLOT_PLAN_STALE_AGE, a Go duration; default
	// 24h). Unlike INBOUND_STALE_RECEIPT_AGE this HAS a default: a
	// day-old half-executed re-slot is the operational question the
	// section exists to answer, so an unset variable falls back to one
	// day rather than disabling the section.
	SlotPlanStaleAge time.Duration

	// CapacityOutlookHorizon is how far ahead the daily brief's
	// warehouse-planning capacity window extends from now
	// (CAPACITY_OUTLOOK_HORIZON, a Go duration; default 8h). It is a time
	// window, not a workload input; only meaningful when WarehousePlanning
	// is configured.
	CapacityOutlookHorizon time.Duration

	// OrderManagementRESTURL, InventoryStorageRESTURL,
	// WesWorkPlanningRESTURL, FulfillmentExecutionRESTURL are each
	// context's own plain REST base URL, used ONLY by the console-bff's
	// order-lifecycle fan-out (internal/adapters/outbound/restclient) --
	// a separate concern from the MCP upstreams above, which back the
	// daily-brief/flow-balance LLM-facing use cases. Local-dev defaults
	// mirror e2e-tests/env.sh's HTTP_PORT map exactly.
	OrderManagementRESTURL      string
	InventoryStorageRESTURL     string
	WesWorkPlanningRESTURL      string
	FulfillmentExecutionRESTURL string

	// *ReportsRESTURL are each context's ANALYTICS reader base URL, used
	// ONLY by the console-bff's WMS/WES dashboard fan-out
	// (GET /console/reports/wms and /wes). These point at each repo's
	// separate *-reports binary (order-reports, inventory-reports,
	// wes-reports, fulfillment-reports, workforce-reports,
	// facility-reports, labor-reports) -- a different process, backed by
	// a different analytical Postgres, from the OLTP REST URLs above.
	//
	// The local-dev defaults are a NEW port assignment, not an existing
	// fleet convention: every *-reports binary today defaults to the same
	// HTTP_ADDR=":8092" (which also collides with e2e-tests/env.sh's
	// INVENTORY_MCP_PORT), so running more than one locally already
	// requires per-service overrides. The 8101-8107 range below mirrors
	// env.sh's own 8081-8086 OLTP ordering shifted by +20, clear of the
	// existing 8081-8096 OLTP/MCP/agent range. Wiring these into
	// e2e-tests/env.sh and each repo's docker-compose.yml is deliberately
	// out of scope here -- see ADR-0003's Deferred section.
	OrderManagementReportsRESTURL      string
	InventoryStorageReportsRESTURL     string
	WesWorkPlanningReportsRESTURL      string
	FulfillmentExecutionReportsRESTURL string
	WorkforceManagementReportsRESTURL  string
	FacilityLayoutReportsRESTURL       string
	LaborPerformanceReportsRESTURL     string

	// PrometheusURL is the warehouse-infra Prometheus base URL, for the
	// telemetry-reader port's real implementation
	// (internal/adapters/outbound/telemetry.PrometheusReader).
	PrometheusURL string

	// LokiURL is the warehouse-infra Loki base URL, for the log-reader
	// port's real implementation
	// (internal/adapters/outbound/logs.LokiReader). Both PrometheusURL
	// and LokiURL power the runtime-signals report (GET
	// /runtime-signals) -- the runtime feedback loop into the fleet's
	// observability stack (Phase 5 Task 5.3 of the
	// harness-coverage-expansion plan). An empty value here means the
	// corresponding source is reported as unavailable in the report,
	// never a hard failure.
	LokiURL string

	// RuntimeSignalsNamespace is the k8s namespace runtime-signals'
	// Loki log query is scoped to.
	RuntimeSignalsNamespace string

	// RuntimeSignalsServices is the list of service names runtime-signals
	// reports on (their Istio destination_service_name / Loki app label).
	// Overridable via RUNTIME_SIGNALS_SERVICES (comma-separated); falls
	// back to the fleet's 8 backend bounded contexts.
	RuntimeSignalsServices []string

	// PathTargets is the set of process paths the daily brief (E3)
	// monitors. Overridable via DAILY_BRIEF_PATH_TARGETS (a JSON array
	// matching PathTarget's fields); falls back to defaultPathTargets only
	// when unset. A set-but-unparseable or empty (`[]`) value is a Load
	// error, so an operator mistake can never degrade to a default brief.
	PathTargets []PathTarget

	// LLM is the ADR 0004 reasoner configuration. LLM.Mode is validated
	// strictly by the composition root (policy.ParseLLMMode): an unknown
	// value is a startup error, never a silent "off". A non-off mode with
	// no API key is also a startup error, so a misconfigured cluster can
	// never quietly run without the model it was told to use.
	LLM LLMConfig

	// Act is the ADR-0027 human-confirmed write slice (opt-in).
	Act ActConfig

	// ExceptionManagementA2AURL is EXCEPTION_MANAGEMENT_A2A_URL, the base
	// URL of the exception-management A2A agent consulted, read-only and
	// advisory, when a release is proposed (ADR 0028). Empty (the default)
	// disables the lookup: proposals answer exceptionContext.status
	// "disabled" and behave exactly as before. A malformed value is a
	// startup error, never silently dropped.
	ExceptionManagementA2AURL string
}

// ActConfig configures the act slice (ADR 0027): the one human-confirmed
// write this agent can perform. It is opt-in — an empty WorkPlanningA2AURL
// leaves the act routes answering 503 and no write adapter is built.
type ActConfig struct {
	// WorkPlanningA2AURL is WES_WORK_PLANNING_A2A_URL, the base URL of the
	// wes-work-planning A2A agent (its card is discovered under it).
	WorkPlanningA2AURL string
	// SigningKey is APPROVAL_SIGNING_KEY, from a Kubernetes Secret. Never
	// logged. Empty means "generate an ephemeral key at startup", which is
	// only coherent with a single replica (the chart default). If set it
	// must be at least 32 bytes; the signer rejects a weak one at wiring.
	SigningKey string
	// TokenTTL is APPROVAL_TTL, how long a confirmation token lives.
	TokenTTL time.Duration
}

// LLMConfig configures the model-backed Reasoner (ADR 0004).
type LLMConfig struct {
	// Mode is the raw LLM_MODE value: off (default) | shadow | on.
	Mode string
	// APIKey is ANTHROPIC_API_KEY, from a Kubernetes Secret. Never logged.
	APIKey string
	// Model is LLM_MODEL; the adapter applies its own default when empty.
	Model string
	// BaseURL is LLM_BASE_URL, for tests/proxies; empty means the public API.
	BaseURL string
	// Timeout is LLM_TIMEOUT (Go duration), bounding one Reason call.
	Timeout time.Duration
	// ToolAllowList is LLM_TOOL_ALLOWLIST: comma-separated
	// "<upstream>/<tool>" entries the model may call. Defaults to the
	// read tools the deterministic flow-balance path already uses.
	ToolAllowList []string
}

// Load reads Config from the environment. Every field defaults to an empty
// string when its env var is unset; the composition root is responsible for
// deciding whether an empty endpoint/key means "skip this client" or "fail
// closed" for its use case. The one exception is a malformed or empty
// DAILY_BRIEF_PATH_TARGETS, which is returned as an error so the process
// fails at startup instead of silently running with the default target.
func Load() (Config, error) {
	pathTargets, err := loadPathTargets()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Addr: getenv("AGENT_ADDR", ":8095"),

		WesWorkPlanning: UpstreamConfig{
			Endpoint: getenv("WES_WORK_PLANNING_MCP_ENDPOINT", ""),
		},
		FulfillmentExecution: UpstreamConfig{
			Endpoint: getenv("FULFILLMENT_EXECUTION_MCP_ENDPOINT", ""),
		},
		InventoryStorage: UpstreamConfig{
			Endpoint: getenv("INVENTORY_STORAGE_MCP_ENDPOINT", ""),
		},
		WorkforceManagement: UpstreamConfig{
			Endpoint: getenv("WORKFORCE_MANAGEMENT_MCP_ENDPOINT", ""),
		},
		FacilityLayout: UpstreamConfig{
			Endpoint: getenv("FACILITY_LAYOUT_MCP_ENDPOINT", ""),
		},
		OrderManagement: UpstreamConfig{
			Endpoint: getenv("ORDER_MANAGEMENT_MCP_ENDPOINT", ""),
		},
		LaborPerformance: UpstreamConfig{
			Endpoint: getenv("LABOR_PERFORMANCE_MCP_ENDPOINT", ""),
		},
		ProcessPathManagement: UpstreamConfig{
			Endpoint: getenv("PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT", ""),
		},
		WarehousePlanning: UpstreamConfig{
			Endpoint: getenv("WAREHOUSE_PLANNING_MCP_ENDPOINT", ""),
		},
		ProductMaster: UpstreamConfig{
			Endpoint: getenv("PRODUCT_MASTER_MCP_ENDPOINT", ""),
		},
		InboundReceiving: UpstreamConfig{
			Endpoint: getenv("INBOUND_RECEIVING_MCP_ENDPOINT", ""),
		},
		InboundStaleReceiptAge: loadDuration("INBOUND_STALE_RECEIPT_AGE", 0),
		NetworkInventoryPlanning: UpstreamConfig{
			Endpoint: getenv("NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT", ""),
		},
		SlottingOptimization: UpstreamConfig{
			Endpoint: getenv("SLOTTING_OPTIMIZATION_MCP_ENDPOINT", ""),
		},
		ReturnsProcessing:      UpstreamConfig{Endpoint: getenv("RETURNS_PROCESSING_MCP_ENDPOINT", "")},
		ExceptionManagement:    UpstreamConfig{Endpoint: getenv("EXCEPTION_MANAGEMENT_MCP_ENDPOINT", "")},
		SlotPlanStaleAge:       loadDuration("SLOT_PLAN_STALE_AGE", defaultSlotPlanStaleAge),
		CapacityOutlookHorizon: loadDuration("CAPACITY_OUTLOOK_HORIZON", defaultCapacityOutlookHorizon),

		OrderManagementRESTURL:      getenv("ORDER_MANAGEMENT_REST_URL", "http://localhost:8086"),
		InventoryStorageRESTURL:     getenv("INVENTORY_STORAGE_REST_URL", "http://localhost:8082"),
		WesWorkPlanningRESTURL:      getenv("WES_WORK_PLANNING_REST_URL", "http://localhost:8083"),
		FulfillmentExecutionRESTURL: getenv("FULFILLMENT_EXECUTION_REST_URL", "http://localhost:8084"),

		PrometheusURL: getenv("PROMETHEUS_URL", ""),
		LokiURL:       getenv("LOKI_URL", ""),

		RuntimeSignalsNamespace: getenv("RUNTIME_SIGNALS_NAMESPACE", "warehouse-systems"),
		RuntimeSignalsServices:  loadRuntimeSignalsServices(),

		PathTargets: pathTargets,

		LLM: LLMConfig{
			Mode:          getenv("LLM_MODE", "off"),
			APIKey:        os.Getenv("ANTHROPIC_API_KEY"),
			Model:         os.Getenv("LLM_MODEL"),
			BaseURL:       os.Getenv("LLM_BASE_URL"),
			Timeout:       loadDuration("LLM_TIMEOUT", 8*time.Second),
			ToolAllowList: loadList("LLM_TOOL_ALLOWLIST", defaultLLMToolAllowList),
		},
	}
	loadNetworkFleetEndpoints(&cfg)
	loadReportsURLs(&cfg)
	if err = loadA2ASettings(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// loadA2ASettings reads the settings of the two outbound A2A features onto
// cfg: the ADR-0027 act slice and the ADR-0028 advisory exception lookup.
func loadA2ASettings(cfg *Config) (err error) {
	if cfg.Act, err = loadAct(); err != nil {
		return err
	}
	cfg.ExceptionManagementA2AURL, err = loadOptionalHTTPURL("EXCEPTION_MANAGEMENT_A2A_URL")
	return err
}

// loadOptionalHTTPURL reads an optional absolute http(s) URL. Unset or empty
// is fine (the feature is off); anything else must parse as an absolute
// http or https URL with a host, or Load fails: a mistyped endpoint must
// stop startup, not quietly turn a feature off.
func loadOptionalHTTPURL(key string) (string, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("config: %s %q must be an absolute http(s) URL (e.g. http://exception-management-a2a.ns.svc.cluster.local:8093/)", key, raw)
	}
	return raw, nil
}

// loadReportsURLs reads the console-bff analytics-reader base URLs (see
// Config's own doc comment for the port-assignment history) onto cfg.
func loadReportsURLs(cfg *Config) {
	cfg.FacilityLayoutReportsRESTURL = getenv("FACILITY_LAYOUT_REPORTS_REST_URL", "http://localhost:8101")
	cfg.InventoryStorageReportsRESTURL = getenv("INVENTORY_STORAGE_REPORTS_REST_URL", "http://localhost:8102")
	cfg.WesWorkPlanningReportsRESTURL = getenv("WES_WORK_PLANNING_REPORTS_REST_URL", "http://localhost:8103")
	cfg.FulfillmentExecutionReportsRESTURL = getenv("FULFILLMENT_EXECUTION_REPORTS_REST_URL", "http://localhost:8104")
	cfg.WorkforceManagementReportsRESTURL = getenv("WORKFORCE_MANAGEMENT_REPORTS_REST_URL", "http://localhost:8105")
	cfg.OrderManagementReportsRESTURL = getenv("ORDER_MANAGEMENT_REPORTS_REST_URL", "http://localhost:8106")
	cfg.LaborPerformanceReportsRESTURL = getenv("LABOR_PERFORMANCE_REPORTS_REST_URL", "http://localhost:8107")
}

// loadNetworkFleetEndpoints reads the twelve ADR-0025 upstream endpoints
// (the ten retail-network contexts plus task-interleaving and
// pick-face-replenishment) onto cfg. Kept as one helper so Load's own
// literal stays readable; every env name matches the chart's rendered
// name exactly.
func loadNetworkFleetEndpoints(cfg *Config) {
	cfg.CustomerOrdering = UpstreamConfig{Endpoint: getenv("CUSTOMER_ORDERING_MCP_ENDPOINT", "")}
	cfg.DeliveryPromise = UpstreamConfig{Endpoint: getenv("DELIVERY_PROMISE_MCP_ENDPOINT", "")}
	cfg.LastMileDelivery = UpstreamConfig{Endpoint: getenv("LAST_MILE_DELIVERY_MCP_ENDPOINT", "")}
	cfg.LinehaulTransportation = UpstreamConfig{Endpoint: getenv("LINEHAUL_TRANSPORTATION_MCP_ENDPOINT", "")}
	cfg.NodePerformance = UpstreamConfig{Endpoint: getenv("NODE_PERFORMANCE_MCP_ENDPOINT", "")}
	cfg.NodeTopology = UpstreamConfig{Endpoint: getenv("NODE_TOPOLOGY_MCP_ENDPOINT", "")}
	cfg.OfferAvailability = UpstreamConfig{Endpoint: getenv("OFFER_AVAILABILITY_MCP_ENDPOINT", "")}
	cfg.OrderSourcing = UpstreamConfig{Endpoint: getenv("ORDER_SOURCING_MCP_ENDPOINT", "")}
	cfg.ShippingAndTracking = UpstreamConfig{Endpoint: getenv("SHIPPING_AND_TRACKING_MCP_ENDPOINT", "")}
	cfg.VendorConnectivity = UpstreamConfig{Endpoint: getenv("VENDOR_CONNECTIVITY_MCP_ENDPOINT", "")}
	cfg.SellerInbound = UpstreamConfig{Endpoint: getenv("SELLER_INBOUND_MCP_ENDPOINT", "")}
	cfg.TaskInterleaving = UpstreamConfig{Endpoint: getenv("TASK_INTERLEAVING_MCP_ENDPOINT", "")}
	cfg.PickFaceReplenishment = UpstreamConfig{Endpoint: getenv("PICK_FACE_REPLENISHMENT_MCP_ENDPOINT", "")}
}

// defaultCapacityOutlookHorizon is one shift: the capacity outlook looks
// 8 hours ahead unless CAPACITY_OUTLOOK_HORIZON says otherwise.
const defaultCapacityOutlookHorizon = 8 * time.Hour

// defaultSlotPlanStaleAge is one day: an Approved slot plan whose moves are
// still not fully executed a day after approval is flagged, unless
// SLOT_PLAN_STALE_AGE says otherwise.
const defaultSlotPlanStaleAge = 24 * time.Hour

// defaultLLMToolAllowList is exactly the read surface the deterministic
// flow-balance path consults, so shadow mode compares like with like.
var defaultLLMToolAllowList = []string{
	"wes-work-planning/get_backlog_telemetry",
	"wes-work-planning/get_rebalance_recommendation",
	"workforce-management/get_staffing_gap",
	"fulfillment-execution/get_queue_status",
	"fulfillment-execution/diagnose_stuck_tasks",
}

// defaultApprovalTTL is how long a confirmation token lives when
// APPROVAL_TTL is unset: long enough for a human to read the evidence,
// short enough that a stale recommendation cannot be redeemed.
const defaultApprovalTTL = 5 * time.Minute

// loadAct reads the act-slice settings. Unlike loadDuration it REJECTS a
// malformed or non-positive APPROVAL_TTL: a mistyped confirmation lifetime
// must stop startup, not quietly become a different security parameter.
func loadAct() (ActConfig, error) {
	ttl := defaultApprovalTTL
	if raw := os.Getenv("APPROVAL_TTL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return ActConfig{}, fmt.Errorf("config: APPROVAL_TTL %q must be a positive Go duration (e.g. 5m)", raw)
		}
		ttl = d
	}
	return ActConfig{
		WorkPlanningA2AURL: getenv("WES_WORK_PLANNING_A2A_URL", ""),
		SigningKey:         os.Getenv("APPROVAL_SIGNING_KEY"),
		TokenTTL:           ttl,
	}, nil
}

func loadDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func loadList(key string, fallback []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// loadPathTargets parses DAILY_BRIEF_PATH_TARGETS as a JSON array of
// PathTarget. Unset uses defaultPathTargets. A set value that is not valid
// JSON for []PathTarget, or that is an empty array (or null), is an operator
// error and is returned as a config error so startup fails loudly: silently
// falling back to the default would hide the misconfiguration and brief
// the wrong paths (audit decision 2026-10-06, ADR 0017).
func loadPathTargets() ([]PathTarget, error) {
	raw := os.Getenv("DAILY_BRIEF_PATH_TARGETS")
	if raw == "" {
		return defaultPathTargets, nil
	}
	var targets []PathTarget
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		return nil, fmt.Errorf("config: DAILY_BRIEF_PATH_TARGETS is not a valid JSON array of path targets: %w", err)
	}
	if len(targets) == 0 {
		return nil, errors.New("config: DAILY_BRIEF_PATH_TARGETS is set but lists no path targets (an empty array monitors nothing); unset the variable to use the built-in default target, or list at least one target")
	}
	return targets, nil
}

// defaultRuntimeSignalsServices is the fleet's 8 backend bounded contexts
// -- the same set runtime-signals reports on out of the box, absent an
// explicit RUNTIME_SIGNALS_SERVICES override.
var defaultRuntimeSignalsServices = []string{
	"order-management", "inventory-storage", "wes-work-planning",
	"fulfillment-execution", "workforce-management", "facility-layout",
	"labor-performance", "process-path-management",
}

func loadRuntimeSignalsServices() []string {
	raw := os.Getenv("RUNTIME_SIGNALS_SERVICES")
	if raw == "" {
		return defaultRuntimeSignalsServices
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return defaultRuntimeSignalsServices
	}
	return out
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
