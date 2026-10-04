// Package ports (this file): the outbound port for the warehouse-planning
// bounded context (ADR 0013), the third-wave upstream after the five
// original contexts (clients.go) and the second wave (clients_phase2.go).
//
// warehouse-planning's MCP server is READ+WRITE (create_/publish_/register_/
// declare_ tools exist on it). This port deliberately exposes ONLY its four
// read tools; no write tool has a method here and the zero-write fitness
// test (internal/architecture/zerowrite) fails the build if the mcpclient
// adapter ever names one.
package ports

import "context"

// WarehousePlanningClient is the outbound port for warehouse-planning's
// published READ tools: get_process_path_capacity, get_capacity_plan,
// get_storage_capacity and list_station_standards.
type WarehousePlanningClient interface {
	// GetProcessPathCapacity calls get_process_path_capacity.
	GetProcessPathCapacity(ctx context.Context, req ProcessPathCapacityRequest) (ProcessPathCapacity, error)
	// GetCapacityPlan calls get_capacity_plan (read by id; never creates
	// or publishes a plan).
	GetCapacityPlan(ctx context.Context, planId string) (CapacityPlan, error)
	// GetStorageCapacity calls get_storage_capacity for a site code.
	GetStorageCapacity(ctx context.Context, location string) (StorageCapacity, error)
	// ListStationStandards calls list_station_standards. An empty location
	// lists every declared standard (the tool's own default).
	ListStationStandards(ctx context.Context, location string) (StationStandardList, error)
}

// ProcessPathCapacityRequest is the argument set of get_process_path_capacity.
// WindowStart/WindowEnd are RFC3339 timestamps. UnitsPerOrder and
// PackagesPerOrder are workload conversion factors: they are passed through
// to the tool exactly as given and are OMITTED when nil -- this client never
// defaults or invents one. warehouse-planning reports a
// missing-conversion-factor tool error when a UNIT/PACKAGE step needs one.
type ProcessPathCapacityRequest struct {
	// PathId is the warehouse-planning process path id (its own
	// register_process_path vocabulary; NOT wes-work-planning's PathId).
	PathId string
	// Location is warehouse-planning's site/building code, e.g. "SIM1".
	Location         string
	WindowStart      string
	WindowEnd        string
	UnitsPerOrder    *float64
	PackagesPerOrder *float64
}

// --- warehouse-planning read-model DTOs -----------------------------------

// PlanningStepBreakdown mirrors one step_breakdown entry of
// get_process_path_capacity (stepBreakdownView in warehouse-planning's
// internal/adapters/inbound/mcp/tools.go). NormalizedRate is ORDER per hour.
type PlanningStepBreakdown struct {
	Step              string  `json:"step"`
	NormalizedRate    float64 `json:"normalized_rate"`
	BindingConstraint string  `json:"binding_constraint"`
}

// ProcessPathCapacity mirrors get_process_path_capacity's output
// (pathCapacityOutput). NormalizedRate is ORDER per hour and NormalizedUnit
// is "ORDER"; callers must check the unit rather than assume it.
type ProcessPathCapacity struct {
	NormalizedRate float64                 `json:"normalized_rate"`
	NormalizedUnit string                  `json:"normalized_unit"`
	BottleneckStep string                  `json:"bottleneck_step"`
	StepBreakdown  []PlanningStepBreakdown `json:"step_breakdown"`
	Warnings       []string                `json:"warnings"`
}

// CapacityPlan mirrors get_capacity_plan's output (capacityPlanOutput).
// PathCapacity is ORDER per hour; AssignedDemand, CapacityOverWindow and
// Shortage are orders. PublishedAt is nil while the plan is a DRAFT.
type CapacityPlan struct {
	Id                   string   `json:"id"`
	WarehouseId          string   `json:"warehouse_id"`
	Location             string   `json:"location"`
	WindowStart          string   `json:"window_start"`
	WindowEnd            string   `json:"window_end"`
	PathId               string   `json:"path_id"`
	AssignedDemand       float64  `json:"assigned_demand"`
	Status               string   `json:"status"`
	PathCapacity         float64  `json:"path_capacity"`
	BottleneckStep       string   `json:"bottleneck_step"`
	CapacityOverWindow   float64  `json:"capacity_over_window"`
	Shortage             float64  `json:"shortage"`
	CreatedAt            string   `json:"created_at"`
	PublishedAt          *string  `json:"published_at,omitempty"`
	BottleneckConstraint string   `json:"bottleneck_constraint"`
	Warnings             []string `json:"warnings"`
}

// StoragePositions mirrors one storage_positions entry of
// get_storage_capacity (storagePositionsView).
type StoragePositions struct {
	ZoneId       string `json:"zone_id"`
	LocationType string `json:"location_type"`
	Positions    int    `json:"positions"`
}

// ZoneStations mirrors one stations entry of get_storage_capacity
// (zoneStationsView): a COUNT of work-center stations, not a throughput.
type ZoneStations struct {
	ZoneId   string `json:"zone_id"`
	Activity string `json:"activity"`
	Stations int    `json:"stations"`
}

// StorageCapacity mirrors get_storage_capacity's output
// (storageCapacityOutput). Both lists are empty, never null, when nothing is
// tallied; positions are a count and carry no consumed/free figure.
type StorageCapacity struct {
	Location         string             `json:"location"`
	StoragePositions []StoragePositions `json:"storage_positions"`
	Stations         []ZoneStations     `json:"stations"`
}

// StationStandard mirrors one standards entry of list_station_standards
// (stationStandardView): the throughput of ONE station of ProcessType at
// Location, Quantity of Unit per PeriodSeconds.
type StationStandard struct {
	Location      string  `json:"location"`
	ProcessType   string  `json:"process_type"`
	Quantity      float64 `json:"quantity"`
	Unit          string  `json:"unit"`
	PeriodSeconds float64 `json:"period_seconds"`
}

// StationStandardList mirrors list_station_standards' output
// (listStationStandardsOutput). Location echoes the filter when one was given.
type StationStandardList struct {
	Location  string            `json:"location,omitempty"`
	Standards []StationStandard `json:"standards"`
}
