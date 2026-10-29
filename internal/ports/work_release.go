package ports

import (
	"context"
	"errors"
)

// WorkReleaser is the ONE write port this agent holds (ADR 0027, the "act
// slice"): ask wes-work-planning to admit the next work unit for a path.
// It is a goal-shaped delegation, not a data write: planning keeps every
// release decision and invariant, this agent only asks. The sole
// implementation is the A2A client in internal/adapters/outbound/a2a, and
// the zero-write arch test pins that nothing else gains a write method.
//
// Only the human-gated ReleaseWork use case may hold this port. It is never
// reachable from this agent's MCP server or from the LLM reasoner (both
// asserted statically in internal/architecture/zerowrite).
type WorkReleaser interface {
	ReleaseNextWork(ctx context.Context, pathId string) (ReleasedWork, error)
}

// Typed outcomes of a release delegation. The adapter wraps the peer's
// answer onto these so the application layer never sees A2A.
var (
	// ErrNoPendingWork: planning has nothing to release for the path (a
	// legitimate business outcome, not a fault).
	ErrNoPendingWork = errors.New("ports: no pending work to release")
	// ErrUnknownWorkPlanningPath: planning does not know the path.
	ErrUnknownWorkPlanningPath = errors.New("ports: work-planning does not know the path")
	// ErrWorkPlanningUnreachable: the agent could not be reached or answered
	// off-contract; the outcome of the release is therefore unknown.
	ErrWorkPlanningUnreachable = errors.New("ports: work-planning agent unreachable")
)
