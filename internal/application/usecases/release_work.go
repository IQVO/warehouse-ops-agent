package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The act slice (ADR 0027): the ONLY write this agent can perform, behind an
// explicit two-call human confirmation.
//
//	Propose  -> runs the existing flow-balance policy; mints a confirmation
//	            token ONLY when the policy itself recommends release_next_work
//	            on complete evidence. Writes nothing.
//	Approve  -> verifies the token, consumes it (at most once), and delegates
//	            "admit the next unit" to wes-work-planning through the
//	            WorkReleaser port (A2A). Planning's own domain layer remains
//	            the backstop, exactly as for a human calling its tool.
//
// This use case is wired into the REST adapter only. It is never handed to
// the MCP server or the LLM reasoner — an MCP caller is typically a model,
// and a model approving its own recommendation defeats the point of the gate
// (statically asserted in internal/architecture/zerowrite).

// Sentinels the REST adapter maps onto statuses.
var (
	// ErrNotRecommended: the policy does not recommend release_next_work for
	// the path right now, so there is nothing for a human to confirm.
	ErrNotRecommended = errors.New("release_next_work is not the recommended action")
	// ErrIncompleteEvidence: the recommendation rests on partial signals; a
	// human must not be asked to confirm a guess.
	ErrIncompleteEvidence = errors.New("recommendation rests on incomplete evidence")
	// ErrTokenAlreadyUsed: the confirmation token was already redeemed.
	ErrTokenAlreadyUsed = errors.New("confirmation token already used")
)

// FlowBalanceDecider is the slice of FlowBalanceAdvisory this use case needs.
type FlowBalanceDecider interface {
	Execute(ctx context.Context, buildingId, shiftId, pathId string) (policy.Decision, error)
}

// ReleaseProposal is what Propose returns: the token plus everything the
// human is being asked to confirm.
type ReleaseProposal struct {
	Token     string
	ExpiresAt time.Time
	Decision  policy.Decision
}

// ReleaseOutcome is what Approve returns on success.
type ReleaseOutcome struct {
	Released   ports.ReleasedWork
	PathId     string
	ExecutedAt time.Time
}

// ReleaseWork implements the propose -> approve -> execute flow.
type ReleaseWork struct {
	advisory FlowBalanceDecider
	signer   *approval.Signer
	releaser ports.WorkReleaser
	logger   *slog.Logger
	now      func() time.Time

	mu   sync.Mutex
	used map[string]time.Time // nonce -> token expiry (kept until then)
}

// NewReleaseWork builds the use case. Every collaborator is required: a
// half-wired act slice must fail at construction, never at the first write.
func NewReleaseWork(advisory FlowBalanceDecider, signer *approval.Signer, releaser ports.WorkReleaser, logger *slog.Logger, now func() time.Time) (*ReleaseWork, error) {
	if advisory == nil || signer == nil || releaser == nil {
		return nil, errors.New("release work: advisory, signer and releaser are all required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &ReleaseWork{advisory: advisory, signer: signer, releaser: releaser, logger: logger, now: now, used: map[string]time.Time{}}, nil
}

// Propose evaluates the path and, only if the policy recommends
// release_next_work on complete evidence, mints the confirmation token.
func (uc *ReleaseWork) Propose(ctx context.Context, buildingId, shiftId, pathId string) (ReleaseProposal, error) {
	d, err := uc.advisory.Execute(ctx, buildingId, shiftId, pathId)
	if err != nil {
		return ReleaseProposal{}, err // ErrInvalidInput or an upstream failure, as the read route reports
	}
	if d.RecommendedAction != policy.ActionReleaseNextWork {
		return ReleaseProposal{Decision: d}, fmt.Errorf("%w: current recommendation for %q is %q", ErrNotRecommended, pathId, d.RecommendedAction)
	}
	if d.Partial {
		return ReleaseProposal{Decision: d}, fmt.Errorf("%w: missing %s", ErrIncompleteEvidence, strings.Join(d.MissingSignals, ", "))
	}
	token, p, err := uc.signer.Mint(approval.ActionReleaseNextWork, pathId, string(d.Source), d.Rationale)
	if err != nil {
		return ReleaseProposal{}, err
	}
	uc.logger.InfoContext(ctx, "act: release_next_work proposed", "pathId", pathId, "source", d.Source, "nonce", p.Nonce, "expiresAt", p.ExpiresAt)
	return ReleaseProposal{Token: token, ExpiresAt: p.ExpiresAt, Decision: d}, nil
}

// Approve redeems a confirmation token and executes the release. The token is
// consumed BEFORE the delegation: at-most-once beats at-least-once for a
// non-idempotent write, and an ambiguous failure (agent unreachable) must be
// re-proposed by a human rather than silently retried.
func (uc *ReleaseWork) Approve(ctx context.Context, token string) (ReleaseOutcome, error) {
	p, err := uc.signer.Verify(token)
	if err != nil {
		return ReleaseOutcome{}, err
	}
	if !uc.consume(p) {
		uc.logger.WarnContext(ctx, "act: confirmation token replay refused", "pathId", p.PathId, "nonce", p.Nonce)
		return ReleaseOutcome{}, ErrTokenAlreadyUsed
	}
	released, err := uc.releaser.ReleaseNextWork(ctx, p.PathId)
	if err != nil {
		uc.logger.WarnContext(ctx, "act: release_next_work delegation failed", "pathId", p.PathId, "nonce", p.Nonce, "error", err)
		return ReleaseOutcome{}, err
	}
	out := ReleaseOutcome{Released: released, PathId: p.PathId, ExecutedAt: uc.now().UTC()}
	uc.logger.InfoContext(ctx, "act: release_next_work executed", "pathId", p.PathId, "workUnitId", released.WorkUnitId, "source", p.Source, "nonce", p.Nonce)
	return out, nil
}

// consume marks the proposal's nonce used and reports whether this call won.
// Entries are pruned once their token could no longer verify anyway, so the
// set is bounded by the (short) token lifetime. The set is process-local:
// the agent runs one replica (see the chart note in ADR 0027).
func (uc *ReleaseWork) consume(p approval.Proposal) bool {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	now := uc.now()
	for n, exp := range uc.used {
		if !now.Before(exp) {
			delete(uc.used, n)
		}
	}
	if _, seen := uc.used[p.Nonce]; seen {
		return false
	}
	uc.used[p.Nonce] = p.ExpiresAt
	return true
}
