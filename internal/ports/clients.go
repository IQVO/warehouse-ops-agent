package ports

import "context"

// WesWorkPlanningClient is the outbound port for wes-work-planning's
// published read tools (get_backlog_telemetry, get_rebalance_recommendation).
// It is a Customer-side query interface over that context's Open Host
// Service — never a Go import of wes-work-planning's own packages.
type WesWorkPlanningClient interface {
	GetBacklogTelemetry(ctx context.Context, pathId string) (BacklogTelemetry, error)
	GetRebalanceRecommendation(ctx context.Context, pathId string) (RebalanceRecommendation, error)
}

// FulfillmentExecutionClient is the outbound port for fulfillment-execution's
// published read tools (get_queue_status, diagnose_stuck_tasks).
type FulfillmentExecutionClient interface {
	GetQueueStatus(ctx context.Context, processPath string) (QueueStatus, error)
	DiagnoseStuckTasks(ctx context.Context, withinSeconds int) (StuckTasksResult, error)
}

// InventoryStorageClient is the outbound port for inventory-storage's
// published read tools (check_availability, get_bin_occupancy).
type InventoryStorageClient interface {
	CheckAvailability(ctx context.Context, sku string) (Availability, error)
	GetBinOccupancy(ctx context.Context, binId string) (BinOccupancy, error)
}

// WorkforceManagementClient is the outbound port for workforce-management's
// published read tool (get_staffing_gap).
type WorkforceManagementClient interface {
	GetStaffingGap(ctx context.Context, buildingId, shiftId, pathId string) (StaffingGap, error)
}

// FacilityLayoutClient is the outbound port for facility-layout's published
// read tools (list_sites, get_site_layout, get_zone_grid,
// estimate_travel_distance).
type FacilityLayoutClient interface {
	ListSites(ctx context.Context) (SitesResult, error)
	GetSiteLayout(ctx context.Context, siteCode string) (SiteLayout, error)
	GetZoneGrid(ctx context.Context, zoneId string) (ZoneGrid, error)
	// EstimateTravelDistance calls estimate_travel_distance for the
	// shortest route between two seven-segment location codes (ADR
	// 0017 there, ADR 0009 here). from/to are opaque strings this
	// port never parses or validates — that is entirely
	// facility-layout's own concern; a malformed code surfaces as the
	// tool call's own error.
	EstimateTravelDistance(ctx context.Context, from, to string) (TravelDistance, error)
}
