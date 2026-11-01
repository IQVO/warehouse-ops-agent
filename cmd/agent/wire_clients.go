package main

import (
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/mcpclient"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// outboundClients bundles every outbound adapter the use cases consume,
// built from env config. Each MCP client satisfies its internal/ports
// interface at compile time (see the var _ assertions in
// internal/adapters/outbound/mcpclient/*.go) and is a genuinely nil
// interface when its endpoint is unset (newMCPClient), so a nil check on
// any of them means "not configured".
type outboundClients struct {
	// om backs the daily brief's promise-health section (ADR 0024).
	om       ports.OrderManagementMCPClient
	wes      ports.WesWorkPlanningClient
	fe       ports.FulfillmentExecutionClient
	wfm      ports.WorkforceManagementClient
	facility ports.FacilityLayoutClient
	inv      ports.InventoryStorageClient
	lp       ports.LaborPerformanceClient
	// planning is nil when WAREHOUSE_PLANNING_MCP_ENDPOINT is unset: the
	// capacity outlook is then not wired at all (ADR 0013).
	planning ports.WarehousePlanningClient
	// productMaster is nil when PRODUCT_MASTER_MCP_ENDPOINT is unset: the
	// master-data-gaps use case is then not wired (ADR 0020).
	productMaster ports.ProductMasterClient
	// inboundReceiving is nil when INBOUND_RECEIVING_MCP_ENDPOINT is unset:
	// the inbound outlook is then not wired (ADR 0021).
	inboundReceiving ports.InboundReceivingClient
	// nip is nil when NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT is unset:
	// the transfer watch is then not wired at all (ADR 0019).
	nip ports.NetworkInventoryPlanningClient
	// slotting is nil when SLOTTING_OPTIMIZATION_MCP_ENDPOINT is unset:
	// the daily brief's slot-plan watch is then not wired (ADR 0024).
	slotting ports.SlottingOptimizationClient
	// returnsProcessing is nil when RETURNS_PROCESSING_MCP_ENDPOINT is
	// unset (fail-open). No use case consumes it yet: it exists so the
	// returns/exceptions read surface is reachable the moment one does.
	returnsProcessing ports.ReturnsProcessingClient
	// exceptionManagement is nil when EXCEPTION_MANAGEMENT_MCP_ENDPOINT is
	// unset (fail-open). No use case consumes it yet: it exists so the
	// returns/exceptions read surface is reachable the moment one does.
	exceptionManagement ports.ExceptionManagementClient
	// The twelve ADR-0025 upstreams (the ten retail-network contexts plus
	// task-interleaving and pick-face-replenishment). Each is nil when
	// its *_MCP_ENDPOINT is unset (fail-open, ADR 0023). No use case
	// consumes any of them yet: they exist so each upstream's read
	// surface is reachable the moment one does.
	customerOrdering       ports.CustomerOrderingClient
	deliveryPromise        ports.DeliveryPromiseClient
	lastMileDelivery       ports.LastMileDeliveryClient
	linehaulTransportation ports.LinehaulTransportationClient
	nodePerformance        ports.NodePerformanceClient
	nodeTopology           ports.NodeTopologyClient
	offerAvailability      ports.OfferAvailabilityClient
	orderSourcing          ports.OrderSourcingClient
	shippingAndTracking    ports.ShippingAndTrackingClient
	vendorConnectivity     ports.VendorConnectivityClient
	// sellerInbound is nil when SELLER_INBOUND_MCP_ENDPOINT is unset
	// (fail-open, ADR 0023). No use case consumes it yet: it exists so
	// the partner-inbound read surface is reachable the moment one does
	// (ADR 0027).
	sellerInbound         ports.SellerInboundClient
	taskInterleaving      ports.TaskInterleavingClient
	pickFaceReplenishment ports.PickFaceReplenishmentClient
	// processPathManagement is the thirteenth ADR-0026 client (re-added
	// after ADR 0024's gap-A9 deletion, now pinned to the current
	// three-tool registry). It is nil when
	// PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT is unset (fail-open); no use
	// case consumes it yet.
	processPathManagement ports.ProcessPathManagementClient
	telemetry             ports.TelemetryReader
	logs                  ports.LogReader
}

// newMCPClient builds one upstream's MCP client, or returns the zero
// value of the port interface P (a genuinely nil interface, never a
// non-nil interface around a nil pointer) when endpoint is empty. No
// client is ever built for an empty endpoint, so boot never depends on an
// upstream and no call is ever dialled to "".
func newMCPClient[P any](name, endpoint string, build func(mcpclient.Config) P) P {
	var none P
	if endpoint == "" {
		return none
	}
	return build(mcpclient.Config{Name: name, Endpoint: endpoint})
}

// newProductMasterClient builds the product-master MCP client (its four
// read tools only), or nil when no endpoint is configured (fail-open).
func newProductMasterClient(cfg config.Config) ports.ProductMasterClient {
	return newMCPClient("product-master", cfg.ProductMaster.Endpoint, func(c mcpclient.Config) ports.ProductMasterClient {
		return mcpclient.NewProductMaster(c)
	})
}

// newInboundReceivingClient builds the inbound-receiving MCP client (its
// seven read tools only), or nil when no endpoint is configured
// (fail-open).
func newInboundReceivingClient(cfg config.Config) ports.InboundReceivingClient {
	return newMCPClient("inbound-receiving", cfg.InboundReceiving.Endpoint, func(c mcpclient.Config) ports.InboundReceivingClient {
		return mcpclient.NewInboundReceiving(c)
	})
}

// newNIPClient builds the network-inventory-planning MCP client (read
// tools only), or nil when no endpoint is configured.
func newNIPClient(cfg config.Config) ports.NetworkInventoryPlanningClient {
	return newMCPClient("network-inventory-planning", cfg.NetworkInventoryPlanning.Endpoint, func(c mcpclient.Config) ports.NetworkInventoryPlanningClient {
		return mcpclient.NewNetworkInventoryPlanning(c)
	})
}

// newSlottingClient builds the slotting-optimization MCP client (its two
// slot-plan read tools only, ADR 0024), or nil when no endpoint is
// configured (fail-open).
func newSlottingClient(cfg config.Config) ports.SlottingOptimizationClient {
	return newMCPClient("slotting-optimization", cfg.SlottingOptimization.Endpoint, func(c mcpclient.Config) ports.SlottingOptimizationClient {
		return mcpclient.NewSlottingOptimization(c)
	})
}

// newReturnsProcessingClient builds the returns-processing MCP client (its
// three read tools only), or nil when no endpoint is configured
// (fail-open). No use case consumes the client yet.
func newReturnsProcessingClient(cfg config.Config) ports.ReturnsProcessingClient {
	return newMCPClient("returns-processing", cfg.ReturnsProcessing.Endpoint, func(c mcpclient.Config) ports.ReturnsProcessingClient {
		return mcpclient.NewReturnsProcessing(c)
	})
}

// newExceptionManagementClient builds the exception-management MCP client
// (its four read tools only), or nil when no endpoint is configured
// (fail-open). No use case consumes the client yet.
func newExceptionManagementClient(cfg config.Config) ports.ExceptionManagementClient {
	return newMCPClient("exception-management", cfg.ExceptionManagement.Endpoint, func(c mcpclient.Config) ports.ExceptionManagementClient {
		return mcpclient.NewExceptionManagement(c)
	})
}

// newPlanningClient builds the warehouse-planning MCP client (read tools
// only), or nil when no endpoint is configured, so the capacity outlook
// degrades to "absent".
func newPlanningClient(cfg config.Config) ports.WarehousePlanningClient {
	return newMCPClient("warehouse-planning", cfg.WarehousePlanning.Endpoint, func(c mcpclient.Config) ports.WarehousePlanningClient {
		return mcpclient.NewWarehousePlanning(c)
	})
}

// newProcessPathManagementClient builds the process-path-management MCP
// client (its three read tools only, ADR 0026), or nil when no endpoint is
// configured (fail-open). No use case consumes the client yet.
func newProcessPathManagementClient(cfg config.Config) ports.ProcessPathManagementClient {
	return newMCPClient("process-path-management", cfg.ProcessPathManagement.Endpoint, func(c mcpclient.Config) ports.ProcessPathManagementClient {
		return mcpclient.NewProcessPathManagement(c)
	})
}

// newOutboundClients builds every outbound MCP client (one per upstream
// bounded context, each only when its endpoint is set) plus the telemetry
// and log readers.
func newOutboundClients(cfg config.Config) outboundClients {
	return outboundClients{
		om: newMCPClient("order-management", cfg.OrderManagement.Endpoint, func(c mcpclient.Config) ports.OrderManagementMCPClient {
			return mcpclient.NewOrderManagement(c)
		}),
		wes: newMCPClient("wes-work-planning", cfg.WesWorkPlanning.Endpoint, func(c mcpclient.Config) ports.WesWorkPlanningClient {
			return mcpclient.NewWesWorkPlanning(c)
		}),
		fe: newMCPClient("fulfillment-execution", cfg.FulfillmentExecution.Endpoint, func(c mcpclient.Config) ports.FulfillmentExecutionClient {
			return mcpclient.NewFulfillmentExecution(c)
		}),
		wfm: newMCPClient("workforce-management", cfg.WorkforceManagement.Endpoint, func(c mcpclient.Config) ports.WorkforceManagementClient {
			return mcpclient.NewWorkforceManagement(c)
		}),
		facility: newMCPClient("facility-layout", cfg.FacilityLayout.Endpoint, func(c mcpclient.Config) ports.FacilityLayoutClient {
			return mcpclient.NewFacilityLayout(c)
		}),
		inv: newMCPClient("inventory-storage", cfg.InventoryStorage.Endpoint, func(c mcpclient.Config) ports.InventoryStorageClient {
			return mcpclient.NewInventoryStorage(c)
		}),
		lp: newMCPClient("labor-performance", cfg.LaborPerformance.Endpoint, func(c mcpclient.Config) ports.LaborPerformanceClient {
			return mcpclient.NewLaborPerformance(c)
		}),
		telemetry:           newTelemetryReader(cfg.PrometheusURL),
		logs:                newLogReader(cfg.LokiURL),
		planning:            newPlanningClient(cfg),
		productMaster:       newProductMasterClient(cfg),
		inboundReceiving:    newInboundReceivingClient(cfg),
		nip:                 newNIPClient(cfg),
		slotting:            newSlottingClient(cfg),
		returnsProcessing:   newReturnsProcessingClient(cfg),
		exceptionManagement: newExceptionManagementClient(cfg),
		customerOrdering: newMCPClient("customer-ordering", cfg.CustomerOrdering.Endpoint, func(c mcpclient.Config) ports.CustomerOrderingClient {
			return mcpclient.NewCustomerOrdering(c)
		}),
		deliveryPromise: newMCPClient("delivery-promise", cfg.DeliveryPromise.Endpoint, func(c mcpclient.Config) ports.DeliveryPromiseClient {
			return mcpclient.NewDeliveryPromise(c)
		}),
		lastMileDelivery: newMCPClient("last-mile-delivery", cfg.LastMileDelivery.Endpoint, func(c mcpclient.Config) ports.LastMileDeliveryClient {
			return mcpclient.NewLastMileDelivery(c)
		}),
		linehaulTransportation: newMCPClient("linehaul-transportation", cfg.LinehaulTransportation.Endpoint, func(c mcpclient.Config) ports.LinehaulTransportationClient {
			return mcpclient.NewLinehaulTransportation(c)
		}),
		nodePerformance: newMCPClient("node-performance", cfg.NodePerformance.Endpoint, func(c mcpclient.Config) ports.NodePerformanceClient {
			return mcpclient.NewNodePerformance(c)
		}),
		nodeTopology: newMCPClient("node-topology", cfg.NodeTopology.Endpoint, func(c mcpclient.Config) ports.NodeTopologyClient {
			return mcpclient.NewNodeTopology(c)
		}),
		offerAvailability: newMCPClient("offer-availability", cfg.OfferAvailability.Endpoint, func(c mcpclient.Config) ports.OfferAvailabilityClient {
			return mcpclient.NewOfferAvailability(c)
		}),
		orderSourcing: newMCPClient("order-sourcing", cfg.OrderSourcing.Endpoint, func(c mcpclient.Config) ports.OrderSourcingClient {
			return mcpclient.NewOrderSourcing(c)
		}),
		shippingAndTracking: newMCPClient("shipping-and-tracking", cfg.ShippingAndTracking.Endpoint, func(c mcpclient.Config) ports.ShippingAndTrackingClient {
			return mcpclient.NewShippingAndTracking(c)
		}),
		vendorConnectivity: newMCPClient("vendor-connectivity", cfg.VendorConnectivity.Endpoint, func(c mcpclient.Config) ports.VendorConnectivityClient {
			return mcpclient.NewVendorConnectivity(c)
		}),
		sellerInbound: newMCPClient("seller-inbound", cfg.SellerInbound.Endpoint, func(c mcpclient.Config) ports.SellerInboundClient {
			return mcpclient.NewSellerInbound(c)
		}),
		taskInterleaving: newMCPClient("task-interleaving", cfg.TaskInterleaving.Endpoint, func(c mcpclient.Config) ports.TaskInterleavingClient {
			return mcpclient.NewTaskInterleaving(c)
		}),
		pickFaceReplenishment: newMCPClient("pick-face-replenishment", cfg.PickFaceReplenishment.Endpoint, func(c mcpclient.Config) ports.PickFaceReplenishmentClient {
			return mcpclient.NewPickFaceReplenishment(c)
		}),
		processPathManagement: newProcessPathManagementClient(cfg),
	}
}
