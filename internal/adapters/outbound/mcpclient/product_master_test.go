package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// productMasterGoldenPath is a VERBATIM copy of product-master's own
// published tool registry golden
// (product-master: internal/adapters/inbound/mcp/testdata/tool_registry.golden.json,
// pinned at product-master origin/develop a0a303e). Refresh it with
//
//	git -C ../product-master show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever product-master amends its ADR 0005 tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const productMasterGoldenPath = "testdata/product_master_tools.golden.json"

// wantProductMasterTools is the exact set of tools this client calls.
var wantProductMasterTools = []string{"get_physical_profile", "get_product", "get_product_classification", "list_products"}

type goldenTool struct {
	Name         string         `json:"name"`
	Annotations  map[string]any `json:"annotations"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
}

func loadProductMasterGolden(t *testing.T) map[string]goldenTool {
	t.Helper()
	raw, err := os.ReadFile(productMasterGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var tools []goldenTool
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	out := make(map[string]goldenTool, len(tools))
	for _, tool := range tools {
		out[tool.Name] = tool
	}
	return out
}

// --- the published contract ------------------------------------------------

func TestProductMaster_GoldenPinsExactlyTheFourReadOnlyTools(t *testing.T) {
	golden := loadProductMasterGolden(t)

	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantProductMasterTools, ",") {
		t.Fatalf("product-master publishes %v, this client is built for exactly %v", names, wantProductMasterTools)
	}
	for _, name := range wantProductMasterTools {
		if golden[name].Annotations["readOnlyHint"] != true {
			t.Errorf("%s is not annotated readOnlyHint in product-master's registry: this agent may only call read tools (ADR 0020)", name)
		}
	}
}

// TestProductMaster_PortDTOsMatchPublishedOutputSchemas walks every port DTO
// against the published output schema: each json tag must be a published
// property (no invented field) and each required property must be mirrored
// (no silently dropped field).
func TestProductMaster_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadProductMasterGolden(t)
	cases := map[string]reflect.Type{
		"get_product":                reflect.TypeFor[ports.Product](),
		"list_products":              reflect.TypeFor[ports.ProductPage](),
		"get_product_classification": reflect.TypeFor[ports.ProductClassificationReading](),
		"get_physical_profile":       reflect.TypeFor[ports.PhysicalProfileReading](),
	}
	for tool, typ := range cases {
		for _, problem := range compareStructToSchema(typ, golden[tool].OutputSchema, tool) {
			t.Error(problem)
		}
	}
}

func TestProductMaster_SchemaComparatorDetectsDrift(t *testing.T) {
	type drifted struct {
		Sku      string `json:"sku"`
		Invented string `json:"invented"`
	}
	schema := map[string]any{
		"properties": map[string]any{"sku": map[string]any{}, "version": map[string]any{}},
		"required":   []any{"sku", "version"},
	}
	problems := compareStructToSchema(reflect.TypeFor[drifted](), schema, "x")
	if len(problems) != 2 {
		t.Fatalf("an invented field and a dropped required field must both be reported, got %v", problems)
	}
}

func compareStructToSchema(typ reflect.Type, schema map[string]any, path string) []string {
	props, _ := schema["properties"].(map[string]any)
	var problems []string
	mirrored := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := jsonName(typ.Field(i))
		if name == "" {
			continue
		}
		mirrored[name] = true
		prop, ok := props[name].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s.%s: not a published property", path, name))
			continue
		}
		problems = append(problems, compareNested(typ.Field(i).Type, prop, path+"."+name)...)
	}
	required, _ := schema["required"].([]any)
	for _, r := range required {
		if name, _ := r.(string); !mirrored[name] {
			problems = append(problems, fmt.Sprintf("%s.%s: required by the published schema but not mirrored", path, name))
		}
	}
	return problems
}

func compareNested(typ reflect.Type, prop map[string]any, path string) []string {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Slice {
		items, _ := prop["items"].(map[string]any)
		return compareNested(typ.Elem(), items, path+"[]")
	}
	if typ.Kind() == reflect.Struct {
		return compareStructToSchema(typ, prop, path)
	}
	return nil
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	return strings.Split(tag, ",")[0]
}

// --- a schema-faithful product-master stand-in -------------------------------

// productMasterUpstream is a real Streamable-HTTP MCP server whose four tools
// carry product-master's PUBLISHED input and output schemas from the golden:
// the SDK validates the client's arguments (additionalProperties:false, so a
// misspelled key is rejected) and the canned results against them.
type productMasterUpstream struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []string
	lastArgs map[string]any
}

func (u *productMasterUpstream) record(tool string, args map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, tool)
	u.lastArgs = args
}

func (u *productMasterUpstream) args() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastArgs
}

const (
	pmSku1 = `{"sku":"SKU-1","description":"Dry ice pack","version":3,
	  "classification":{"handling_tags":["Hazmat","TemperatureSensitive"],"temperature_class":"Frozen","dot_hazard_class":9,"classification_source":"native"},
	  "physical_profile":{"declared":{"length_mm":100,"width_mm":100,"height_mm":100,"weight_g":500,"volume_mm3":1000000},
	    "measured":{"length_mm":130,"width_mm":100,"height_mm":100,"weight_g":520,"volume_mm3":1300000,"measured_at":"2026-10-06T10:00:00Z","device_id":"cubiscan-1"},
	    "effective":{"length_mm":130,"width_mm":100,"height_mm":100,"weight_g":520,"volume_mm3":1300000},
	    "effective_source":"measured","discrepancy":true}}`
	pmSku2 = `{"sku":"SKU-2","description":"Unknown widget","version":1,
	  "physical_profile":{"effective_source":"none","discrepancy":false}}`
)

func pmFixture(raw string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}

func pmToolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func pmProductBySku(sku string) (map[string]any, *mcp.CallToolResult) {
	switch sku {
	case "SKU-1":
		return pmFixture(pmSku1), nil
	case "SKU-2":
		return pmFixture(pmSku2), nil
	}
	return nil, pmToolError("product-not-found: no product with SKU " + sku)
}

func pmGetProduct(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	p, toolErr := pmProductBySku(fmt.Sprint(in["sku"]))
	if toolErr != nil {
		return toolErr, nil, nil
	}
	return nil, p, nil
}

func pmListProducts(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	if limit, ok := in["limit"].(float64); ok && (limit < 1 || limit > 500) {
		return pmToolError("malformed-request: limit must be between 1 and 500"), nil, nil
	}
	if in["cursor"] == "c2" {
		return nil, map[string]any{"items": []any{pmFixture(pmSku2)}}, nil
	}
	return nil, map[string]any{"items": []any{pmFixture(pmSku1)}, "next_cursor": "c2"}, nil
}

func pmGetClassification(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	p, toolErr := pmProductBySku(fmt.Sprint(in["sku"]))
	if toolErr != nil {
		return toolErr, nil, nil
	}
	c, ok := p["classification"].(map[string]any)
	if !ok {
		return pmToolError("product-classification-not-found: SKU-2 has no classification"), nil, nil
	}
	c["sku"], c["version"] = p["sku"], p["version"]
	return nil, c, nil
}

func pmGetPhysicalProfile(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	p, toolErr := pmProductBySku(fmt.Sprint(in["sku"]))
	if toolErr != nil {
		return toolErr, nil, nil
	}
	pp := p["physical_profile"].(map[string]any)
	pp["sku"], pp["version"] = p["sku"], p["version"]
	return nil, pp, nil
}

func newProductMasterTestUpstream(t *testing.T) *productMasterUpstream {
	t.Helper()
	golden := loadProductMasterGolden(t)
	up := &productMasterUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "product-master-test", Version: "0"}, nil)
	handlers := map[string]mcp.ToolHandlerFor[map[string]any, any]{
		"get_product":                pmGetProduct,
		"list_products":              pmListProducts,
		"get_product_classification": pmGetClassification,
		"get_physical_profile":       pmGetPhysicalProfile,
	}
	for name, h := range handlers {
		g, h := golden[name], h
		mcp.AddTool(server, &mcp.Tool{Name: name, InputSchema: g.InputSchema, OutputSchema: g.OutputSchema},
			func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				up.record(g.Name, in)
				return h(ctx, req, in)
			})
	}
	// A write tool product-master does NOT publish; the client must never
	// reach anything like it.
	mcp.AddTool(server, &mcp.Tool{Name: "classify_product"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		t.Error("write tool classify_product was invoked")
		return nil, map[string]any{}, nil
	})
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

// --- behaviour over the wire --------------------------------------------------

func TestProductMaster_GetProduct_DecodesClassificationAndProfile(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	c := NewProductMaster(Config{Endpoint: up.URL})

	p, err := c.GetProduct(context.Background(), "SKU-1")
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if p.Sku != "SKU-1" || p.Version != 3 || p.Classification == nil {
		t.Fatalf("unexpected product: %+v", p)
	}
	assertSku1Classification(t, p.Classification)
	assertSku1Profile(t, p.PhysicalProfile)
	if up.args()["sku"] != "SKU-1" {
		t.Fatalf("sku argument not sent: %v", up.args())
	}
}

func assertSku1Classification(t *testing.T, c *ports.ProductClassification) {
	t.Helper()
	if got := strings.Join(c.HandlingTags, ","); got != "Hazmat,TemperatureSensitive" || c.TemperatureClass != "Frozen" {
		t.Fatalf("unexpected tags/temperature: %+v", c)
	}
	if c.DotHazardClass == nil || *c.DotHazardClass != 9 || c.ClassificationSource != "native" {
		t.Fatalf("unexpected hazard class/source: %+v", c)
	}
}

func assertSku1Profile(t *testing.T, pp ports.ProductPhysicalProfile) {
	t.Helper()
	if !pp.Discrepancy || pp.EffectiveSource != "measured" || pp.Effective == nil {
		t.Fatalf("unexpected physical profile: %+v", pp)
	}
	if pp.Declared == nil || pp.Declared.LengthMm != 100 || pp.Measured == nil || pp.Measured.LengthMm != 130 || pp.Measured.DeviceId != "cubiscan-1" {
		t.Fatalf("unexpected declared/measured: %+v", pp)
	}
}

func TestProductMaster_GetProduct_UnclassifiedHasNilClassification(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	p, err := NewProductMaster(Config{Endpoint: up.URL}).GetProduct(context.Background(), "SKU-2")
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if p.Classification != nil || p.PhysicalProfile.EffectiveSource != "none" || p.PhysicalProfile.Declared != nil {
		t.Fatalf("an unclassified, unmeasured product must decode as such: %+v", p)
	}
}

func TestProductMaster_GetProduct_NotFoundKeepsSlugAndIsNotInvalidInput(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	_, err := NewProductMaster(Config{Endpoint: up.URL}).GetProduct(context.Background(), "SKU-404")
	var te *ToolError
	if !errors.As(err, &te) || te.Slug != "product-not-found" {
		t.Fatalf("want a product-not-found ToolError, got %v", err)
	}
	if errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatal("not-found is an upstream answer, not an input rejection")
	}
}

func TestProductMaster_ListProducts_OmitsUnsetArgumentsAndFollowsCursor(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	c := NewProductMaster(Config{Endpoint: up.URL})

	first, err := c.ListProducts(context.Background(), ports.ProductListQuery{})
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(up.args()) != 0 {
		t.Fatalf("a zero query must send no argument at all (product-master defaults apply), sent %v", up.args())
	}
	if len(first.Items) != 1 || first.Items[0].Sku != "SKU-1" || first.NextCursor != "c2" {
		t.Fatalf("unexpected first page: %+v", first)
	}

	second, err := c.ListProducts(context.Background(), ports.ProductListQuery{Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("ListProducts (page 2): %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Sku != "SKU-2" || second.NextCursor != "" {
		t.Fatalf("unexpected last page: %+v", second)
	}
}

func TestProductMaster_ListProducts_SendsEveryFilterUnderItsPublishedName(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	unclassified := false
	_, err := NewProductMaster(Config{Endpoint: up.URL}).ListProducts(context.Background(),
		ports.ProductListQuery{Limit: 500, Cursor: "c1", HandlingTag: "Hazmat", Classified: &unclassified})
	if err != nil {
		t.Fatalf("ListProducts: %v (a key outside the published input schema is rejected by the stand-in)", err)
	}
	got := up.args()
	if got["limit"] != float64(500) || got["cursor"] != "c1" || got["handling_tag"] != "Hazmat" || got["classified"] != false {
		t.Fatalf("filters not sent under product-master's names: %v", got)
	}
}

// TestProductMaster_StandInRejectsUnpublishedArgumentKeys proves the stand-in
// can fail: a camelCase key that product-master does not publish is rejected
// by its input schema, so the argument-name assertions above are real.
func TestProductMaster_StandInRejectsUnpublishedArgumentKeys(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	err := New(Config{Name: "product-master", Endpoint: up.URL}).callTool(context.Background(), "list_products", map[string]any{"handlingTag": "Hazmat"}, nil)
	if err == nil {
		t.Fatal("an argument key outside product-master's published input schema must be rejected")
	}
}

func TestProductMaster_ListProducts_MalformedRequestIsInvalidInput(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	_, err := NewProductMaster(Config{Endpoint: up.URL}).ListProducts(context.Background(), ports.ProductListQuery{Limit: 501})
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) || !strings.Contains(err.Error(), "malformed-request") {
		t.Fatalf("malformed-request must classify as an input rejection, got %v", err)
	}
}

func TestProductMaster_GetProductClassification(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	c := NewProductMaster(Config{Endpoint: up.URL})

	got, err := c.GetProductClassification(context.Background(), "SKU-1")
	if err != nil {
		t.Fatalf("GetProductClassification: %v", err)
	}
	if got.Sku != "SKU-1" || got.Version != 3 || len(got.HandlingTags) != 2 || got.TemperatureClass != "Frozen" || got.DotHazardClass == nil {
		t.Fatalf("unexpected classification: %+v", got)
	}

	_, err = c.GetProductClassification(context.Background(), "SKU-2")
	var te *ToolError
	if !errors.As(err, &te) || te.Slug != "product-classification-not-found" {
		t.Fatalf("an unclassified product must surface product-classification-not-found, got %v", err)
	}
}

func TestProductMaster_GetPhysicalProfile(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	got, err := NewProductMaster(Config{Endpoint: up.URL}).GetPhysicalProfile(context.Background(), "SKU-1")
	if err != nil {
		t.Fatalf("GetPhysicalProfile: %v", err)
	}
	if got.Sku != "SKU-1" || got.Version != 3 || !got.Discrepancy || got.EffectiveSource != "measured" ||
		got.Declared == nil || got.Measured == nil || got.Measured.MeasuredAt != "2026-10-06T10:00:00Z" {
		t.Fatalf("unexpected physical profile: %+v", got)
	}
}

func TestProductMaster_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	c := NewProductMaster(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetPhysicalProfile(ctx, "SKU-1")
	_, _ = c.GetProduct(ctx, "SKU-1")
	_, _ = c.GetProductClassification(ctx, "SKU-1")
	_, _ = c.ListProducts(ctx, ports.ProductListQuery{})

	up.mu.Lock()
	defer up.mu.Unlock()
	if strings.Join(up.calls, ",") != strings.Join(wantProductMasterTools, ",") {
		t.Fatalf("tools called = %v, want exactly %v", up.calls, wantProductMasterTools)
	}
}

func TestProductMaster_UnreachableUpstream(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.

	c := NewProductMaster(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetProduct(ctx, "SKU-1"); err == nil {
		t.Error("GetProduct: an unreachable upstream must surface as an error")
	}
	if _, err := c.ListProducts(ctx, ports.ProductListQuery{}); err == nil {
		t.Error("ListProducts: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetProductClassification(ctx, "SKU-1"); err == nil {
		t.Error("GetProductClassification: an unreachable upstream must surface as an error")
	}
	if _, err := c.GetPhysicalProfile(ctx, "SKU-1"); err == nil {
		t.Error("GetPhysicalProfile: an unreachable upstream must surface as an error")
	}
}

func TestProductMaster_RespectsCallerContext(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewProductMaster(Config{Endpoint: up.URL}).GetProduct(ctx, "SKU-1")
	if err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
}
