package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// FulfillmentExecution implements ports.FulfillmentExecutionClient by
// calling fulfillment-execution's published get_queue_status
// and diagnose_stuck_tasks MCP tools.
type FulfillmentExecution struct {
	session *Session
}

// NewFulfillmentExecution builds a FulfillmentExecution client for the given
// connection config.
func NewFulfillmentExecution(cfg Config) *FulfillmentExecution {
	cfg.Name = "fulfillment-execution"
	return &FulfillmentExecution{session: New(cfg)}
}

var _ ports.FulfillmentExecutionClient = (*FulfillmentExecution)(nil)

func (c *FulfillmentExecution) GetQueueStatus(ctx context.Context, processPath string) (ports.QueueStatus, error) {
	var out ports.QueueStatus
	err := c.session.callTool(ctx, "get_queue_status", map[string]any{"processPath": processPath}, &out)
	return out, err
}

func (c *FulfillmentExecution) DiagnoseStuckTasks(ctx context.Context, withinSeconds int) (ports.StuckTasksResult, error) {
	var out ports.StuckTasksResult
	err := c.session.callTool(ctx, "diagnose_stuck_tasks", map[string]any{"withinSeconds": withinSeconds}, &out)
	return out, err
}
