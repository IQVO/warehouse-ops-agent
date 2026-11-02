// Package restclient is the outbound REST-client adapter for the
// console-bff's order-lifecycle fan-out ONLY. Unlike mcpclient (which
// wraps each upstream context's curated MCP tools for LLM-facing use
// cases), these four thin clients call each context's own plain public
// REST API directly -- exactly the endpoints a human operator or any
// other REST consumer would call, never a Go import of another
// service's packages and never a database read (governance charter: no
// cross-service DB reads, ever).
package restclient

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

// httpGetJSON issues a GET to baseURL+path (with the given query params)
// and decodes a 200 JSON body into out. A non-2xx status is reported as an
// error rather than silently decoded, since none of these callers can
// meaningfully proceed on partial garbage.
func httpGetJSON(ctx context.Context, client *http.Client, baseURL, path string, query url.Values, out any) error {
	u := baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("restclient: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("restclient: %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return ports.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("restclient: %s: unexpected status %d", u, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("restclient: %s: decode response: %w", u, err)
	}
	return nil
}

// newHTTPClient is every REST client's transport: httpclient.New's
// otelhttp-instrumented client, so the console-bff fan-out continues the
// inbound request's trace into each upstream.
func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return httpclient.New(timeout)
}

const (
	// listPageLimit is the REST guideline's maximum page size (limit 1-500).
	listPageLimit = 500
	// listMaxPages bounds a single list read: listPageLimit*listMaxPages rows
	// per call. A provider still returning a nextCursor beyond it is an error,
	// never a silent truncation.
	listMaxPages = 20
)

// pagedList is the fleet's paged collection envelope (REST guideline §3.3).
type pagedList[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor"`
}

// listAll reads every page of a collection GET: it reads `items`, follows
// `nextCursor` (repeating the filters on each request) and fails once
// listMaxPages is exceeded.
func listAll[T any](ctx context.Context, client *http.Client, baseURL, path string, filters url.Values) ([]T, error) {
	var all []T
	cursor := ""
	for page := 0; page < listMaxPages; page++ {
		q := url.Values{}
		for k, v := range filters {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(listPageLimit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var out pagedList[T]
		if err := httpGetJSON(ctx, client, baseURL, path, q, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if out.NextCursor == "" {
			return all, nil
		}
		cursor = out.NextCursor
	}
	return nil, fmt.Errorf("restclient: %s: more than %d pages, refusing to truncate", path, listMaxPages)
}
