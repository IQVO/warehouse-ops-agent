package main

import (
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/config"
)

func TestNewProductMasterClient_UnsetEndpoint_IsANilInterface(t *testing.T) {
	if got := newProductMasterClient(config.Config{}); got != nil {
		t.Fatalf("an unset PRODUCT_MASTER_MCP_ENDPOINT must yield a nil client (fail-open), got %T", got)
	}
}

func TestNewDecisionSupport_MasterDataGapsOnlyWiredWhenProductMasterConfigured(t *testing.T) {
	cfg := config.Config{PathTargets: []config.PathTarget{{SiteCode: "WH1", PathId: "p", ProcessPath: "PICK"}}}
	if got := newDecisionSupport(cfg, outboundClients{}).masterDataGaps; got != nil {
		t.Fatal("no product-master client => no master-data-gaps use case")
	}

	cfg.ProductMaster.Endpoint = "http://product-master-mcp:8090/mcp"
	clients := newOutboundClients(cfg)
	if clients.productMaster == nil {
		t.Fatal("a configured endpoint must build the product-master client")
	}
	got := newDecisionSupport(cfg, clients).masterDataGaps
	if got == nil || got.ProductMaster == nil {
		t.Fatalf("a configured product-master must wire the use case: %+v", got)
	}
}

func TestNewInboundReceivingClient_UnsetEndpoint_IsANilInterface(t *testing.T) {
	// A typed-nil would pass a `!= nil` check and then crash on first call.
	if got := newInboundReceivingClient(config.Config{}); got != nil {
		t.Fatalf("an unset INBOUND_RECEIVING_MCP_ENDPOINT must yield a nil client (fail-open), got %T", got)
	}
}

func TestNewDecisionSupport_InboundOutlookOnlyWiredWhenInboundReceivingConfigured(t *testing.T) {
	cfg := config.Config{PathTargets: []config.PathTarget{{SiteCode: "WH1", PathId: "p", ProcessPath: "PICK"}}}
	if got := newDecisionSupport(cfg, outboundClients{}).inboundOutlook; got != nil {
		t.Fatalf("no inbound-receiving client => no inbound outlook (a genuine nil, so adapters answer 503), got %+v", got)
	}

	cfg.InboundReceiving.Endpoint = "http://inbound-receiving-mcp:8090/mcp"
	cfg.InboundStaleReceiptAge = 6 * time.Hour
	clients := newOutboundClients(cfg)
	if clients.inboundReceiving == nil {
		t.Fatal("a configured endpoint must build the inbound-receiving client")
	}
	got := newDecisionSupport(cfg, clients).inboundOutlook
	if got == nil || got.Inbound == nil || got.StaleReceiptAge != 6*time.Hour {
		t.Fatalf("a configured inbound-receiving must wire the outlook with the configured stale age: %+v", got)
	}
}

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

func TestNewNIPClient_UnsetEndpoint_IsANilInterface(t *testing.T) {
	// Same typed-nil trap as the planning client: a non-nil interface around a
	// nil pointer would pass the "configured?" check and crash on first call.
	if got := newNIPClient(config.Config{}); got != nil {
		t.Fatalf("an unset NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT must yield a nil client, got %T", got)
	}
}

func TestNewNIPClient_SetEndpoint_BuildsClient(t *testing.T) {
	cfg := config.Config{NetworkInventoryPlanning: config.UpstreamConfig{Endpoint: "http://network-inventory-planning-mcp:8090/mcp"}}
	if got := newNIPClient(cfg); got == nil {
		t.Fatal("a configured endpoint must build the client")
	}
}

func TestNewDecisionSupport_TransferWatchOnlyWiredWhenNIPConfigured(t *testing.T) {
	cfg := config.Config{}

	if got := newDecisionSupport(cfg, outboundClients{}).transferWatch; got != nil {
		t.Fatalf("no NIP client => no transfer watch (a genuine nil, so adapters answer 503), got %+v", got)
	}

	cfg.NetworkInventoryPlanning.Endpoint = "http://network-inventory-planning-mcp:8090/mcp"
	got := newDecisionSupport(cfg, outboundClients{nip: newNIPClient(cfg)}).transferWatch
	if got == nil || got.NIP == nil {
		t.Fatalf("a configured NIP client must wire the transfer watch: %+v", got)
	}
}
