package main

import (
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/config"
)

func TestNewPlanningClient_UnsetEndpoint_IsANilInterface(t *testing.T) {
	// A typed-nil would pass a `!= nil` check and then crash on first call;
	// the wiring must return a genuinely nil interface when not configured.
	if got := newPlanningClient(config.Config{}); got != nil {
		t.Fatalf("an unset WAREHOUSE_PLANNING_MCP_ENDPOINT must yield a nil client, got %T", got)
	}
}

func TestNewPlanningClient_SetEndpoint_BuildsClient(t *testing.T) {
	cfg := config.Config{WarehousePlanning: config.UpstreamConfig{Endpoint: "http://warehouse-planning-mcp:8090/mcp"}}
	if got := newPlanningClient(cfg); got == nil {
		t.Fatal("a configured endpoint must build the client")
	}
}

func TestNewDecisionSupport_OutlookOnlyWiredWhenPlanningConfigured(t *testing.T) {
	cfg := config.Config{CapacityOutlookHorizon: 6 * time.Hour, PathTargets: []config.PathTarget{{SiteCode: "WH1", PathId: "p", ProcessPath: "PICK"}}}

	unconfigured := newDecisionSupport(cfg, outboundClients{})
	if unconfigured.dailyBrief.Outlook != nil {
		t.Fatal("no planning client => no outlook: the brief must be unchanged")
	}

	cfg.WarehousePlanning.Endpoint = "http://warehouse-planning-mcp:8090/mcp"
	configured := newDecisionSupport(cfg, outboundClients{planning: newPlanningClient(cfg)})
	if configured.dailyBrief.Outlook == nil || configured.dailyBrief.Outlook.Planning == nil || configured.dailyBrief.Outlook.Horizon != 6*time.Hour {
		t.Fatalf("a configured planning client must wire the outlook with the configured horizon: %+v", configured.dailyBrief.Outlook)
	}
}

func TestToUseCaseTargets_CarriesPlanningBinding(t *testing.T) {
	units := 2.5
	out := toUseCaseTargets([]config.PathTarget{{SiteCode: "SIM1", PathId: "p", PlanningPathId: "tote-path", UnitsPerOrder: &units}})

	if len(out) != 1 || out[0].PlanningPathId != "tote-path" || out[0].UnitsPerOrder == nil || *out[0].UnitsPerOrder != 2.5 || out[0].PackagesPerOrder != nil {
		t.Fatalf("planning binding not carried through: %+v", out)
	}
}
