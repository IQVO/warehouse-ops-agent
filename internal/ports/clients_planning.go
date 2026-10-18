// Package ports (this file): the outbound port for the warehouse-planning
// bounded context (ADR 0013), the third-wave upstream after the five
// original contexts (clients.go) and the second wave (clients_phase2.go).
//
// warehouse-planning's MCP server is READ+WRITE (create_/publish_/register_/
// declare_ tools exist on it). This port deliberately exposes ONLY its one
// consumed read tool; no write tool has a method here and the zero-write fitness
// test (internal/architecture/zerowrite) fails the build if the mcpclient
// adapter ever names one.
package ports

import "context"

// WarehousePlanningClient is the outbound port for warehouse-planning's
// published READ tool get_process_path_capacity.
type WarehousePlanningClient interface {
	// GetProcessPathCapacity calls get_process_path_capacity.
	GetProcessPathCapacity(ctx context.Context, req ProcessPathCapacityRequest) (ProcessPathCapacity, error)
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
