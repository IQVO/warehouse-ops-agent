package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

type flowBalanceEvidenceDTO struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
}

type utilizationCorrelationDTO struct {
	Kind      string `json:"kind"`
	Rationale string `json:"rationale"`
}

type flowBalanceExceptionDTO struct {
	PathId            string                     `json:"pathId"`
	RecommendedAction string                     `json:"recommendedAction"`
	ProposedHeads     int                        `json:"proposedHeads,omitempty"`
	Rationale         string                     `json:"rationale"`
	Partial           bool                       `json:"partial"`
	MissingSignals    []string                   `json:"missingSignals,omitempty"`
	Evidence          []flowBalanceEvidenceDTO   `json:"evidence"`
	Utilization       *utilizationCorrelationDTO `json:"utilization,omitempty"`
	// Source is the ADR-0004 arbitration source that produced this
	// Decision: deterministic, llm, or fallback. Omitted when empty
	// (tests/paths that never went through FlowBalanceAdvisory.arbitrate).
	Source string `json:"source,omitempty"`
}

func toFlowBalanceExceptionDTO(d policy.Decision) flowBalanceExceptionDTO {
	evidence := make([]flowBalanceEvidenceDTO, 0, len(d.Evidence))
	for _, e := range d.Evidence {
		evidence = append(evidence, flowBalanceEvidenceDTO{Source: e.Source, Detail: e.Detail})
	}
	dto := flowBalanceExceptionDTO{
		PathId:            d.PathId,
		RecommendedAction: string(d.RecommendedAction),
		ProposedHeads:     d.ProposedHeads,
		Rationale:         d.Rationale,
		Partial:           d.Partial,
		MissingSignals:    d.MissingSignals,
		Evidence:          evidence,
		Source:            string(d.Source),
	}
	if d.Utilization != nil {
		dto.Utilization = &utilizationCorrelationDTO{
			Kind:      string(d.Utilization.Kind),
			Rationale: d.Utilization.Rationale,
		}
	}
	return dto
}

// getFlowBalanceException handles GET /flow-balance/{pathId}?buildingId=&shiftId=.
// buildingId and shiftId are required query params (the workforce-management
// staffing-gap lookup needs them); a missing FlowBalanceAdvisory (not wired
// by the composition root) responds 503 rather than a nil-pointer panic.
func (h *Handlers) getFlowBalanceException(w http.ResponseWriter, r *http.Request) {
	if h.FlowBalanceAdvisory == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "flow balance advisory not configured")
		return
	}
	pathId := chi.URLParam(r, "pathId")
	buildingId := r.URL.Query().Get("buildingId")
	shiftId := r.URL.Query().Get("shiftId")

	decision, err := h.FlowBalanceAdvisory.Execute(r.Context(), buildingId, shiftId, pathId)
	if err != nil {
		// Execute only ever returns this agent's own authored errors
		// (missing params, an unrecognized rebalance action): upstream
		// failures degrade to a partial decision instead of escaping, so
		// the detail is callerDetail's authored text (or its slug-only
		// form should a wrapped tool error ever surface here).
		writeProblem(w, r, http.StatusBadRequest, callerDetail(err))
		return
	}
	writeJSON(w, toFlowBalanceExceptionDTO(decision))
}
