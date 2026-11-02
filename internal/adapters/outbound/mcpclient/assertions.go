package mcpclient

import "github.com/claudioed/warehouse-ops-agent/internal/ports"

// Compile-time proof that every typed client in this package satisfies the
// internal/ports interface it implements. Collected in one place (rather
// than one assertion per client file) so each client file carries only its
// constructor and its tool methods (fleet dupl rule, 2026-11-02).
var _ ports.CustomerOrderingClient = (*CustomerOrdering)(nil)
var _ ports.DeliveryPromiseClient = (*DeliveryPromise)(nil)
var _ ports.ExceptionManagementClient = (*ExceptionManagement)(nil)
var _ ports.FacilityLayoutClient = (*FacilityLayout)(nil)
var _ ports.FulfillmentExecutionClient = (*FulfillmentExecution)(nil)
var _ ports.InboundReceivingClient = (*InboundReceiving)(nil)
var _ ports.InventoryStorageClient = (*InventoryStorage)(nil)
var _ ports.LaborPerformanceClient = (*LaborPerformance)(nil)
var _ ports.LastMileDeliveryClient = (*LastMileDelivery)(nil)
var _ ports.LinehaulTransportationClient = (*LinehaulTransportation)(nil)
var _ ports.NetworkInventoryPlanningClient = (*NetworkInventoryPlanning)(nil)
var _ ports.NodePerformanceClient = (*NodePerformance)(nil)
var _ ports.NodeTopologyClient = (*NodeTopology)(nil)
var _ ports.OfferAvailabilityClient = (*OfferAvailability)(nil)
var _ ports.OrderManagementMCPClient = (*OrderManagement)(nil)
var _ ports.OrderSourcingClient = (*OrderSourcing)(nil)
var _ ports.PickFaceReplenishmentClient = (*PickFaceReplenishment)(nil)
var _ ports.ProcessPathManagementClient = (*ProcessPathManagement)(nil)
var _ ports.ProductMasterClient = (*ProductMaster)(nil)
var _ ports.ReturnsProcessingClient = (*ReturnsProcessing)(nil)
var _ ports.SellerInboundClient = (*SellerInbound)(nil)
var _ ports.ShippingAndTrackingClient = (*ShippingAndTracking)(nil)
var _ ports.SlottingOptimizationClient = (*SlottingOptimization)(nil)
var _ ports.TaskInterleavingClient = (*TaskInterleaving)(nil)
var _ ports.VendorConnectivityClient = (*VendorConnectivity)(nil)
var _ ports.WarehousePlanningClient = (*WarehousePlanning)(nil)
var _ ports.WesWorkPlanningClient = (*WesWorkPlanning)(nil)
var _ ports.WorkforceManagementClient = (*WorkforceManagement)(nil)
var _ ports.StaffingGapBySiteClient = (*WorkforceManagement)(nil)
