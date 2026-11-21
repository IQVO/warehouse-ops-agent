package http

import (
	"errors"
	nethttp "net/http"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Test-only exports for the replay-class tests (package http_test): the
// verbatim harness template (warehouse-harness-template
// templates/idempotency/replay_classes_test.go.tmpl) drives the production
// middleware, catalogue and error writer through these shims only — the same
// shape as checkout's export_test.go (#65).

// RequireIdempotencyKey exposes the route-scoped Idempotency-Key middleware
// constructor to the external tests.
func RequireIdempotencyKey(store ports.IdempotencyStore) func(nethttp.Handler) nethttp.Handler {
	return requireIdempotencyKey(store)
}

// ReplayEntry is one problem type as the replay-class tests read it.
type ReplayEntry struct {
	Slug   string
	Status int
	Class  string // the replay class the catalogue entry declares; "" when none
}

// ReplayCatalogue lists every problem type the keyed routes can answer, from
// the production catalogue (NOT a copy of it).
func ReplayCatalogue() []ReplayEntry {
	out := make([]ReplayEntry, 0, len(actProblemCatalogue)+len(standaloneProblems))
	for _, p := range allProblemTypes() {
		out = append(out, ReplayEntry(p))
	}
	return out
}

// RefuseProblem answers the problem type slug through the production error
// writer, so the class the catalogue declares is what marks the middleware's
// capture. Do NOT write the response by hand in the tests; call this.
func RefuseProblem(w nethttp.ResponseWriter, r *nethttp.Request, slug string) {
	for _, e := range actProblemCatalogue {
		if e.slug == slug {
			writeActErrorFor(w, r, e.match, "wes-work-planning")
			return
		}
	}
	switch slug {
	case "idempotency-key-required":
		markReplayClass(w, classFinal)
		writeKeyRequired(w, r)
	case "idempotency-key-invalid":
		markReplayClass(w, classFinal)
		writeKeyInvalid(w, r)
	case "idempotency-key-reused":
		markReplayClass(w, classFinal)
		writeKeyReused(w, r)
	case "unsupported-media-type":
		writeFinalProblem(w, r, nethttp.StatusUnsupportedMediaType, "Content-Type must be application/json")
	case "malformed-body":
		writeFinalProblem(w, r, nethttp.StatusBadRequest, "invalid JSON body")
	case "request-body-too-large":
		writeFinalProblem(w, r, nethttp.StatusRequestEntityTooLarge, "request body too large")
	case "idempotency-store-not-configured":
		markReplayClass(w, classRetryable)
		writeProblem(w, r, nethttp.StatusServiceUnavailable, idempotencyNotConfigured)
	case "idempotency-key-wait-cancelled":
		markReplayClass(w, classRetryable)
		writeProblem(w, r, nethttp.StatusServiceUnavailable, "request cancelled while an earlier request with the same Idempotency-Key was running")
	case "slice-not-configured":
		markReplayClass(w, classRetryable)
		writeProblem(w, r, nethttp.StatusServiceUnavailable, actNotConfigured)
	case "upstream-unavailable":
		writeActErrorFor(w, r, errors.New("test: peer answered an unrecognised problem"), "wes-work-planning")
	default:
		panic("unknown problem type " + slug)
	}
}
