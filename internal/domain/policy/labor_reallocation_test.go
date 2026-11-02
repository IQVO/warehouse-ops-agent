package policy

import (
	"reflect"
	"strings"
	"testing"
)

func ps(path string, planned, active int, under bool) PathStaffing {
	return PathStaffing{PathId: path, PlannedHeads: planned, ActiveHeads: active, Understaffed: under}
}

func statusMap(ss ...PathStaffing) map[string]PathStaffing {
	m := map[string]PathStaffing{}
	for _, s := range ss {
		m[s.PathId] = s
	}
	return m
}

var testScope = ReallocationScope{SiteCode: "WH1", ShiftId: "S1"}

func moveIDs(r ReallocationResult) []string {
	out := []string{}
	for _, m := range r.Moves {
		out = append(out, m.AssociateId)
	}
	return out
}

func skipMap(r ReallocationResult) map[string]string {
	out := map[string]string{}
	for _, s := range r.Skipped {
		out[s.AssociateId] = s.Reason
	}
	return out
}

func TestProposeLaborReallocation_NoMoveUnlessTargetShortOfPlan(t *testing.T) {
	cands := []ReallocationCandidate{{AssociateId: "a1", FromPathId: "pick"}}
	origins := statusMap(ps("pick", 5, 8, false))
	cases := []struct {
		name string
		gap  PathStaffing
		want int
	}{
		{"understaffed and short", ps("pack", 10, 7, true), 1},
		{"short by exactly one head", ps("pack", 10, 9, true), 1},
		{"flagged understaffed but active equals planned", ps("pack", 10, 10, true), 0},
		{"flagged understaffed but active above planned", ps("pack", 10, 11, true), 0},
		{"short of plan but upstream says not understaffed", ps("pack", 10, 7, false), 0},
		{"fully staffed", ps("pack", 10, 10, false), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ProposeLaborReallocation(tc.gap, origins, cands, testScope)
			if len(got.Moves) != tc.want {
				t.Fatalf("moves = %d, want %d (%+v)", len(got.Moves), tc.want, got)
			}
			if tc.want == 0 && len(got.Skipped) != 0 {
				t.Fatalf("no proposal possible, so nothing should be screened or limited: %+v", got.Skipped)
			}
			if got.Moves == nil || got.Skipped == nil {
				t.Fatalf("Moves/Skipped must be non-nil empty slices for stable JSON, got %#v", got)
			}
		})
	}
}

func TestProposeLaborReallocation_MoveCountIsBoundedByShortfallAndThree(t *testing.T) {
	origins := statusMap(ps("pick", 2, 12, false))
	cands := []ReallocationCandidate{
		{AssociateId: "a1", FromPathId: "pick"}, {AssociateId: "a2", FromPathId: "pick"},
		{AssociateId: "a3", FromPathId: "pick"}, {AssociateId: "a4", FromPathId: "pick"},
		{AssociateId: "a5", FromPathId: "pick"},
	}
	cases := []struct {
		name   string
		active int
		want   []string
	}{
		{"short by 1", 9, []string{"a1"}},
		{"short by 2", 8, []string{"a1", "a2"}},
		{"short by exactly the cap", 7, []string{"a1", "a2", "a3"}},
		{"short by more than the cap", 2, []string{"a1", "a2", "a3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ProposeLaborReallocation(ps("pack", 10, tc.active, true), origins, cands, testScope)
			if !reflect.DeepEqual(moveIDs(got), tc.want) {
				t.Fatalf("moves = %v, want %v", moveIDs(got), tc.want)
			}
			for _, id := range []string{"a4", "a5", "a3", "a2"} {
				if r, ok := skipMap(got)[id]; ok && r != SkipMoveLimitReached {
					t.Fatalf("%s skipped for %q, want %q", id, r, SkipMoveLimitReached)
				}
			}
		})
	}
	if MaxReallocationMoves != 3 {
		t.Fatalf("MaxReallocationMoves = %d, brief fixes it at 3", MaxReallocationMoves)
	}
}

