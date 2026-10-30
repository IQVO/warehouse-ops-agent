package ports

import (
	"context"
	"errors"
)

// ExceptionContext is the READ-ONLY port behind the ADR-0028 cross-tier
// choreography: when the agent proposes a release it also asks the
// exception hub "what is open that a human should know before approving?".
// The answer is advisory text for the human; it never gates or alters the
// proposal (the use case guarantees that, and the zero-write fitness tests
// pin the port to a single read verb).
//
// The only implementation is the second client in
// internal/adapters/outbound/a2a, which delegates the QUESTION to the
// exception-management A2A agent (skill find_exceptions, read-only). The
// answer is returned to the caller and not persisted: no data sync.
type ExceptionContext interface {
	// OpenExceptions returns the count of unattended (status OPEN) cases
	// and a bounded list of the most relevant ones.
	OpenExceptions(ctx context.Context, query ExceptionContextQuery) (ExceptionSummary, error)
}

// Bounds of the case list returned in a summary.
const (
	// DefaultExceptionCases is the list size used when the query leaves
	// Limit unset.
	DefaultExceptionCases = 5
	// MaxExceptionCases is the hard cap an adapter must apply to Limit.
	MaxExceptionCases = 10
)

// ExceptionContextQuery narrows the lookup. Exception cases carry no path
// id, so the question is warehouse-wide by construction (ADR 0028).
type ExceptionContextQuery struct {
	// Limit bounds ExceptionSummary.Cases; <= 0 means DefaultExceptionCases
	// and anything above MaxExceptionCases is clamped.
	Limit int
}

// ExceptionBrief is one case reduced to what a human needs at a glance.
type ExceptionBrief struct {
	CaseID    string
	Status    string
	Queue     string
	Severity  string
	Escalated bool
	// Reason is the short name of the fact that opened the case (the last
	// segment of its CloudEvents type), never the frozen payload.
	Reason string
}

// ExceptionSummary is the typed answer: OpenCount is the total number of
// OPEN-status cases at the peer, Cases the bounded top of them (escalated
// first, then newest opened first).
type ExceptionSummary struct {
	OpenCount int
	Cases     []ExceptionBrief
}

// ErrExceptionContextUnavailable: the peer could not be reached, timed out,
// answered a problem or answered off-contract. Callers degrade to an
// "unavailable" status; it is never a reason to refuse a proposal.
var ErrExceptionContextUnavailable = errors.New("ports: exception-management agent unavailable")
