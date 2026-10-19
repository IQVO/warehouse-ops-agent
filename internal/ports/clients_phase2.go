// Package ports (this file): the outbound ports for the
// second-wave upstream contexts (order-management, labor-performance)
// whose MCP servers came online in a fleet-wide wiring pass after the
// original five ports in clients.go were defined. Kept in a separate
// file, mirroring the split already used for
// order_lifecycle_clients.go / console_reports_clients.go, to keep this
// addition's diff isolated from the original five-context surface.
//
// Consumption today: LaborPerformanceClient IS consumed -- by
// FlowBalanceAdvisory's labor-utilization correlation overlay (ADR 0008,
// via GetTaskTypeUtilization). OrderManagementMCPClient IS consumed by
// the daily brief's promise-health section (ADR 0024, via
// GetPromiseHealth); its get_order tool remains deliberately unwired
// until a use case needs it.
package ports

import (
	"context"
	"time"
)

// OrderManagementMCPClient is the outbound port for order-management's
// curated MCP read tools. Named distinctly from OrderManagementClient in
// order_lifecycle_clients.go (that one wraps order-management's plain REST
// GET /orders/{id} for the console-bff's order-lifecycle fan-out) to avoid
// a name collision: this one wraps its read-only decision-support tools
// for the daily brief. The two port families (MCP vs REST) are
// deliberately never mixed into one interface -- see
// order_lifecycle_clients.go's own doc comment for that rationale.
type OrderManagementMCPClient interface {
	// GetPromiseHealth calls get_promise_health: order-management's
	// promise KPIs (promise basis distribution, re-promise rate,
	// split-shipment rate, promise-to-cutoff gap) aggregated over the
	// window [From, To), optionally filtered to one exact-match path.
	GetPromiseHealth(ctx context.Context, query PromiseHealthQuery) (PromiseHealth, error)
}

// PromiseHealthQuery mirrors get_promise_health's published input: the
// inclusive-from / exclusive-to window (RFC 3339 on the wire) and the
// optional exact-match pathId filter ("" means no filter).
type PromiseHealthQuery struct {
	From   time.Time
	To     time.Time
	PathId string
}

// --- order-management read-model DTOs -------------------------------------

// PromiseHealth mirrors order-management's get_promise_health tool output
// (promiseHealthDTO in its internal/adapters/inbound/mcp/promise_health.go):
// the promise KPI summary for the requested window. OrdersAllocatedTotal
// is the sum of the three basis counts; the two rates are fractions of it
// (0 when nothing was allocated).
type PromiseHealth struct {
	PromiseBasisCapability    int     `json:"promiseBasisCapability"`
	PromiseBasisLeadTime      int     `json:"promiseBasisLeadTime"`
	PromiseBasisNetwork       int     `json:"promiseBasisNetwork"`
	OrdersAllocatedTotal      int     `json:"ordersAllocatedTotal"`
	OrdersSplitShipment       int     `json:"ordersSplitShipment"`
	SplitShipmentRate         float64 `json:"splitShipmentRate"`
	OrdersRepromised          int     `json:"ordersRepromised"`
	RepromiseRate             float64 `json:"repromiseRate"`
	PromiseToCutoffGapSeconds float64 `json:"promiseToCutoffGapSeconds"`
}

// LaborPerformanceClient is the outbound port for labor-performance's
// published read tool (get_task_type_utilization).
type LaborPerformanceClient interface {
	// GetTaskTypeUtilization calls get_task_type_utilization. windowSeconds
	// non-positive means "use the tool's own default" (1h); it is passed
	// through unchanged, never defaulted here.
	GetTaskTypeUtilization(ctx context.Context, taskType string, windowSeconds int64) (TaskTypeUtilization, error)
}

// ProcessPathManagementClient and its DTOs were REMOVED (ADR 0024, gap
// A9): process-path-management's catalogue configuration is not an ops
// signal, no use case ever consumed the client, and a wired-but-unused
// client is exactly the dead wiring this change set closes.

// --- labor-performance read-model DTOs ------------------------------------

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
