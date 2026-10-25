// Package ports (this file): the outbound port for the product-master
// bounded context (ADR 0020), the fleet's owner of the product catalogue's
// master data: description, handling classification and the physical
// profile (declared vs measured unit dimensions).
//
// product-master's MCP server publishes eight tools: six reads annotated
// readOnlyHint (list_products, get_product, get_product_classification,
// get_physical_profile, get_pack_levels, resolve_gtin) and two pack-level
// writes (declare_pack_level, retire_pack_level; its ADR 0011). This port
// mirrors only the one the agent consumes (list_products; the other five
// reads are not wired because no use case calls them); every product-master
// write, tool or REST (register, classify, declare dimensions, record
// measurement, declare/retire a pack level), has no method here. The zero-write fitness test
// (internal/architecture/zerowrite) fails the build if the mcpclient adapter
// ever names a write-verb tool, and the contract snapshot test in mcpclient
// pins the published tools against product-master's published registry.
package ports

import "context"

// ProductMasterClient is the outbound port for product-master's published
// read tool list_products.
type ProductMasterClient interface {
	// ListProducts calls list_products: one page in ascending SKU order.
	ListProducts(ctx context.Context, query ProductListQuery) (ProductPage, error)
}

// ProductListQuery is the argument set of list_products. Every field is
// optional and is OMITTED from the call when zero/nil, so product-master
// applies its own defaults (limit 100, first page, no filter); this client
// never invents one.
type ProductListQuery struct {
	// Limit is the page size, 1..500 (product-master rejects anything else
	// with malformed-request). 0 omits it.
	Limit int
	// Cursor is the opaque next_cursor of the previous page.
	Cursor string
	// HandlingTag keeps only products whose classification carries it
	// (Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue).
	HandlingTag string
	// Classified keeps only classified (true) or unclassified (false)
	// products; nil keeps both.
	Classified *bool
}

// --- product-master read-model DTOs ----------------------------------------
//
// Field-for-field mirrors of product-master's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). The contract
// snapshot test in mcpclient checks every json tag below against the
// published output schemas in testdata/product_master_tools.golden.json.

// ProductDimensions mirrors dimensionsView: one unit's dimensions in
// millimetres, weight in grams and the derived volume in cubic millimetres.
type ProductDimensions struct {
	LengthMm  int64 `json:"length_mm"`
	WidthMm   int64 `json:"width_mm"`
	HeightMm  int64 `json:"height_mm"`
	WeightG   int64 `json:"weight_g"`
	VolumeMm3 int64 `json:"volume_mm3"`
}

// ProductMeasurement mirrors measurementView: the latest measured
// dimensions plus when and (optionally) by which device.
type ProductMeasurement struct {
	LengthMm   int64  `json:"length_mm"`
	WidthMm    int64  `json:"width_mm"`
	HeightMm   int64  `json:"height_mm"`
	WeightG    int64  `json:"weight_g"`
	VolumeMm3  int64  `json:"volume_mm3"`
	MeasuredAt string `json:"measured_at"`
	DeviceId   string `json:"device_id,omitempty"`
}

// ProductPhysicalProfile mirrors physicalProfileView. EffectiveSource is
// "measured", "declared" or "none"; Discrepancy is product-master's own
// judgement that declared and measured differ by more than its tolerance
// (10% in its ADR 0002) -- this agent never recomputes it.
type ProductPhysicalProfile struct {
	Declared        *ProductDimensions  `json:"declared,omitempty"`
	Measured        *ProductMeasurement `json:"measured,omitempty"`
	Effective       *ProductDimensions  `json:"effective,omitempty"`
	EffectiveSource string              `json:"effective_source"`
	Discrepancy     bool                `json:"discrepancy"`
}

// ProductClassification mirrors classificationView. HandlingTags is in
// product-master's stable order; DotHazardClass is nil when not recorded.
type ProductClassification struct {
	HandlingTags         []string `json:"handling_tags"`
	TemperatureClass     string   `json:"temperature_class,omitempty"`
	DotHazardClass       *int     `json:"dot_hazard_class,omitempty"`
	ClassificationSource string   `json:"classification_source"`
}

// Product mirrors productView (each list_products item).
// Classification is nil when the product is unclassified.
type Product struct {
	Sku             string                 `json:"sku"`
	Description     string                 `json:"description"`
	Version         int64                  `json:"version"`
	Classification  *ProductClassification `json:"classification,omitempty"`
	PhysicalProfile ProductPhysicalProfile `json:"physical_profile"`
}

// ProductPage mirrors productPageView. NextCursor is empty on the last page.
type ProductPage struct {
	Items      []Product `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
