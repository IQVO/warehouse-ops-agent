// Package ports (this file): the outbound port for the returns-processing
// bounded context (wms), the fleet's owner of return authorizations (RMAs),
// return receipts and the disposition policy.
//
// returns-processing's MCP server is READ-ONLY (its own governance test
// pins its three tools, every one annotated readOnlyHint). This port
// mirrors all three: get_return, list_returns_by_order and
// get_disposition_policy. returns-processing's writes (authorize a return,
// reject a return authorization, open a receipt, inspect a line, override a
// disposition, declare a policy) are REST-only there and have no method
// here. The zero-write fitness test (internal/architecture/zerowrite) fails
// the build if the mcpclient adapter ever names a write-verb tool, and the
// contract snapshot test in mcpclient pins the published tools against
// returns-processing's published registry.
package ports

import (
	"context"
)

// ReturnsProcessingClient is the outbound port for returns-processing's
// three published read tools.
type ReturnsProcessingClient interface {
	// GetReturn calls get_return: one return authorization by id.
	GetReturn(ctx context.Context, rmaID string) (ReturnAuthorization, error)
	// ListReturnsByOrder calls list_returns_by_order: every return
	// authorization of one original order (empty for an order with no
	// returns — never an error).
	ListReturnsByOrder(ctx context.Context, orderID string) (ReturnsByOrder, error)
	// GetDispositionPolicy calls get_disposition_policy: the disposition
	// policy table — version 0 means the CURRENT version, a positive
	// version that declared one.
	GetDispositionPolicy(ctx context.Context, version int64) (DispositionPolicy, error)
}

// --- returns-processing read-model DTOs --------------------------------------
//
// Field-for-field mirrors of returns-processing's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay the
// RFC 3339 strings the server publishes; a consuming use case parses them.
// The contract snapshot test in mcpclient checks every json tag below
// against the published output schemas in
// testdata/returns_processing_tools.golden.json.

// ReturnAuthorization mirrors returnView (get_return and each
// list_returns_by_order item). WorkUnit is the shipped work unit
// ({order_id}-line-{line_no}) once the RMA is linked to one; ReasonCode is
// empty on a rejected return, RejectionCode empty on an accepted one;
// WindowEndsAt is empty once the return window has ended or on a rejection;
// Status is REQUESTED, AUTHORIZED, REJECTED, RECEIVED or CLOSED.
type ReturnAuthorization struct {
	RmaID         string `json:"rma_id"`
	OrderID       string `json:"order_id"`
	LineNo        int    `json:"line_no"`
	WorkUnit      string `json:"work_unit,omitempty"`
	Sku           string `json:"sku"`
	Quantity      int64  `json:"quantity"`
	SiteID        string `json:"site_id"`
	ReasonCode    string `json:"reason_code,omitempty"`
	RejectionCode string `json:"rejection_code,omitempty"`
	Status        string `json:"status"`
	WindowEndsAt  string `json:"window_ends_at,omitempty"`
	Version       int64  `json:"version"`
}

// ReturnsByOrder mirrors returnsByOrderView (list_returns_by_order):
// every RMA of one original order, ordered by line number then id.
type ReturnsByOrder struct {
	Items []ReturnAuthorization `json:"items"`
}

// DispositionPolicyRule mirrors policyRuleView: one grade (A, B, C or
// SCRAP) x product-class cell of the disposition rule table and the
// outcome it maps to (RESTOCK, REFURBISH, LIQUIDATE, DISPOSE or RTV).
// ProductClass "*" is the default class of a rule.
type DispositionPolicyRule struct {
	Grade        string `json:"grade"`
	ProductClass string `json:"product_class"`
	Outcome      string `json:"outcome"`
}

// DispositionPolicy mirrors policyView (get_disposition_policy). The
// policy is append-only: a change is a new Version, never a rewrite.
// VetoOutcome is what a guardrail demotes to (default DISPOSE).
type DispositionPolicy struct {
	Version          int64                   `json:"version"`
	ReturnWindowDays int                     `json:"return_window_days"`
	MaxUnitsPerLine  int64                   `json:"max_units_per_line"`
	VetoOutcome      string                  `json:"veto_outcome"`
	Rules            []DispositionPolicyRule `json:"rules"`
	DeclaredAt       string                  `json:"declared_at"`
}
