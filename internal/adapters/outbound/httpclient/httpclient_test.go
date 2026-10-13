package httpclient_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient/httpclienttest"
)

func TestNew_PropagatesTraceContext(t *testing.T) {
	var rec httpclienttest.Recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Record(r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ctx := httpclienttest.TracedContext(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := httpclient.New(2 * time.Second).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()

	rec.AssertPropagated(t)
}

func TestNew_KeepsTheTimeout(t *testing.T) {
	if got := httpclient.New(3 * time.Second).Timeout; got != 3*time.Second {
		t.Fatalf("timeout = %v, want 3s", got)
	}
	if got := httpclient.New(0).Timeout; got != 0 {
		t.Fatalf("a zero timeout must stay zero (no client-side cap; the caller's context bounds the call), got %v", got)
	}
}

func TestNew_ReturnsIndependentClients(t *testing.T) {
	a, b := httpclient.New(time.Second), httpclient.New(time.Second)
	if a == b {
		t.Fatal("every caller must get its own *http.Client")
	}
}
