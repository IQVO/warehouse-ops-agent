// Package telemetry is the outbound telemetry-reader adapter: it implements
// internal/ports.TelemetryReader against the warehouse-infra observability
// stack (Prometheus). PrometheusReader replaces T1's StubReader with a real
// HTTP client against Prometheus's own HTTP API
// (https://prometheus.io/docs/prometheus/latest/querying/api/) --
// deliberately hand-rolled rather than a dependency on
// github.com/prometheus/client_golang/api, since this adapter only ever
// needs two read-only endpoints (instant + range query) and a bespoke
// ~80-line client keeps this repo's dependency surface (and its
// zero-write guarantee's auditable surface) smaller than pulling in a
// full Prometheus client SDK for two GET calls.
package telemetry

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

// PrometheusReader implements ports.TelemetryReader against a Prometheus
// HTTP API base URL (e.g. http://prometheus-server.observability:9090 in
// cluster, or a port-forwarded localhost URL in local dev/manual probing).
// Every method issues a GET only -- there is no write path on Prometheus's
// query API to begin with, but that property is exactly what makes this
// adapter safe to add without touching the zerowrite fitness test's scope
// (internal/architecture/zerowrite only scans restclient/mcpclient, the
// clients that talk to the five upstream BOUNDED CONTEXTS -- this adapter
// talks to observability infrastructure, a different trust boundary, and
// GET-only is enforced here by construction: httpGetJSON is the only
// request-building helper in this package).
type PrometheusReader struct {
	baseURL string
	client  *http.Client
}

// NewPrometheusReader builds a PrometheusReader. timeout bounds every
// query call; callers should keep this well under the reasoner's own
// request budget since a slow/unreachable Prometheus must never block the
// runtime-signals report longer than a few seconds (see RuntimeSignals'
// own per-source timeout composition).
func NewPrometheusReader(baseURL string, timeout time.Duration) *PrometheusReader {
	return &PrometheusReader{
		baseURL: baseURL,
		client:  httpclient.New(timeout),
	}
}

var _ ports.TelemetryReader = (*PrometheusReader)(nil)

// promResponse is the envelope every Prometheus HTTP API query returns.
// See https://prometheus.io/docs/prometheus/latest/querying/api/#format-overview.
type promResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

// promVectorSample is one element of an instant-query "vector" result.
type promVectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"` // [unixSeconds float64, stringValue string]
}

// promMatrixSeries is one element of a range-query "matrix" result: one
// label set with a list of [timestamp, value] samples.
type promMatrixSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"`
}

func (r *PrometheusReader) InstantQuery(ctx context.Context, promQL string) ([]ports.MetricSample, error) {
	resp, err := r.get(ctx, "/api/v1/query", url.Values{"query": {promQL}})
	if err != nil {
		return nil, err
	}
	if resp.Data.ResultType != "vector" {
		return nil, fmt.Errorf("telemetry: instant query returned resultType %q, want vector", resp.Data.ResultType)
	}
	var vec []promVectorSample
	if err := json.Unmarshal(resp.Data.Result, &vec); err != nil {
		return nil, fmt.Errorf("telemetry: decode vector result: %w", err)
	}
	out := make([]ports.MetricSample, 0, len(vec))
	for _, v := range vec {
		sample, err := toSample(v.Metric, v.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, sample)
	}
	return out, nil
}

func (r *PrometheusReader) RangeQuery(ctx context.Context, promQL string, start, end time.Time, step time.Duration) ([]ports.MetricSample, error) {
	resp, err := r.get(ctx, "/api/v1/query_range", url.Values{
		"query": {promQL},
		"start": {formatTimestamp(start)},
		"end":   {formatTimestamp(end)},
		"step":  {step.String()},
	})
	if err != nil {
		return nil, err
	}
	if resp.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("telemetry: range query returned resultType %q, want matrix", resp.Data.ResultType)
	}
	var matrix []promMatrixSeries
	if err := json.Unmarshal(resp.Data.Result, &matrix); err != nil {
		return nil, fmt.Errorf("telemetry: decode matrix result: %w", err)
	}
	var out []ports.MetricSample
	for _, series := range matrix {
		for _, v := range series.Values {
			sample, err := toSample(series.Metric, v)
			if err != nil {
				return nil, err
			}
			out = append(out, sample)
		}
	}
	return out, nil
}

func (r *PrometheusReader) get(ctx context.Context, path string, query url.Values) (*promResponse, error) {
	u := r.baseURL + path + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telemetry: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telemetry: %s: unexpected status %d", path, resp.StatusCode)
	}
	var out promResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("telemetry: %s: decode response: %w", path, err)
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("telemetry: %s: prometheus returned status %q: %s", path, out.Status, out.Error)
	}
	return &out, nil
}

// toSample converts one raw [timestamp, stringValue] pair plus its label
// set into a ports.MetricSample. Prometheus encodes sample VALUES as
// strings even in a "vector"/"matrix" JSON result (only the timestamp is a
// bare float64) -- this is a documented quirk of the API, not a decode bug.
func toSample(labels map[string]string, pair [2]any) (ports.MetricSample, error) {
	ts, ok := pair[0].(float64)
	if !ok {
		return ports.MetricSample{}, fmt.Errorf("telemetry: sample timestamp is not a number: %v", pair[0])
	}
	valStr, ok := pair[1].(string)
	if !ok {
		return ports.MetricSample{}, fmt.Errorf("telemetry: sample value is not a string: %v", pair[1])
	}
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return ports.MetricSample{}, fmt.Errorf("telemetry: parse sample value %q: %w", valStr, err)
	}
	return ports.MetricSample{
		Labels:    labels,
		Value:     val,
		Timestamp: time.Unix(int64(ts), 0).UTC(),
	}, nil
}

func formatTimestamp(t time.Time) string {
	return strconv.FormatFloat(float64(t.Unix()), 'f', 3, 64)
}
