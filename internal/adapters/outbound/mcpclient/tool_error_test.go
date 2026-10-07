package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Table for the fleet's tool-error convention (ADR 0018): the text of an
// isError tool result is "<slug>: <detail>", slug = the upstream REST
// problem slug. Only the explicit validation slugs classify as the
// caller's invalid input; everything else -- including text with no slug
// prefix (an old, pre-convention facility-layout), "internal-error" and
// every "*-not-found" -- keeps today's behaviour.
func TestClassifyToolErrorText(t *testing.T) {
	cases := []struct {
		name        string
		text        string
		wantSlug    string
		wantInvalid bool
	}{
		{"malformed location code", "malformed-location-code: location code must have seven segments", "malformed-location-code", true},
		{"missing location code", "missing-location-code: Location code is required", "missing-location-code", true},
		{"invalid site code", "invalid-site-code: site code must not be blank", "invalid-site-code", true},
		{"invalid prefix, multi segment", "invalid-capacity-window: window end before start", "invalid-capacity-window", true},
		{"required suffix", "fromLocation-required: x", "", false}, // uppercase is not a slug
		{"required suffix, kebab", "from-location-required: from is required", "from-location-required", true},
		{"validation-failed", "validation-failed: from and to must be set", "validation-failed", true},

		{"not found stays 502", "site-not-found: no such site", "site-not-found", false},
		{"zone not found stays 502", "zone-not-found: no such zone", "zone-not-found", false},
		{"internal-error stays 502", "internal-error: an unexpected internal error occurred", "internal-error", false},
		{"conflict stays 502", "duplicate-site-code: exists", "duplicate-site-code", false},
		{"semantic 422 slug is not in the table", "no-route: no route between waypoints", "no-route", false},
		{"unknown-* is not in the table", "unknown-temperature-class: x", "unknown-temperature-class", false},

		{"legacy slug-less text stays 502", "from and to are both required", "", false},
		{"legacy slug-less with colon stays 502", "invalid input: from is blank", "", false},
		{"slug-looking prose with no space after colon", "malformed-location-code:no-space", "", false},
		{"empty text", "", "", false},
		{"no content marker", "(no content)", "", false},
		{"slug only, no detail", "malformed-location-code", "", false},
		{"slug must lead the text", "boom: malformed-location-code: x", "boom", false},
		{"leading whitespace is not a slug", " malformed-location-code: x", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slug, invalid := classifyToolErrorText(tc.text)
			if slug != tc.wantSlug || invalid != tc.wantInvalid {
				t.Fatalf("classifyToolErrorText(%q) = (%q, %v), want (%q, %v)", tc.text, slug, invalid, tc.wantSlug, tc.wantInvalid)
			}
		})
	}
}

// toolErrorUpstream serves estimate_travel_distance and rejects every call
// with the configured tool-error text.
func toolErrorUpstream(t *testing.T, text string) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "facility-layout-test", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "estimate_travel_distance"}, func(_ context.Context, _ *mcp.CallToolRequest, _ estimateTravelDistanceTestIn) (*mcp.CallToolResult, ports.TravelDistance, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}, ports.TravelDistance{}, nil
	})
	return httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
}

// Over the wire: a validation-slug rejection wraps
// ports.ErrUpstreamInvalidInput (so the use case can answer 400); every
// other rejection does not (502, as before). The error TEXT is identical
// in both cases to what it was before the classification existed.
func TestFacilityLayout_ToolRejection_Classification(t *testing.T) {
	cases := []struct {
		name        string
		text        string
		wantInvalid bool
	}{
		{"validation slug", "missing-location-code: Location code is required", true},
		{"malformed slug", "malformed-location-code: bad code", true},
		{"not-found slug", "site-not-found: nope", false},
		{"internal-error", "internal-error: an unexpected internal error occurred", false},
		{"legacy slug-less message", "from and to are both required", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := toolErrorUpstream(t, tc.text)
			defer up.Close()

			c := NewFacilityLayout(Config{Endpoint: up.URL})
			_, err := c.EstimateTravelDistance(context.Background(), "A", "B")
			if err == nil {
				t.Fatal("expected an error for a tool-level rejection")
			}
			if got := errors.Is(err, ports.ErrUpstreamInvalidInput); got != tc.wantInvalid {
				t.Fatalf("errors.Is(err, ErrUpstreamInvalidInput) = %v, want %v (err = %v)", got, tc.wantInvalid, err)
			}
			want := "facility-layout: tool estimate_travel_distance reported an error: " + tc.text
			if err.Error() != want {
				t.Fatalf("error text changed:\n got: %s\nwant: %s", err.Error(), want)
			}
		})
	}
}

// A transport failure is never classified as invalid input.
func TestFacilityLayout_UnreachableUpstream_IsNotInvalidInput(t *testing.T) {
	up := toolErrorUpstream(t, "malformed-location-code: x")
	up.Close()

	c := NewFacilityLayout(Config{Endpoint: up.URL})
	_, err := c.EstimateTravelDistance(context.Background(), "A", "B")
	if err == nil || errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatalf("a connection failure must stay a plain upstream error, got %v", err)
	}
}

// warehouse-planning already follows the slug convention: the same helper
// classifies it without changing its message (the capacity outlook keeps
// reporting the full text as its omitted reason).
func TestWarehousePlanning_ToolRejection_SharesTheClassifier(t *testing.T) {
	up := newPlanningTestUpstream(t)
	defer up.Close()

	c := NewWarehousePlanning(Config{Endpoint: up.URL})
	_, err := c.GetProcessPathCapacity(context.Background(), ports.ProcessPathCapacityRequest{PathId: "missing", Location: "SIM1"})
	if err == nil || errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatalf("process-path-not-found must stay a plain upstream error, got %v", err)
	}
	if !strings.Contains(err.Error(), "process-path-not-found: no such path") {
		t.Fatalf("message must be unchanged, got %v", err)
	}
	// ADR 0019: "*-not-found" additionally answers ErrUpstreamNotFound; it
	// is opt-in and changes no existing classification.
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("a *-not-found slug must satisfy ErrUpstreamNotFound, got %v", err)
	}
}

// The not-found sentinel is exactly the "-not-found" suffix: validation,
// internal-error and slug-less text never satisfy it.
func TestToolError_NotFoundSentinel(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"transfer-not-found: transfer t-1 not found", true},
		{"site-not-found: no such site", true},
		{"invalid-query: unknown state", false},
		{"internal-error: an unexpected internal error occurred", false},
		{"read-models-incomplete: facts are stale", false},
		{"transfer not found", false}, // slug-less prose is never inspected
		{"", false},
	}
	for _, tc := range cases {
		e := newToolError("up", "tool", tc.text)
		if got := errors.Is(e, ports.ErrUpstreamNotFound); got != tc.want {
			t.Errorf("errors.Is(%q, ErrUpstreamNotFound) = %v, want %v", tc.text, got, tc.want)
		}
		if errors.Is(e, ports.ErrUpstreamNotFound) && errors.Is(e, ports.ErrUpstreamInvalidInput) {
			t.Errorf("%q must not be both not-found and invalid input", tc.text)
		}
	}
}