func TestProposeLaborReallocation_OriginScreening(t *testing.T) {
	cases := []struct {
		name       string
		from       string
		origins    map[string]PathStaffing
		wantMove   bool
		wantReason string
	}{
		{"origin one head above plan", "pick", statusMap(ps("pick", 5, 6, false)), true, ""},
		{"origin exactly at plan", "pick", statusMap(ps("pick", 5, 5, false)), false, SkipOriginNotAbovePlan},
		{"origin below plan but not flagged", "pick", statusMap(ps("pick", 5, 4, false)), false, SkipOriginNotAbovePlan},
		{"origin flagged understaffed", "pick", statusMap(ps("pick", 5, 8, true)), false, SkipOriginUnderstaffed},
		{"origin reading unavailable", "pick", statusMap(), false, SkipOriginStatusUnavailable},
		{"origin is the target path", "pack", statusMap(ps("pack", 10, 7, true)), false, SkipAlreadyOnTargetPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ProposeLaborReallocation(ps("pack", 10, 7, true), tc.origins, []ReallocationCandidate{{AssociateId: "a1", FromPathId: tc.from}}, testScope)
			if (len(got.Moves) == 1) != tc.wantMove {
				t.Fatalf("moves = %+v, want move=%t", got.Moves, tc.wantMove)
			}
			if tc.wantMove {
				if len(got.Skipped) != 0 {
					t.Fatalf("unexpected skips %+v", got.Skipped)
				}
				return
			}
			if r := skipMap(got)["a1"]; r != tc.wantReason {
				t.Fatalf("skip reason = %q, want %q", r, tc.wantReason)
			}
		})
	}
}

func TestProposeLaborReallocation_SurplusIsSpentOncePerMove(t *testing.T) {
	origins := statusMap(ps("pick", 5, 6, false)) // exactly one head of surplus
	cands := []ReallocationCandidate{{AssociateId: "a1", FromPathId: "pick"}, {AssociateId: "a2", FromPathId: "pick"}}
	got := ProposeLaborReallocation(ps("pack", 10, 5, true), origins, cands, testScope)
	if !reflect.DeepEqual(moveIDs(got), []string{"a1"}) {
		t.Fatalf("moves = %v, want only a1", moveIDs(got))
	}
	if r := skipMap(got)["a2"]; r != SkipOriginSurplusUsed {
		t.Fatalf("a2 skip = %q, want %q", r, SkipOriginSurplusUsed)
	}
	// Two heads of surplus allow two moves off the same path.
	got = ProposeLaborReallocation(ps("pack", 10, 5, true), statusMap(ps("pick", 5, 7, false)), cands, testScope)
	if !reflect.DeepEqual(moveIDs(got), []string{"a1", "a2"}) {
		t.Fatalf("moves = %v, want a1,a2", moveIDs(got))
	}
}

func TestProposeLaborReallocation_Ranking(t *testing.T) {
	origins := statusMap(ps("pick", 20, 21, false), ps("stow", 1, 5, false))
	cands := []ReallocationCandidate{
		{AssociateId: "z-nopath"},
		{AssociateId: "b-pick", FromPathId: "pick"},
		{AssociateId: "a-nopath"},
		{AssociateId: "c-stow", FromPathId: "stow"},
		{AssociateId: "a-pick", FromPathId: "stow"},
	}
	got := ProposeLaborReallocation(ps("pack", 10, 0, true), origins, cands, testScope)
	// Biggest surplus first (stow 4), then pick (1), then the unstated origins
	// last, each group by associate id.
	want := []string{"a-pick", "c-stow", "b-pick"}
	if !reflect.DeepEqual(moveIDs(got), want) {
		t.Fatalf("moves = %v, want %v", moveIDs(got), want)
	}
	for i, m := range got.Moves {
		if m.Rank != i+1 {
			t.Errorf("move %d rank = %d, want %d", i, m.Rank, i+1)
		}
	}

	// With room for all three, the unstated-origin candidates fill the last
	// slot, ordered by id.
	got = ProposeLaborReallocation(ps("pack", 10, 0, true), statusMap(), []ReallocationCandidate{{AssociateId: "z-nopath"}, {AssociateId: "a-nopath"}}, testScope)
	if !reflect.DeepEqual(moveIDs(got), []string{"a-nopath", "z-nopath"}) {
		t.Fatalf("moves = %v", moveIDs(got))
	}
}

func TestProposeLaborReallocation_RankingTieBreaksOnAssociateIdAfterSurplus(t *testing.T) {
	origins := statusMap(ps("pick", 5, 7, false), ps("stow", 5, 7, false))
	cands := []ReallocationCandidate{{AssociateId: "m", FromPathId: "stow"}, {AssociateId: "k", FromPathId: "pick"}, {AssociateId: "c", FromPathId: "stow"}}
	got := ProposeLaborReallocation(ps("pack", 10, 5, true), origins, cands, testScope)
	if !reflect.DeepEqual(moveIDs(got), []string{"c", "k", "m"}) {
		t.Fatalf("equal surplus must order by associate id, got %v", moveIDs(got))
	}
}

