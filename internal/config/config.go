// Package config loads warehouse-ops-agent's runtime configuration from the
// environment: one Streamable-HTTP endpoint + static bearer read-key pair
// per upstream bounded context, plus this agent's own listen address and
// the set of process paths the daily brief (E3) monitors. It is
// deliberately dumb (env-var reads, defaults, no validation beyond
// presence) — the composition root (cmd/agent) decides what to do with a
// missing value.
package config

import (
	"encoding/json"
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

	// OrderManagement, LaborPerformance, ProcessPathManagement are the
	// second-wave upstream contexts whose MCP servers came online in a
	// later fleet-wide wiring pass. Like the five above, an empty
	// endpoint means "skip this client"; the composition root wires
	// them as available dependencies even before any use case consumes
	// them, matching the existing InventoryStorageClient
	// wired-but-unconsumed precedent.
	OrderManagement       UpstreamConfig
	LaborPerformance      UpstreamConfig
	ProcessPathManagement UpstreamConfig

	// WarehousePlanning is the third-wave upstream (ADR 0013). An empty
	// endpoint means "not configured": the composition root then builds no
	// client and no capacity outlook, and the daily brief is unchanged.
	WarehousePlanning UpstreamConfig

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
	// matching PathTarget's fields); falls back to defaultPathTargets when
	// unset or unparseable, so an operator error degrades to a working
	// default rather than an empty, useless brief.
	PathTargets []PathTarget

	// LLM is the ADR 0004 reasoner configuration. LLM.Mode is validated
	// strictly by the composition root (policy.ParseLLMMode): an unknown
	// value is a startup error, never a silent "off". A non-off mode with
	// no API key is also a startup error, so a misconfigured cluster can
	// never quietly run without the model it was told to use.
	LLM LLMConfig
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
// closed" for its use case.
func Load() Config {
	return Config{
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
		CapacityOutlookHorizon: loadDuration("CAPACITY_OUTLOOK_HORIZON", defaultCapacityOutlookHorizon),

		OrderManagementRESTURL:      getenv("ORDER_MANAGEMENT_REST_URL", "http://localhost:8086"),
		InventoryStorageRESTURL:     getenv("INVENTORY_STORAGE_REST_URL", "http://localhost:8082"),
		WesWorkPlanningRESTURL:      getenv("WES_WORK_PLANNING_REST_URL", "http://localhost:8083"),
		FulfillmentExecutionRESTURL: getenv("FULFILLMENT_EXECUTION_REST_URL", "http://localhost:8084"),

		FacilityLayoutReportsRESTURL:       getenv("FACILITY_LAYOUT_REPORTS_REST_URL", "http://localhost:8101"),
		InventoryStorageReportsRESTURL:     getenv("INVENTORY_STORAGE_REPORTS_REST_URL", "http://localhost:8102"),
		WesWorkPlanningReportsRESTURL:      getenv("WES_WORK_PLANNING_REPORTS_REST_URL", "http://localhost:8103"),
		FulfillmentExecutionReportsRESTURL: getenv("FULFILLMENT_EXECUTION_REPORTS_REST_URL", "http://localhost:8104"),
		WorkforceManagementReportsRESTURL:  getenv("WORKFORCE_MANAGEMENT_REPORTS_REST_URL", "http://localhost:8105"),
		OrderManagementReportsRESTURL:      getenv("ORDER_MANAGEMENT_REPORTS_REST_URL", "http://localhost:8106"),
		LaborPerformanceReportsRESTURL:     getenv("LABOR_PERFORMANCE_REPORTS_REST_URL", "http://localhost:8107"),

		PrometheusURL: getenv("PROMETHEUS_URL", ""),
		LokiURL:       getenv("LOKI_URL", ""),

		RuntimeSignalsNamespace: getenv("RUNTIME_SIGNALS_NAMESPACE", "warehouse-systems"),
		RuntimeSignalsServices:  loadRuntimeSignalsServices(),

		PathTargets: loadPathTargets(),

		LLM: LLMConfig{
			Mode:          getenv("LLM_MODE", "off"),
			APIKey:        os.Getenv("ANTHROPIC_API_KEY"),
			Model:         os.Getenv("LLM_MODEL"),
			BaseURL:       os.Getenv("LLM_BASE_URL"),
			Timeout:       loadDuration("LLM_TIMEOUT", 8*time.Second),
			ToolAllowList: loadList("LLM_TOOL_ALLOWLIST", defaultLLMToolAllowList),
		},
	}
}

// defaultCapacityOutlookHorizon is one shift: the capacity outlook looks
// 8 hours ahead unless CAPACITY_OUTLOOK_HORIZON says otherwise.
const defaultCapacityOutlookHorizon = 8 * time.Hour

// defaultLLMToolAllowList is exactly the read surface the deterministic
// flow-balance path consults, so shadow mode compares like with like.
var defaultLLMToolAllowList = []string{
	"wes-work-planning/get_backlog_telemetry",
	"wes-work-planning/get_rebalance_recommendation",
	"workforce-management/get_staffing_gap",
	"fulfillment-execution/get_queue_status",
	"fulfillment-execution/diagnose_stuck_tasks",
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
// PathTarget, falling back to defaultPathTargets when the env var is unset
// or fails to parse. A malformed override must never silently produce an
// empty (and therefore useless) daily brief.
func loadPathTargets() []PathTarget {
	raw := os.Getenv("DAILY_BRIEF_PATH_TARGETS")
	if raw == "" {
		return defaultPathTargets
	}
	var targets []PathTarget
	if err := json.Unmarshal([]byte(raw), &targets); err != nil || len(targets) == 0 {
		return defaultPathTargets
	}
	return targets
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
