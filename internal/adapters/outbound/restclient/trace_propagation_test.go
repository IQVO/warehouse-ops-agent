package restclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient/httpclienttest"
)

// Every REST client (OLTP and *-reports) shares newHTTPClient, so one
// call through each family proves the otelhttp transport is in place.
func TestRESTClients_PropagateTraceContext(t *testing.T) {
	var rec httpclienttest.Recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lagSeconds": 1}`))
	}))
	defer srv.Close()

	ctx := httpclienttest.TracedContext(t)
	if _, err := NewOrderManagement(srv.URL, 5*time.Second).GetOrder(ctx, "o-1"); err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if _, err := NewOrderFunnelReports(srv.URL, 5*time.Second).GetFunnelFreshnessLagSeconds(ctx); err != nil {
		t.Fatalf("GetFunnelFreshnessLagSeconds: %v", err)
	}
	rec.AssertPropagated(t)
}

func TestNewHTTPClient_DefaultsTheTimeout(t *testing.T) {
	if got := newHTTPClient(0).Timeout; got != 5*time.Second {
		t.Fatalf("zero timeout must default to 5s, got %v", got)
	}
	if got := newHTTPClient(2 * time.Second).Timeout; got != 2*time.Second {
		t.Fatalf("timeout = %v, want 2s", got)
	}
}
