package anthropic

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient/httpclienttest"
)

// TestReason_DefaultHTTPClientPropagatesTraceContext: with no HTTPClient
// configured, the Messages API call goes through an otelhttp transport,
// so the caller's trace continues into the LLM call.
func TestReason_DefaultHTTPClientPropagatesTraceContext(t *testing.T) {
	var rec httpclienttest.Recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_1","name":"submit_plan","input":{"recommendedAction":"hold","proposedHeads":0,"rationale":"nothing to do"}}]}`))
	}))
	defer srv.Close()

	r, err := New(Config{APIKey: "k", BaseURL: srv.URL, Timeout: 5 * time.Second, MaxTurns: 2}, &fakeInvoker{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reason(httpclienttest.TracedContext(t), brief()); err != nil {
		t.Fatalf("Reason: %v", err)
	}
	rec.AssertPropagated(t)
}
