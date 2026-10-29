package usecases_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeDecider struct {
	decision policy.Decision
	err      error
	calls    int
}

func (f *fakeDecider) Execute(_ context.Context, _, _, pathId string) (policy.Decision, error) {
	f.calls++
	d := f.decision
	d.PathId = pathId
	return d, f.err
}

type fakeReleaser struct {
	calls atomic.Int32
	path  string
	out   ports.ReleasedWork
	err   error
}

func (f *fakeReleaser) ReleaseNextWork(_ context.Context, pathId string) (ports.ReleasedWork, error) {
	f.calls.Add(1)
	f.path = pathId
	return f.out, f.err
}

var actNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

type actFixture struct {
	uc  *usecases.ReleaseWork
	dec *fakeDecider
	rel *fakeReleaser
	now *time.Time
}

func newActFixture(t *testing.T, d policy.Decision) *actFixture {
	t.Helper()
	now := actNow
	clock := func() time.Time { return now }
	signer, err := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, clock)
	if err != nil {
		t.Fatal(err)
	}
	f := &actFixture{
		dec: &fakeDecider{decision: d},
		rel: &fakeReleaser{out: ports.ReleasedWork{WorkUnitId: "wu-1", CPT: "2026-12-24T09:00:00Z", Ref: "order-1"}},
		now: &now,
	}
	f.uc, err = usecases.NewReleaseWork(f.dec, signer, f.rel, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return f
}

var releaseDecision = policy.Decision{
	RecommendedAction: policy.ActionReleaseNextWork,
	Rationale:         "backlog 12 over wip 3",
	Source:            policy.SourceDeterministic,
}

func TestProposeMintsATokenOnlyWhenThePolicyRecommendsRelease(t *testing.T) {
	f := newActFixture(t, releaseDecision)
	p, err := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if p.Token == "" || !p.ExpiresAt.Equal(actNow.Add(5*time.Minute)) || p.Decision.PathId != "pick-zone-a" {
		t.Fatalf("proposal = %+v", p)
	}
	if f.rel.calls.Load() != 0 {
		t.Fatal("Propose must never call the releaser")
	}
}

func TestProposeRefusesEveryOtherRecommendation(t *testing.T) {
	for _, a := range []policy.RecommendedAction{policy.ActionAssignLabor, policy.FlowBalanceActionHold} {
		f := newActFixture(t, policy.Decision{RecommendedAction: a, Source: policy.SourceDeterministic})
		p, err := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
		if !errors.Is(err, usecases.ErrNotRecommended) {
			t.Fatalf("%s: err = %v, want ErrNotRecommended", a, err)
		}
		if p.Token != "" {
			t.Fatalf("%s: a token was minted for a non-release recommendation", a)
		}
	}
}

func TestProposeRefusesPartialEvidence(t *testing.T) {
	d := releaseDecision
	d.Partial = true
	d.MissingSignals = []string{"workforce-management"}
	f := newActFixture(t, d)
	p, err := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	if !errors.Is(err, usecases.ErrIncompleteEvidence) || p.Token != "" {
		t.Fatalf("err = %v token = %q, want ErrIncompleteEvidence and no token", err, p.Token)
	}
}

func TestProposePassesAdvisoryErrorsThrough(t *testing.T) {
	f := newActFixture(t, releaseDecision)
	f.dec.err = usecases.ErrInvalidInput
	if _, err := f.uc.Propose(context.Background(), "", "", ""); !errors.Is(err, usecases.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestApproveDelegatesExactlyTheProposedPath(t *testing.T) {
	f := newActFixture(t, releaseDecision)
	p, _ := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	out, err := f.uc.Approve(context.Background(), p.Token)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if f.rel.path != "pick-zone-a" || f.rel.calls.Load() != 1 {
		t.Fatalf("releaser path=%q calls=%d", f.rel.path, f.rel.calls.Load())
	}
	if out.Released.WorkUnitId != "wu-1" || out.PathId != "pick-zone-a" || !out.ExecutedAt.Equal(actNow) {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestApproveIsAtMostOncePerToken(t *testing.T) {
	f := newActFixture(t, releaseDecision)
	p, _ := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	if _, err := f.uc.Approve(context.Background(), p.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.Approve(context.Background(), p.Token); !errors.Is(err, usecases.ErrTokenAlreadyUsed) {
		t.Fatalf("replay err = %v, want ErrTokenAlreadyUsed", err)
	}
	if f.rel.calls.Load() != 1 {
		t.Fatalf("releaser called %d times, want exactly 1", f.rel.calls.Load())
	}
}

func TestConcurrentApprovalsOfOneTokenReleaseExactlyOnce(t *testing.T) {
	f := newActFixture(t, releaseDecision)
	p, _ := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	var wg sync.WaitGroup
	var won atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.uc.Approve(context.Background(), p.Token); err == nil {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 || f.rel.calls.Load() != 1 {
		t.Fatalf("winners=%d releaser calls=%d, want 1 and 1", won.Load(), f.rel.calls.Load())
	}
}

func TestApproveRefusesExpiredAndForgedTokensWithoutCallingTheReleaser(t *testing.T) {
	f := newActFixture(t, releaseDecision)
	p, _ := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")

	*f.now = actNow.Add(6 * time.Minute)
	if _, err := f.uc.Approve(context.Background(), p.Token); !errors.Is(err, approval.ErrExpired) {
		t.Fatalf("expired err = %v, want ErrExpired", err)
	}
	if _, err := f.uc.Approve(context.Background(), "garbage"); !errors.Is(err, approval.ErrInvalidToken) {
		t.Fatalf("forged err = %v, want ErrInvalidToken", err)
	}
	if f.rel.calls.Load() != 0 {
		t.Fatal("a refused token must never reach the releaser")
	}
}

func TestApprovePassesTypedDelegationOutcomesThroughAndStillConsumesTheToken(t *testing.T) {
	for _, want := range []error{ports.ErrNoPendingWork, ports.ErrUnknownWorkPlanningPath, ports.ErrWorkPlanningUnreachable} {
		f := newActFixture(t, releaseDecision)
		f.rel.err = want
		p, _ := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
		if _, err := f.uc.Approve(context.Background(), p.Token); !errors.Is(err, want) {
			t.Fatalf("err = %v, want %v", err, want)
		}
		// At-most-once: an ambiguous failure is re-proposed, never replayed.
		if _, err := f.uc.Approve(context.Background(), p.Token); !errors.Is(err, usecases.ErrTokenAlreadyUsed) {
			t.Fatalf("%v: second approve err = %v, want ErrTokenAlreadyUsed", want, err)
		}
	}
}

func TestNewReleaseWorkRequiresEveryCollaborator(t *testing.T) {
	signer, _ := approval.NewEphemeralSigner(time.Minute, nil)
	if _, err := usecases.NewReleaseWork(nil, signer, &fakeReleaser{}, nil, nil); err == nil {
		t.Error("nil advisory must be rejected")
	}
	if _, err := usecases.NewReleaseWork(&fakeDecider{}, nil, &fakeReleaser{}, nil, nil); err == nil {
		t.Error("nil signer must be rejected")
	}
	if _, err := usecases.NewReleaseWork(&fakeDecider{}, signer, nil, nil, nil); err == nil {
		t.Error("nil releaser must be rejected")
	}
}
