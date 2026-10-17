// Package ports (this file): the outbound port for slotting-optimization's
// read-only MCP server (ADR 0024). Its server serves four read tools
// (get_slot_plan, list_slot_plans, get_forward_slots, get_sku_velocity);
// this port deliberately carries only the TWO the slot-plan watch consumes
// (list_slot_plans, get_slot_plan). Wiring a tool this agent never calls is
// the wired-but-unused bug class this port exists to avoid.
package ports

import "context"

// SlottingOptimizationClient is the outbound port for slotting-optimization's
// published slot-plan read tools.
type SlottingOptimizationClient interface {
	// ListSlotPlans calls list_slot_plans: summaries of plans newest first,
	// optionally filtered to one site and/or one state (Draft, Approved,
	// Rejected, Superseded), a page at a time.
	ListSlotPlans(ctx context.Context, query SlotPlanListQuery) (SlotPlanPage, error)
	// GetSlotPlan calls get_slot_plan: one plan by id, including its
	// execution progress (moves_total / moves_executed per relocation)
	// recorded from inventory-storage.
	GetSlotPlan(ctx context.Context, planId string) (SlotPlan, error)
}

// SlotPlanListQuery mirrors list_slot_plans' published input: limit (1..500),
// cursor, site_id and state, every one optional. Zero fields are omitted so
// slotting-optimization's own defaults apply.
type SlotPlanListQuery struct {
	Limit  int
	Cursor string
	SiteId string
	State  string
}

// --- slotting-optimization read-model DTOs ---------------------------------
//
// Field-for-field mirrors of slotting-optimization's published tool output
// (its internal/adapters/inbound/mcp testdata/tool_registry.golden.json),
// pinned by mcpclient's contract tests against that golden.

// SlotPlanPage is list_slot_plans' output: one page of summaries plus the
// next cursor (absent on the last page).
type SlotPlanPage struct {
	Items      []SlotPlanSummary `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// SlotPlanSummary is one list_slot_plans item: a plan's identity and counts
// without its assignments or execution progress.
type SlotPlanSummary struct {
	PlanId          string `json:"plan_id"`
	SiteId          string `json:"site_id"`
	State           string `json:"state"`
	Policy          string `json:"policy"`
	WindowFrom      string `json:"window_from"`
	WindowTo        string `json:"window_to"`
	GeneratedAt     string `json:"generated_at"`
	ApprovedAt      string `json:"approved_at,omitempty"`
	AssignmentCount int    `json:"assignment_count"`
	MoveCount       int    `json:"move_count"`
	UnassignedCount int    `json:"unassigned_count"`
	Version         int    `json:"version"`
}

// SlotPlanAssignment is one forward-slot assignment of a plan.
type SlotPlanAssignment struct {
	Sku      string `json:"sku"`
	Slot     string `json:"slot"`
	AbcClass string `json:"abc_class"`
	Picks    int    `json:"picks"`
	Units    int    `json:"units"`
}

// SlotPlanMove is one planned move against the previous plan.
type SlotPlanMove struct {
	Sku      string `json:"sku"`
	Kind     string `json:"kind"`
	FromSlot string `json:"from_slot,omitempty"`
	ToSlot   string `json:"to_slot,omitempty"`
}

// SlotPlanUnassigned is one SKU the planner could not assign, with its
// reason.
type SlotPlanUnassigned struct {
	Sku    string `json:"sku"`
	Reason string `json:"reason"`
}

// SlotPlanExecutionMove is one move's execution progress, recorded from
// inventory-storage's relocations.
type SlotPlanExecutionMove struct {
	MoveSeq          int    `json:"move_seq"`
	Sku              string `json:"sku"`
	Kind             string `json:"kind"`
	Executed         bool   `json:"executed"`
	ExecutedQuantity int    `json:"executed_quantity"`
	LastExecutedAt   string `json:"last_executed_at,omitempty"`
}

// SlotPlanExecution is get_slot_plan's execution view: how much of the
// approved plan's moves has actually been executed. Nil means
// slotting-optimization has recorded no execution progress at all yet.
type SlotPlanExecution struct {
	MovesTotal     int                     `json:"moves_total"`
	MovesExecuted  int                     `json:"moves_executed"`
	Moves          []SlotPlanExecutionMove `json:"moves"`
	LastExecutedAt string                  `json:"last_executed_at,omitempty"`
}

// SlotPlan is get_slot_plan's output: one plan in full.
type SlotPlan struct {
	PlanId     string `json:"plan_id"`
	SiteId     string `json:"site_id"`
	State      string `json:"state"`
	Policy     string `json:"policy"`
	WindowFrom string `json:"window_from"`
	WindowTo   string `json:"window_to"`
	// GeneratedAt / ApprovedAt / RejectedAt / SupersededAt are RFC 3339
	// instants; each decision instant is absent until it happens.
	GeneratedAt    string `json:"generated_at"`
	ApprovedAt     string `json:"approved_at,omitempty"`
	RejectedAt     string `json:"rejected_at,omitempty"`
	SupersededAt   string `json:"superseded_at,omitempty"`
	RejectReason   string `json:"reject_reason,omitempty"`
	SupersedesPlan string `json:"supersedes_plan_id,omitempty"`

	Assignments []SlotPlanAssignment `json:"assignments"`
	Moves       []SlotPlanMove       `json:"moves"`
	Unassigned  []SlotPlanUnassigned `json:"unassigned"`
	// Execution is nil until inventory-storage records relocations
	// against this plan.
	Execution *SlotPlanExecution `json:"execution,omitempty"`
	Version   int                `json:"version"`
}
