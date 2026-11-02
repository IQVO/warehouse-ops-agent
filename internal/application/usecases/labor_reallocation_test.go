package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakeSiteStaffing answers get_staffing_gap by siteCode from a table and
// records every path it was asked about.
type fakeSiteStaffing struct {
	mu    sync.Mutex
	gaps  map[string]ports.StaffingGap
	errs  map[string]error
	asked []string
	sites []string
}

func (f *fakeSiteStaffing) GetStaffingGapForSite(_ context.Context, siteCode, shiftId, pathId string) (ports.StaffingGap, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, pathId)
	f.sites = append(f.sites, siteCode+"/"+shiftId)
	if err := f.errs[pathId]; err != nil {
		return ports.StaffingGap{}, err
	}
	g, ok := f.gaps[pathId]
	if !ok {
		return ports.StaffingGap{}, errors.New("no such path")
	}
	return g, nil
}

func gap(path string, planned, active int, under bool) ports.StaffingGap {
	return ports.StaffingGap{PathId: path, PlannedHeads: planned, ActiveHeads: active, Understaffed: under}
}

func newOutlook(t *testing.T, f *fakeSiteStaffing) *usecases.LaborReallocationOutlook {
	t.Helper()
	uc, err := usecases.NewLaborReallocationOutlook(f)
	if err != nil {
		t.Fatal(err)
	}
	return uc
}

func okInput() usecases.LaborReallocationInput {
	return usecases.LaborReallocationInput{
		SiteCode: "WH1", ShiftId: "S1", ToPathId: "pack",
		Candidates: []policy.ReallocationCandidate{{AssociateId: "a1", FromPathId: "pick"}},
	}
}

func okStaffing() *fakeSiteStaffing {
	return &fakeSiteStaffing{gaps: map[string]ports.StaffingGap{
		"pack": gap("pack", 10, 7, true),
		"pick": gap("pick", 5, 8, false),
	}}
}

func TestNewLaborReallocationOutlookRequiresThePort(t *testing.T) {
	if _, err := usecases.NewLaborReallocationOutlook(nil); err == nil {
		t.Fatal("a nil staffing port must be a construction error")
	}
}

func TestOutlookRejectsInvalidInputBeforeCallingUpstream(t *testing.T) {
	long := strings.Repeat("x", 129)
	many := make([]policy.ReallocationCandidate, usecases.MaxReallocationCandidates+1)
	for i := range many {
		many[i] = policy.ReallocationCandidate{AssociateId: fmt.Sprintf("a%d", i)}
	}
	mut := func(f func(*usecases.LaborReallocationInput)) usecases.LaborReallocationInput {
		in := okInput()
		f(&in)
		return in
	}
	cases := map[string]usecases.LaborReallocationInput{
		"missing siteCode":      mut(func(in *usecases.LaborReallocationInput) { in.SiteCode = "" }),
		"blank shiftId":         mut(func(in *usecases.LaborReallocationInput) { in.ShiftId = "  " }),
		"missing toPathId":      mut(func(in *usecases.LaborReallocationInput) { in.ToPathId = "" }),
		"padded toPathId":       mut(func(in *usecases.LaborReallocationInput) { in.ToPathId = " pack" }),
		"no candidates":         mut(func(in *usecases.LaborReallocationInput) { in.Candidates = nil }),
		"too many candidates":   mut(func(in *usecases.LaborReallocationInput) { in.Candidates = many }),
		"blank associate":       mut(func(in *usecases.LaborReallocationInput) { in.Candidates[0].AssociateId = " " }),
		"blank fromPathId":      mut(func(in *usecases.LaborReallocationInput) { in.Candidates[0].FromPathId = " " }),
		"control char in id":    mut(func(in *usecases.LaborReallocationInput) { in.Candidates[0].AssociateId = "a\n1" }),
		"over-long associateId": mut(func(in *usecases.LaborReallocationInput) { in.Candidates[0].AssociateId = long }),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			f := okStaffing()
			_, err := newOutlook(t, f).Execute(context.Background(), in)
			if !errors.Is(err, usecases.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if len(f.asked) != 0 {
				t.Fatalf("upstream was called for invalid input: %v", f.asked)
			}
		})
	}
	// Boundaries that are valid.
	in := okInput()
	in.Candidates = make([]policy.ReallocationCandidate, usecases.MaxReallocationCandidates)
	for i := range in.Candidates {
		in.Candidates[i] = policy.ReallocationCandidate{AssociateId: fmt.Sprintf("a%d", i)}
	}
	in.Candidates[0].AssociateId = strings.Repeat("é", 128)
	if _, err := newOutlook(t, okStaffing()).Execute(context.Background(), in); err != nil {
		t.Fatalf("20 candidates and a 128-rune id are valid: %v", err)
	}
}

