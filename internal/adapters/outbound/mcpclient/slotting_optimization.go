package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// SlottingOptimization implements ports.SlottingOptimizationClient by
// calling slotting-optimization's published read tools list_slot_plans and
// get_slot_plan (ADR 0024). The server also publishes get_forward_slots and
// get_sku_velocity; this client does not name them because no use case of
// this agent calls them — a wired-but-unused method is the bug class this
// change closes (gap A14).
//
// slotting-optimization's MCP server is read-only (every tool annotated
// readOnlyHint in its published registry); the zero-write fitness test
// (internal/architecture/zerowrite) scans this package's tool-name literals,
// and zerowrite.TestSlottingOptimizationClientCallsOnlyPinnedTools pins this
// file to exactly these two names. The argument keys and result shapes are
// pinned against slotting-optimization's published registry in
// testdata/slotting_optimization_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error; the
// consuming use case decides what to do with it.
type SlottingOptimization struct {
	session *Session
}

// NewSlottingOptimization builds a SlottingOptimization client for the given
// connection config.
func NewSlottingOptimization(cfg Config) *SlottingOptimization {
	cfg.Name = "slotting-optimization"
	return &SlottingOptimization{session: New(cfg)}
}

var _ ports.SlottingOptimizationClient = (*SlottingOptimization)(nil)

// ListSlotPlans calls list_slot_plans. Zero query fields are omitted so
// slotting-optimization applies its own defaults.
func (c *SlottingOptimization) ListSlotPlans(ctx context.Context, query ports.SlotPlanListQuery) (ports.SlotPlanPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "site_id", query.SiteId)
	setIfNotEmpty(args, "state", query.State)
	var out ports.SlotPlanPage
	err := c.session.callTool(ctx, "list_slot_plans", args, &out)
	return out, err
}

// GetSlotPlan calls get_slot_plan for one plan id.
func (c *SlottingOptimization) GetSlotPlan(ctx context.Context, planId string) (ports.SlotPlan, error) {
	var out ports.SlotPlan
	err := c.session.callTool(ctx, "get_slot_plan", map[string]any{"plan_id": planId}, &out)
	return out, err
}
