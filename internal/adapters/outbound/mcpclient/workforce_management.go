package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// WorkforceManagement implements ports.WorkforceManagementClient by calling
// workforce-management's published get_staffing_gap MCP tool.
type WorkforceManagement struct {
	session *Session
}

// NewWorkforceManagement builds a WorkforceManagement client for the given
// connection config.
func NewWorkforceManagement(cfg Config) *WorkforceManagement {
	cfg.Name = "workforce-management"
	return &WorkforceManagement{session: New(cfg)}
}

func (c *WorkforceManagement) GetStaffingGap(ctx context.Context, buildingId, shiftId, pathId string) (ports.StaffingGap, error) {
	var out ports.StaffingGap
	err := c.session.callTool(ctx, "get_staffing_gap", map[string]any{
		"buildingId": buildingId,
		"shiftId":    shiftId,
		argPathID:    pathId,
	}, &out)
	return out, err
}

// GetStaffingGapForSite calls get_staffing_gap by the canonical siteCode,
// which scopes the associate count to that site (ADR 0034 there). It is the
// read behind the labor-reallocation proposals (ADR 0029).
func (c *WorkforceManagement) GetStaffingGapForSite(ctx context.Context, siteCode, shiftId, pathId string) (ports.StaffingGap, error) {
	var out ports.StaffingGap
	err := c.session.callTool(ctx, "get_staffing_gap", map[string]any{
		"siteCode": siteCode,
		"shiftId":  shiftId,
		"pathId":   pathId,
	}, &out)
	return out, err
}
