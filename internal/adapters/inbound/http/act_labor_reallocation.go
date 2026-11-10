package http

import (
	nethttp "net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// The labor-reallocation act slice routes (ADR 0029, act slice #2) -- the
// second and last pair of POST routes on this agent:
//
//	POST /labor-reallocation/proposals                         {siteCode, shiftId, toPathId, candidates[]}
//	POST /labor-reallocation/proposals/{proposalId}/approvals  {token, approvedBy}
//
// (.../{proposalId}/approve is the deprecated alias of the second, ADR 0031.)
//
// Propose writes nothing: it returns up to three ranked moves, each with its
// own short-lived confirmation token bound to that exact move. Approve is
// the single write: it delegates ONE move to workforce-management. Exactly as
// for ADR 0027 the routes are REST-only (never MCP tools, never reachable by
// the LLM) and share its transport protections (strict application/json,
// bounded body, unknown fields rejected, GET/OPTIONS-only CORS).

const laborReallocationNotConfigured = "labor reallocation not configured (WORKFORCE_MANAGEMENT_A2A_URL unset)"

type reallocationCandidateRequest struct {
	AssociateId string `json:"associateId"`
	FromPathId  string `json:"fromPathId"`
}

type reallocationProposeRequest struct {
	SiteCode   string                         `json:"siteCode"`
	ShiftId    string                         `json:"shiftId"`
	ToPathId   string                         `json:"toPathId"`
	Candidates []reallocationCandidateRequest `json:"candidates"`
}

type reallocationApproveRequest struct {
	Token      string `json:"token"`
	ApprovedBy string `json:"approvedBy"`
}

type pathStaffingDTO struct {
	PathId       string `json:"pathId"`
	PlannedHeads int    `json:"plannedHeads"`
	ActiveHeads  int    `json:"activeHeads"`
	Understaffed bool   `json:"understaffed"`
}

type reallocationEvidenceDTO struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

type skippedCandidateDTO struct {
	AssociateId string `json:"associateId"`
	Reason      string `json:"reason"`
}

type reallocationProposalDTO struct {
	ProposalId  string                    `json:"proposalId"`
	Rank        int                       `json:"rank"`
	Token       string                    `json:"token"`
	ExpiresAt   string                    `json:"expiresAt"`
	AssociateId string                    `json:"associateId"`
	FromPathId  string                    `json:"fromPathId,omitempty"`
	ToPathId    string                    `json:"toPathId"`
	ReasonCodes []string                  `json:"reasonCodes"`
	Evidence    []reallocationEvidenceDTO `json:"evidence"`
	NextCheck   string                    `json:"nextCheck"`
}

type reallocationProposalsDTO struct {
	SiteCode  string                    `json:"siteCode"`
	ShiftId   string                    `json:"shiftId"`
	Target    pathStaffingDTO           `json:"target"`
	Proposals []reallocationProposalDTO `json:"proposals"`
	Skipped   []skippedCandidateDTO     `json:"skipped"`
}

type reallocationOutcomeDTO struct {
	ProposalId   string `json:"proposalId"`
	AssociateId  string `json:"associateId"`
	PathId       string `json:"pathId"`
	AssignmentId string `json:"assignmentId"`
	ApprovedBy   string `json:"approvedBy"`
	ExecutedAt   string `json:"executedAt"`
}

func toPathStaffingDTO(s policy.PathStaffing) pathStaffingDTO {
	return pathStaffingDTO{PathId: s.PathId, PlannedHeads: s.PlannedHeads, ActiveHeads: s.ActiveHeads, Understaffed: s.Understaffed}
}

func toReallocationProposalDTO(p usecases.ReallocationProposal) reallocationProposalDTO {
	evidence := make([]reallocationEvidenceDTO, 0, len(p.Move.Evidence))
	for _, e := range p.Move.Evidence {
		evidence = append(evidence, reallocationEvidenceDTO{Tool: e.Tool, Summary: e.Summary})
	}
	return reallocationProposalDTO{
		ProposalId:  p.ProposalId,
		Rank:        p.Move.Rank,
		Token:       p.Token,
		ExpiresAt:   p.ExpiresAt.UTC().Format(time.RFC3339),
		AssociateId: p.Move.AssociateId,
		FromPathId:  p.Move.FromPathId,
		ToPathId:    p.Move.ToPathId,
		ReasonCodes: p.Move.ReasonCodes,
		Evidence:    evidence,
		NextCheck:   p.Move.NextCheck,
	}
}

// postReallocationProposal handles POST /labor-reallocation/proposals.
func (h *Handlers) postReallocationProposal(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.LaborReallocation == nil {
		writeProblem(w, r, nethttp.StatusServiceUnavailable, laborReallocationNotConfigured)
		return
	}
	var req reallocationProposeRequest
	if !decodeActBody(w, r, &req) {
		return
	}
	in := usecases.LaborReallocationInput{SiteCode: req.SiteCode, ShiftId: req.ShiftId, ToPathId: req.ToPathId}
	for _, c := range req.Candidates {
		in.Candidates = append(in.Candidates, policy.ReallocationCandidate{AssociateId: c.AssociateId, FromPathId: c.FromPathId})
	}
	p, err := h.LaborReallocation.Propose(r.Context(), in)
	if err != nil {
		writeActErrorFor(w, r, err, "workforce-management")
		return
	}
	out := reallocationProposalsDTO{
		SiteCode:  p.SiteCode,
		ShiftId:   p.ShiftId,
		Target:    toPathStaffingDTO(p.Target),
		Proposals: make([]reallocationProposalDTO, 0, len(p.Proposals)),
		Skipped:   make([]skippedCandidateDTO, 0, len(p.Skipped)),
	}
	for _, pr := range p.Proposals {
		out.Proposals = append(out.Proposals, toReallocationProposalDTO(pr))
	}
	for _, s := range p.Skipped {
		out.Skipped = append(out.Skipped, skippedCandidateDTO{AssociateId: s.AssociateId, Reason: s.Reason})
	}
	writeJSON(w, out)
}

// postReallocationApproval handles
// POST /labor-reallocation/proposals/{proposalId}/approvals and its
// deprecated alias .../approve (ADR 0031).
func (h *Handlers) postReallocationApproval(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.LaborReallocation == nil {
		writeProblem(w, r, nethttp.StatusServiceUnavailable, laborReallocationNotConfigured)
		return
	}
	var req reallocationApproveRequest
	if !decodeActBody(w, r, &req) {
		return
	}
	out, err := h.LaborReallocation.Approve(r.Context(), chi.URLParam(r, "proposalId"), req.Token, req.ApprovedBy)
	if err != nil {
		writeActErrorFor(w, r, err, "workforce-management")
		return
	}
	writeJSON(w, reallocationOutcomeDTO{
		ProposalId:   out.ProposalId,
		AssociateId:  out.AssociateId,
		PathId:       out.PathId,
		AssignmentId: out.AssignmentId,
		ApprovedBy:   out.ApprovedBy,
		ExecutedAt:   out.ExecutedAt.Format(time.RFC3339),
	})
}
