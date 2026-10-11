package policy

import (
	"reflect"
	"strings"
	"testing"
)

func dims(l int64) *ProductDimensionsFact {
	return &ProductDimensionsFact{LengthMm: l, WidthMm: 10, HeightMm: 10, WeightG: 100, VolumeMm3: l * 100}
}

func gapFixtures() []ProductMasterFact {
	return []ProductMasterFact{
		{Sku: "SKU-OK", Description: "fine", Version: 2, Classified: true, Declared: dims(100), Measured: dims(102)},
		{Sku: "SKU-UNC", Description: "no class", Version: 1},
		{Sku: "SKU-DIS", Description: "off", Version: 4, Classified: true, Discrepancy: true,
			Declared: dims(100), Measured: dims(130), MeasuredAt: "2026-10-06T10:00:00Z", DeviceId: "cubiscan-1"},
		{Sku: "SKU-BOTH", Description: "both", Version: 5, Discrepancy: true, Declared: dims(50), Measured: dims(80), MeasuredAt: "t", DeviceId: "d"},
	}
}

func skus(gaps []MasterDataGap) string {
	out := make([]string, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, g.Sku)
	}
	return strings.Join(out, ",")
}

func TestParseMasterDataGapKind(t *testing.T) {
	for raw, want := range map[string]MasterDataGapKind{"unclassified": GapUnclassified, "dimension-discrepancy": GapDimensionDiscrepancy} {
		got, ok := ParseMasterDataGapKind(raw)
		if !ok || got != want {
			t.Errorf("ParseMasterDataGapKind(%q) = %q,%v", raw, got, ok)
		}
	}
	for _, bad := range []string{"", "Unclassified", "discrepancy", "missing"} {
		if got, ok := ParseMasterDataGapKind(bad); ok || got != "" {
			t.Errorf("ParseMasterDataGapKind(%q) must reject, got %q,%v", bad, got, ok)
		}
	}
}

func TestFindMasterDataGaps_AllKinds(t *testing.T) {
	r := FindMasterDataGaps(gapFixtures(), "")

	if r.Scanned != 4 || r.UnclassifiedCount != 2 || r.DimensionDiscrepancyCount != 2 {
		t.Fatalf("counts = scanned %d / unclassified %d / discrepancy %d, want 4/2/2", r.Scanned, r.UnclassifiedCount, r.DimensionDiscrepancyCount)
	}
	if got := skus(r.Gaps); got != "SKU-UNC,SKU-DIS,SKU-BOTH" {
		t.Fatalf("gaps = %s, want the three products with a gap in input order", got)
	}
	assertUnclassifiedOnlyGap(t, r.Gaps[0])
	assertDiscrepancyOnlyGap(t, r.Gaps[1])

	both := r.Gaps[2]
	if !reflect.DeepEqual(both.Kinds, []MasterDataGapKind{GapUnclassified, GapDimensionDiscrepancy}) ||
		!reflect.DeepEqual(both.Rationale, []string{rationaleUnclassified, rationaleDiscrepancy}) {
		t.Errorf("a product with both gaps lists both, in stable order: %+v", both)
	}
}

func assertUnclassifiedOnlyGap(t *testing.T, unc MasterDataGap) {
	t.Helper()
	if !reflect.DeepEqual(unc.Kinds, []MasterDataGapKind{GapUnclassified}) || !reflect.DeepEqual(unc.Rationale, []string{rationaleUnclassified}) {
		t.Errorf("unclassified gap: %+v", unc)
	}
	if unc.Description != "no class" || unc.Version != 1 {
		t.Errorf("identity not carried: %+v", unc)
	}
	if unc.Declared != nil || unc.Measured != nil || unc.MeasuredAt != "" || unc.DeviceId != "" {
		t.Errorf("an unclassified-only gap must not echo dimensions: %+v", unc)
	}
}

func assertDiscrepancyOnlyGap(t *testing.T, dis MasterDataGap) {
	t.Helper()
	if !reflect.DeepEqual(dis.Kinds, []MasterDataGapKind{GapDimensionDiscrepancy}) || !reflect.DeepEqual(dis.Rationale, []string{rationaleDiscrepancy}) {
		t.Errorf("discrepancy gap: %+v", dis)
	}
	if dis.Declared.LengthMm != 100 || dis.Measured.LengthMm != 130 || dis.Version != 4 {
		t.Errorf("a discrepancy gap must echo product-master's two dimension sets unchanged: %+v", dis)
	}
	if dis.MeasuredAt != "2026-10-06T10:00:00Z" || dis.DeviceId != "cubiscan-1" {
		t.Errorf("measurement provenance not echoed: %+v", dis)
	}
}

func TestFindMasterDataGaps_FilterUnclassified(t *testing.T) {
	r := FindMasterDataGaps(gapFixtures(), GapUnclassified)

	if got := skus(r.Gaps); got != "SKU-UNC,SKU-BOTH" {
		t.Fatalf("gaps = %s", got)
	}
	both := r.Gaps[1]
	if !reflect.DeepEqual(both.Kinds, []MasterDataGapKind{GapUnclassified}) || both.Declared != nil || both.Measured != nil {
		t.Errorf("the filter must drop the other kind entirely: %+v", both)
	}
	if r.UnclassifiedCount != 2 || r.DimensionDiscrepancyCount != 2 || r.Scanned != 4 {
		t.Errorf("counts must cover the whole scan regardless of the filter: %+v", r)
	}
}

func TestFindMasterDataGaps_FilterDiscrepancy(t *testing.T) {
	r := FindMasterDataGaps(gapFixtures(), GapDimensionDiscrepancy)

	if got := skus(r.Gaps); got != "SKU-DIS,SKU-BOTH" {
		t.Fatalf("gaps = %s", got)
	}
	both := r.Gaps[1]
	if !reflect.DeepEqual(both.Kinds, []MasterDataGapKind{GapDimensionDiscrepancy}) || both.Declared.LengthMm != 50 || both.Measured.LengthMm != 80 {
		t.Errorf("the filter must keep only the discrepancy with its dimensions: %+v", both)
	}
}

func TestFindMasterDataGaps_EmptyScanIsAnEmptyNonNilReport(t *testing.T) {
	r := FindMasterDataGaps(nil, "")
	if r.Gaps == nil || len(r.Gaps) != 0 || r.Scanned != 0 || r.UnclassifiedCount != 0 || r.DimensionDiscrepancyCount != 0 {
		t.Fatalf("an empty scan must be an empty, non-nil report: %+v", r)
	}
}

func TestFindMasterDataGaps_CleanCatalogueHasNoGaps(t *testing.T) {
	r := FindMasterDataGaps([]ProductMasterFact{{Sku: "A", Classified: true}, {Sku: "B", Classified: true, Declared: dims(1)}}, "")
	if len(r.Gaps) != 0 || r.Scanned != 2 || r.UnclassifiedCount != 0 || r.DimensionDiscrepancyCount != 0 {
		t.Fatalf("classified products without a discrepancy flag are not gaps: %+v", r)
	}
}
