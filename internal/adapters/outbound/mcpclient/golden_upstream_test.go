package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file is the ADR-0025 twelve-client generalization of the
// per-client schema-faithful stand-ins the ADR-0020/0021/0024 clients
// hand-rolled (returnsProcessingUpstream, exceptionManagementUpstream,
// ...): one golden-driven upstream double shared by the twelve newest
// clients' tests, plus the small assertion helpers they all use. Each
// per-client test file still pins its own golden tool set, its own port
// DTOs and its own fixtures; only the machinery lives here.

// loadGoldenTools reads one upstream's verbatim published tool-registry
// golden into a name-keyed map.
func loadGoldenTools(t *testing.T, path string) map[string]goldenTool {
	t.Helper()
	raw, err := os.ReadFile(path)
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

// goldenCall is one recorded upstream call: the tool name and the exact
// argument map the client sent.
type goldenCall struct {
	Tool string
	Args map[string]any
}

// goldenUpstream is a real Streamable-HTTP MCP server whose tools carry
// an upstream's PUBLISHED input and output schemas, taken from that
// upstream's verbatim registry golden: the SDK validates the client's
// arguments against the real input schema (additionalProperties:false,
// so a misspelled key is rejected) and the canned fixtures against the
// real output schema, exactly as the real upstream would.
type goldenUpstream struct {
	*httptest.Server
	mu    sync.Mutex
	calls []goldenCall
}

// goldenFixture answers one tool call: either a result value, or a tool
// error result (IsError) to script a rejection.
type goldenFixture func(args map[string]any) (any, *mcp.CallToolResult)

// goldenResult fixes one canned result for every call of a tool.
func goldenResult(v any) goldenFixture {
	return func(map[string]any) (any, *mcp.CallToolResult) { return v, nil }
}

// goldenToolError renders an isError tool result carrying the fleet's
// "<slug>: <detail>" convention text (ADR 0018).
func goldenToolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// goldenJSON decodes a canned JSON fixture once, at stand-in build time.
func goldenJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("fixture JSON: %v", err)
	}
	return m
}

// newGoldenUpstream serves one tool per fixture, each carrying its
// published schemas from the golden, and records every call.
func newGoldenUpstream(t *testing.T, name string, golden map[string]goldenTool, fixtures map[string]goldenFixture) *goldenUpstream {
	t.Helper()
	up := &goldenUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: name, Version: "0"}, nil)
	for tool, fix := range fixtures {
		g, fix := golden[tool], fix
		if g.Name == "" {
			t.Fatalf("%s is not in the golden; a fixture may only serve a published tool", tool)
		}
		mcp.AddTool(server, &mcp.Tool{Name: g.Name, InputSchema: g.InputSchema, OutputSchema: g.OutputSchema},
			func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				up.record(g.Name, in)
				v, toolErr := fix(in)
				if toolErr != nil {
					return toolErr, nil, nil
				}
				return nil, v, nil
			})
	}
	up.Server = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(up.Close)
	return up
}

func (u *goldenUpstream) record(tool string, args map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, goldenCall{Tool: tool, Args: args})
}

// toolsCalled returns every recorded tool name, in call order.
func (u *goldenUpstream) toolsCalled() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]string, 0, len(u.calls))
	for _, c := range u.calls {
		out = append(out, c.Tool)
	}
	return out
}

// lastArgs returns the argument map of the most recent call.
func (u *goldenUpstream) lastArgs() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.calls) == 0 {
		return nil
	}
	return u.calls[len(u.calls)-1].Args
}

// assertPinnedGoldenSet pins a golden to exactly want, every tool
// annotated readOnlyHint.
func assertPinnedGoldenSet(t *testing.T, golden map[string]goldenTool, upstream string, want []string) {
	t.Helper()
	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("%s publishes %v, this golden is built for exactly %v", upstream, names, want)
	}
	for _, name := range want {
		if golden[name].Annotations["readOnlyHint"] != true {
			t.Errorf("%s is not annotated readOnlyHint in %s's registry: this agent may only call read tools", name, upstream)
		}
	}
}

// assertOnlyPinnedToolsCalled fails unless the stand-in saw exactly the
// wanted tool names (order-insensitive).
func assertOnlyPinnedToolsCalled(t *testing.T, up *goldenUpstream, want []string) {
	t.Helper()
	got := up.toolsCalled()
	sortedWant := append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(sortedWant)
	if strings.Join(got, ",") != strings.Join(sortedWant, ",") {
		t.Fatalf("tools called = %v, want exactly %v", got, sortedWant)
	}
}

// wantEq is the flat field assertion the per-client decode tests use: one
// call per field keeps each test's cyclomatic complexity at one.
func wantEq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// withID builds a fixture that answers ok when args[idKey] equals want,
// and a "<slug>: no id ..." tool error otherwise — the not-found shape
// every ADR-0025 upstream uses.
func withID(idKey, want, slug string, ok any) goldenFixture {
	return func(args map[string]any) (any, *mcp.CallToolResult) {
		if id, _ := args[idKey].(string); id != want {
			return nil, goldenToolError(slug + ": no id " + id)
		}
		return ok, nil
	}
}
