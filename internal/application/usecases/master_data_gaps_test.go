package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakeProductMaster serves list_products pages keyed by cursor ("" = first
// page) and records every query it received.
type fakeProductMaster struct {
	pages   map[string]ports.ProductPage
	err     error
	errAt   string
	queries []ports.ProductListQuery
}

func (f *fakeProductMaster) ListProducts(_ context.Context, q ports.ProductListQuery) (ports.ProductPage, error) {
	f.queries = append(f.queries, q)
	if f.err != nil && q.Cursor == f.errAt {
		return ports.ProductPage{}, f.err
	}
	return f.pages[q.Cursor], nil
}

func (f *fakeProductMaster) GetProduct(context.Context, string) (ports.Product, error) {
	return ports.Product{}, errors.New("not used")
}

func (f *fakeProductMaster) GetProductClassification(context.Context, string) (ports.ProductClassificationReading, error) {
	return ports.ProductClassificationReading{}, errors.New("not used")
}

func (f *fakeProductMaster) GetPhysicalProfile(context.Context, string) (ports.PhysicalProfileReading, error) {
	return ports.PhysicalProfileReading{}, errors.New("not used")
}

func pmClassified() *ports.ProductClassification {
	return &ports.ProductClassification{HandlingTags: []string{"Fragile"}, ClassificationSource: "native"}
}

func twoPageCatalogue() *fakeProductMaster {
	return &fakeProductMaster{pages: map[string]ports.ProductPage{
		"": {Items: []ports.Product{
			{Sku: "SKU-1", Description: "ok", Version: 2, Classification: pmClassified()},
			{Sku: "SKU-2", Description: "unclassified", Version: 1},
		}, NextCursor: "p2"},
		"p2": {Items: []ports.Product{{
			Sku: "SKU-3", Description: "off", Version: 7, Classification: pmClassified(),
			PhysicalProfile: ports.ProductPhysicalProfile{
				Declared:        &ports.ProductDimensions{LengthMm: 100, WidthMm: 20, HeightMm: 30, WeightG: 400, VolumeMm3: 60000},
				Measured:        &ports.ProductMeasurement{LengthMm: 140, WidthMm: 21, HeightMm: 31, WeightG: 410, VolumeMm3: 91140, MeasuredAt: "2026-10-06T10:00:00Z", DeviceId: "cubiscan-1"},
				EffectiveSource: "measured", Discrepancy: true,
			},
		}}},
	}}
}

func TestMasterDataGaps_ScansEveryPageAndReportsBothKinds(t *testing.T) {
	pm := twoPageCatalogue()
	res, err := (&MasterDataGaps{ProductMaster: pm}).Execute(context.Background(), MasterDataGapsRequest{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Complete || res.NextCursor != "" {
		t.Fatalf("a scan that reached the last page is complete: %+v", res)
	}
	r := res.Report
	if r.Scanned != 3 || r.UnclassifiedCount != 1 || r.DimensionDiscrepancyCount != 1 || len(r.Gaps) != 2 {
		t.Fatalf("unexpected report: %+v", r)
	}
	if r.Gaps[0].Sku != "SKU-2" || r.Gaps[0].Kinds[0] != policy.GapUnclassified {
		t.Errorf("first gap: %+v", r.Gaps[0])
	}
	assertDiscrepancyGapMapped(t, r.Gaps[1])
	assertFullScanQueries(t, pm.queries)
}

func assertFullScanQueries(t *testing.T, queries []ports.ProductListQuery) {
	t.Helper()
	if len(queries) != 2 || queries[0].Limit != 500 || queries[0].Cursor != "" || queries[1].Cursor != "p2" {
		t.Errorf("expected two pages of 500 following next_cursor, got %+v", queries)
		return
	}
	if queries[0].Classified != nil || queries[0].HandlingTag != "" {
		t.Errorf("an unfiltered scan must not filter upstream: %+v", queries[0])
	}
}

func assertDiscrepancyGapMapped(t *testing.T, g policy.MasterDataGap) {
	t.Helper()
	if g.Sku != "SKU-3" || g.Version != 7 || g.Kinds[0] != policy.GapDimensionDiscrepancy {
		t.Fatalf("discrepancy gap: %+v", g)
	}
	want := policy.ProductDimensionsFact{LengthMm: 100, WidthMm: 20, HeightMm: 30, WeightG: 400, VolumeMm3: 60000}
	if g.Declared == nil || *g.Declared != want {
		t.Errorf("declared not mapped field-for-field: %+v", g.Declared)
	}
	wantM := policy.ProductDimensionsFact{LengthMm: 140, WidthMm: 21, HeightMm: 31, WeightG: 410, VolumeMm3: 91140}
	if g.Measured == nil || *g.Measured != wantM || g.MeasuredAt != "2026-10-06T10:00:00Z" || g.DeviceId != "cubiscan-1" {
		t.Errorf("measured not mapped field-for-field: %+v at %q by %q", g.Measured, g.MeasuredAt, g.DeviceId)
	}
}

func TestMasterDataGaps_UnclassifiedFilterAsksProductMasterForUnclassifiedOnly(t *testing.T) {
	pm := &fakeProductMaster{pages: map[string]ports.ProductPage{"": {Items: []ports.Product{{Sku: "SKU-2"}}}}}
	res, err := (&MasterDataGaps{ProductMaster: pm}).Execute(context.Background(), MasterDataGapsRequest{Kind: "unclassified"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pm.queries) != 1 || pm.queries[0].Classified == nil || *pm.queries[0].Classified {
		t.Fatalf("the unclassified filter must be pushed down as classified=false: %+v", pm.queries)
	}
	if len(res.Report.Gaps) != 1 || res.Report.Gaps[0].Sku != "SKU-2" {
		t.Fatalf("unexpected gaps: %+v", res.Report.Gaps)
	}
}

func TestMasterDataGaps_DiscrepancyFilterScansEverythingAndKeepsOnlyDiscrepancies(t *testing.T) {
	pm := twoPageCatalogue()
	res, err := (&MasterDataGaps{ProductMaster: pm}).Execute(context.Background(), MasterDataGapsRequest{Kind: "dimension-discrepancy"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if pm.queries[0].Classified != nil {
		t.Fatalf("a discrepancy scan must not filter by classification: %+v", pm.queries[0])
	}
	if len(res.Report.Gaps) != 1 || res.Report.Gaps[0].Sku != "SKU-3" {
		t.Fatalf("unexpected gaps: %+v", res.Report.Gaps)
	}
}

func TestMasterDataGaps_UnknownKindIsRejectedBeforeAnyCall(t *testing.T) {
	pm := twoPageCatalogue()
	_, err := (&MasterDataGaps{ProductMaster: pm}).Execute(context.Background(), MasterDataGapsRequest{Kind: "missing-weight"})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "missing-weight") {
		t.Fatalf("an unknown kind must be an invalid-input error naming it, got %v", err)
	}
	if len(pm.queries) != 0 {
		t.Fatalf("no upstream call for a rejected request, got %+v", pm.queries)
	}
}

func TestMasterDataGaps_ResumesFromCallerCursor(t *testing.T) {
	pm := twoPageCatalogue()
	res, err := (&MasterDataGaps{ProductMaster: pm}).Execute(context.Background(), MasterDataGapsRequest{Cursor: "p2"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pm.queries) != 1 || pm.queries[0].Cursor != "p2" || res.Report.Scanned != 1 || !res.Complete {
		t.Fatalf("a resumed scan starts at the caller's cursor: queries %+v, result %+v", pm.queries, res)
	}
}

func TestMasterDataGaps_StopsAtThePageBoundAndSaysSo(t *testing.T) {
	pages := map[string]ports.ProductPage{}
	for i := 0; i < masterDataMaxPages+2; i++ {
		cursor := ""
		if i > 0 {
			cursor = fmt.Sprintf("c%d", i)
		}
		pages[cursor] = ports.ProductPage{Items: []ports.Product{{Sku: fmt.Sprintf("SKU-%d", i)}}, NextCursor: fmt.Sprintf("c%d", i+1)}
	}
	pm := &fakeProductMaster{pages: pages}

	res, err := (&MasterDataGaps{ProductMaster: pm}).Execute(context.Background(), MasterDataGapsRequest{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pm.queries) != masterDataMaxPages || res.Report.Scanned != masterDataMaxPages {
		t.Fatalf("scan must stop after %d pages, made %d calls / scanned %d", masterDataMaxPages, len(pm.queries), res.Report.Scanned)
	}
	if res.Complete || res.NextCursor != fmt.Sprintf("c%d", masterDataMaxPages) {
		t.Fatalf("a bounded scan must be marked incomplete with the resume cursor: %+v", res)
	}
}

func TestMasterDataGaps_UpstreamFailureMidScanIsAnErrorNotAPartialReport(t *testing.T) {
	pm := twoPageCatalogue()
	pm.err, pm.errAt = errors.New("product-master: connect: connection refused"), "p2"

	_, err := (&MasterDataGaps{ProductMaster: pm}).Execute(context.Background(), MasterDataGapsRequest{})
	if err == nil || errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("a failing page must fail the scan as an upstream error, got %v", err)
	}
}

func TestMasterDataGaps_UpstreamInputRejectionIsInvalidInput(t *testing.T) {
	pm := twoPageCatalogue()
	pm.err, pm.errAt = fmt.Errorf("malformed-request: bad cursor: %w", ports.ErrUpstreamInvalidInput), "bogus"

	_, err := (&MasterDataGaps{ProductMaster: pm}).Execute(context.Background(), MasterDataGapsRequest{Cursor: "bogus"})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("a cursor product-master rejects is the caller's invalid input, got %v", err)
	}
}

func TestMasterDataGaps_NotConfigured(t *testing.T) {
	var nilUC *MasterDataGaps
	for _, uc := range []*MasterDataGaps{nilUC, {}} {
		if _, err := uc.Execute(context.Background(), MasterDataGapsRequest{}); !errors.Is(err, ErrProductMasterNotConfigured) {
			t.Fatalf("an unwired use case must say product-master is not configured, got %v", err)
		}
	}
}
