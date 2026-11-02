package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
)

// getExplainTravelFactor handles
// GET /explain-travel-factor?pathId=&fromLocationCode=&toLocationCode=.
// fromLocationCode and toLocationCode are required query params -- the
// caller must already know both facility-layout location codes (see
// usecases.ExplainTravelFactor's own doc comment for why this handler
// never infers them). A missing ExplainTravelFactor (not wired by the
// composition root) responds 503 rather than a nil-pointer panic.
func (h *Handlers) getExplainTravelFactor(w http.ResponseWriter, r *http.Request) {
	if h.ExplainTravelFactor == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "explain travel factor not configured")
		return
	}
	pathId := r.URL.Query().Get("pathId")
	from := r.URL.Query().Get("fromLocationCode")
	to := r.URL.Query().Get("toLocationCode")

	result, err := h.ExplainTravelFactor.Execute(r.Context(), pathId, from, to)
	if err != nil {
		// Invalid caller input is a 400; anything else is facility-layout
		// being unreachable or rejecting the call -- an upstream
		// degradation, reported as 502 rather than blamed on the caller.
		if errors.Is(err, usecases.ErrInvalidInput) {
			writeProblem(w, r, http.StatusBadRequest, callerDetail(err))
			return
		}
		writeProblem(w, r, http.StatusBadGateway, upstreamDetail("facility-layout", err))
		return
	}
	writeJSON(w, toTravelFactorDTO(result))
}

// travelFactorDTO is the GET /explain-travel-factor response body.
// Kind/Rationale are omitted entirely (never a zero-valued/empty string
// masquerading as an outcome) when facility-layout was unreachable and
// no correlation could be produced.
type travelFactorDTO struct {
	MetresM   float64 `json:"metresM"`
	Estimated bool    `json:"estimated"`
	Kind      string  `json:"kind,omitempty"`
	Rationale string  `json:"rationale,omitempty"`
}

func toTravelFactorDTO(r usecases.TravelFactorResult) travelFactorDTO {
	dto := travelFactorDTO{}
	if r.Reading != nil {
		dto.MetresM = r.Reading.MetresM
		dto.Estimated = r.Reading.Estimated
	}
	if r.Correlation != nil {
		dto.Kind = string(r.Correlation.Kind)
		dto.Rationale = r.Correlation.Rationale
	}
	return dto
}
