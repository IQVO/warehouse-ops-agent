package http_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

func TestGetExplainTravelFactor_Returns200WithCorrelation(t *testing.T) {
	handlers := &inboundhttp.Handlers{
		DailyBrief: newTestDailyBrief(),
		ExplainTravelFactor: &usecases.ExplainTravelFactor{
			Facility: &fakeFacility{travel: ports.TravelDistance{MetresM: 90.0, Estimated: false}},
		},
	}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/explain-travel-factor?pathId=pick-a&fromLocationCode=WH1-STOR-AMB-A07-01-01-A&toLocationCode=WH1-STOR-AMB-A09-03-01-A", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		MetresM   float64 `json:"metresM"`
		Estimated bool    `json:"estimated"`
		Kind      string  `json:"kind"`
		Rationale string  `json:"rationale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, rec.Body.String())
	}
	if body.MetresM != 90.0 {
		t.Errorf("metresM = %v, want 90.0", body.MetresM)
	}
	if body.Kind != "travel_significant" {
		t.Errorf("kind = %q, want travel_significant", body.Kind)
	}
	if body.Rationale == "" {
		t.Error("expected a non-empty rationale")
	}
}

func TestGetExplainTravelFactor_NotConfigured_Returns503(t *testing.T) {
	handlers := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/explain-travel-factor?pathId=pick-a&fromLocationCode=A&toLocationCode=B", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestGetExplainTravelFactor_MissingLocationCode_Returns400(t *testing.T) {
	handlers := &inboundhttp.Handlers{
		DailyBrief: newTestDailyBrief(),
		ExplainTravelFactor: &usecases.ExplainTravelFactor{
			Facility: &fakeFacility{travel: ports.TravelDistance{MetresM: 90.0}},
		},
	}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/explain-travel-factor?pathId=pick-a&fromLocationCode=WH1-STOR-AMB-A07-01-01-A", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// An unreachable / failing facility-layout is an upstream degradation, not
// a caller mistake: it must surface as 502, never as the 400 reserved for
// invalid input.
func TestGetExplainTravelFactor_UpstreamFailure_Returns502(t *testing.T) {
	handlers := &inboundhttp.Handlers{
		DailyBrief: newTestDailyBrief(),
		ExplainTravelFactor: &usecases.ExplainTravelFactor{
			Facility: &fakeFacility{err: errors.New("facility-layout unreachable")},
		},
	}
	router := inboundhttp.NewRouter(handlers, "warehouse-ops-agent-test")

	req := httptest.NewRequest(http.MethodGet, "/explain-travel-factor?pathId=pick-a&fromLocationCode=WH1-STOR-AMB-A07-01-01-A&toLocationCode=WH1-STOR-AMB-A09-03-01-A", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rec.Code, rec.Body.String())
	}
}
