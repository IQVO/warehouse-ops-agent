package config

import (
	"strings"
	"testing"
	"time"
)

// mustLoad calls Load and fails the test on a config error.
func mustLoad(t *testing.T) Config {
	t.Helper()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	return cfg
}

func TestLoad_ProductMaster_UnsetIsNotConfigured(t *testing.T) {
	t.Setenv("PRODUCT_MASTER_MCP_ENDPOINT", "")
	if got := mustLoad(t).ProductMaster.Endpoint; got != "" {
		t.Errorf("an unset PRODUCT_MASTER_MCP_ENDPOINT must stay empty (= client disabled, fail-open), got %q", got)
	}
}

func TestLoad_ProductMaster_ReadsEndpoint(t *testing.T) {
	const url = "http://product-master-mcp.warehouse-systems.svc.cluster.local:8090/mcp"
	t.Setenv("PRODUCT_MASTER_MCP_ENDPOINT", url)
	if got := mustLoad(t).ProductMaster.Endpoint; got != url {
		t.Errorf("endpoint = %q, want %q", got, url)
	}
}

func TestLoad_WarehousePlanning_DefaultsToNotConfigured(t *testing.T) {
	t.Setenv("WAREHOUSE_PLANNING_MCP_ENDPOINT", "")
	t.Setenv("CAPACITY_OUTLOOK_HORIZON", "")

	cfg := mustLoad(t)

	if cfg.WarehousePlanning.Endpoint != "" {
		t.Errorf("an unset endpoint must stay empty (= not configured), got %q", cfg.WarehousePlanning.Endpoint)
	}
	if cfg.CapacityOutlookHorizon != 8*time.Hour {
		t.Errorf("default horizon = %v, want 8h", cfg.CapacityOutlookHorizon)
	}
}

func TestLoad_WarehousePlanning_ReadsEndpointAndHorizon(t *testing.T) {
	t.Setenv("WAREHOUSE_PLANNING_MCP_ENDPOINT", "http://warehouse-planning-mcp.apps.svc.cluster.local:8090/mcp")
	t.Setenv("CAPACITY_OUTLOOK_HORIZON", "4h30m")

	cfg := mustLoad(t)

	if cfg.WarehousePlanning.Endpoint != "http://warehouse-planning-mcp.apps.svc.cluster.local:8090/mcp" {
		t.Errorf("endpoint = %q", cfg.WarehousePlanning.Endpoint)
	}
	if cfg.CapacityOutlookHorizon != 4*time.Hour+30*time.Minute {
		t.Errorf("horizon = %v, want 4h30m", cfg.CapacityOutlookHorizon)
	}
}

func TestLoad_CapacityOutlookHorizon_InvalidFallsBackToDefault(t *testing.T) {
	for _, bad := range []string{"soon", "-1h", "0s"} {
		t.Setenv("CAPACITY_OUTLOOK_HORIZON", bad)
		if got := mustLoad(t).CapacityOutlookHorizon; got != 8*time.Hour {
			t.Errorf("horizon for %q = %v, want the 8h default", bad, got)
		}
	}
}

func TestLoad_PathTargets_PlanningFieldsAreOptionalAndNeverDefaulted(t *testing.T) {
	t.Setenv("DAILY_BRIEF_PATH_TARGETS", `[
	  {"siteCode":"SIM1","pathId":"pick-zone-a","processPath":"PICK","buildingId":"sim1","shiftId":"s1",
	   "planningPathId":"tote-path","unitsPerOrder":2.5,"packagesPerOrder":1.2},
	  {"siteCode":"SIM1","pathId":"pack-zone-a","processPath":"PACK","buildingId":"sim1","shiftId":"s1"}
	]`)

	targets := mustLoad(t).PathTargets

	if len(targets) != 2 {
		t.Fatalf("targets = %+v", targets)
	}
	full := targets[0]
	if full.PlanningPathId != "tote-path" || full.UnitsPerOrder == nil || *full.UnitsPerOrder != 2.5 || full.PackagesPerOrder == nil || *full.PackagesPerOrder != 1.2 {
		t.Errorf("planning fields not parsed: %+v", full)
	}
	bare := targets[1]
	if bare.PlanningPathId != "" || bare.UnitsPerOrder != nil || bare.PackagesPerOrder != nil {
		t.Errorf("unset planning fields must stay unset, not defaulted: %+v", bare)
	}
}

