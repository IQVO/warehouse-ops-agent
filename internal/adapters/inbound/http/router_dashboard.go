package http

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
)

// getWMSDashboard handles GET /console/reports/wms?from=&to=.
func (h *Handlers) getWMSDashboard(w http.ResponseWriter, r *http.Request) {
	h.serveDashboard(w, r, func(ctx context.Context, from, to time.Time) usecases.DashboardResult {
		return h.ConsoleReports.ExecuteWMS(ctx, from, to)
	})
}

// getWESDashboard handles GET /console/reports/wes?from=&to=.
func (h *Handlers) getWESDashboard(w http.ResponseWriter, r *http.Request) {
	h.serveDashboard(w, r, func(ctx context.Context, from, to time.Time) usecases.DashboardResult {
		return h.ConsoleReports.ExecuteWES(ctx, from, to)
	})
}

// serveDashboard is the shared request handling for both dashboards:
// the same optional-window parsing, the same 503-when-unwired
// convention, and the same envelope.
//
// Unlike the seven upstreams these fan out to -- where from and to are
// REQUIRED RFC3339 params -- both are OPTIONAL here, defaulting to a
// 24-hour trailing window ending now. This is a screen a human opens
// without necessarily knowing what window to ask for; making them state
// one before seeing anything would be a worse default than showing them
// the last day. A param that IS supplied must still be well-formed: a
// malformed timestamp is the caller's bug and gets a 400, never a
// silently substituted default that would answer a question nobody asked.
func (h *Handlers) serveDashboard(w http.ResponseWriter, r *http.Request, execute func(context.Context, time.Time, time.Time) usecases.DashboardResult) {
	if h.ConsoleReports == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "console reports not configured"})
		return
	}

	from, err := parseOptionalTime(r.URL.Query().Get("from"), "from")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	to, err := parseOptionalTime(r.URL.Query().Get("to"), "to")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resolvedFrom, resolvedTo := h.ConsoleReports.ResolveWindow(from, to)
	if !resolvedTo.After(resolvedFrom) {
		// Rejected here rather than fanned out: an inverted window would
		// make every upstream either 400 or return nothing, degrading
		// all sections at once and reporting "the contexts are down"
		// when in fact the request was wrong.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query parameter 'to' must be after 'from'"})
		return
	}

	result := execute(r.Context(), resolvedFrom, resolvedTo)
	writeJSON(w, http.StatusOK, toDashboardDTO(result))
}

// parseOptionalTime parses an RFC3339 timestamp, returning nil (not an
// error) when the param is absent so the use case can apply its default
// window.
func parseOptionalTime(raw, name string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("query parameter '%s' must be an RFC3339 timestamp", name)
	}
	return &t, nil
}

// dashboardDTO is the wire envelope both /console/reports/wms and
// /console/reports/wes return -- deliberately IDENTICAL in shape, so the
// console renders either dashboard with one component that switches only
// on each section's chartKind.
type dashboardDTO struct {
	From        string             `json:"from"`
	To          string             `json:"to"`
	GeneratedAt string             `json:"generatedAt"`
	Sections    []reportSectionDTO `json:"sections"`
}

// reportSectionDTO is one panel. error and freshnessLagSeconds are
// pointers so they serialise as explicit JSON null rather than being
// omitted: the console distinguishes "available, no staleness known"
// from "unavailable, here is why", and an absent key would collapse both
// into undefined.
type reportSectionDTO struct {
	Id                  string           `json:"id"`
	Title               string           `json:"title"`
	SourceContext       string           `json:"sourceContext"`
	ChartKind           string           `json:"chartKind"`
	Available           bool             `json:"available"`
	Error               *string          `json:"error"`
	FreshnessLagSeconds *float64         `json:"freshnessLagSeconds"`
	Series              []seriesPointDTO `json:"series"`
}

// seriesPointDTO's shape is the contract with warehouse-ui-kit: its
// FunnelChart, BarChart and LineChart all take a {label, value}[] `data`
// prop, so all three chartKinds share this one point type.
type seriesPointDTO struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

func toDashboardDTO(r usecases.DashboardResult) dashboardDTO {
	sections := make([]reportSectionDTO, 0, len(r.Sections))
	for _, s := range r.Sections {
		sections = append(sections, toReportSectionDTO(s))
	}
	return dashboardDTO{
		From:        r.From.UTC().Format(time.RFC3339),
		To:          r.To.UTC().Format(time.RFC3339),
		GeneratedAt: r.GeneratedAt.UTC().Format(time.RFC3339),
		Sections:    sections,
	}
}

func toReportSectionDTO(s usecases.ReportSection) reportSectionDTO {
	// Built with make(..., 0, n) so an empty or degraded section
	// marshals as [] rather than null -- a chart component binding to
	// null is a client-side crash, binding to [] is an honest empty
	// chart. Same rule labor-performance's own reports adapter applies.
	series := make([]seriesPointDTO, 0, len(s.Series))
	for _, p := range s.Series {
		series = append(series, seriesPointDTO{Label: p.Label, Value: p.Value})
	}

	dto := reportSectionDTO{
		Id:                  s.Id,
		Title:               s.Title,
		SourceContext:       s.SourceContext,
		ChartKind:           s.ChartKind,
		Available:           s.Available,
		FreshnessLagSeconds: s.FreshnessLagSeconds,
		Series:              series,
	}
	if s.Error != "" {
		errMsg := s.Error
		dto.Error = &errMsg
	}
	return dto
}
