// Package ports declares warehouse-ops-agent's outbound port interfaces: one
// per upstream bounded context's published MCP tool surface, plus a
// telemetry-reader port. These interfaces are owned by the inward layers
// (application/domain) and implemented by the outbound adapters in
// internal/adapters/outbound/**, per the Dependency Inversion Principle —
// exactly the same seam each of the five contexts already uses for their own
// repository ports.
//
// Every DTO in this file is a tool-boundary shape, JSON-tagged to match the
// field names each upstream MCP tool's structuredContent returns (see each
// context's internal/adapters/inbound/mcp/mapping.go and tools.go, which are
// the published contract these DTOs bind to). warehouse-ops-agent depends on
// those published tool schemas only — never on any context's Go packages;
// see architecture_test.go's TestNoDirectDependencyOnBoundedContexts.
package ports

// --- wes-work-planning read-model DTOs -----------------------------------

// BacklogTelemetry mirrors wes-work-planning's get_backlog_telemetry tool
// output.
type BacklogTelemetry struct {
	PathId             string `json:"pathId"`
	BacklogDepth       int    `json:"backlogDepth"`
	WIP                int    `json:"wip"`
	Mode               string `json:"mode"`
	OverAlarmThreshold bool   `json:"overAlarmThreshold"`
}

// RebalanceRecommendation mirrors wes-work-planning's
// get_rebalance_recommendation tool output.
type RebalanceRecommendation struct {
	PathId       string `json:"pathId"`
	Action       string `json:"action"`
	BacklogDepth int    `json:"backlogDepth"`
	WIP          int    `json:"wip"`
}

// ReleasedWork mirrors wes-work-planning's release_next_work tool output
// (write tool — bound for completeness; not called by any T1 use case).
type ReleasedWork struct {
	WorkUnitId string `json:"workUnitId"`
	CPT        string `json:"cpt"`
	Ref        string `json:"ref"`
}

// --- fulfillment-execution read-model DTOs -------------------------------

// QueueStatus mirrors fulfillment-execution's get_queue_status tool output.
type QueueStatus struct {
	ProcessPath string `json:"processPath"`
	Depth       int    `json:"depth"`
}

// StuckTask mirrors one entry of fulfillment-execution's diagnose_stuck_tasks
// tool output.
type StuckTask struct {
	TaskId         string `json:"taskId"`
	Type           string `json:"type"`
	LeaseStationId string `json:"leaseStationId"`
	LeaseExpiry    string `json:"leaseExpiry"`
	Reason         string `json:"reason"`
}

// StuckTasksResult mirrors fulfillment-execution's diagnose_stuck_tasks tool
// output.
type StuckTasksResult struct {
	Count int         `json:"count"`
	Tasks []StuckTask `json:"tasks"`
}

// CompletedTask mirrors fulfillment-execution's complete_task tool output
// (write tool — bound for completeness; not called by any T1 use case).
type CompletedTask struct {
	TaskId    string `json:"taskId"`
	StationId string `json:"stationId"`
	Completed bool   `json:"completed"`
}

// --- inventory-storage read-model DTOs ------------------------------------

// Availability mirrors inventory-storage's check_availability tool output.
type Availability struct {
	SKU    string `json:"sku"`
	Usable int    `json:"usable"`
}

// BinOccupancyLine mirrors one line of inventory-storage's get_bin_occupancy
// tool output.
type BinOccupancyLine struct {
	StockUnitId string `json:"stockUnitId"`
	SKU         string `json:"sku"`
	OnHand      int    `json:"onHand"`
	Reserved    int    `json:"reserved"`
	Usable      int    `json:"usable"`
	State       string `json:"state"`
}

// BinOccupancy mirrors inventory-storage's get_bin_occupancy tool output.
type BinOccupancy struct {
	BinId     string             `json:"binId"`
	UnitCount int                `json:"unitCount"`
	OnHand    int                `json:"onHand"`
	Reserved  int                `json:"reserved"`
	Usable    int                `json:"usable"`
	Lines     []BinOccupancyLine `json:"lines"`
}

