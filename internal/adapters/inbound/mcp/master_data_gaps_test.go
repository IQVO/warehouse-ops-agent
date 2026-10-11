package mcp_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeProductMaster struct{ page ports.ProductPage }

func (f *fakeProductMaster) ListProducts(context.Context, ports.ProductListQuery) (ports.ProductPage, error) {
	return f.page, nil
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

func newServerWithMasterDataGaps(t *testing.T) string {
	t.Helper()
	pm := &fakeProductMaster{page: ports.ProductPage{Items: []ports.Product{
		{Sku: "SKU-2", Description: "bare", Version: 1},
		{Sku: "SKU-3", Description: "off", Version: 4,
			Classification: &ports.ProductClassification{HandlingTags: []string{"Fragile"}, ClassificationSource: "native"},
			PhysicalProfile: ports.ProductPhysicalProfile{
				Declared:        &ports.ProductDimensions{LengthMm: 100, WidthMm: 10, HeightMm: 10, WeightG: 50, VolumeMm3: 10000},
				Measured:        &ports.ProductMeasurement{LengthMm: 150, WidthMm: 10, HeightMm: 10, WeightG: 55, VolumeMm3: 15000, MeasuredAt: "2026-10-06T10:00:00Z"},
				EffectiveSource: "measured", Discrepancy: true,
			}},
	}}}
	server := inboundmcp.NewServer(inboundmcp.Deps{
		DailyBrief:     &usecases.DailyBrief{},
		MasterDataGaps: &usecases.MasterDataGaps{ProductMaster: pm},
	})
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

func advertised(t *testing.T, url, name string) *sdk.Tool {
	t.Helper()
	tools, err := connect(t, url).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == name {
			return tool
		}
	}
	return nil
}

func TestServer_FindMasterDataGaps_OnlyAdvertisedWhenWiredAndReadOnly(t *testing.T) {
	if advertised(t, newServer(t), "find_master_data_gaps") != nil {
		t.Fatal("find_master_data_gaps must not be advertised when product-master is not configured")
	}
	tool := advertised(t, newServerWithMasterDataGaps(t), "find_master_data_gaps")
	if tool == nil {
		t.Fatal("find_master_data_gaps not advertised when MasterDataGaps is wired")
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("find_master_data_gaps must be read-only: %+v", tool.Annotations)
	}
}

func TestServer_FindMasterDataGaps_OverTheWire(t *testing.T) {
	session := connect(t, newServerWithMasterDataGaps(t))
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "find_master_data_gaps", Arguments: map[string]any{"kind": "dimension-discrepancy"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	if out["scanned"] != float64(2) || out["complete"] != true || out["unclassifiedCount"] != float64(1) || out["dimensionDiscrepancyCount"] != float64(1) {
		t.Fatalf("unexpected summary: %v", out)
	}
	gaps := out["gaps"].([]any)
	if len(gaps) != 1 {
		t.Fatalf("the kind filter must keep only the discrepancy: %v", gaps)
	}
	g := gaps[0].(map[string]any)
	if g["sku"] != "SKU-3" || g["declared"].(map[string]any)["lengthMm"] != float64(100) || g["measured"].(map[string]any)["lengthMm"] != float64(150) {
		t.Fatalf("unexpected gap: %v", g)
	}
}

func TestServer_FindMasterDataGaps_UnknownKindIsAToolError(t *testing.T) {
	session := connect(t, newServerWithMasterDataGaps(t))
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "find_master_data_gaps", Arguments: map[string]any{"kind": "everything"},
	})
	if err != nil {
		t.Fatalf("call tool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("an unknown kind must be rejected as a tool error, never defaulted")
	}
}
