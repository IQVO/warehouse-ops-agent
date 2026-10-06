// Package ports (this file): the three outbound ports for the
// second-wave upstream contexts (order-management, labor-performance,
// process-path-management) whose MCP servers came online in a
// fleet-wide wiring pass after the original five ports in clients.go
// were defined. Kept in a separate file, mirroring the split already
// used for order_lifecycle_clients.go / console_reports_clients.go, to
// keep this addition's diff isolated from the original five-context
// surface.
//
// Consumption today: LaborPerformanceClient IS consumed -- by
// FlowBalanceAdvisory's labor-utilization correlation overlay (ADR 0008,
// via GetTaskTypeUtilization). OrderManagementMCPClient and
// ProcessPathManagementClient are wired in cmd/agent/main.go as available
// dependencies but are NOT consumed by any use case yet -- see the
// "_ = om" / "_ = ppm" lines there, mirroring the existing
// InventoryStorageClient wired-but-unconsumed precedent (ADR 0007).
package ports

import "context"

// OrderManagementMCPClient is the outbound port for order-management's
// published MCP read tool (get_order). Named distinctly from
// OrderManagementClient in order_lifecycle_clients.go (that one wraps
// order-management's plain REST GET /orders/{id} for the console-bff's
// order-lifecycle fan-out) to avoid a name collision: this one wraps its
// curated MCP tool for a future LLM-facing / decision-support use case.
// The two port families (MCP vs REST) are deliberately never mixed into
// one interface -- see order_lifecycle_clients.go's own doc comment for
// that rationale.
type OrderManagementMCPClient interface {
	GetOrder(ctx context.Context, orderId string) (Order, error)
}

// LaborPerformanceClient is the outbound port for labor-performance's
// published read tools (get_associate_scorecard,
// get_task_type_performance, get_labor_standard,
// get_task_type_utilization).
type LaborPerformanceClient interface {
	GetAssociateScorecard(ctx context.Context, associateId string) (AssociateScorecard, error)
	GetTaskTypePerformance(ctx context.Context, taskType string) (TaskTypePerformance, error)
	GetLaborStandard(ctx context.Context, taskType string) (LaborStandard, error)
	// GetTaskTypeUtilization calls get_task_type_utilization. windowSeconds
	// non-positive means "use the tool's own default" (1h); it is passed
	// through unchanged, never defaulted here.
	GetTaskTypeUtilization(ctx context.Context, taskType string, windowSeconds int64) (TaskTypeUtilization, error)
}

// ProcessPathManagementClient is the outbound port for
// process-path-management's published read tools (get_process_path,
// list_process_paths).
type ProcessPathManagementClient interface {
	GetProcessPath(ctx context.Context, pathId string) (ProcessPath, error)
	ListProcessPaths(ctx context.Context, activeOnly bool) (ProcessPathList, error)
}

// --- order-management read-model DTOs -------------------------------------

// OrderLine mirrors one line of order-management's get_order tool output
// (orderDTO.Lines[i] in its internal/adapters/inbound/mcp/mapping.go).
type OrderLine struct {
	LineNo        int     `json:"lineNo"`
	SKU           string  `json:"sku"`
	Quantity      int     `json:"quantity"`
	PathId        string  `json:"pathId"`
	GiftWrap      bool    `json:"giftWrap"`
	Status        string  `json:"status"`
	ReservationId *string `json:"reservationId,omitempty"`
}

// Order mirrors order-management's get_order tool output (orderDTO in its
// internal/adapters/inbound/mcp/mapping.go).
type Order struct {
	Id                   string      `json:"id"`
	Status               string      `json:"status"`
	AllowPartialShipment bool        `json:"allowPartialShipment"`
	PromiseDate          *string     `json:"promiseDate,omitempty"`
	Lines                []OrderLine `json:"lines"`
}

// --- labor-performance read-model DTOs ------------------------------------

// TaskTypeBreakdown mirrors one TaskType entry of labor-performance's
// get_associate_scorecard tool output (taskTypeBreakdownDTO in its
// internal/adapters/inbound/mcp/mapping.go).
type TaskTypeBreakdown struct {
	TaskCount         int      `json:"taskCount"`
	MeanEfficiencyPct *float64 `json:"meanEfficiencyPct"`
}

// AssociateScorecard mirrors labor-performance's get_associate_scorecard
// tool output (scorecardDTO in its internal/adapters/inbound/mcp/mapping.go).
type AssociateScorecard struct {
	AssociateId       string                       `json:"associateId"`
	TaskCount         int                          `json:"taskCount"`
	MeanEfficiencyPct *float64                     `json:"meanEfficiencyPct"`
	ByTaskType        map[string]TaskTypeBreakdown `json:"byTaskType"`
	Trend             string                       `json:"trend"`
	CoachingFlag      bool                         `json:"coachingFlag"`
}

// TaskTypePerformance mirrors labor-performance's
// get_task_type_performance tool output (taskTypePerformanceDTO in its
// internal/adapters/inbound/mcp/mapping.go).
type TaskTypePerformance struct {
	TaskType          string   `json:"taskType"`
	TaskCount         int      `json:"taskCount"`
	MeanEfficiencyPct *float64 `json:"meanEfficiencyPct"`
	MeanActualSeconds *float64 `json:"meanActualSeconds"`
}

// LaborStandard mirrors labor-performance's get_labor_standard tool
// output (standardDTO in its internal/adapters/inbound/mcp/mapping.go).
type LaborStandard struct {
	TaskType        string  `json:"taskType"`
	ExpectedSeconds int64   `json:"expectedSeconds"`
	EffectiveFrom   string  `json:"effectiveFrom"`
	EffectiveTo     *string `json:"effectiveTo,omitempty"`
}

// TaskTypeUtilization mirrors labor-performance's
// get_task_type_utilization tool output (utilizationDTO in its
// internal/adapters/inbound/mcp/mapping.go). UtilizationPct is nil when
// nothing was observed in the window -- callers must never coerce that
// nil to 0%, per that tool's published contract.
type TaskTypeUtilization struct {
	TaskType       string   `json:"taskType"`
	Associates     int      `json:"associates"`
	WindowSeconds  int64    `json:"windowSeconds"`
	TaskSeconds    int64    `json:"taskSeconds"`
	IdleSeconds    int64    `json:"idleSeconds"`
	OpenGapSeconds int64    `json:"openGapSeconds"`
	UtilizationPct *float64 `json:"utilizationPct"`
}

// --- process-path-management read-model DTOs ------------------------------

// ProcessPath mirrors process-path-management's get_process_path /
// list_process_paths tool output (processPathDTO in its
// internal/adapters/inbound/mcp/mapping.go).
type ProcessPath struct {
	PathId               string   `json:"pathId"`
	MatchPrefix          string   `json:"matchPrefix"`
	Direct               bool     `json:"direct"`
	RequiredCapabilities []string `json:"requiredCapabilities"`
	Status               string   `json:"status"`
	Active               bool     `json:"active"`
	CreatedAt            string   `json:"createdAt"`
	UpdatedAt            string   `json:"updatedAt"`
}

// ProcessPathList mirrors process-path-management's list_process_paths
// tool output (listProcessPathsOutput in its
// internal/adapters/inbound/mcp/tools.go).
type ProcessPathList struct {
	Paths []ProcessPath `json:"paths"`
}
