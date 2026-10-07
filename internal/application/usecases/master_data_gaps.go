// Package usecases: MasterDataGaps (ADR 0020) — reads product-master's
// catalogue through its list_products read tool and reports the products
// whose master data is missing or contradictory (unclassified, or a
// declared-vs-measured dimension discrepancy flagged by product-master).
package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

const (
	// masterDataPageSize is the list_products page size: product-master's
	// own maximum, so a scan needs as few calls as possible.
	masterDataPageSize = 500
	// masterDataMaxPages bounds one Execute call to 10 pages (5,000
	// products). It is a per-request work bound, not a business input: when
	// more products remain, the result carries NextCursor and says the scan
	// is incomplete; it never pretends the rest has no gaps.
	masterDataMaxPages = 10
)

// ErrProductMasterNotConfigured is returned when the use case has no
// product-master port (PRODUCT_MASTER_MCP_ENDPOINT unset).
var ErrProductMasterNotConfigured = errors.New("product-master is not configured")

// MasterDataGapsRequest is the caller's (all optional) input. Kind is ""
// (both kinds), "unclassified" or "dimension-discrepancy"; anything else is
// rejected. Cursor resumes a previous incomplete scan from its NextCursor.
type MasterDataGapsRequest struct {
	Kind   string
	Cursor string
}

// MasterDataGapsResult is one scan's report. Complete is false when the
// per-request page bound was reached before product-master's last page;
// NextCursor then resumes the scan.
type MasterDataGapsResult struct {
	Report     policy.MasterDataGapReport
	Complete   bool
	NextCursor string
}

// MasterDataGaps is the ADR 0020 decision-support use case. It is
// read-only (list_products only) and invents nothing: every gap is a fact
// product-master published (no classification, its own discrepancy flag),
// and the only caller inputs are the kind filter and the resume cursor.
//
// It is NOT fail-open: the whole answer comes from product-master, so an
// unreachable product-master or a failing page is an error, never a partial
// report presented as complete.
type MasterDataGaps struct {
	ProductMaster ports.ProductMasterClient
}

// Execute scans product-master's catalogue (from req.Cursor, at most
// masterDataMaxPages pages) and returns the gaps found. When the kind filter
// is "unclassified" the scan asks product-master for unclassified products
// only (classified=false), so the counts then cover that subset.
func (uc *MasterDataGaps) Execute(ctx context.Context, req MasterDataGapsRequest) (MasterDataGapsResult, error) {
	if uc == nil || uc.ProductMaster == nil {
		return MasterDataGapsResult{}, ErrProductMasterNotConfigured
	}
	var only policy.MasterDataGapKind
	if req.Kind != "" {
		kind, ok := policy.ParseMasterDataGapKind(req.Kind)
		if !ok {
			return MasterDataGapsResult{}, fmt.Errorf("%w: unknown master-data gap kind %q (want unclassified or dimension-discrepancy)", ErrInvalidInput, req.Kind)
		}
		only = kind
	}

	facts, next, err := uc.scan(ctx, req.Cursor, only == policy.GapUnclassified)
	if err != nil {
		return MasterDataGapsResult{}, err
	}
	return MasterDataGapsResult{
		Report:     policy.FindMasterDataGaps(facts, only),
		Complete:   next == "",
		NextCursor: next,
	}, nil
}

// scan pages through list_products from cursor and returns every product
// read plus the cursor to resume from ("" when product-master's last page
// was reached).
func (uc *MasterDataGaps) scan(ctx context.Context, cursor string, unclassifiedOnly bool) ([]policy.ProductMasterFact, string, error) {
	query := ports.ProductListQuery{Limit: masterDataPageSize, Cursor: cursor}
	if unclassifiedOnly {
		classified := false
		query.Classified = &classified
	}
	var facts []policy.ProductMasterFact
	for page := 0; page < masterDataMaxPages; page++ {
		result, err := uc.ProductMaster.ListProducts(ctx, query)
		if err != nil {
			if errors.Is(err, ports.ErrUpstreamInvalidInput) {
				return nil, "", fmt.Errorf("%w: product-master rejected the scan (cursor %q): %v", ErrInvalidInput, query.Cursor, err)
			}
			return nil, "", fmt.Errorf("product-master list_products: %w", err)
		}
		for _, p := range result.Items {
			facts = append(facts, toProductMasterFact(p))
		}
		if result.NextCursor == "" {
			return facts, "", nil
		}
		query.Cursor = result.NextCursor
	}
	return facts, query.Cursor, nil
}

func toProductMasterFact(p ports.Product) policy.ProductMasterFact {
	fact := policy.ProductMasterFact{
		Sku:         p.Sku,
		Description: p.Description,
		Version:     p.Version,
		Classified:  p.Classification != nil,
		Discrepancy: p.PhysicalProfile.Discrepancy,
	}
	if d := p.PhysicalProfile.Declared; d != nil {
		fact.Declared = &policy.ProductDimensionsFact{LengthMm: d.LengthMm, WidthMm: d.WidthMm, HeightMm: d.HeightMm, WeightG: d.WeightG, VolumeMm3: d.VolumeMm3}
	}
	if m := p.PhysicalProfile.Measured; m != nil {
		fact.Measured = &policy.ProductDimensionsFact{LengthMm: m.LengthMm, WidthMm: m.WidthMm, HeightMm: m.HeightMm, WeightG: m.WeightG, VolumeMm3: m.VolumeMm3}
		fact.MeasuredAt, fact.DeviceId = m.MeasuredAt, m.DeviceId
	}
	return fact
}
