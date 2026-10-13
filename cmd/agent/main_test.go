package main

import (
	"context"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
)

func TestNewProductMasterClient_UnsetEndpoint_IsANilInterface(t *testing.T) {
	if got := newProductMasterClient(config.Config{}); got != nil {
		t.Fatalf("an unset PRODUCT_MASTER_MCP_ENDPOINT must yield a nil client (fail-open), got %T", got)
	}
}

// TestNewOutboundClients_EmptyEndpointBuildsNoMCPClient: an MCP client
// whose endpoint is empty is not built at all (a genuinely nil
// interface, never a typed nil), so no call is ever dialled to "" and
// each use case degrades through its existing nil-port path.
func TestNewOutboundClients_EmptyEndpointBuildsNoMCPClient(t *testing.T) {
	c := newOutboundClients(config.Config{})
	for name, got := range map[string]any{
		"wes-work-planning":          c.wes,
		"fulfillment-execution":      c.fe,
		"workforce-management":       c.wfm,
		"facility-layout":            c.facility,
		"inventory-storage":          c.inv,
		"labor-performance":          c.lp,
		"order-management":           c.om,
		"process-path-management":    c.ppm,
		"warehouse-planning":         c.planning,
		"product-master":             c.productMaster,
		"inbound-receiving":          c.inboundReceiving,
		"network-inventory-planning": c.nip,
	} {
		if got != nil {
			t.Errorf("%s: an empty endpoint must build no client, got %T", name, got)
		}
	}
}

func TestNewOutboundClients_SetEndpointBuildsEveryMCPClient(t *testing.T) {
	up := func(name string) config.UpstreamConfig {
		return config.UpstreamConfig{Endpoint: "http://" + name + "-mcp:8090/mcp"}
	}
	c := newOutboundClients(config.Config{
		WesWorkPlanning:          up("wes-work-planning"),
		FulfillmentExecution:     up("fulfillment-execution"),
		WorkforceManagement:      up("workforce-management"),
		FacilityLayout:           up("facility-layout"),
		InventoryStorage:         up("inventory-storage"),
		LaborPerformance:         up("labor-performance"),
		OrderManagement:          up("order-management"),
		ProcessPathManagement:    up("process-path-management"),
		WarehousePlanning:        up("warehouse-planning"),
		ProductMaster:            up("product-master"),
		InboundReceiving:         up("inbound-receiving"),
		NetworkInventoryPlanning: up("network-inventory-planning"),
	})
	for name, got := range map[string]any{
		"wes-work-planning":          c.wes,
		"fulfillment-execution":      c.fe,
		"workforce-management":       c.wfm,
		"facility-layout":            c.facility,
		"inventory-storage":          c.inv,
		"labor-performance":          c.lp,
		"order-management":           c.om,
		"process-path-management":    c.ppm,
		"warehouse-planning":         c.planning,
		"product-master":             c.productMaster,
		"inbound-receiving":          c.inboundReceiving,
		"network-inventory-planning": c.nip,
	} {
		if got == nil {
			t.Errorf("%s: a set endpoint must build the client", name)
		}
	}
}

// TestNewDecisionSupport_NoUpstreamsConfigured_DegradesWithoutPanicking:
// with every MCP endpoint empty the use cases still run and report their
// signals as unavailable instead of dereferencing a nil client.
func TestNewDecisionSupport_NoUpstreamsConfigured_DegradesWithoutPanicking(t *testing.T) {
	cfg := config.Config{PathTargets: []config.PathTarget{{SiteCode: "WH1", PathId: "p", ProcessPath: "PICK", BuildingId: "B1", ShiftId: "S1"}}}
	d := newDecisionSupport(cfg, newOutboundClients(cfg))
	ctx := context.Background()

	_ = d.dailyBrief.Execute(ctx)
	decision, err := d.flowBalance.Execute(ctx, "B1", "S1", "p")
	if err != nil {
		t.Fatalf("flow balance: %v", err)
	}
	if !decision.Partial {
		t.Fatalf("flow balance with no upstream must be partial, got %+v", decision)
	}
	if _, err := d.strandedReservation.Execute(ctx, usecases.StrandedReservationRequest{TaskType: "PICK", SKU: "SKU-1", ReservationId: "r", BinId: "b"}); err != nil {
		t.Fatalf("stranded reservation: %v", err)
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

func TestNewDecisionSupport_ExplainTravelFactorOnlyWiredWhenFacilityConfigured(t *testing.T) {
	if got := newDecisionSupport(config.Config{}, outboundClients{}).explainTravelFactor; got != nil {
		t.Fatalf("no facility-layout client => no explain-travel-factor (a genuine nil, so REST answers 503 and the tool is not registered), got %+v", got)
	}

	cfg := config.Config{FacilityLayout: config.UpstreamConfig{Endpoint: "http://facility-layout-mcp:8090/mcp"}}
	got := newDecisionSupport(cfg, newOutboundClients(cfg)).explainTravelFactor
	if got == nil || got.Facility == nil {
		t.Fatalf("a configured facility-layout must wire explain-travel-factor: %+v", got)
	}
}

func TestHTTPRouteList_NamesEveryRegisteredRESTRoute(t *testing.T) {
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{}, "test")
	got := httpRouteList(router)
	for _, want := range []string{"/healthz", "/daily-brief", "/inbound-outlook", "/transfer-watch/stuck", "/transfer-watch/transfers/{id}", "/transfer-watch/imbalance"} {
		if !strings.Contains(got, want) {
			t.Errorf("startup route list %q is missing %s", got, want)
		}
	}
	if strings.Contains(got, "/mcp") {
		t.Errorf("the REST route list must not repeat the MCP route: %q", got)
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
