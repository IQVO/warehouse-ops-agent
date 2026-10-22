package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// ExceptionManagement implements ports.ExceptionManagementClient by calling
// exception-management's four published READ tools list_open_exceptions,
// get_exception, get_exception_policies and get_sla_report.
//
// exception-management's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestExceptionManagementClientCallsOnlyPinnedTools allows exactly these
// four names. The argument keys and result shapes are pinned against
// exception-management's published registry in
// testdata/exception_management_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput, a
// *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming use
// case decides what to do with it.
type ExceptionManagement struct {
	session *Session
}

// NewExceptionManagement builds an ExceptionManagement client for the
// given connection config.
func NewExceptionManagement(cfg Config) *ExceptionManagement {
	cfg.Name = "exception-management"
	return &ExceptionManagement{session: New(cfg)}
}

var _ ports.ExceptionManagementClient = (*ExceptionManagement)(nil)

// ListOpenExceptions calls list_open_exceptions. Zero query fields are
// omitted so exception-management applies its own defaults (every status,
// queue, site and source type alike).
func (c *ExceptionManagement) ListOpenExceptions(ctx context.Context, query ports.ExceptionListQuery) (ports.ExceptionCaseList, error) {
	args := map[string]any{}
	setIfNotEmpty(args, "status", query.Status)
	setIfNotEmpty(args, "queue", query.Queue)
	setIfNotEmpty(args, "site_id", query.SiteID)
	setIfNotEmpty(args, "source_type", query.SourceType)
	var out ports.ExceptionCaseList
	err := c.session.callTool(ctx, "list_open_exceptions", args, &out)
	return out, err
}

// GetException calls get_exception for one exception case id.
func (c *ExceptionManagement) GetException(ctx context.Context, caseID string) (ports.ExceptionCase, error) {
	var out ports.ExceptionCase
	err := c.session.callTool(ctx, "get_exception", map[string]any{"case_id": caseID}, &out)
	return out, err
}

// GetExceptionPolicies calls get_exception_policies. It takes no
// arguments; exception-management answers the latest policy declared for
// every source type, ordered by source type.
func (c *ExceptionManagement) GetExceptionPolicies(ctx context.Context) (ports.ExceptionPolicyList, error) {
	var out ports.ExceptionPolicyList
	err := c.session.callTool(ctx, "get_exception_policies", map[string]any{}, &out)
	return out, err
}

// GetSLAReport calls get_sla_report. It takes no arguments;
// exception-management answers the open caseload against its SLA clocks
// as of the report's own generated_at.
func (c *ExceptionManagement) GetSLAReport(ctx context.Context) (ports.ExceptionSLAReport, error) {
	var out ports.ExceptionSLAReport
	err := c.session.callTool(ctx, "get_sla_report", map[string]any{}, &out)
	return out, err
}
