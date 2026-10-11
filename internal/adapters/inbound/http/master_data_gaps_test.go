package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeProductMaster struct {
	page ports.ProductPage
	err  error
}

func (f *fakeProductMaster) ListProducts(context.Context, ports.ProductListQuery) (ports.ProductPage, error) {
	return f.page, f.err
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

func gapCatalogue() *fakeProductMaster {
	return &fakeProductMaster{page: ports.ProductPage{Items: []ports.Product{
		{Sku: "SKU-1", Description: "ok", Version: 1, Classification: &ports.ProductClassification{HandlingTags: []string{}, ClassificationSource: "native"}},
		{Sku: "SKU-2", Description: "bare", Version: 1},
		{Sku: "SKU-3", Description: "off", Version: 4, Classification: &ports.ProductClassification{HandlingTags: []string{"Fragile"}, ClassificationSource: "native"},
			PhysicalProfile: ports.ProductPhysicalProfile{
				Declared:        &ports.ProductDimensions{LengthMm: 100, WidthMm: 10, HeightMm: 10, WeightG: 50, VolumeMm3: 10000},
				Measured:        &ports.ProductMeasurement{LengthMm: 150, WidthMm: 10, HeightMm: 10, WeightG: 55, VolumeMm3: 15000, MeasuredAt: "2026-10-06T10:00:00Z", DeviceId: "cubiscan-1"},
				EffectiveSource: "measured", Discrepancy: true,
			}},
	}}}
}

func getGaps(t *testing.T, uc *usecases.MasterDataGaps, query string) (int, map[string]any) {
	t.Helper()
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{MasterDataGaps: uc}, "warehouse-ops-agent-test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/master-data-gaps"+query, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func TestGetMasterDataGaps_ReportsBothKindsWithEvidence(t *testing.T) {
	code, body := getGaps(t, &usecases.MasterDataGaps{ProductMaster: gapCatalogue()}, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %v", code, body)
	}
	if body["scanned"] != float64(3) || body["complete"] != true || body["unclassifiedCount"] != float64(1) || body["dimensionDiscrepancyCount"] != float64(1) {
		t.Fatalf("unexpected summary: %v", body)
	}
	if _, has := body["nextCursor"]; has {
		t.Errorf("a complete scan carries no nextCursor: %v", body)
	}
	gaps := body["gaps"].([]any)
	if len(gaps) != 2 {
		t.Fatalf("gaps = %v", gaps)
	}
	assertUnclassifiedGapJSON(t, gaps[0].(map[string]any))
	assertDiscrepancyGapJSON(t, gaps[1].(map[string]any))
}

func assertUnclassifiedGapJSON(t *testing.T, unc map[string]any) {
	t.Helper()
	if unc["sku"] != "SKU-2" || fmt.Sprint(unc["kinds"]) != "[unclassified]" || len(unc["rationale"].([]any)) != 1 {
		t.Errorf("unclassified gap: %v", unc)
	}
	if _, has := unc["declared"]; has {
		t.Errorf("an unclassified gap carries no dimensions: %v", unc)
	}
}

func assertDiscrepancyGapJSON(t *testing.T, dis map[string]any) {
	t.Helper()
	if dis["sku"] != "SKU-3" || fmt.Sprint(dis["kinds"]) != "[dimension-discrepancy]" || dis["deviceId"] != "cubiscan-1" || dis["measuredAt"] != "2026-10-06T10:00:00Z" {
		t.Errorf("discrepancy gap: %v", dis)
	}
	if dis["declared"].(map[string]any)["lengthMm"] != float64(100) || dis["measured"].(map[string]any)["lengthMm"] != float64(150) {
		t.Errorf("declared/measured not serialised: %v", dis)
	}
}

func TestGetMasterDataGaps_EmptyCatalogueIsAnEmptyArray(t *testing.T) {
	code, body := getGaps(t, &usecases.MasterDataGaps{ProductMaster: &fakeProductMaster{}}, "")
	if code != http.StatusOK || body["gaps"] == nil || len(body["gaps"].([]any)) != 0 {
		t.Fatalf("an empty catalogue is 200 with gaps: [] (never null), got %d %v", code, body)
	}
}

func TestGetMasterDataGaps_StatusMapping(t *testing.T) {
	cases := []struct {
		name  string
		uc    *usecases.MasterDataGaps
		query string
		want  int
	}{
		{"not wired", nil, "", http.StatusServiceUnavailable},
		{"wired without a port", &usecases.MasterDataGaps{}, "", http.StatusServiceUnavailable},
		{"unknown kind", &usecases.MasterDataGaps{ProductMaster: gapCatalogue()}, "?kind=bogus", http.StatusBadRequest},
		{"upstream rejects the cursor", &usecases.MasterDataGaps{ProductMaster: &fakeProductMaster{err: fmt.Errorf("malformed-request: %w", ports.ErrUpstreamInvalidInput)}}, "?cursor=x", http.StatusBadRequest},
		{"upstream down", &usecases.MasterDataGaps{ProductMaster: &fakeProductMaster{err: errors.New("product-master: connect: refused")}}, "", http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getGaps(t, tc.uc, tc.query)
			if msg, _ := body["error"].(string); code != tc.want || strings.TrimSpace(msg) == "" {
				t.Fatalf("status = %d (%v), want %d with an error body", code, body, tc.want)
			}
		})
	}
}

func TestGetMasterDataGaps_KindFilterIsPassedThrough(t *testing.T) {
	code, body := getGaps(t, &usecases.MasterDataGaps{ProductMaster: gapCatalogue()}, "?kind=dimension-discrepancy")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %v", code, body)
	}
	gaps := body["gaps"].([]any)
	if len(gaps) != 1 || gaps[0].(map[string]any)["sku"] != "SKU-3" {
		t.Fatalf("only the discrepancy must be listed: %v", gaps)
	}
}
