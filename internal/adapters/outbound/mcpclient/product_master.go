package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// ProductMaster implements ports.ProductMasterClient by calling
// product-master's published READ tool list_products (ADR 0020). The other
// three published read tools (get_product, get_product_classification,
// get_physical_profile) are deliberately not wired: no use case calls them.
//
// product-master's MCP server is read-only by its own ADR 0005; this client
// is additionally held read-only from this side: the zero-write fitness test
// (internal/architecture/zerowrite) scans this package's tool-name literals,
// and TestProductMasterClientCallsOnlyPinnedTools allows exactly this one
// name. The argument keys and result shapes are pinned against
// product-master's published registry in
// testdata/product_master_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// ADR 0018); the consuming use case decides what to do with it.
type ProductMaster struct {
	session *Session
}

// NewProductMaster builds a ProductMaster client for the given connection
// config.
func NewProductMaster(cfg Config) *ProductMaster {
	cfg.Name = "product-master"
	return &ProductMaster{session: New(cfg)}
}

var _ ports.ProductMasterClient = (*ProductMaster)(nil)

// ListProducts calls list_products. Zero/nil query fields are omitted so
// product-master applies its own defaults.
func (c *ProductMaster) ListProducts(ctx context.Context, query ports.ProductListQuery) (ports.ProductPage, error) {
	args := map[string]any{}
	if query.Limit != 0 {
		args["limit"] = query.Limit
	}
	if query.Cursor != "" {
		args["cursor"] = query.Cursor
	}
	if query.HandlingTag != "" {
		args["handling_tag"] = query.HandlingTag
	}
	if query.Classified != nil {
		args["classified"] = *query.Classified
	}
	var out ports.ProductPage
	err := c.session.callTool(ctx, "list_products", args, &out)
	return out, err
}