func TestOutlookReadsTargetAndEachDistinctOriginOnce(t *testing.T) {
	f := okStaffing()
	f.gaps["stow"] = gap("stow", 3, 9, false)
	in := okInput()
	in.Candidates = []policy.ReallocationCandidate{
		{AssociateId: "a1", FromPathId: "pick"}, {AssociateId: "a2", FromPathId: "pick"},
		{AssociateId: "a3", FromPathId: "stow"}, {AssociateId: "a4"},
		{AssociateId: "a5", FromPathId: "pack"}, // the target itself: not re-read
	}
	got, err := newOutlook(t, f).Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.asked, ",") != "pack,pick,stow" {
		t.Fatalf("paths read = %v, want pack,pick,stow once each", f.asked)
	}
	for _, s := range f.sites {
		if s != "WH1/S1" {
			t.Fatalf("read scoped to %q, want WH1/S1", s)
		}
	}
	if got.Target != (policy.PathStaffing{PathId: "pack", PlannedHeads: 10, ActiveHeads: 7, Understaffed: true}) {
		t.Fatalf("target = %+v", got.Target)
	}
	if got.SiteCode != "WH1" || got.ShiftId != "S1" {
		t.Fatalf("scope = %s/%s", got.SiteCode, got.ShiftId)
	}
	if len(got.Result.Moves) != 3 {
		t.Fatalf("moves = %+v", got.Result.Moves)
	}
}

func TestOutlookOriginReadFailureOnlyScreensThoseAssociates(t *testing.T) {
	f := okStaffing()
	f.errs = map[string]error{"pick": errors.New("upstream down")}
	in := okInput()
	in.Candidates = append(in.Candidates, policy.ReallocationCandidate{AssociateId: "a2"})
	got, err := newOutlook(t, f).Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("an unreadable origin must not fail the outlook: %v", err)
	}
	if len(got.Result.Moves) != 1 || got.Result.Moves[0].AssociateId != "a2" {
		t.Fatalf("moves = %+v", got.Result.Moves)
	}
	if len(got.Result.Skipped) != 1 || got.Result.Skipped[0].Reason != policy.SkipOriginStatusUnavailable {
		t.Fatalf("skipped = %+v", got.Result.Skipped)
	}
}

