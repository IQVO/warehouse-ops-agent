package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// WarehousePlanning implements ports.WarehousePlanningClient by calling
// warehouse-planning's published READ tools: get_process_path_capacity,
// get_capacity_plan, get_storage_capacity and list_station_standards.
//
// warehouse-planning's MCP server also publishes write tools
// (register_*, create_*, publish_*, declare_*). This client never names
// one: the zero-write fitness test
// (internal/architecture/zerowrite) scans this package's tool-name
// literals and fails the build if a write-prefixed tool appears (ADR 0013).
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker (those exist only on the Anthropic reasoner path, ADR
// 0011). Every failure is returned as an ordinary error; the consuming use
// case decides to fail open.
type WarehousePlanning struct {
	session *Session
}

// NewWarehousePlanning builds a WarehousePlanning client for the given
// connection config.
func NewWarehousePlanning(cfg Config) *WarehousePlanning {
	cfg.Name = "warehouse-planning"
	return &WarehousePlanning{session: New(cfg)}
}

var _ ports.WarehousePlanningClient = (*WarehousePlanning)(nil)

// GetProcessPathCapacity calls get_process_path_capacity. The optional
// conversion factors are sent only when non-nil.
func (c *WarehousePlanning) GetProcessPathCapacity(ctx context.Context, req ports.ProcessPathCapacityRequest) (ports.ProcessPathCapacity, error) {
	args := map[string]any{
		"id":           req.PathId,
		"location":     req.Location,
		"window_start": req.WindowStart,
		"window_end":   req.WindowEnd,
	}
	if req.UnitsPerOrder != nil {
		args["units_per_order"] = *req.UnitsPerOrder
	}
	if req.PackagesPerOrder != nil {
		args["packages_per_order"] = *req.PackagesPerOrder
	}
	var out ports.ProcessPathCapacity
	err := c.session.callTool(ctx, "get_process_path_capacity", args, &out)
	return out, err
}

// GetCapacityPlan calls get_capacity_plan (read by id).
func (c *WarehousePlanning) GetCapacityPlan(ctx context.Context, planId string) (ports.CapacityPlan, error) {
	var out ports.CapacityPlan
	err := c.session.callTool(ctx, "get_capacity_plan", map[string]any{"id": planId}, &out)
	return out, err
}

// GetStorageCapacity calls get_storage_capacity for a site code.
func (c *WarehousePlanning) GetStorageCapacity(ctx context.Context, location string) (ports.StorageCapacity, error) {
	var out ports.StorageCapacity
	err := c.session.callTool(ctx, "get_storage_capacity", map[string]any{"location": location}, &out)
	return out, err
}

// ListStationStandards calls list_station_standards; an empty location
// omits the filter and lists every declared standard.
func (c *WarehousePlanning) ListStationStandards(ctx context.Context, location string) (ports.StationStandardList, error) {
	args := map[string]any{}
	if location != "" {
		args["location"] = location
	}
	var out ports.StationStandardList
	err := c.session.callTool(ctx, "list_station_standards", args, &out)
	return out, err
}
