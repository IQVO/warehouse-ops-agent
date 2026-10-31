// Package ports (this file): the outbound port for the
// pick-face-replenishment bounded context (wms), the fleet's owner of
// forward pick faces, their min/max policies and the replenishment
// requests that refill them from reserve.
//
// pick-face-replenishment's MCP server is READ-ONLY (its own governance
// test pins its five tools, every one annotated readOnlyHint). This
// port mirrors all five: get_policy, list_policies, get_replenishment,
// list_replenishments and list_pick_faces.
// pick-face-replenishment's writes (declare a policy, request a
// replenishment, complete or cancel one) are upstream of it and have no
// method here. The zero-write fitness test
// (internal/architecture/zerowrite) fails the build if the mcpclient
// adapter ever names a write-verb tool, and the contract snapshot test
// in mcpclient pins the published tools against
// pick-face-replenishment's published registry.
package ports

import (
	"context"
)

// PickFaceReplenishmentClient is the outbound port for
// pick-face-replenishment's five published read tools.
type PickFaceReplenishmentClient interface {
	// GetPolicy calls get_policy: the min/max policy of one site and
	// SKU.
	GetPolicy(ctx context.Context, siteID, sku string) (ReplenishmentPolicy, error)
	// ListPolicies calls list_policies: the declared policies by site
	// then SKU, one page at a time. Zero query fields are omitted so
	// pick-face-replenishment applies its own defaults.
	ListPolicies(ctx context.Context, query ReplenishmentPolicyListQuery) (ReplenishmentPolicyPage, error)
	// GetReplenishment calls get_replenishment: one replenishment
	// request by id.
	GetReplenishment(ctx context.Context, requestID string) (ReplenishmentRequest, error)
	// ListReplenishments calls list_replenishments: the requests newest
	// first, one page at a time. Zero query fields are omitted so
	// pick-face-replenishment applies its own defaults.
	ListReplenishments(ctx context.Context, query ReplenishmentListQuery) (ReplenishmentPage, error)
	// ListPickFaces calls list_pick_faces: the forward pick faces of a
	// site by forward bin, one page at a time. Status is an optional
	// BELOW_MIN / OK / NO_SOURCE / NO_POLICY filter.
	ListPickFaces(ctx context.Context, query PickFaceListQuery) (PickFacePage, error)
}

// ReplenishmentPolicyListQuery filters and pages list_policies: an
// optional site and SKU, the previous page's cursor and an optional
// page size (1..500).
type ReplenishmentPolicyListQuery struct {
	SiteID string
	SKU    string
	Cursor string
	Limit  int
}

// ReplenishmentListQuery filters and pages list_replenishments: an
// optional site, SKU and state (Requested, Completed or Cancelled), the
// previous page's cursor and an optional page size.
type ReplenishmentListQuery struct {
	SiteID string
	SKU    string
	State  string
	Cursor string
	Limit  int
}

// PickFaceListQuery filters and pages list_pick_faces: the REQUIRED
// site, an optional status (BELOW_MIN, OK, NO_SOURCE or NO_POLICY), the
// previous page's cursor and an optional page size.
type PickFaceListQuery struct {
	SiteID string
	Status string
	Cursor string
	Limit  int
}

// --- pick-face-replenishment read-model DTOs ---------------------------------
//
// Field-for-field mirrors of pick-face-replenishment's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/pick_face_replenishment_tools.golden.json.

// ReplenishmentPolicy mirrors policyView (get_policy and each
// list_policies item). Source is which pack-derived policy stream
// declared it; stale says a newer pack profile has superseded the one it
// was derived from.
type ReplenishmentPolicy struct {
	SiteID     string `json:"site_id"`
	SKU        string `json:"sku"`
	MinQty     int    `json:"min_qty"`
	MaxQty     int    `json:"max_qty"`
	Multiple   int    `json:"multiple"`
	Source     string `json:"source"`
	Stale      bool   `json:"stale"`
	Version    int    `json:"version"`
	DeclaredAt string `json:"declared_at"`
}

// ReplenishmentRequest mirrors requestView (get_replenishment and each
// list_replenishments item). State is Requested, Completed or
// Cancelled.
type ReplenishmentRequest struct {
	RequestID     string `json:"request_id"`
	SiteID        string `json:"site_id"`
	SKU           string `json:"sku"`
	ForwardBin    string `json:"forward_bin"`
	SourceBin     string `json:"source_bin"`
	Quantity      int    `json:"quantity"`
	Priority      string `json:"priority"`
	State         string `json:"state"`
	PolicyMin     int    `json:"policy_min"`
	PolicyMax     int    `json:"policy_max"`
	RequestedAt   string `json:"requested_at"`
	Version       int    `json:"version"`
	MovedQuantity int    `json:"moved_quantity,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
	CancelledAt   string `json:"cancelled_at,omitempty"`
	CancelReason  string `json:"cancel_reason,omitempty"`
}

// PickFace mirrors pickFaceView (one list_pick_faces item): one forward
// bin with its SKU, quantities and status (BELOW_MIN, OK, NO_SOURCE or
// NO_POLICY). MinQty/MaxQty are null without a declared policy;
// OpenRequestID is set while a replenishment is open against the face.
type PickFace struct {
	SiteID        string `json:"site_id"`
	ForwardBin    string `json:"forward_bin"`
	SKU           string `json:"sku"`
	OnHand        int    `json:"on_hand"`
	Reserved      int    `json:"reserved"`
	Available     int    `json:"available"`
	Status        string `json:"status"`
	MinQty        *int   `json:"min_qty,omitempty"`
	MaxQty        *int   `json:"max_qty,omitempty"`
	OpenRequestID string `json:"open_request_id,omitempty"`
}

// PickFacePage mirrors the list_pick_faces result.
type PickFacePage struct {
	Items      []PickFace `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// ReplenishmentPolicyPage mirrors the list_policies result.
type ReplenishmentPolicyPage struct {
	Items      []ReplenishmentPolicy `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

// ReplenishmentPage mirrors the list_replenishments result.
type ReplenishmentPage struct {
	Items      []ReplenishmentRequest `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}
