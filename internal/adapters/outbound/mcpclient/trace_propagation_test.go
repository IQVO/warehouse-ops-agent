package mcpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient/httpclienttest"
)

// TestSession_PropagatesTraceContext: the Streamable-HTTP transport's
// requests carry the caller's trace context (otelhttp transport), so an
// upstream context's MCP span joins the agent's trace. The upstream here
// refuses the session; only the request headers matter.
func TestSession_PropagatesTraceContext(t *testing.T) {
	var rec httpclienttest.Recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Record(r)
		http.Error(w, "no session", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s := New(Config{Name: "upstream", Endpoint: srv.URL, Timeout: 2 * time.Second})
	if err := s.callTool(httpclienttest.TracedContext(t), "any_tool", map[string]any{}, nil); err == nil {
		t.Fatal("a refused session must surface an error")
	}
	rec.AssertPropagated(t)
}
