package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// LaborPerformance implements ports.LaborPerformanceClient by calling
// labor-performance's published get_task_type_utilization MCP tool.
type LaborPerformance struct {
	session *Session
}

// NewLaborPerformance builds a LaborPerformance client for the given
// connection config.
func NewLaborPerformance(cfg Config) *LaborPerformance {
	cfg.Name = "labor-performance"
	return &LaborPerformance{session: New(cfg)}
}

var _ ports.LaborPerformanceClient = (*LaborPerformance)(nil)

// GetTaskTypeUtilization calls get_task_type_utilization. windowSeconds is
// forwarded as-is: the tool itself treats a non-positive or omitted value
// as "apply its own default" (1h), so this client never substitutes a
// default of its own.
func (c *LaborPerformance) GetTaskTypeUtilization(ctx context.Context, taskType string, windowSeconds int64) (ports.TaskTypeUtilization, error) {
	var out ports.TaskTypeUtilization
	args := map[string]any{"taskType": taskType}
	if windowSeconds > 0 {
		args["windowSeconds"] = windowSeconds
	}
	err := c.session.callTool(ctx, "get_task_type_utilization", args, &out)
	return out, err
}
