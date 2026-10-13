// Package httpclient builds the http.Client every outbound adapter uses
// (REST, MCP, Prometheus, Loki, Anthropic). The client's transport is an
// otelhttp transport, so each call gets a client span and carries the
// caller's trace context (W3C traceparent) to the upstream.
package httpclient

import (
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// New returns a fresh *http.Client whose transport is
// otelhttp.NewTransport over http.DefaultTransport, with the given
// timeout (zero means no client-side cap; the caller's context bounds the
// call).
func New(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
}
