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
// pinned at product-master origin/develop cf18289). Refresh it with
//
//	git -C ../product-master show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json
//
// whenever product-master amends its ADR 0005 tool surface; the tests below
// then say exactly which consumer-side assumption broke.
const productMasterGoldenPath = "testdata/product_master_tools.golden.json"

// wantProductMasterTools is every tool product-master publishes (eight, the
// verbatim golden: six reads plus the two pack-level writes the mastercase
// programme added); the client itself deliberately calls only the one in
// wantProductMasterCalledTools below.
var wantProductMasterTools = []string{
	"declare_pack_level", "get_pack_levels", "get_physical_profile", "get_product",
	"get_product_classification", "list_products", "resolve_gtin", "retire_pack_level",
}

// wantProductMasterWriteTools is the subset of the registry annotated
// readOnlyHint=false (product-master ADR 0011 pack-level writes). They are
// pinned so their schemas are tracked, never so they can be called: the
// client must not name them (zerowrite.TestMCPClientsCallOnlyReadTools).
var wantProductMasterWriteTools = []string{"declare_pack_level", "retire_pack_level"}

// wantProductMasterToolCount is the number of tools in the pinned registry.
// It is deliberately a literal, not len(wantProductMasterTools), so growing
// either side alone fails TestProductMaster_PinnedToolCountMatchesRegistry.
const wantProductMasterToolCount = 8

// wantProductMasterCalledTools is the exact set of tools this client calls
// (the one the master-data-gaps use case consumes, ADR 0020).
var wantProductMasterCalledTools = []string{"list_products"}

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

func TestProductMaster_GoldenPinsExactlyTheEightPublishedTools(t *testing.T) {
	golden := loadProductMasterGolden(t)

	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantProductMasterTools, ",") {
		t.Fatalf("product-master publishes %v, this golden is built for exactly %v", names, wantProductMasterTools)
	}
	isWrite := map[string]bool{}
	for _, name := range wantProductMasterWriteTools {
		isWrite[name] = true
	}
	for _, name := range wantProductMasterTools {
		// Strict both ways: a read tool must stay readOnlyHint=true and a
		// known write tool must stay readOnlyHint=false, so a flipped
		// annotation (a write silently becoming "read", or the reverse)
		// is drift this agent must look at.
		if got, want := golden[name].Annotations["readOnlyHint"], !isWrite[name]; got != want {
			t.Errorf("%s: readOnlyHint = %v in product-master's registry, pinned as %v", name, got, want)
		}
	}
	for _, name := range wantProductMasterCalledTools {
		if isWrite[name] {
			t.Errorf("the client calls %s, a product-master write tool: this agent may only call read tools (ADR 0020)", name)
		}
	}
}

// TestProductMaster_PinnedToolCountMatchesRegistry fails when the number of
// tools in the pinned registry copy, the expected-name list and the
// expected count disagree. It counts the raw JSON array (not the name-keyed
// map) so a duplicated entry cannot hide, and fails with the refresh command
// so drift is a one-step fix.
func TestProductMaster_PinnedToolCountMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile(productMasterGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if len(entries) != wantProductMasterToolCount || len(wantProductMasterTools) != wantProductMasterToolCount {
		t.Fatalf("pinned registry has %d tools, expected-name list has %d, expected count is %d: "+
			"refresh the golden with `git -C ../product-master show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json` "+
			"and update wantProductMasterTools",
			len(entries), len(wantProductMasterTools), wantProductMasterToolCount)
	}
}

