// Package httpclienttest holds the shared test fixture for asserting that
// an outbound adapter's HTTP calls carry the caller's trace context (the
// W3C traceparent header), i.e. that the adapter's http.Client goes
// through httpclient.New's otelhttp transport.
package httpclienttest

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TraceID is the fixed trace id TracedContext puts on the context.
const TraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

// TracedContext installs the W3C trace-context propagator globally for
// the duration of the test (restored on cleanup) and returns a context
// carrying a sampled remote span with TraceID. Tests using it must not
// run in parallel with other tests that touch the global propagator.
func TracedContext(t *testing.T) context.Context {
	t.Helper()
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	traceID, err := trace.TraceIDFromHex(TraceID)
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

// Recorder remembers the traceparent headers an httptest handler saw.
type Recorder struct {
	mu   sync.Mutex
	seen []string
}

// Record stores r's traceparent header (possibly empty).
func (rec *Recorder) Record(r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.seen = append(rec.seen, r.Header.Get("traceparent"))
}

// AssertPropagated fails the test unless at least one request was seen
// and every request carried a traceparent with TraceID.
func (rec *Recorder) AssertPropagated(t *testing.T) {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.seen) == 0 {
		t.Fatal("the upstream saw no request")
	}
	for i, h := range rec.seen {
		if !strings.Contains(h, TraceID) {
			t.Fatalf("request %d: traceparent %q does not carry trace id %s: the http.Client is not using the otelhttp transport", i, h, TraceID)
		}
	}
}
