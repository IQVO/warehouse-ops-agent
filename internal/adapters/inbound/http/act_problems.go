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
// status-derived RFC 9457 type URIs of problem.go unchanged. The replay class
// (FINAL, OUTCOME, RETRYABLE; ADR 0033) is declared on the entry, and the
// error writer marks the middleware's capture with it.

// actProblem is one problem type: the sentinel error that triggers it, the
// status it answers, its catalogue slug, its replay class, and an optional
// fixed detail sentence (the peer's prose never reaches the body; problem.go).
type actProblem struct {
	slug   string
	status int
	class  replayClass
	match  error  // matched with errors.Is
	detail string // fixed sentence when set; otherwise callerDetail(err)
}

// actProblemCatalogue, in first-match order. A 502 blames the upstream by
// name (upstreamDetail), exactly as the switch did.
//
// Classes (ADR 0033 holds the reasons):
//   - the approvals consume the confirmation token's nonce BEFORE delegating
//     (usecases/release_work.go Approve: consume then releaser.ReleaseNextWork;
//     usecases/labor_reallocation.go Approve: consume then assigner.AssignLabor),
//     so every peer refusal that follows the delegation is an OUTCOME: the
//     token is spent (state committed in-process) and a re-execution under a
//     new key could only answer token-already-used;
//   - the proposals write nothing and their refusals read live peer state, so
//     they are RETRYABLE;
//   - the token pre-consume refusals are functions of the request (or of
//     one-way state) and are FINAL.
var actProblemCatalogue = []actProblem{
	{slug: "invalid-input", status: nethttp.StatusBadRequest, class: classFinal, match: usecases.ErrInvalidInput},
	{slug: "invalid-token", status: nethttp.StatusBadRequest, class: classFinal, match: approval.ErrInvalidToken},
	{slug: "unsupported-action", status: nethttp.StatusBadRequest, class: classFinal, match: approval.ErrUnsupportedAction},
	{slug: "proposal-mismatch", status: nethttp.StatusBadRequest, class: classFinal, match: usecases.ErrProposalMismatch},
	{slug: "assignment-invalid", status: nethttp.StatusBadRequest, class: classOutcome, match: ports.ErrAssignmentInvalid},
	{slug: "token-expired", status: nethttp.StatusGone, class: classFinal, match: approval.ErrExpired},
	{slug: "not-recommended", status: nethttp.StatusConflict, class: classRetryable, match: usecases.ErrNotRecommended},
	{slug: "incomplete-evidence", status: nethttp.StatusConflict, class: classRetryable, match: usecases.ErrIncompleteEvidence},
	{slug: "token-already-used", status: nethttp.StatusConflict, class: classFinal, match: usecases.ErrTokenAlreadyUsed},
	{slug: "no-pending-work", status: nethttp.StatusConflict, class: classOutcome, match: ports.ErrNoPendingWork},
	{slug: "not-understaffed", status: nethttp.StatusConflict, class: classRetryable, match: usecases.ErrNotUnderstaffed},
	{slug: "no-reallocation-move", status: nethttp.StatusConflict, class: classRetryable, match: usecases.ErrNoReallocationMove},
	{slug: "assignment-rejected", status: nethttp.StatusConflict, class: classOutcome, match: ports.ErrAssignmentRejected,
		detail: "workforce-management rejected the move on a domain rule"},
	{slug: "unknown-work-planning-path", status: nethttp.StatusNotFound, class: classOutcome, match: ports.ErrUnknownWorkPlanningPath},
	{slug: "assignment-not-found", status: nethttp.StatusNotFound, class: classOutcome, match: ports.ErrAssignmentNotFound},
	{slug: "work-planning-unreachable", status: nethttp.StatusBadGateway, class: classRetryable, match: ports.ErrWorkPlanningUnreachable},
	{slug: "workforce-agent-unreachable", status: nethttp.StatusBadGateway, class: classRetryable, match: ports.ErrWorkforceAgentUnreachable},
}

// standaloneProblems are the problem types answered outside the catalogue
// loop: the Idempotency-Key middleware's own answers (written before the
// store is touched, so never stored; FINAL records that each is a function of
// the request alone), the act body decoder's answers, and the catalogue's 502
// fallback for an unrecognised error. A 5xx is never stored, so its class is
// RETRYABLE.
var standaloneProblems = []actProblem{
	{slug: "idempotency-key-required", status: nethttp.StatusBadRequest, class: classFinal},
	{slug: "idempotency-key-invalid", status: nethttp.StatusBadRequest, class: classFinal},
	{slug: "idempotency-key-reused", status: nethttp.StatusConflict, class: classFinal},
	{slug: "unsupported-media-type", status: nethttp.StatusUnsupportedMediaType, class: classFinal},
	{slug: "malformed-body", status: nethttp.StatusBadRequest, class: classFinal},
	{slug: "request-body-too-large", status: nethttp.StatusRequestEntityTooLarge, class: classFinal},
	{slug: "idempotency-store-not-configured", status: nethttp.StatusServiceUnavailable, class: classRetryable},
	{slug: "idempotency-key-wait-cancelled", status: nethttp.StatusServiceUnavailable, class: classRetryable},
	// The handlers' own 503 while a slice is not configured (inside the
	// capture; a 5xx, so never stored).
	{slug: "slice-not-configured", status: nethttp.StatusServiceUnavailable, class: classRetryable},
	{slug: "upstream-unavailable", status: nethttp.StatusBadGateway, class: classRetryable},
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
		pt := problemType{Slug: p.slug, Status: p.status, Class: string(p.class)}
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
// workforce-management). The catalogue decides the status and the replay
// class; the class marks the middleware's capture (ADR 0033); a 502 keeps
// naming the upstream.
func writeActErrorFor(w nethttp.ResponseWriter, r *nethttp.Request, err error, upstream string) {
	for _, e := range actProblemCatalogue {
		if !errors.Is(err, e.match) {
			continue
		}
		markReplayClass(w, e.class)
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
	markReplayClass(w, classRetryable)
	writeProblem(w, r, nethttp.StatusBadGateway, upstreamDetail(upstream, err))
}

// writeFinalProblem answers a problem whose class is FINAL — a function of
// the request alone, the act body decoder's failures — and marks the capture
// so the middleware stores the answer (ADR 0033).
func writeFinalProblem(w nethttp.ResponseWriter, r *nethttp.Request, status int, detail string) {
	markReplayClass(w, classFinal)
	writeProblem(w, r, status, detail)
}
