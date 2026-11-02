package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/logsanitize"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The labor-reallocation act slice (ADR 0029, act slice #2): the second and
// last human-gated write this agent performs.
//
//	Propose  -> runs the read-only outlook; for each ranked move mints its own
//	            confirmation token bound to that exact move (associate, from,
//	            to). Writes nothing.
//	Approve  -> verifies the token, checks it is the proposal named in the URL,
//	            consumes it (at most once) and delegates ONE move to
//	            workforce-management through the LaborAssigner port (A2A
//	            assign_labor), stating who approved and which proposal.
//
// Like ReleaseWork, this use case is wired into the REST adapter only. It is
// never handed to the MCP server or the LLM reasoner (statically asserted in
// internal/architecture/zerowrite). The MCP tool labor_reallocation_outlook
// holds only the token-less LaborReallocationOutlook.

// LaborReallocationAgentId is the agentId this agent states on every
// delegated assignment (fleet contract G4); it is not caller-supplied.
const LaborReallocationAgentId = "warehouse-ops-agent"

// ErrProposalMismatch: the token is genuine but belongs to another proposal
// than the one named in the approve URL.
var ErrProposalMismatch = errors.New("confirmation token does not belong to this proposal")

// ReallocationProposal is one ranked move with its own token. The token is
// the human's capability to approve exactly this move.
type ReallocationProposal struct {
	ProposalId string
	Token      string
	ExpiresAt  time.Time
	Move       policy.ReallocationMove
}

// ReallocationProposals is what Propose returns.
type ReallocationProposals struct {
	SiteCode  string
	ShiftId   string
	Target    policy.PathStaffing
	Proposals []ReallocationProposal
	Skipped   []policy.SkippedCandidate
}

// ReallocationOutcome is what Approve returns on success.
type ReallocationOutcome struct {
	ProposalId   string
	AssociateId  string
	PathId       string
	AssignmentId string
	ApprovedBy   string
	ExecutedAt   time.Time
}

// LaborReallocation implements the propose -> approve -> execute flow.
type LaborReallocation struct {
	outlook  ReallocationOutlookReader
	signer   *approval.Signer
	assigner ports.LaborAssigner
	logger   *slog.Logger
	now      func() time.Time

	mu   sync.Mutex
	used map[string]time.Time // nonce -> token expiry (kept until then)
}

