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
		writeKeyRequired(w, r)
	case "idempotency-key-invalid":
		writeKeyInvalid(w, r)
	case "idempotency-key-reused":
		writeKeyReused(w, r)
	case "unsupported-media-type":
		writeProblem(w, r, nethttp.StatusUnsupportedMediaType, "Content-Type must be application/json")
	case "malformed-body":
		writeProblem(w, r, nethttp.StatusBadRequest, "invalid JSON body")
	case "request-body-too-large":
		writeProblem(w, r, nethttp.StatusRequestEntityTooLarge, "request body too large")
	case "idempotency-store-not-configured":
		writeProblem(w, r, nethttp.StatusServiceUnavailable, idempotencyNotConfigured)
	case "idempotency-key-wait-cancelled":
		writeProblem(w, r, nethttp.StatusServiceUnavailable, "request cancelled while an earlier request with the same Idempotency-Key was running")
	case "upstream-unavailable":
		writeActErrorFor(w, r, errors.New("test: peer answered an unrecognised problem"), "wes-work-planning")
	default:
		panic("unknown problem type " + slug)
	}
}
