package main

import (
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/restclient"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// decisionSupport bundles the MCP-Customer / decision-support use case
// family: the daily brief (E3), the flow-balance advisory (E1) with its
// ADR-0008 utilization overlay, explain-travel-factor (ADR 0009), the
// E2 stranded-reservation correlation and the ADR 0020 master-data gaps
// (nil when product-master is not configured).
type decisionSupport struct {
	dailyBrief          *usecases.DailyBrief
	flowBalance         *usecases.FlowBalanceAdvisory
	explainTravelFactor *usecases.ExplainTravelFactor
	strandedReservation *usecases.DetectStrandedReservation
	masterDataGaps      *usecases.MasterDataGaps
	// inboundOutlook is nil when inbound-receiving is not configured
	// (ADR 0021).
	inboundOutlook *usecases.InboundOutlook
	// transferWatch is nil when network-inventory-planning is not
	// configured (ADR 0019).
	transferWatch *usecases.TransferWatch
	// networkOrderOutlook is nil when customer-ordering is not configured
	// (ADR 0026): get_network_order_outlook is then not registered.
	networkOrderOutlook *usecases.NetworkOrderOutlook
	// networkNodeOutlook is nil when node-topology is not configured
	// (ADR 0026): get_network_node_outlook is then not registered.
	networkNodeOutlook *usecases.NetworkNodeOutlook
}

// newDecisionSupport wires the decision-support use cases over the
// outbound MCP clients.
func newDecisionSupport(cfg config.Config, clients outboundClients) decisionSupport {
	flowBalance := &usecases.FlowBalanceAdvisory{
		Wes:           clients.wes,
		WFM:           clients.wfm,
		FE:            clients.fe,
		LP:            clients.lp,
		PathTaskTypes: toPathTaskTypes(cfg.PathTargets),
	}
	dailyBrief := &usecases.DailyBrief{
		Facility: clients.facility,
		Wes:      clients.wes,
		Fe:       clients.fe,
		Wfm:      clients.wfm,
		Targets:  toUseCaseTargets(cfg.PathTargets),
	}
	// Capacity outlook (ADR 0013): only wired when warehouse-planning is
	// configured. A nil Outlook leaves the daily brief byte-for-byte as it
	// was; a configured one is fail-open per path.
	if clients.planning != nil {
		dailyBrief.Outlook = &usecases.CapacityOutlook{
			Planning: clients.planning,
			Horizon:  cfg.CapacityOutlookHorizon,
		}
	}

	// Slot-plan watch (ADR 0024): only wired when slotting-optimization is
	// configured. A nil watch leaves the daily brief byte-for-byte as it
	// was; a configured one is fail-open per plan.
	if clients.slotting != nil {
		dailyBrief.SlotPlanWatch = &usecases.SlotPlanWatch{
			Slotting:   clients.slotting,
			StaleAfter: cfg.SlotPlanStaleAge,
		}
	}

	// Promise health (ADR 0024): only wired when order-management is
	// configured. A nil section use case leaves the daily brief
	// byte-for-byte as it was; a configured one is fail-open.
	if clients.om != nil {
		dailyBrief.PromiseHealth = &usecases.PromiseHealth{OrderMgmt: clients.om}
	}

	// StrandedReservation is the E2 correlation use case: read-only,
	// consuming the already-wired fulfillment-execution and
	// inventory-storage clients above. It only ever recommends a
	// revoke_reservation; it never calls one.
	strandedReservation := &usecases.DetectStrandedReservation{
		FulfillmentExecution: clients.fe,
		InventoryStorage:     clients.inv,
	}

	// Master-data gaps (ADR 0020): only wired when product-master is
	// configured; otherwise GET /master-data-gaps answers 503 and
	// find_master_data_gaps is not registered.
	var masterDataGaps *usecases.MasterDataGaps
	if clients.productMaster != nil {
		masterDataGaps = &usecases.MasterDataGaps{ProductMaster: clients.productMaster}
	}

	return decisionSupport{
		dailyBrief:          dailyBrief,
		flowBalance:         flowBalance,
		explainTravelFactor: newExplainTravelFactor(clients),
		strandedReservation: strandedReservation,
		masterDataGaps:      masterDataGaps,
		inboundOutlook:      newInboundOutlook(cfg, clients),
		transferWatch:       newTransferWatch(clients),
		networkOrderOutlook: newNetworkOrderOutlook(clients),
		networkNodeOutlook:  newNetworkNodeOutlook(clients),
	}
}

// newNetworkOrderOutlook wires the ADR-0026 order outlook over the
// ADR-0025 clients, or returns nil when the anchor client customer-
// ordering is not configured. The nil must be a real nil
// *NetworkOrderOutlook so the inbound adapter's "not configured" check is
// genuine; every non-anchor section degrades per section at run time.
func newNetworkOrderOutlook(clients outboundClients) *usecases.NetworkOrderOutlook {
	if clients.customerOrdering == nil {
		return nil
	}
	return &usecases.NetworkOrderOutlook{
		Orders:   clients.customerOrdering,
		Promises: clients.deliveryPromise,
		Sourcing: clients.orderSourcing,
		Journeys: clients.shippingAndTracking,
	}
}

// newNetworkNodeOutlook wires the ADR-0026 node outlook over the
// ADR-0025 clients, or returns nil when the anchor client node-topology
// is not configured. Same genuine-nil rule as newNetworkOrderOutlook.
func newNetworkNodeOutlook(clients outboundClients) *usecases.NetworkNodeOutlook {
	if clients.nodeTopology == nil {
		return nil
	}
	return &usecases.NetworkNodeOutlook{
		Topology:    clients.nodeTopology,
		Performance: clients.nodePerformance,
		Listings:    clients.offerAvailability,
		Vendor:      clients.vendorConnectivity,
		Departures:  clients.linehaulTransportation,
		Waves:       clients.lastMileDelivery,
	}
}

// newExplainTravelFactor wires the ADR-0009 use case, or returns nil when
// facility-layout is not configured: GET /explain-travel-factor then
// answers 503 "not configured" and explain_travel_factor is not
// registered, the same as every other upstream-specific feature.
func newExplainTravelFactor(clients outboundClients) *usecases.ExplainTravelFactor {
	if clients.facility == nil {
		return nil
	}
	return &usecases.ExplainTravelFactor{Facility: clients.facility}
}

// newInboundOutlook wires the ADR-0021 read-only inbound outlook, or returns
// nil when inbound-receiving is not configured. The nil must be a real nil
// *InboundOutlook (never a non-nil struct around a nil port) so the inbound
// adapters' "not configured" checks are genuine.
func newInboundOutlook(cfg config.Config, clients outboundClients) *usecases.InboundOutlook {
	if clients.inboundReceiving == nil {
		return nil
	}
	return &usecases.InboundOutlook{
		Inbound:         clients.inboundReceiving,
		StaleReceiptAge: cfg.InboundStaleReceiptAge,
	}
}

// newTransferWatch wires the ADR-0019 read-only transfer watch, or returns
// nil when network-inventory-planning is not configured. The nil must be a
// real nil *TransferWatch (never a non-nil struct around a nil port) so the
// inbound adapters' "not configured" checks are genuine.
func newTransferWatch(clients outboundClients) *usecases.TransferWatch {
	if clients.nip == nil {
		return nil
	}
	return &usecases.TransferWatch{NIP: clients.nip}
}

// newOrderLifecycle wires the console-bff order-lifecycle use case. Its
// REST clients are separate from the MCP clients above (see
// internal/ports/order_lifecycle_clients.go's doc comment for why these
// are a deliberately distinct port shape).
func newOrderLifecycle(cfg config.Config) *usecases.OrderLifecycle {
	var orderMgmtClient ports.OrderManagementClient = restclient.NewOrderManagement(cfg.OrderManagementRESTURL, 5*time.Second)
	return &usecases.OrderLifecycle{
		OrderManagement: &orderMgmtClient,
		Inventory:       restclient.NewInventoryReservations(cfg.InventoryStorageRESTURL, 5*time.Second),
		WorkUnits:       restclient.NewWorkUnits(cfg.WesWorkPlanningRESTURL, 5*time.Second),
		Tasks:           restclient.NewTasksByOrder(cfg.FulfillmentExecutionRESTURL, 5*time.Second),
	}
}

// newConsoleReports wires the console-bff WMS/WES dashboards: a THIRD set
// of clients, pointed at each context's *-reports READER binary rather
// than its OLTP API (different process, different analytical database,
// different base URL) -- see internal/ports/console_reports_clients.go.
func newConsoleReports(cfg config.Config) *usecases.ConsoleReports {
	return &usecases.ConsoleReports{
		OrderFunnel:           restclient.NewOrderFunnelReports(cfg.OrderManagementReportsRESTURL, 5*time.Second),
		InventoryFlowAccuracy: restclient.NewFlowAccuracyReports(cfg.InventoryStorageReportsRESTURL, 5*time.Second),
		CatalogGrowth:         restclient.NewCatalogGrowthReports(cfg.FacilityLayoutReportsRESTURL, 5*time.Second),
		PlanningThroughput:    restclient.NewPlanningThroughputReports(cfg.WesWorkPlanningReportsRESTURL, 5*time.Second),
		FulfillmentThroughput: restclient.NewFulfillmentThroughputReports(cfg.FulfillmentExecutionReportsRESTURL, 5*time.Second),
		Labor:                 restclient.NewLaborReports(cfg.WorkforceManagementReportsRESTURL, 5*time.Second),
		LaborPerformance:      restclient.NewLaborPerformanceReports(cfg.LaborPerformanceReportsRESTURL, 5*time.Second),
	}
}

// newRuntimeSignals wires the runtime-signals report over the telemetry
// (Prometheus) and log (Loki) readers, with a 10-minute rolling window.
func newRuntimeSignals(cfg config.Config, telemetryReader ports.TelemetryReader, logReader ports.LogReader) *usecases.RuntimeSignals {
	return &usecases.RuntimeSignals{
		Telemetry:     telemetryReader,
		Logs:          logReader,
		Services:      cfg.RuntimeSignalsServices,
		Namespace:     cfg.RuntimeSignalsNamespace,
		WindowMinutes: 10,
	}
}