// NewLaborReallocation builds the use case. Every collaborator is required: a
// half-wired act slice must fail at construction, never at the first write.
func NewLaborReallocation(outlook ReallocationOutlookReader, signer *approval.Signer, assigner ports.LaborAssigner, logger *slog.Logger, now func() time.Time) (*LaborReallocation, error) {
	if outlook == nil || signer == nil || assigner == nil {
		return nil, errors.New("labor reallocation: outlook, signer and assigner are all required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &LaborReallocation{outlook: outlook, signer: signer, assigner: assigner, logger: logger, now: now, used: map[string]time.Time{}}, nil
}

// Propose ranks the moves and mints one token per move. It refuses (nothing
// for a human to confirm) when the target is not understaffed or no offered
// associate can move.
func (uc *LaborReallocation) Propose(ctx context.Context, in LaborReallocationInput) (ReallocationProposals, error) {
	o, err := uc.outlook.Execute(ctx, in)
	if err != nil {
		return ReallocationProposals{}, err
	}
	if len(o.Result.Moves) == 0 {
		if !o.Target.Understaffed || o.Target.ActiveHeads >= o.Target.PlannedHeads {
			return ReallocationProposals{}, fmt.Errorf("%w: workforce-management reports %q planned %d, active %d, understaffed=%t", ErrNotUnderstaffed, in.ToPathId, o.Target.PlannedHeads, o.Target.ActiveHeads, o.Target.Understaffed)
		}
		return ReallocationProposals{}, fmt.Errorf("%w: %d candidate(s) screened out", ErrNoReallocationMove, len(o.Result.Skipped))
	}
	out := ReallocationProposals{SiteCode: o.SiteCode, ShiftId: o.ShiftId, Target: o.Target, Skipped: o.Result.Skipped, Proposals: make([]ReallocationProposal, 0, len(o.Result.Moves))}
	for _, m := range o.Result.Moves {
		token, r, err := uc.signer.MintReallocation(m.AssociateId, m.FromPathId, m.ToPathId)
		if err != nil {
			return ReallocationProposals{}, err
		}
		uc.logger.InfoContext(ctx, "act: labor reallocation proposed", "proposalId", r.ProposalId(), "associateId", logsanitize.Sanitize(m.AssociateId), "fromPathId", logsanitize.Sanitize(m.FromPathId), "toPathId", logsanitize.Sanitize(m.ToPathId), "rank", m.Rank, "expiresAt", r.ExpiresAt)
		out.Proposals = append(out.Proposals, ReallocationProposal{ProposalId: r.ProposalId(), Token: token, ExpiresAt: r.ExpiresAt, Move: m})
	}
	return out, nil
}

// Approve redeems a confirmation token and executes ONE move. The token is
// consumed BEFORE the delegation: at-most-once beats at-least-once for a
// non-idempotent write, and an ambiguous failure (agent unreachable) is
// re-proposed by a human, never silently retried. A token that does not
// verify, or that names another proposal, never reaches the peer.
func (uc *LaborReallocation) Approve(ctx context.Context, proposalId, token, approvedBy string) (ReallocationOutcome, error) {
	if err := validateReallocationID("approvedBy", approvedBy, true); err != nil {
		return ReallocationOutcome{}, err
	}
	r, err := uc.signer.VerifyReallocation(token)
	if err != nil {
		return ReallocationOutcome{}, err
	}
	if r.ProposalId() != proposalId {
		return ReallocationOutcome{}, ErrProposalMismatch
	}
	if !uc.consume(r) {
		uc.logger.WarnContext(ctx, "act: labor reallocation token replay refused", "proposalId", r.ProposalId())
		return ReallocationOutcome{}, ErrTokenAlreadyUsed
	}
	assigned, err := uc.assigner.AssignLabor(ctx, ports.AssignLaborRequest{
		AssociateId: r.AssociateId,
		PathId:      r.ToPathId,
		ProposalId:  r.ProposalId(),
		ApprovedBy:  approvedBy,
		AgentId:     LaborReallocationAgentId,
	})
	if err != nil {
		uc.logger.WarnContext(ctx, "act: assign_labor delegation failed", "proposalId", r.ProposalId(), "associateId", logsanitize.Sanitize(r.AssociateId), "approvedBy", logsanitize.Sanitize(approvedBy), "error", err)
		return ReallocationOutcome{}, err
	}
	uc.logger.InfoContext(ctx, "act: labor reallocation executed", "proposalId", r.ProposalId(), "associateId", logsanitize.Sanitize(assigned.AssociateId), "pathId", logsanitize.Sanitize(assigned.PathId), "approvedBy", logsanitize.Sanitize(approvedBy))
	return ReallocationOutcome{
		ProposalId:   r.ProposalId(),
		AssociateId:  assigned.AssociateId,
		PathId:       assigned.PathId,
		AssignmentId: assigned.AssignmentId,
		ApprovedBy:   approvedBy,
		ExecutedAt:   uc.now().UTC(),
	}, nil
}

// consume marks the proposal's nonce used and reports whether this call won.
// Entries are pruned once their token could no longer verify anyway, so the
// set is bounded by the (short) token lifetime. The set is process-local:
// the agent runs one replica (ADR 0027).
func (uc *LaborReallocation) consume(r approval.Reallocation) bool {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	now := uc.now()
	for n, exp := range uc.used {
		if !now.Before(exp) {
			delete(uc.used, n)
		}
	}
	if _, seen := uc.used[r.Nonce]; seen {
		return false
	}
	uc.used[r.Nonce] = r.ExpiresAt
	return true
}
