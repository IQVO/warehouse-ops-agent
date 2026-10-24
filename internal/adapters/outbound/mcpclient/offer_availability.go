package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// OfferAvailability implements ports.OfferAvailabilityClient by calling
// offer-availability's four published READ tools browse_catalog,
// get_catalog_item, list_listings and get_inventory_feed.
//
// offer-availability's MCP server is read-only by its own governance
// test; this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestOfferAvailabilityClientCallsOnlyPinnedTools allows exactly these
// four names. The argument keys and result shapes are pinned against
// offer-availability's published registry in
// testdata/offer_availability_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type OfferAvailability struct {
	session *Session
}

// NewOfferAvailability builds an OfferAvailability client for the given
// connection config.
func NewOfferAvailability(cfg Config) *OfferAvailability {
	cfg.Name = "offer-availability"
	return &OfferAvailability{session: New(cfg)}
}

var _ ports.OfferAvailabilityClient = (*OfferAvailability)(nil)

// BrowseCatalog calls browse_catalog. Zero query fields are omitted so
// offer-availability answers the first page at its default page size.
func (c *OfferAvailability) BrowseCatalog(ctx context.Context, query ports.CatalogPageQuery) (ports.CatalogPage, error) {
	args := map[string]any{}
	if query.Limit != 0 {
		args["limit"] = query.Limit
	}
	setIfNotEmpty(args, "after", query.After)
	var out ports.CatalogPage
	err := c.session.callTool(ctx, "browse_catalog", args, &out)
	return out, err
}

// GetCatalogItem calls get_catalog_item for one catalogue item id.
func (c *OfferAvailability) GetCatalogItem(ctx context.Context, catalogItemID string) (ports.CatalogItem, error) {
	var out ports.CatalogItem
	err := c.session.callTool(ctx, "get_catalog_item", map[string]any{"catalogItemId": catalogItemID}, &out)
	return out, err
}

// ListListings calls list_listings. Zero query fields are omitted so
// offer-availability answers every listing's first page at its default
// page size.
func (c *OfferAvailability) ListListings(ctx context.Context, query ports.ListingListQuery) (ports.ListingPage, error) {
	args := map[string]any{}
	if query.Limit != 0 {
		args["limit"] = query.Limit
	}
	setIfNotEmpty(args, "nodeId", query.NodeID)
	setIfNotEmpty(args, "catalogItemId", query.CatalogItemID)
	setIfNotEmpty(args, "after", query.After)
	var out ports.ListingPage
	err := c.session.callTool(ctx, "list_listings", args, &out)
	return out, err
}

// GetInventoryFeed calls get_inventory_feed for one feed id.
func (c *OfferAvailability) GetInventoryFeed(ctx context.Context, feedID string) (ports.InventoryFeed, error) {
	var out ports.InventoryFeed
	err := c.session.callTool(ctx, "get_inventory_feed", map[string]any{"feedId": feedID}, &out)
	return out, err
}
