package http

import (
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
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
