// Package logs is the outbound log-reader adapter: it implements
// internal/ports.LogReader against the warehouse-infra observability
// stack's log aggregator (Grafana Loki). Read-only by construction --
// LokiReader only ever issues a GET to Loki's query_range endpoint
// (https://grafana.com/docs/loki/latest/reference/loki-http-api/#query-loki-logs).
package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// LokiReader implements ports.LogReader against a Loki HTTP API base URL.
type LokiReader struct {
	baseURL string
	client  *http.Client
}

// NewLokiReader builds a LokiReader. timeout bounds every query call, same
// convention as telemetry.NewPrometheusReader.
func NewLokiReader(baseURL string, timeout time.Duration) *LokiReader {
	return &LokiReader{
		baseURL: baseURL,
		client:  httpclient.New(timeout),
	}
}

var _ ports.LogReader = (*LokiReader)(nil)

// lokiQueryRangeResponse is the subset of Loki's query_range response body
// this adapter needs. resultType is always "streams" for a log (as
// opposed to metric) query.
type lokiQueryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []lokiStream `json:"result"`
	} `json:"data"`
}

type lokiStream struct {
	Stream  map[string]string `json:"stream"`
	Entries [][2]string       `json:"values"` // [unixNanoTimestamp, line]
}

// QueryErrorLines implements ports.LogReader. It scopes the LogQL query to
// {namespace="<namespace>"} |~ "(?i)error|fatal" -- a case-insensitive
// substring match on the raw line, deliberately simple: this fleet's
// services log via plain slog text lines, not a structured `level` field
// consistently enough to rely on Loki's `level` label alone (verified:
// `level` IS a real Loki label here, but not every emitted line carries
// one -- see the label list surfaced by GET /loki/api/v1/label). Matching
// the raw line text catches both cases.
func (r *LokiReader) QueryErrorLines(ctx context.Context, namespace string, sinceSeconds int64, limit int) ([]ports.LogEntry, error) {
	end := time.Now()
	start := end.Add(-time.Duration(sinceSeconds) * time.Second)

	query := fmt.Sprintf(`{namespace=%q} |~ "(?i)error|fatal"`, namespace)
	q := url.Values{
		"query":     {query},
		"limit":     {strconv.Itoa(limit)},
		"start":     {strconv.FormatInt(start.UnixNano(), 10)},
		"end":       {strconv.FormatInt(end.UnixNano(), 10)},
		"direction": {"backward"}, // newest first
	}

	u := r.baseURL + "/loki/api/v1/query_range?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("logs: build request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("logs: query_range: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("logs: query_range: unexpected status %d", resp.StatusCode)
	}
	var out lokiQueryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("logs: query_range: decode response: %w", err)
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("logs: query_range: loki returned status %q", out.Status)
	}

	var entries []ports.LogEntry
	for _, stream := range out.Data.Result {
		for _, e := range stream.Entries {
			nanos, err := strconv.ParseInt(e[0], 10, 64)
			if err != nil {
				continue // skip a malformed timestamp rather than fail the whole read
			}
			entries = append(entries, ports.LogEntry{
				Labels:    stream.Stream,
				Line:      e[1],
				Timestamp: time.Unix(0, nanos).UTC().Format(time.RFC3339),
			})
		}
	}
	return entries, nil
}
