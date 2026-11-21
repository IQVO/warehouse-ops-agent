package http

import "net/http"

// replayClass says what the Idempotency-Key middleware does with the answer
// of a request retried under the same key (ADR 0033; the fleet rule
// .claude/rules/fleet/idempotency-and-outbox.md). It is declared ON the
// catalogue entry of each problem type (act_problems.go), next to its status
// and slug; there is no default in code, no list of slugs and no status range
// beyond 2xx and 5xx in the middleware. A test holds the catalogue fully
// classified (idempotency_replay_classes_test.go).
type replayClass string

const (
	// classFinal: the answer is a function of the request alone (or of state
	// that only moves one way for it). Stored and replayed.
	classFinal replayClass = "FINAL"
	// classOutcome: the command COMMITTED state and then answered an error
	// that must be remembered. Stored and replayed; the commit stands. On the
	// act slice the commit is the confirmation token's used-nonce entry,
	// consumed in-process before the delegation (ADR 0027/0029): there is no
	// transaction to roll back, so "the writes commit with the stored answer"
	// is a fact of this design, not a middleware step.
	classOutcome replayClass = "OUTCOME"
	// classRetryable: nothing committed and the answer depends on state that
	// can change (a live peer decision, staffing state, a peer not ready).
	// NOT stored: the key is released and a retry executes again.
	classRetryable replayClass = "RETRYABLE"
)

// validReplayClass reports whether c is one of the three classes; "" is
// "unclassified".
func validReplayClass(c replayClass) bool {
	return c == classFinal || c == classOutcome || c == classRetryable
}

// replayMarker is implemented by the capturing writer the idempotency
// middleware hands the handler (captureWriter, idempotency.go). The
// production error writer marks it with the class of the problem it answers;
// the middleware reads the mark and stores or releases the key.
type replayMarker interface {
	markReplayClass(replayClass)
}

// markReplayClass marks w when it is the middleware's capture; any other
// writer (a route without the middleware, the keyless deprecated alias, a
// test recorder) has nothing to mark, and a refusal that reaches the
// middleware with no class is released like RETRYABLE as the safe fallback.
func markReplayClass(w http.ResponseWriter, class replayClass) {
	if m, ok := w.(replayMarker); ok {
		m.markReplayClass(class)
	}
}
