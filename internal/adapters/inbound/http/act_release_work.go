package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The act slice routes (ADR 0027) — the only POST routes on this agent:
//
//	POST /act/release-next-work/proposals  {pathId, buildingId, shiftId}
//	POST /act/release-next-work/approvals  {token}
//
// Propose writes nothing; approve is the single write, and only with a
// confirmation token that propose minted for a recommendation this agent
// actually made. These routes are deliberately REST-only: they are never
// MCP tools (an MCP caller is typically a model), and CORS stays GET/OPTIONS
// so a page on another origin can never pass the preflight for them.
//
// With no authentication fleet-wide (ADR 0006), the transport-level
// protections here are: (1) strict application/json — a cross-site HTML form
// can only send "simple" content types, so rejecting everything else with 415
// forces a CORS preflight that the GET/OPTIONS-only policy refuses; (2) a
// bounded body and unknown fields rejected; (3) a token a cross-origin
// attacker cannot read from the propose response.

const (
	actNotConfigured = "act slice not configured (WES_WORK_PLANNING_A2A_URL unset)"
	maxActBodyBytes  = 8 << 10
)

type releaseProposeRequest struct {
	PathId     string `json:"pathId"`
	BuildingId string `json:"buildingId"`
	ShiftId    string `json:"shiftId"`
}

type releaseApproveRequest struct {
	Token string `json:"token"`
}

type releaseProposalDTO struct {
	Token          string                  `json:"token"`
	ExpiresAt      string                  `json:"expiresAt"`
	Action         string                  `json:"action"`
	PathId         string                  `json:"pathId"`
	Source         string                  `json:"source"`
	Recommendation flowBalanceExceptionDTO `json:"recommendation"`
	// ExceptionContext is advisory only (ADR 0028): it never changes
	// whether a token is minted.
	ExceptionContext exceptionContextDTO `json:"exceptionContext"`
}

type exceptionContextDTO struct {
	Status  string               `json:"status"`
	Summary *exceptionSummaryDTO `json:"summary,omitempty"`
}

type exceptionSummaryDTO struct {
	OpenCount int                `json:"openCount"`
	Cases     []exceptionCaseDTO `json:"cases"`
}

type exceptionCaseDTO struct {
	CaseID    string `json:"caseId"`
	Status    string `json:"status"`
	Queue     string `json:"queue"`
	Severity  string `json:"severity"`
	Escalated bool   `json:"escalated"`
	Reason    string `json:"reason"`
}

func toExceptionContextDTO(r usecases.ExceptionContextResult) exceptionContextDTO {
	out := exceptionContextDTO{Status: string(r.Status)}
	if r.Status != usecases.ExceptionContextOK || r.Summary == nil {
		return out
	}
	cases := make([]exceptionCaseDTO, 0, len(r.Summary.Cases))
	for _, c := range r.Summary.Cases {
		cases = append(cases, exceptionCaseDTO{CaseID: c.CaseID, Status: c.Status, Queue: c.Queue, Severity: c.Severity, Escalated: c.Escalated, Reason: c.Reason})
	}
	out.Summary = &exceptionSummaryDTO{OpenCount: r.Summary.OpenCount, Cases: cases}
	return out
}

type releaseOutcomeDTO struct {
	PathId     string `json:"pathId"`
	WorkUnitId string `json:"workUnitId"`
	CPT        string `json:"cpt"`
	Reference  string `json:"reference"`
	ExecutedAt string `json:"executedAt"`
}

// postReleaseProposal handles POST /act/release-next-work/proposals.
func (h *Handlers) postReleaseProposal(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.ReleaseWork == nil {
		writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"error": actNotConfigured})
		return
	}
	var req releaseProposeRequest
	if !decodeActBody(w, r, &req) {
		return
	}
	p, err := h.ReleaseWork.Propose(r.Context(), req.BuildingId, req.ShiftId, req.PathId)
	if err != nil {
		writeActError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, releaseProposalDTO{
		Token:            p.Token,
		ExpiresAt:        p.ExpiresAt.UTC().Format(time.RFC3339),
		Action:           string(approval.ActionReleaseNextWork),
		PathId:           req.PathId,
		Source:           string(p.Decision.Source),
		Recommendation:   toFlowBalanceExceptionDTO(p.Decision),
		ExceptionContext: toExceptionContextDTO(p.ExceptionContext),
	})
}

// postReleaseApproval handles POST /act/release-next-work/approvals.
func (h *Handlers) postReleaseApproval(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.ReleaseWork == nil {
		writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"error": actNotConfigured})
		return
	}
	var req releaseApproveRequest
	if !decodeActBody(w, r, &req) {
		return
	}
	out, err := h.ReleaseWork.Approve(r.Context(), req.Token)
	if err != nil {
		writeActError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, releaseOutcomeDTO{
		PathId:     out.PathId,
		WorkUnitId: out.Released.WorkUnitId,
		CPT:        out.Released.CPT,
		Reference:  out.Released.Ref,
		ExecutedAt: out.ExecutedAt.Format(time.RFC3339),
	})
}

// decodeActBody enforces the transport protections described above and
// decodes the JSON body into dst. It writes the error response itself and
// reports whether the handler should continue.
func decodeActBody(w nethttp.ResponseWriter, r *nethttp.Request, dst any) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeJSON(w, nethttp.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	dec := json.NewDecoder(nethttp.MaxBytesReader(w, r.Body, maxActBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *nethttp.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, nethttp.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return false
		}
		writeJSON(w, nethttp.StatusBadRequest, map[string]string{"error": "invalid JSON body: " + err.Error()})
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeJSON(w, nethttp.StatusBadRequest, map[string]string{"error": "invalid JSON body: trailing data"})
		return false
	}
	return true
}

// writeActError maps the act slice's typed outcomes onto statuses. Anything
// unrecognised is an upstream degradation (502), never blamed on the caller.
func writeActError(w nethttp.ResponseWriter, err error) {
	status := nethttp.StatusBadGateway
	switch {
	case errors.Is(err, usecases.ErrInvalidInput), errors.Is(err, approval.ErrInvalidToken), errors.Is(err, approval.ErrUnsupportedAction):
		status = nethttp.StatusBadRequest
	case errors.Is(err, approval.ErrExpired):
		status = nethttp.StatusGone
	case errors.Is(err, usecases.ErrNotRecommended),
		errors.Is(err, usecases.ErrIncompleteEvidence),
		errors.Is(err, usecases.ErrTokenAlreadyUsed),
		errors.Is(err, ports.ErrNoPendingWork):
		status = nethttp.StatusConflict
	case errors.Is(err, ports.ErrUnknownWorkPlanningPath):
		status = nethttp.StatusNotFound
	case errors.Is(err, ports.ErrWorkPlanningUnreachable):
		status = nethttp.StatusBadGateway
	}
	msg := err.Error()
	if status == nethttp.StatusBadGateway {
		msg = strings.TrimSpace(msg)
	}
	writeJSON(w, status, map[string]string{"error": msg})
}
