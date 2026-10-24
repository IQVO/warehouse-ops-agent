package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// PickFaceReplenishment implements ports.PickFaceReplenishmentClient by
// calling pick-face-replenishment's five published READ tools
// get_policy, list_policies, get_replenishment, list_replenishments and
// list_pick_faces.
//
// pick-face-replenishment's MCP server is read-only by its own
// governance test; this client is additionally held read-only from this
// side: the zero-write fitness test (internal/architecture/zerowrite)
// scans this package's tool-name literals, and
// TestPickFaceReplenishmentClientCallsOnlyPinnedTools allows exactly
// these five names. The argument keys and result shapes are pinned
// against pick-face-replenishment's published registry in
// testdata/pick_face_replenishment_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type PickFaceReplenishment struct {
	session *Session
}

// NewPickFaceReplenishment builds a PickFaceReplenishment client for the
// given connection config.
func NewPickFaceReplenishment(cfg Config) *PickFaceReplenishment {
	cfg.Name = "pick-face-replenishment"
	return &PickFaceReplenishment{session: New(cfg)}
}

var _ ports.PickFaceReplenishmentClient = (*PickFaceReplenishment)(nil)

// GetPolicy calls get_policy for one site and SKU.
func (c *PickFaceReplenishment) GetPolicy(ctx context.Context, siteID, sku string) (ports.ReplenishmentPolicy, error) {
	var out ports.ReplenishmentPolicy
	err := c.session.callTool(ctx, "get_policy", map[string]any{"site_id": siteID, "sku": sku}, &out)
	return out, err
}

// ListPolicies calls list_policies. Zero query fields are omitted so
// pick-face-replenishment applies its own defaults (every policy, the
// first page at the default of 100).
func (c *PickFaceReplenishment) ListPolicies(ctx context.Context, query ports.ReplenishmentPolicyListQuery) (ports.ReplenishmentPolicyPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "site_id", query.SiteID)
	setIfNotEmpty(args, "sku", query.SKU)
	var out ports.ReplenishmentPolicyPage
	err := c.session.callTool(ctx, "list_policies", args, &out)
	return out, err
}

// GetReplenishment calls get_replenishment for one request id.
func (c *PickFaceReplenishment) GetReplenishment(ctx context.Context, requestID string) (ports.ReplenishmentRequest, error) {
	var out ports.ReplenishmentRequest
	err := c.session.callTool(ctx, "get_replenishment", map[string]any{"request_id": requestID}, &out)
	return out, err
}

// ListReplenishments calls list_replenishments. Zero query fields are
// omitted so pick-face-replenishment applies its own defaults.
func (c *PickFaceReplenishment) ListReplenishments(ctx context.Context, query ports.ReplenishmentListQuery) (ports.ReplenishmentPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "site_id", query.SiteID)
	setIfNotEmpty(args, "sku", query.SKU)
	setIfNotEmpty(args, "state", query.State)
	var out ports.ReplenishmentPage
	err := c.session.callTool(ctx, "list_replenishments", args, &out)
	return out, err
}

// ListPickFaces calls list_pick_faces for one site (required), filtered
// and paged. Zero optional fields are omitted so
// pick-face-replenishment answers every status at its default page
// size.
func (c *PickFaceReplenishment) ListPickFaces(ctx context.Context, query ports.PickFaceListQuery) (ports.PickFacePage, error) {
	args := map[string]any{"site_id": query.SiteID}
	if query.Limit != 0 {
		args["limit"] = query.Limit
	}
	setIfNotEmpty(args, "cursor", query.Cursor)
	setIfNotEmpty(args, "status", query.Status)
	var out ports.PickFacePage
	err := c.session.callTool(ctx, "list_pick_faces", args, &out)
	return out, err
}
