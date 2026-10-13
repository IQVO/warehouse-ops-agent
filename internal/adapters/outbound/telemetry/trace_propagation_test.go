package telemetry_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient/httpclienttest"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/telemetry"
)

func TestPrometheusReader_PropagatesTraceContext(t *testing.T) {
	var rec httpclienttest.Recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "success", "data": {"resultType": "vector", "result": []}}`))
	}))
	defer srv.Close()

	reader := telemetry.NewPrometheusReader(srv.URL, 5*time.Second)
	if _, err := reader.InstantQuery(httpclienttest.TracedContext(t), `up`); err != nil {
		t.Fatalf("InstantQuery: %v", err)
	}
	rec.AssertPropagated(t)
}
