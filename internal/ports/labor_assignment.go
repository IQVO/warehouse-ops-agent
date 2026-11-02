package ports

import (
	"context"
	"errors"
)

// StaffingGapBySiteClient is the READ port behind the labor-reallocation
// proposals (ADR 0029): workforce-management's get_staffing_gap addressed by
// its canonical siteCode (the deprecated buildingId alias is an unscoped
// lookup across every site, which would let another site's heads hide a
// gap). It is separate from WorkforceManagementClient so the older
// buildingId readers keep their contract unchanged.
type StaffingGapBySiteClient interface {
	GetStaffingGapForSite(ctx context.Context, siteCode, shiftId, pathId string) (StaffingGap, error)
}

// AssignLaborRequest is one move delegated to workforce-management. The
// attribution fields are the optional arguments workforce-management's
// assign_labor skill accepts so its LaborAssigned event can say who asked
// and who approved (fleet contract G4); this agent always sets all three.
type AssignLaborRequest struct {
	AssociateId string
	PathId      string
	ProposalId  string
	ApprovedBy  string
	AgentId     string
}

// LaborAssigner is the second write port this agent holds (ADR 0029, act
// slice #2): ask workforce-management to place ONE associate on a path.
// Like WorkReleaser it is a goal-shaped delegation, not a data write:
// workforce-management keeps every invariant (one active assignment per
// associate, certification match, break and shift rules). The sole
// implementation is the A2A client in internal/adapters/outbound/a2a, and
// the zero-write arch test pins that nothing else declares AssignLabor.
//
// Only the human-gated LaborReallocation use case may hold this port. It is
// never reachable from this agent's MCP server or from the LLM reasoner
// (asserted statically in internal/architecture/zerowrite).
type LaborAssigner interface {
	AssignLabor(ctx context.Context, req AssignLaborRequest) (LaborAssignment, error)
}

// Typed outcomes of an assign_labor delegation. The adapter wraps the
// peer's answer onto these so the application layer never sees A2A.
var (
	// ErrAssignmentRejected: workforce-management refused the move on a
	// domain rule (a 409 problem: certification, break, shift ended, ...).
	ErrAssignmentRejected = errors.New("ports: workforce-management rejected the assignment")
	// ErrAssignmentNotFound: workforce-management does not know the
	// associate or the path (a 404 problem).
	ErrAssignmentNotFound = errors.New("ports: workforce-management does not know the associate or path")
	// ErrAssignmentInvalid: workforce-management refused the arguments (a
	// 400 problem, e.g. a path outside its catalogue).
	ErrAssignmentInvalid = errors.New("ports: workforce-management refused the assignment arguments")
	// ErrWorkforceAgentUnreachable: the agent could not be reached or
	// answered off-contract; whether the assignment happened is unknown.
	ErrWorkforceAgentUnreachable = errors.New("ports: workforce-management agent unreachable")
)
