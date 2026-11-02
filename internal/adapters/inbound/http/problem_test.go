package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The sanitization contract of problem.go: upstream free text never
// reaches a response body; only handler-authored sentences or a fixed
// sentence plus the upstream's ADR 0018 classification slug do.
func TestUpstreamSlug(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			"tool-error marker names the slug, not the upstream",
			fmt.Errorf("invalid input: explain_travel_factor: facility-layout: tool get_travel_factor reported an error: malformed-location-code: XX is not a seven-segment code"),
			"malformed-location-code",
		},
		{
			"raw leading slug without the ToolError wrapper",
			errors.New("internal-error: routing graph not loaded"),
			"internal-error",
		},
		{
			"authored text has no slug",
			fmt.Errorf("%w: fromLocationCode and toLocationCode are both required", errors.New("invalid input")),
			"",
		},
		{
			"transport text has no slug",
			errors.New(`dial tcp 127.0.0.1:8080: connect: connection refused`),
			"",
		},
	}
	for _, tc := range cases {
		if got := upstreamSlug(tc.err); got != tc.want {
			t.Errorf("%s: upstreamSlug = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCallerAndUpstreamDetailDropUpstreamProse(t *testing.T) {
	slugged := errors.New(
		"facility-layout: tool get_travel_factor reported an error: internal-error: routing graph not loaded")
	if got, want := callerDetail(slugged), "invalid request (internal-error)"; got != want {
		t.Errorf("callerDetail = %q, want %q", got, want)
	}
	if strings.Contains(callerDetail(slugged), "routing graph not loaded") {
		t.Error("callerDetail leaked upstream prose")
	}
	if got, want := upstreamDetail("facility-layout", slugged),
		"facility-layout rejected the request (internal-error)"; got != want {
		t.Errorf("upstreamDetail = %q, want %q", got, want)
	}
	bare := errors.New("connection refused")
	if got, want := upstreamDetail("facility-layout", bare), "facility-layout is unavailable"; got != want {
		t.Errorf("upstreamDetail (no slug) = %q, want %q", got, want)
	}
}

// writeProblem answers RFC 9457 application/problem+json with the five
// problem members and never echoes the upstream text it was handed.
func TestWriteProblemShape(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/explain-travel-factor?pathId=p", nil)
	writeProblem(rec, req, http.StatusBadGateway,
		upstreamDetail("facility-layout", errors.New("internal-error: routing graph not loaded")))

	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"type":"https://problems.warehouse-ops-agent.io/upstream-unavailable"`,
		`"title":"Upstream Unavailable"`,
		`"status":502`,
		`"instance":"/explain-travel-factor"`,
		`"detail":"facility-layout rejected the request (internal-error)"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s lacks %s", body, want)
		}
	}
	if strings.Contains(body, "routing graph not loaded") {
		t.Error("problem body leaked upstream prose")
	}
}
