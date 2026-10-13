package logs_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient/httpclienttest"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/logs"
)

func TestLokiReader_PropagatesTraceContext(t *testing.T) {
	var rec httpclienttest.Recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "success", "data": {"result": []}}`))
	}))
	defer srv.Close()

	reader := logs.NewLokiReader(srv.URL, 5*time.Second)
	if _, err := reader.QueryErrorLines(httpclienttest.TracedContext(t), "warehouse-systems", 600, 50); err != nil {
		t.Fatalf("QueryErrorLines: %v", err)
	}
	rec.AssertPropagated(t)
}
