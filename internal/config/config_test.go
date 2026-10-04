package config

import (
	"testing"
	"time"
)

func TestLoad_WarehousePlanning_DefaultsToNotConfigured(t *testing.T) {
	t.Setenv("WAREHOUSE_PLANNING_MCP_ENDPOINT", "")
	t.Setenv("CAPACITY_OUTLOOK_HORIZON", "")

	cfg := Load()

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

	cfg := Load()

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
		if got := Load().CapacityOutlookHorizon; got != 8*time.Hour {
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

	targets := Load().PathTargets

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

	for _, tg := range Load().PathTargets {
		if tg.PlanningPathId != "" || tg.UnitsPerOrder != nil || tg.PackagesPerOrder != nil {
			t.Errorf("the built-in default target must not invent a planning binding: %+v", tg)
		}
	}
}
