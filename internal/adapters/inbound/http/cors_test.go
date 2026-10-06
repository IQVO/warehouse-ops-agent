package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
)

// preflight sends a CORS preflight for method from the local-dev console
// origin and returns the recorder.
func preflight(t *testing.T, method string) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodOptions, "/daily-brief", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", method)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// Every route is a GET, so CORS must permit GET (and OPTIONS preflight) only.
func TestCORS_PreflightAllowsGET(t *testing.T) {
	rec := preflight(t, http.MethodGet)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the console origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != http.MethodGet {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, http.MethodGet)
	}
}

func TestCORS_PreflightRejectsWriteMethods(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := preflight(t, method)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s preflight: Access-Control-Allow-Origin = %q, want it withheld", method, got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
			t.Errorf("%s preflight: Access-Control-Allow-Methods = %q, want empty", method, got)
		}
	}
}