// RevokedReservation mirrors inventory-storage's revoke_reservation tool
// output (write tool — bound for completeness; not called by any T1 use
// case).
type RevokedReservation struct {
	ReservationId string `json:"reservationId"`
	Revoked       bool   `json:"revoked"`
}

// --- workforce-management read-model DTOs ---------------------------------

// StaffingGap mirrors workforce-management's get_staffing_gap tool output.
type StaffingGap struct {
	BuildingId   string `json:"buildingId"`
	ShiftId      string `json:"shiftId"`
	PathId       string `json:"pathId"`
	PlannedHeads int    `json:"plannedHeads"`
	ActiveHeads  int    `json:"activeHeads"`
	Understaffed bool   `json:"understaffed"`
}

// ProposedHeads mirrors workforce-management's propose_path_heads tool
// output.
type ProposedHeads struct {
	BuildingId    string  `json:"buildingId"`
	PathId        string  `json:"pathId"`
	Charge        float64 `json:"charge"`
	PlannedRate   float64 `json:"plannedRate"`
	ProposedHeads int     `json:"proposedHeads"`
}

// LaborAssignment mirrors workforce-management's assign_labor tool output
// (write tool — bound for completeness; not called by any T1 use case).
type LaborAssignment struct {
	AssociateId  string `json:"associateId"`
	PathId       string `json:"pathId"`
	AssignmentId string `json:"assignmentId"`
}

// --- facility-layout read-model DTOs ---------------------------------------

// SiteRef mirrors facility-layout's list_sites entries.
type SiteRef struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// SitesResult mirrors facility-layout's list_sites tool output.
type SitesResult struct {
	Sites []SiteRef `json:"sites"`
}

// AisleLayout mirrors one aisle within facility-layout's get_site_layout
// tool output.
type AisleLayout struct {
	AisleID      string   `json:"aisleId"`
	AisleCode    string   `json:"aisleCode"`
	SequenceHint int      `json:"sequenceHint"`
	Direction    string   `json:"direction"`
	SlotCodes    []string `json:"slotCodes"`
}

// ZoneLayout mirrors one zone within facility-layout's get_site_layout tool
// output.
type ZoneLayout struct {
	ZoneID           string        `json:"zoneId"`
	AreaCode         string        `json:"areaCode"`
	ZoneCode         string        `json:"zoneCode"`
	TemperatureClass string        `json:"temperatureClass"`
	Hazmat           bool          `json:"hazmat"`
	Aisles           []AisleLayout `json:"aisles"`
}

// SiteLayout mirrors facility-layout's get_site_layout tool output.
type SiteLayout struct {
	Site  SiteRef      `json:"site"`
	Zones []ZoneLayout `json:"zones"`
}

// GridColumn mirrors one column of facility-layout's get_zone_grid tool
// output.
type GridColumn struct {
	AisleID      string `json:"aisleId"`
	AisleCode    string `json:"aisleCode"`
	Bay          string `json:"bay"`
	SequenceHint int    `json:"sequenceHint"`
}

// GridRow mirrors one row of facility-layout's get_zone_grid tool output.
type GridRow struct {
	Level string   `json:"level"`
	Cells []string `json:"cells"`
}

// ZoneGrid mirrors facility-layout's get_zone_grid tool output.
type ZoneGrid struct {
	ZoneID  string       `json:"zoneId"`
	Columns []GridColumn `json:"columns"`
	Levels  []string     `json:"levels"`
	Rows    []GridRow    `json:"rows"`
}

// TravelNode mirrors one waypoint (aisle + bay) of facility-layout's
// estimate_travel_distance tool output route.
type TravelNode struct {
	AisleID string `json:"aisleId"`
	Bay     string `json:"bay"`
}

// TravelDistance mirrors facility-layout's estimate_travel_distance tool
// output (travelDistanceDTO in its internal/adapters/inbound/mcp/mapping.go,
// ADR 0017 there). Estimated is true when one or more legs of the route
// were graph-estimated rather than measured -- callers surfacing this
// reading should say so rather than presenting every result as an
// equally precise measurement.
type TravelDistance struct {
	MetresM   float64      `json:"metresM"`
	Estimated bool         `json:"estimated"`
	Route     []TravelNode `json:"route"`
}
