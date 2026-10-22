// Package ports (this file): the outbound port for the exception-management
// bounded context (wes), the fleet's owner of the warehouse's exception
// cases, their policies and their SLA clocks.
//
// exception-management's MCP server is READ-ONLY (its own governance test
// pins its four tools, every one annotated readOnlyHint). This port
// mirrors all four: list_open_exceptions, get_exception,
// get_exception_policies and get_sla_report. exception-management's
// writes (triage, assign, escalate, resolve, declare a policy) are
// REST-only there and have no method here. The zero-write fitness test
// (internal/architecture/zerowrite) fails the build if the mcpclient
// adapter ever names a write-verb tool, and the contract snapshot test in
// mcpclient pins the published tools against exception-management's
// published registry.
package ports

import (
	"context"
)

// ExceptionListQuery is the argument set of list_open_exceptions. Every
// field is a filter and is OMITTED from the call when empty, so
// exception-management lists every status (open and resolved alike), every
// queue, site and source type; this client never invents one.
type ExceptionListQuery struct {
	// Status keeps only cases in this status: OPEN, TRIAGED, ASSIGNED or
	// RESOLVED.
	Status string
	// Queue keeps only cases of this queue.
	Queue string
	// SiteID keeps only cases of this site.
	SiteID string
	// SourceType keeps only cases opened by this CloudEvents source type.
	SourceType string
}

// ExceptionManagementClient is the outbound port for
// exception-management's four published read tools.
type ExceptionManagementClient interface {
	// ListOpenExceptions calls list_open_exceptions: the exception cases,
	// newest opened first, narrowed by the query's non-empty filters.
	ListOpenExceptions(ctx context.Context, query ExceptionListQuery) (ExceptionCaseList, error)
	// GetException calls get_exception: one exception case by id.
	GetException(ctx context.Context, caseID string) (ExceptionCase, error)
	// GetExceptionPolicies calls get_exception_policies: the policy
	// declared for every source type, ordered by source type.
	GetExceptionPolicies(ctx context.Context) (ExceptionPolicyList, error)
	// GetSLAReport calls get_sla_report: how the open caseload sits
	// against its SLA clocks.
	GetSLAReport(ctx context.Context) (ExceptionSLAReport, error)
}

// --- exception-management read-model DTOs ------------------------------------
//
// Field-for-field mirrors of exception-management's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay the
// RFC 3339 strings the server publishes; a consuming use case parses them.
// The contract snapshot test in mcpclient checks every json tag below
// against the published output schemas in
// testdata/exception_management_tools.golden.json.

// ExceptionAuditEntry mirrors auditView: one accepted transition of a
// case's state trail.
type ExceptionAuditEntry struct {
	Action string `json:"action"`
	Actor  string `json:"actor"`
	At     string `json:"at"`
}

// ExceptionCase mirrors caseView (get_exception and each
// list_open_exceptions item). SiteID, Assignee and the resolution fields
// (ResolutionCode, RootCause, Note, ResolvedAt) are empty until they
// exist; AutoResolveRuleID, CorrelatedType and CorrelatedEventID are set
// only on an auto-resolved case. SourcePayload is the frozen JSON object
// of the opening fact, exactly as exception-management stored it.
type ExceptionCase struct {
	CaseID            string                `json:"case_id"`
	SourceType        string                `json:"source_type"`
	SourceSubject     string                `json:"source_subject"`
	SiteID            string                `json:"site_id,omitempty"`
	Status            string                `json:"status"`
	Severity          string                `json:"severity"`
	Queue             string                `json:"queue"`
	OccurrenceCount   int64                 `json:"occurrence_count"`
	Escalated         bool                  `json:"escalated"`
	Assignee          string                `json:"assignee,omitempty"`
	ResolutionCode    string                `json:"resolution_code,omitempty"`
	RootCause         string                `json:"root_cause,omitempty"`
	Note              string                `json:"note,omitempty"`
	ResolvedAt        string                `json:"resolved_at,omitempty"`
	AutoResolveRuleID string                `json:"auto_resolve_rule_id,omitempty"`
	CorrelatedType    string                `json:"correlated_type,omitempty"`
	CorrelatedEventID string                `json:"correlated_event_id,omitempty"`
	OpenedAt          string                `json:"opened_at"`
	LastSeenAt        string                `json:"last_seen_at"`
	SLAMinutes        int64                 `json:"sla_minutes"`
	SLADueAt          string                `json:"sla_due_at"`
	SourceEventID     string                `json:"source_event_id"`
	SourcePayload     map[string]any        `json:"source_payload"`
	Audit             []ExceptionAuditEntry `json:"audit"`
	Version           int64                 `json:"version"`
}

// ExceptionCaseList mirrors exceptionListView (list_open_exceptions).
type ExceptionCaseList struct {
	Items []ExceptionCase `json:"items"`
}

// ExceptionAutoResolveRule mirrors autoResolveView: the correlated
// CloudEvents type and the window within which it auto-resolves a case.
type ExceptionAutoResolveRule struct {
	OnType        string `json:"on_type"`
	WithinMinutes int64  `json:"within_minutes"`
}

// ExceptionPolicy mirrors policyView (each get_exception_policies item):
// the LATEST policy declared for one source type. AutoResolve is nil when
// the type has no auto-resolve rule.
type ExceptionPolicy struct {
	SourceType  string                    `json:"source_type"`
	Version     int64                     `json:"version"`
	Severity    string                    `json:"severity"`
	Queue       string                    `json:"queue"`
	SLAMinutes  int64                     `json:"sla_minutes"`
	AutoResolve *ExceptionAutoResolveRule `json:"auto_resolve"`
	DeclaredAt  string                    `json:"declared_at"`
}

// ExceptionPolicyList mirrors policyListView (get_exception_policies).
type ExceptionPolicyList struct {
	Items []ExceptionPolicy `json:"items"`
}

// ExceptionSLAReport mirrors slaReportView (get_sla_report): the open
// caseload against its SLA clocks at GeneratedAt. Breached counts open
// cases past their SLA due instant, Escalated the cases the SLA sweeper
// flagged; BySeverity and ByQueue are the open counts by each key.
type ExceptionSLAReport struct {
	GeneratedAt string         `json:"generated_at"`
	OpenCases   int            `json:"open_cases"`
	Breached    int            `json:"breached"`
	Escalated   int            `json:"escalated"`
	BySeverity  map[string]int `json:"by_severity"`
	ByQueue     map[string]int `json:"by_queue"`
}
