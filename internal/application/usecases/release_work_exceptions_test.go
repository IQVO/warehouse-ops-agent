package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakeExceptions scripts the ADR-0028 advisory port.
type fakeExceptions struct {
	calls   atomic.Int32
	summary ports.ExceptionSummary
	err     error
	block   bool // wait for the caller's deadline, then fail
}

func (f *fakeExceptions) OpenExceptions(ctx context.Context, _ ports.ExceptionContextQuery) (ports.ExceptionSummary, error) {
	f.calls.Add(1)
	if f.block {
		<-ctx.Done()
		return ports.ExceptionSummary{}, ctx.Err()
	}
	return f.summary, f.err
}

var someExceptions = ports.ExceptionSummary{OpenCount: 2, Cases: []ports.ExceptionBrief{
	{CaseID: "case-1", Status: "OPEN", Queue: "problem-solve", Severity: "HIGH", Escalated: true, Reason: "CartonDiscrepancyRaised"},
	{CaseID: "case-2", Status: "OPEN", Queue: "ps-main", Severity: "LOW", Reason: "TaskStuck"},
}}

// newExceptionFixture is newActFixture with the advisory port wired in.
func newExceptionFixture(t *testing.T, d policy.Decision, port ports.ExceptionContext, timeout time.Duration) (*actFixture, *approval.Signer) {
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
	f.uc, err = usecases.NewReleaseWork(f.dec, signer, f.rel, nil, clock, usecases.WithExceptionContext(port, timeout))
	if err != nil {
		t.Fatal(err)
	}
	return f, signer
}

// minted is everything about a proposal that the human confirms and the
// approval later redeems, i.e. everything the lookup must not influence.
// The nonce is random per mint by design, so it is the one field excluded.
type minted struct {
	Decision policy.Decision
	Bound    approval.Proposal
}

func mintedFrom(t *testing.T, signer *approval.Signer, p usecases.ReleaseProposal) minted {
	t.Helper()
	bound, err := signer.Verify(p.Token)
	if err != nil {
		t.Fatalf("token does not verify: %v", err)
	}
	bound.Nonce = ""
	return minted{Decision: p.Decision, Bound: bound}
}

func TestExceptionLookupNeverChangesTheDecisionOrTheToken(t *testing.T) {
	// The baseline is the pre-ADR-0028 behaviour: no lookup configured.
	base, baseSigner := newExceptionFixture(t, releaseDecision, nil, 0)
	baseProposal, err := base.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	if err != nil {
		t.Fatal(err)
	}
	want := mintedFrom(t, baseSigner, baseProposal)

	scenarios := map[string]struct {
		port   ports.ExceptionContext
		status usecases.ExceptionContextStatus
	}{
		"exceptions present":      {&fakeExceptions{summary: someExceptions}, usecases.ExceptionContextOK},
		"exceptions absent":       {&fakeExceptions{summary: ports.ExceptionSummary{Cases: []ports.ExceptionBrief{}}}, usecases.ExceptionContextOK},
		"peer failed":             {&fakeExceptions{err: ports.ErrExceptionContextUnavailable}, usecases.ExceptionContextUnavailable},
		"peer answered off-shape": {&fakeExceptions{err: errors.New("anything at all")}, usecases.ExceptionContextUnavailable},
		"peer timed out":          {&fakeExceptions{block: true}, usecases.ExceptionContextUnavailable},
		"lookup disabled":         {nil, usecases.ExceptionContextDisabled},
	}
	for name, sc := range scenarios {
		t.Run(name, func(t *testing.T) {
			f, signer := newExceptionFixture(t, releaseDecision, sc.port, 50*time.Millisecond)
			p, err := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
			if err != nil {
				t.Fatalf("Propose failed because of the lookup: %v", err)
			}
			if p.ExceptionContext.Status != sc.status {
				t.Errorf("exceptionContext.status = %q, want %q", p.ExceptionContext.Status, sc.status)
			}
			if got := mintedFrom(t, signer, p); !reflect.DeepEqual(got, want) {
				t.Errorf("decision/token changed by the lookup:\n got %+v\nwant %+v", got, want)
			}
			if !p.ExpiresAt.Equal(baseProposal.ExpiresAt) {
				t.Errorf("expiry changed: %v vs %v", p.ExpiresAt, baseProposal.ExpiresAt)
			}
			// The minted token still redeems exactly the proposed release.
			if _, err := f.uc.Approve(context.Background(), p.Token); err != nil || f.rel.path != "pick-zone-a" || f.rel.calls.Load() != 1 {
				t.Errorf("approve after lookup: err=%v path=%q calls=%d", err, f.rel.path, f.rel.calls.Load())
			}
		})
	}
}

func TestExceptionLookupResultIsCarriedOnlyWhenOk(t *testing.T) {
	f, _ := newExceptionFixture(t, releaseDecision, &fakeExceptions{summary: someExceptions}, 0)
	p, _ := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	if p.ExceptionContext.Summary == nil || !reflect.DeepEqual(*p.ExceptionContext.Summary, someExceptions) {
		t.Fatalf("summary = %+v, want %+v", p.ExceptionContext.Summary, someExceptions)
	}
	for name, port := range map[string]ports.ExceptionContext{"unavailable": &fakeExceptions{err: errors.New("x")}, "disabled": nil} {
		f, _ := newExceptionFixture(t, releaseDecision, port, 0)
		p, _ := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
		if p.ExceptionContext.Summary != nil {
			t.Errorf("%s: summary = %+v, want none", name, p.ExceptionContext.Summary)
		}
	}
}

func TestExceptionLookupIsBoundedByItsOwnTimeout(t *testing.T) {
	f, _ := newExceptionFixture(t, releaseDecision, &fakeExceptions{block: true}, 100*time.Millisecond)
	start := time.Now()
	p, err := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	if err != nil || p.Token == "" {
		t.Fatalf("a hung peer must not fail the proposal: token=%q err=%v", p.Token, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("propose took %s, the lookup deadline was not applied", elapsed)
	}
}

func TestRefusedProposalsNeverConsultTheExceptionHub(t *testing.T) {
	partial := releaseDecision
	partial.Partial = true
	partial.MissingSignals = []string{"workforce-management"}
	for name, tc := range map[string]struct {
		d    policy.Decision
		want error
	}{
		"other recommendation": {policy.Decision{RecommendedAction: policy.ActionAssignLabor, Source: policy.SourceDeterministic}, usecases.ErrNotRecommended},
		"partial evidence":     {partial, usecases.ErrIncompleteEvidence},
	} {
		port := &fakeExceptions{summary: someExceptions}
		f, _ := newExceptionFixture(t, tc.d, port, 0)
		p, err := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
		if !errors.Is(err, tc.want) || p.Token != "" {
			t.Errorf("%s: err=%v token=%q, want %v and no token", name, err, p.Token, tc.want)
		}
		if port.calls.Load() != 0 {
			t.Errorf("%s: the hub was consulted for a refused proposal", name)
		}
	}
}

func TestApproveNeverConsultsTheExceptionHub(t *testing.T) {
	port := &fakeExceptions{summary: someExceptions}
	f, _ := newExceptionFixture(t, releaseDecision, port, 0)
	p, _ := f.uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	before := port.calls.Load()
	if _, err := f.uc.Approve(context.Background(), p.Token); err != nil {
		t.Fatal(err)
	}
	if port.calls.Load() != before {
		t.Fatal("Approve consulted the exception hub; the answer is advisory at propose time only")
	}
}