func TestOutlookTargetReadFailureClassification(t *testing.T) {
	f := okStaffing()
	f.errs = map[string]error{"pack": errors.New("boom")}
	_, err := newOutlook(t, f).Execute(context.Background(), okInput())
	if err == nil || errors.Is(err, usecases.ErrInvalidInput) {
		t.Fatalf("err = %v, want a plain upstream failure", err)
	}
	f.errs = map[string]error{"pack": fmt.Errorf("tool said no: %w", ports.ErrUpstreamInvalidInput)}
	if _, err := newOutlook(t, f).Execute(context.Background(), okInput()); !errors.Is(err, usecases.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestOutlookNotUnderstaffedIsAnAnswerNotAnError(t *testing.T) {
	f := okStaffing()
	f.gaps["pack"] = gap("pack", 10, 10, false)
	got, err := newOutlook(t, f).Execute(context.Background(), okInput())
	if err != nil || len(got.Result.Moves) != 0 || got.Target.Understaffed {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
}

// --- act side -------------------------------------------------------------

type fakeAssigner struct {
	calls atomic.Int32
	mu    sync.Mutex
	last  ports.AssignLaborRequest
	out   ports.LaborAssignment
	err   error
}

func (f *fakeAssigner) AssignLabor(_ context.Context, req ports.AssignLaborRequest) (ports.LaborAssignment, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.last = req
	f.mu.Unlock()
	return f.out, f.err
}

type reallocFixture struct {
	uc  *usecases.LaborReallocation
	in  *fakeSiteStaffing
	asg *fakeAssigner
	now *time.Time
	log *strings.Builder
}

func newReallocFixture(t *testing.T) *reallocFixture {
	t.Helper()
	now := actNow
	clock := func() time.Time { return now }
	signer, err := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, clock)
	if err != nil {
		t.Fatal(err)
	}
	f := &reallocFixture{in: okStaffing(), asg: &fakeAssigner{out: ports.LaborAssignment{AssociateId: "a1", PathId: "pack", AssignmentId: "a1@pack"}}, log: &strings.Builder{}}
	f.now = &now
	logger := slog.New(slog.NewTextHandler(f.log, nil))
	uc, err := usecases.NewLaborReallocation(newOutlook(t, f.in), signer, f.asg, logger, clock)
	if err != nil {
		t.Fatal(err)
	}
	f.uc = uc
	return f
}

func TestNewLaborReallocationRequiresEveryCollaborator(t *testing.T) {
	signer, _ := approval.NewEphemeralSigner(time.Minute, nil)
	o := newOutlook(t, okStaffing())
	a := &fakeAssigner{}
	for name, args := range map[string]func() (*usecases.LaborReallocation, error){
		"no outlook": func() (*usecases.LaborReallocation, error) {
			return usecases.NewLaborReallocation(nil, signer, a, nil, nil)
		},
		"no signer": func() (*usecases.LaborReallocation, error) { return usecases.NewLaborReallocation(o, nil, a, nil, nil) },
		"no assigner": func() (*usecases.LaborReallocation, error) {
			return usecases.NewLaborReallocation(o, signer, nil, nil, nil)
		},
	} {
		if uc, err := args(); err == nil || uc != nil {
			t.Errorf("%s: want a construction error, got %v", name, err)
		}
	}
	if _, err := usecases.NewLaborReallocation(o, signer, a, nil, nil); err != nil {
		t.Fatalf("nil logger and clock must default: %v", err)
	}
}

func proposeTwo(t *testing.T) (*reallocFixture, usecases.ReallocationProposals) {
	t.Helper()
	f := newReallocFixture(t)
	in := okInput()
	in.Candidates = []policy.ReallocationCandidate{{AssociateId: "a1", FromPathId: "pick"}, {AssociateId: "a2", FromPathId: "pick"}}
	got, err := f.uc.Propose(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return f, got
}

func TestProposeWritesNothingAndRanksTheMoves(t *testing.T) {
	f, got := proposeTwo(t)
	if f.asg.calls.Load() != 0 {
		t.Fatal("propose must not call the peer")
	}
	if len(got.Proposals) != 2 || got.SiteCode != "WH1" || got.ShiftId != "S1" || got.Target.PathId != "pack" {
		t.Fatalf("proposals = %+v", got)
	}
	for i, p := range got.Proposals {
		if p.Move.Rank != i+1 {
			t.Errorf("rank = %d, want %d", p.Move.Rank, i+1)
		}
	}
}

func TestProposeMintsOneBoundTokenPerMove(t *testing.T) {
	_, got := proposeTwo(t)
	seen := map[string]bool{}
	signer, _ := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, func() time.Time { return actNow })
	for i, p := range got.Proposals {
		r, err := signer.VerifyReallocation(p.Token)
		if err != nil {
			t.Fatalf("token %d does not verify: %v", i, err)
		}
		if r.AssociateId != p.Move.AssociateId || r.FromPathId != p.Move.FromPathId || r.ToPathId != "pack" || r.ProposalId() != p.ProposalId || !r.ExpiresAt.Equal(p.ExpiresAt) {
			t.Fatalf("token binds %+v, proposal shows %+v", r, p)
		}
		if seen[p.ProposalId] {
			t.Fatalf("proposal id %s is not unique", p.ProposalId)
		}
		seen[p.ProposalId] = true
	}
}

func TestProposeRefusesWhenThereIsNothingToConfirm(t *testing.T) {
	f := newReallocFixture(t)
	f.in.gaps["pack"] = gap("pack", 10, 10, false)
	if _, err := f.uc.Propose(context.Background(), okInput()); !errors.Is(err, usecases.ErrNotUnderstaffed) {
		t.Fatalf("err = %v, want ErrNotUnderstaffed", err)
	}
	f.in.gaps["pack"] = gap("pack", 10, 10, true) // flagged but not actually short
	if _, err := f.uc.Propose(context.Background(), okInput()); !errors.Is(err, usecases.ErrNotUnderstaffed) {
		t.Fatalf("flagged-but-full err = %v, want ErrNotUnderstaffed", err)
	}
	f.in.gaps["pack"] = gap("pack", 10, 7, true)
	f.in.gaps["pick"] = gap("pick", 5, 5, false) // no surplus to give
	if _, err := f.uc.Propose(context.Background(), okInput()); !errors.Is(err, usecases.ErrNoReallocationMove) {
		t.Fatalf("err = %v, want ErrNoReallocationMove", err)
	}
	if _, err := f.uc.Propose(context.Background(), usecases.LaborReallocationInput{}); !errors.Is(err, usecases.ErrInvalidInput) {
		t.Fatalf("empty input err = %v, want ErrInvalidInput", err)
	}
}

func proposeOne(t *testing.T, f *reallocFixture) usecases.ReallocationProposal {
	t.Helper()
	got, err := f.uc.Propose(context.Background(), okInput())
	if err != nil || len(got.Proposals) != 1 {
		t.Fatalf("propose: %+v, %v", got, err)
	}
	return got.Proposals[0]
}

func TestApproveDelegatesExactlyTheBoundMoveWithAttribution(t *testing.T) {
	f := newReallocFixture(t)
	p := proposeOne(t, f)
	*f.now = actNow.Add(time.Minute)
	out, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "supervisor-1")
	if err != nil {
		t.Fatal(err)
	}
	want := ports.AssignLaborRequest{AssociateId: "a1", PathId: "pack", ProposalId: p.ProposalId, ApprovedBy: "supervisor-1", AgentId: "warehouse-ops-agent"}
	if f.asg.calls.Load() != 1 || f.asg.last != want {
		t.Fatalf("peer saw %+v (%d calls), want %+v once", f.asg.last, f.asg.calls.Load(), want)
	}
	if out.ProposalId != p.ProposalId || out.AssociateId != "a1" || out.PathId != "pack" || out.AssignmentId != "a1@pack" || out.ApprovedBy != "supervisor-1" || !out.ExecutedAt.Equal(actNow.Add(time.Minute)) {
		t.Fatalf("outcome = %+v", out)
	}
	if !strings.Contains(f.log.String(), p.ProposalId) || !strings.Contains(f.log.String(), "supervisor-1") {
		t.Fatalf("the audit log does not carry proposal and approver:\n%s", f.log)
	}
}

func TestApproveRefusalsNeverCallThePeer(t *testing.T) {
	f := newReallocFixture(t)
	p := proposeOne(t, f)
	other := proposeOne(t, f)
	cases := []struct {
		name       string
		proposalId string
		token      string
		approvedBy string
		want       error
	}{
		{"forged token", p.ProposalId, p.Token + "x", "sup", approval.ErrInvalidToken},
		{"garbage token", p.ProposalId, "nope", "sup", approval.ErrInvalidToken},
		{"empty token", p.ProposalId, "", "sup", approval.ErrInvalidToken},
		{"another proposal's token", p.ProposalId, other.Token, "sup", usecases.ErrProposalMismatch},
		{"unknown proposal id", "lrp-deadbeef", p.Token, "sup", usecases.ErrProposalMismatch},
		{"missing approvedBy", p.ProposalId, p.Token, "", usecases.ErrInvalidInput},
		{"blank approvedBy", p.ProposalId, p.Token, "   ", usecases.ErrInvalidInput},
		{"control char approvedBy", p.ProposalId, p.Token, "a\r\nb", usecases.ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.uc.Approve(context.Background(), tc.proposalId, tc.token, tc.approvedBy); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if f.asg.calls.Load() != 0 {
				t.Fatal("the peer was called")
			}
		})
	}
	// The refusals above must not have burned the genuine token.
	if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); err != nil {
		t.Fatalf("genuine token after refusals: %v", err)
	}
}

