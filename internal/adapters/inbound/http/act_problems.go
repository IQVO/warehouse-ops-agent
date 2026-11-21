package http

import (
	"errors"
	nethttp "net/http"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The act slice's problem catalogue: every problem type its four keyed POST
// routes can answer, the status switch of writeActErrorFor reshaped into one
// literal per problem type (first match wins, the order the switch had).
// Each entry identifies the problem by a slug for the catalogue tests and the
// ADR class table; the slug is NOT a wire value — the wire keeps the
// status-derived RFC 9457 type URIs of problem.go unchanged.

// actProblem is one problem type: the sentinel error that triggers it, the
// status it answers, its catalogue slug, and an optional fixed detail sentence
// (the peer's prose never reaches the body; problem.go).
type actProblem struct {
	slug   string
	status int
	match  error  // matched with errors.Is
	detail string // fixed sentence when set; otherwise callerDetail(err)
}

// actProblemCatalogue, in first-match order. A 502 blames the upstream by
// name (upstreamDetail), exactly as the switch did.
var actProblemCatalogue = []actProblem{
	{slug: "invalid-input", status: nethttp.StatusBadRequest, match: usecases.ErrInvalidInput},
	{slug: "invalid-token", status: nethttp.StatusBadRequest, match: approval.ErrInvalidToken},
	{slug: "unsupported-action", status: nethttp.StatusBadRequest, match: approval.ErrUnsupportedAction},
	{slug: "proposal-mismatch", status: nethttp.StatusBadRequest, match: usecases.ErrProposalMismatch},
	{slug: "assignment-invalid", status: nethttp.StatusBadRequest, match: ports.ErrAssignmentInvalid},
	{slug: "token-expired", status: nethttp.StatusGone, match: approval.ErrExpired},
	{slug: "not-recommended", status: nethttp.StatusConflict, match: usecases.ErrNotRecommended},
	{slug: "incomplete-evidence", status: nethttp.StatusConflict, match: usecases.ErrIncompleteEvidence},
	{slug: "token-already-used", status: nethttp.StatusConflict, match: usecases.ErrTokenAlreadyUsed},
	{slug: "no-pending-work", status: nethttp.StatusConflict, match: ports.ErrNoPendingWork},
	{slug: "not-understaffed", status: nethttp.StatusConflict, match: usecases.ErrNotUnderstaffed},
	{slug: "no-reallocation-move", status: nethttp.StatusConflict, match: usecases.ErrNoReallocationMove},
	{slug: "assignment-rejected", status: nethttp.StatusConflict, match: ports.ErrAssignmentRejected,
		detail: "workforce-management rejected the move on a domain rule"},
	{slug: "unknown-work-planning-path", status: nethttp.StatusNotFound, match: ports.ErrUnknownWorkPlanningPath},
	{slug: "assignment-not-found", status: nethttp.StatusNotFound, match: ports.ErrAssignmentNotFound},
	{slug: "work-planning-unreachable", status: nethttp.StatusBadGateway, match: ports.ErrWorkPlanningUnreachable},
	{slug: "workforce-agent-unreachable", status: nethttp.StatusBadGateway, match: ports.ErrWorkforceAgentUnreachable},
}

// standaloneProblems are the problem types answered outside the catalogue
// loop: the Idempotency-Key middleware's own answers (written before the
// store is touched, so never stored), the act body decoder's answers, and the
// catalogue's 502 fallback for an unrecognised error.
var standaloneProblems = []actProblem{
	{slug: "idempotency-key-required", status: nethttp.StatusBadRequest},
	{slug: "idempotency-key-invalid", status: nethttp.StatusBadRequest},
	{slug: "idempotency-key-reused", status: nethttp.StatusConflict},
	{slug: "unsupported-media-type", status: nethttp.StatusUnsupportedMediaType},
	{slug: "malformed-body", status: nethttp.StatusBadRequest},
	{slug: "request-body-too-large", status: nethttp.StatusRequestEntityTooLarge},
	{slug: "idempotency-store-not-configured", status: nethttp.StatusServiceUnavailable},
	{slug: "idempotency-key-wait-cancelled", status: nethttp.StatusServiceUnavailable},
	{slug: "upstream-unavailable", status: nethttp.StatusBadGateway},
}

// problemType is one problem type of the catalogue as the replay-class tests
// and the ADR table see it.
type problemType struct {
	Slug   string
	Status int
	Class  string // the replay class the entry declares; "" when it declares none
}

// allProblemTypes is the whole catalogue: the sentinel-driven entries, then
// the standalone ones, deduplicated per (slug, status).
func allProblemTypes() []problemType {
	var out []problemType
	seen := map[problemType]bool{}
	add := func(p actProblem) {
		pt := problemType{Slug: p.slug, Status: p.status}
		if !seen[pt] {
			seen[pt] = true
			out = append(out, pt)
		}
	}
	for _, e := range actProblemCatalogue {
		add(e)
	}
	for _, p := range standaloneProblems {
		add(p)
	}
	return out
}

// writeActError maps the act slice's typed outcomes onto statuses. Anything
// unrecognised is an upstream degradation (502), never blamed on the caller.
func writeActError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	writeActErrorFor(w, r, err, "wes-work-planning")
}

// writeActErrorFor is writeActError naming the upstream a 502 blames: each act
// slice delegates to a different peer (ADR 0027 wes-work-planning, ADR 0029
// workforce-management). The catalogue decides the status; a 502 keeps naming
// the upstream.
func writeActErrorFor(w nethttp.ResponseWriter, r *nethttp.Request, err error, upstream string) {
	for _, e := range actProblemCatalogue {
		if !errors.Is(err, e.match) {
			continue
		}
		if e.status == nethttp.StatusBadGateway {
			writeProblem(w, r, e.status, upstreamDetail(upstream, err))
			return
		}
		if e.detail != "" {
			writeProblem(w, r, e.status, e.detail)
			return
		}
		writeProblem(w, r, e.status, callerDetail(err))
		return
	}
	writeProblem(w, r, nethttp.StatusBadGateway, upstreamDetail(upstream, err))
}
