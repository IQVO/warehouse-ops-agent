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
