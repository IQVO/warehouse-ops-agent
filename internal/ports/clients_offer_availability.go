// Package ports (this file): the outbound port for the offer-availability
// bounded context (retail network), the fleet's owner of the sellable
// catalogue, the per-node listings and the vendor inventory feeds behind
// them.
//
// offer-availability's MCP server is READ-ONLY (its own governance test
// pins its four tools, every one annotated readOnlyHint). This port
// mirrors all four: browse_catalog, get_catalog_item, list_listings and
// get_inventory_feed. offer-availability's writes (declare a listing,
// submit a feed) are upstream of it and have no method here. The
// zero-write fitness test (internal/architecture/zerowrite) fails the
// build if the mcpclient adapter ever names a write-verb tool, and the
// contract snapshot test in mcpclient pins the published tools against
// offer-availability's published registry.
package ports

import (
	"context"
)

// OfferAvailabilityClient is the outbound port for offer-availability's
// four published read tools.
type OfferAvailabilityClient interface {
	// BrowseCatalog calls browse_catalog: the catalogue of sellable
	// items, one keyset page at a time. Zero query fields are omitted so
	// offer-availability applies its own defaults (first page, default
	// page size).
	BrowseCatalog(ctx context.Context, query CatalogPageQuery) (CatalogPage, error)
	// GetCatalogItem calls get_catalog_item: one catalogue item by id.
	GetCatalogItem(ctx context.Context, catalogItemID string) (CatalogItem, error)
	// ListListings calls list_listings: which nodes sell what, one
	// keyset page at a time. Zero query fields are omitted so
	// offer-availability applies its own defaults.
	ListListings(ctx context.Context, query ListingListQuery) (ListingPage, error)
	// GetInventoryFeed calls get_inventory_feed: one vendor inventory
	// feed and its outcome.
	GetInventoryFeed(ctx context.Context, feedID string) (InventoryFeed, error)
}

// CatalogPageQuery pages browse_catalog: the previous page's cursor and
// an optional page size (1..500).
type CatalogPageQuery struct {
	After string
	Limit int
}

// ListingListQuery filters and pages list_listings: an optional node and
// catalogue item, the previous page's cursor and an optional page size.
type ListingListQuery struct {
	NodeID        string
	CatalogItemID string
	After         string
	Limit         int
}

// --- offer-availability read-model DTOs --------------------------------------
//
// Field-for-field mirrors of offer-availability's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/offer_availability_tools.golden.json.

// CatalogItem mirrors catalogItemView (get_catalog_item and each
// browse_catalog item). State is ACTIVE or DISCONTINUED; identifiers and
// packaging are null/empty until declared.
type CatalogItem struct {
	CatalogItemID string                  `json:"catalogItemId"`
	Title         string                  `json:"title"`
	LengthMm      int                     `json:"lengthMm"`
	WidthMm       int                     `json:"widthMm"`
	HeightMm      int                     `json:"heightMm"`
	WeightG       int                     `json:"weightG"`
	Hazmat        bool                    `json:"hazmat"`
	Fragile       bool                    `json:"fragile"`
	State         string                  `json:"state"`
	Identifiers   *CatalogItemIdentifiers `json:"identifiers,omitempty"`
	Packaging     []CatalogPackLevel      `json:"packaging"`
}

// CatalogItemIdentifiers mirrors the item's GS1 identifier.
type CatalogItemIdentifiers struct {
	GTIN string `json:"gtin,omitempty"`
}

// CatalogPackDims mirrors one pack level's dimensions in mm.
type CatalogPackDims struct {
	LengthMm int `json:"lengthMm"`
	WidthMm  int `json:"widthMm"`
	HeightMm int `json:"heightMm"`
}

// CatalogPackLevel mirrors one pack level of an item's packaging
// hierarchy (level, its GTIN, units per pack, and dims/weight once
// known).
type CatalogPackLevel struct {
	Level        string           `json:"level"`
	GTIN         string           `json:"gtin"`
	UnitsPerPack int              `json:"unitsPerPack"`
	Active       bool             `json:"active"`
	ChildGtin    string           `json:"childGtin,omitempty"`
	GrossWeightG int              `json:"grossWeightG,omitempty"`
	DimsMm       *CatalogPackDims `json:"dimsMm,omitempty"`
}

// CatalogPage mirrors the browse_catalog result.
type CatalogPage struct {
	Items      []CatalogItem `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// ListingAvailability mirrors a listing's availability: the on-hand
// quantity on its stated basis, plus the feed and instant it came from.
type ListingAvailability struct {
	AvailableQty int    `json:"availableQty"`
	Basis        string `json:"basis"`
	AsOf         string `json:"asOf,omitempty"`
	FeedID       string `json:"feedId,omitempty"`
}

// Listing mirrors listingView (one list_listings item): which node sells
// which catalogue item, in which state, with the availability behind it.
type Listing struct {
	ListingID         string              `json:"listingId"`
	CatalogItemID     string              `json:"catalogItemId"`
	NodeID            string              `json:"nodeId"`
	State             string              `json:"state"`
	Availability      ListingAvailability `json:"availability"`
	OwnerRef          string              `json:"ownerRef,omitempty"`
	SuppressionReason string              `json:"suppressionReason,omitempty"`
}

// ListingPage mirrors the list_listings result.
type ListingPage struct {
	Listings   []Listing `json:"listings"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// InventoryFeed mirrors feedView (get_inventory_feed): one vendor
// inventory feed and its outcome. State is APPLIED, SUPERSEDED or
// FAILED; AppliedAt/FailureCode are set once the outcome exists.
type InventoryFeed struct {
	FeedID       string `json:"feedId"`
	NodeID       string `json:"nodeId"`
	FullUpdate   bool   `json:"fullUpdate"`
	AsOf         string `json:"asOf"`
	State        string `json:"state"`
	LineCount    int    `json:"lineCount"`
	AppliedCount int    `json:"appliedCount"`
	StaleCount   int    `json:"staleCount"`
	ZeroedCount  int    `json:"zeroedCount"`
	ReceivedAt   string `json:"receivedAt"`
	AppliedAt    string `json:"appliedAt,omitempty"`
	FailureCode  string `json:"failureCode,omitempty"`
}