// TestProductMaster_PortDTOsMatchPublishedOutputSchemas walks every port DTO
// against the published output schema: each json tag must be a published
// property (no invented field) and each required property must be mirrored
// (no silently dropped field).
func TestProductMaster_PortDTOsMatchPublishedOutputSchemas(t *testing.T) {
	golden := loadProductMasterGolden(t)
	cases := map[string]reflect.Type{
		"list_products": reflect.TypeFor[ports.ProductPage](),
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
// (the client calls only list_products)
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

func pmListProducts(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	if limit, ok := in["limit"].(float64); ok && (limit < 1 || limit > 500) {
		return pmToolError("malformed-request: limit must be between 1 and 500"), nil, nil
	}
	if in["cursor"] == "c2" {
		return nil, map[string]any{"items": []any{pmFixture(pmSku2)}}, nil
	}
	return nil, map[string]any{"items": []any{pmFixture(pmSku1)}, "next_cursor": "c2"}, nil
}

func newProductMasterTestUpstream(t *testing.T) *productMasterUpstream {
	t.Helper()
	golden := loadProductMasterGolden(t)
	up := &productMasterUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "product-master-test", Version: "0"}, nil)
	handlers := map[string]mcp.ToolHandlerFor[map[string]any, any]{
		"list_products": pmListProducts,
	}
	for name, h := range handlers {
		g, h := golden[name], h
		mcp.AddTool(server, &mcp.Tool{Name: name, InputSchema: g.InputSchema, OutputSchema: g.OutputSchema},
			func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				up.record(g.Name, in)
				return h(ctx, req, in)
			})
	}
	// The write tools product-master really publishes (carrying their
	// golden schemas) plus one it does not: the client must never reach
	// any of them.
	trap := func(name string) mcp.ToolHandlerFor[map[string]any, any] {
		return func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			t.Errorf("write tool %s was invoked", name)
			return nil, map[string]any{}, nil
		}
	}
	mcp.AddTool(server, &mcp.Tool{Name: "classify_product"}, trap("classify_product"))
	for _, name := range wantProductMasterWriteTools {
		g := golden[name]
		mcp.AddTool(server, &mcp.Tool{Name: name, InputSchema: g.InputSchema, OutputSchema: g.OutputSchema}, trap(name))
	}
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

// --- behaviour over the wire --------------------------------------------------

func TestProductMaster_ListProducts_DecodesClassificationAndProfile(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	page, err := NewProductMaster(Config{Endpoint: up.URL}).ListProducts(context.Background(), ports.ProductListQuery{})
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	p := page.Items[0]
	if p.Sku != "SKU-1" || p.Version != 3 || p.Classification == nil {
		t.Fatalf("unexpected product: %+v", p)
	}
	assertSku1Classification(t, p.Classification)
	assertSku1Profile(t, p.PhysicalProfile)

	// An unclassified, unmeasured product decodes as such.
	second, err := NewProductMaster(Config{Endpoint: up.URL}).ListProducts(context.Background(), ports.ProductListQuery{Cursor: "c2"})
	if err != nil {
		t.Fatalf("ListProducts (page 2): %v", err)
	}
	q := second.Items[0]
	if q.Classification != nil || q.PhysicalProfile.EffectiveSource != "none" || q.PhysicalProfile.Declared != nil {
		t.Fatalf("an unclassified, unmeasured product must decode as such: %+v", q)
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

func TestProductMaster_OnlyThePinnedReadToolsAreCalled(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	c := NewProductMaster(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.ListProducts(ctx, ports.ProductListQuery{})

	up.mu.Lock()
	defer up.mu.Unlock()
	if strings.Join(up.calls, ",") != strings.Join(wantProductMasterCalledTools, ",") {
		t.Fatalf("tools called = %v, want exactly %v", up.calls, wantProductMasterCalledTools)
	}
}

func TestProductMaster_UnreachableUpstream(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.

	c := NewProductMaster(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.ListProducts(ctx, ports.ProductListQuery{}); err == nil {
		t.Error("ListProducts: an unreachable upstream must surface as an error")
	}
}

func TestProductMaster_RespectsCallerContext(t *testing.T) {
	up := newProductMasterTestUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewProductMaster(Config{Endpoint: up.URL}).ListProducts(ctx, ports.ProductListQuery{})
	if err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
}