func TestLoad_DefaultPathTarget_HasNoPlanningBinding(t *testing.T) {
	t.Setenv("DAILY_BRIEF_PATH_TARGETS", "")

	for _, tg := range mustLoad(t).PathTargets {
		if tg.PlanningPathId != "" || tg.UnitsPerOrder != nil || tg.PackagesPerOrder != nil {
			t.Errorf("the built-in default target must not invent a planning binding: %+v", tg)
		}
	}
}

func TestLoad_PathTargets_UnsetUsesDefault(t *testing.T) {
	t.Setenv("DAILY_BRIEF_PATH_TARGETS", "")

	targets := mustLoad(t).PathTargets

	if len(targets) != len(defaultPathTargets) || targets[0].PathId != defaultPathTargets[0].PathId {
		t.Errorf("unset DAILY_BRIEF_PATH_TARGETS must use the built-in default, got %+v", targets)
	}
}

// A set-but-unparseable DAILY_BRIEF_PATH_TARGETS is an operator error. It
// must fail startup with a clear config error, never silently fall back to
// the built-in default target (which would hide the misconfiguration).
func TestLoad_PathTargets_MalformedJSON_IsAConfigError(t *testing.T) {
	for _, bad := range []string{
		`not json`,
		`[{"siteCode":"WH1",`,
		`{"siteCode":"WH1"}`, // an object, not the required array
	} {
		t.Setenv("DAILY_BRIEF_PATH_TARGETS", bad)

		_, err := Load()
		if err == nil {
			t.Errorf("Load() with DAILY_BRIEF_PATH_TARGETS=%q: expected an error, got nil", bad)
			continue
		}
		if !strings.Contains(err.Error(), "DAILY_BRIEF_PATH_TARGETS") {
			t.Errorf("error for %q must name the offending variable, got: %v", bad, err)
		}
	}
}

// An explicitly empty target list ("[]", or JSON null) is a set value that
// monitors nothing: an operator mistake, or an intent the daily brief cannot
// honour silently. It must fail startup, name the variable and say how to
// get the default (unset it) -- never fall back to the default target.
func TestLoad_PathTargets_EmptyList_IsAConfigError(t *testing.T) {
	for _, empty := range []string{`[]`, ` [ ] `, "[\n]", `null`} {
		t.Setenv("DAILY_BRIEF_PATH_TARGETS", empty)

		_, err := Load()
		if err == nil {
			t.Errorf("Load() with DAILY_BRIEF_PATH_TARGETS=%q: expected an error, got nil", empty)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "DAILY_BRIEF_PATH_TARGETS") {
			t.Errorf("error for %q must name the offending variable, got: %v", empty, err)
		}
		if !strings.Contains(msg, "unset") {
			t.Errorf("error for %q must say how to get the default (unset the variable), got: %v", empty, err)
		}
	}
}

func TestLoad_NetworkInventoryPlanning_DefaultsToNotConfigured(t *testing.T) {
	t.Setenv("NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT", "")
	if got := mustLoad(t).NetworkInventoryPlanning.Endpoint; got != "" {
		t.Errorf("an unset endpoint must stay empty (= not configured), got %q", got)
	}
}

func TestLoad_NetworkInventoryPlanning_ReadsEndpoint(t *testing.T) {
	t.Setenv("NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT", "http://network-inventory-planning-mcp.apps.svc.cluster.local:8090/mcp")
	want := "http://network-inventory-planning-mcp.apps.svc.cluster.local:8090/mcp"
	if got := mustLoad(t).NetworkInventoryPlanning.Endpoint; got != want {
		t.Errorf("endpoint = %q, want %q", got, want)
	}
}