func TestProposeLaborReallocation_DuplicateAssociateIsScreened(t *testing.T) {
	origins := statusMap(ps("pick", 5, 9, false))
	cands := []ReallocationCandidate{{AssociateId: "a1", FromPathId: "pick"}, {AssociateId: "a1", FromPathId: "pick"}}
	got := ProposeLaborReallocation(ps("pack", 10, 5, true), origins, cands, testScope)
	if !reflect.DeepEqual(moveIDs(got), []string{"a1"}) {
		t.Fatalf("moves = %v", moveIDs(got))
	}
	if r := skipMap(got)["a1"]; r != SkipDuplicateAssociate {
		t.Fatalf("skip = %q", r)
	}
}

func TestProposeLaborReallocation_MoveContent(t *testing.T) {
	origins := statusMap(ps("pick", 5, 8, false))
	got := ProposeLaborReallocation(ps("pack", 10, 7, true), origins, []ReallocationCandidate{{AssociateId: "a1", FromPathId: "pick"}}, testScope)
	if len(got.Moves) != 1 {
		t.Fatalf("moves = %+v", got.Moves)
	}
	m := got.Moves[0]
	if m.Rank != 1 || m.AssociateId != "a1" || m.FromPathId != "pick" || m.ToPathId != "pack" {
		t.Fatalf("move = %+v", m)
	}
	if !reflect.DeepEqual(m.ReasonCodes, []string{ReasonTargetUnderstaffed, ReasonOriginAbovePlan}) {
		t.Fatalf("reasons = %v", m.ReasonCodes)
	}
	wantEv := []ReallocationEvidence{
		{Tool: "workforce-management get_staffing_gap", Summary: "path pack: plannedHeads=10 activeHeads=7 understaffed=true"},
		{Tool: "workforce-management get_staffing_gap", Summary: "path pick: plannedHeads=5 activeHeads=8 understaffed=false"},
	}
	if !reflect.DeepEqual(m.Evidence, wantEv) {
		t.Fatalf("evidence = %+v, want %+v", m.Evidence, wantEv)
	}
	wantCheck := "Read-only: call workforce-management get_staffing_gap (siteCode=WH1, shiftId=S1) for pathId=pack and confirm it is still understaffed, and for pathId=pick and confirm it is still above its plannedHeads"
	if m.NextCheck != wantCheck {
		t.Fatalf("nextCheck = %q, want %q", m.NextCheck, wantCheck)
	}
}

func TestProposeLaborReallocation_NoStatedOriginSaysSo(t *testing.T) {
	got := ProposeLaborReallocation(ps("pack", 10, 7, true), statusMap(), []ReallocationCandidate{{AssociateId: "a1"}}, testScope)
	if len(got.Moves) != 1 {
		t.Fatalf("moves = %+v", got.Moves)
	}
	m := got.Moves[0]
	if m.FromPathId != "" {
		t.Fatalf("fromPathId = %q, want empty", m.FromPathId)
	}
	if !reflect.DeepEqual(m.ReasonCodes, []string{ReasonTargetUnderstaffed, ReasonNoCurrentPath}) {
		t.Fatalf("reasons = %v", m.ReasonCodes)
	}
	if len(m.Evidence) != 1 {
		t.Fatalf("evidence = %+v, want only the target reading", m.Evidence)
	}
	if !strings.Contains(m.NextCheck, "current path was not stated") || !strings.Contains(m.NextCheck, "pathId=pack") {
		t.Fatalf("nextCheck = %q", m.NextCheck)
	}
}

func TestProposeLaborReallocation_EveryMoveCarriesNextCheckAndEvidence(t *testing.T) {
	origins := statusMap(ps("pick", 5, 9, false))
	cands := []ReallocationCandidate{{AssociateId: "a1", FromPathId: "pick"}, {AssociateId: "a2"}}
	got := ProposeLaborReallocation(ps("pack", 10, 5, true), origins, cands, testScope)
	if len(got.Moves) != 2 {
		t.Fatalf("moves = %+v", got.Moves)
	}
	for _, m := range got.Moves {
		if !strings.HasPrefix(m.NextCheck, "Read-only:") || len(m.Evidence) == 0 || len(m.ReasonCodes) == 0 {
			t.Errorf("move %+v lacks next check / evidence / reasons", m)
		}
	}
}