func TestApproveExpiredTokenNeverCallsThePeer(t *testing.T) {
	f := newReallocFixture(t)
	p := proposeOne(t, f)
	*f.now = actNow.Add(5 * time.Minute)
	if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); !errors.Is(err, approval.ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
	if f.asg.calls.Load() != 0 {
		t.Fatal("the peer was called with an expired token")
	}
}

func TestApproveIsAtMostOnce(t *testing.T) {
	f := newReallocFixture(t)
	p := proposeOne(t, f)
	if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); !errors.Is(err, usecases.ErrTokenAlreadyUsed) {
		t.Fatalf("replay err = %v, want ErrTokenAlreadyUsed", err)
	}
	if f.asg.calls.Load() != 1 {
		t.Fatalf("peer calls = %d, want 1", f.asg.calls.Load())
	}
}

func TestApproveConcurrentRedemptionsDelegateOnce(t *testing.T) {
	f := newReallocFixture(t)
	p := proposeOne(t, f)
	var wg sync.WaitGroup
	var won atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); err == nil {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 || f.asg.calls.Load() != 1 {
		t.Fatalf("winners = %d, peer calls = %d, want 1 and 1", won.Load(), f.asg.calls.Load())
	}
}

func TestApprovePeerFailureConsumesTheTokenAndSurfacesTheTypedError(t *testing.T) {
	f := newReallocFixture(t)
	f.asg.err = fmt.Errorf("%w: certification required", ports.ErrAssignmentRejected)
	p := proposeOne(t, f)
	if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); !errors.Is(err, ports.ErrAssignmentRejected) {
		t.Fatalf("err = %v, want ErrAssignmentRejected", err)
	}
	f.asg.err = nil
	if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); !errors.Is(err, usecases.ErrTokenAlreadyUsed) {
		t.Fatalf("a failed delegation must not be silently retried: err = %v", err)
	}
}

func TestUsedNonceSetIsPrunedOnceTokensExpire(t *testing.T) {
	f := newReallocFixture(t)
	p := proposeOne(t, f)
	if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); err != nil {
		t.Fatal(err)
	}
	// Just before expiry the replay is still refused as "used".
	*f.now = actNow.Add(5*time.Minute - time.Second)
	if _, err := f.uc.Approve(context.Background(), p.ProposalId, p.Token, "sup"); !errors.Is(err, usecases.ErrTokenAlreadyUsed) {
		t.Fatalf("err = %v, want ErrTokenAlreadyUsed", err)
	}
	// A later token's redemption prunes the stale entry without error.
	*f.now = actNow.Add(10 * time.Minute)
	p2 := proposeOne(t, f)
	if _, err := f.uc.Approve(context.Background(), p2.ProposalId, p2.Token, "sup"); err != nil {
		t.Fatal(err)
	}
}
