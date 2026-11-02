// Package policy: labor reallocation rules (ADR 0029, act slice #2).
//
// Given the staffing reading of an understaffed target path, the staffing
// readings of the paths the candidate associates currently work on, and the
// candidate list itself, the rules rank at most MaxReallocationMoves moves
// {associateId, fromPathId?, toPathId}. Every figure that appears in a
// move's evidence is a reading workforce-management returned verbatim
// (plannedHeads, activeHeads, understaffed); nothing is estimated, no
// threshold is invented and no quantity is derived for display.
//
// Two facts are used only as plain comparisons of returned readings:
//
//   - an origin path may give an associate up only while its activeHeads is
//     strictly above its plannedHeads (moving someone off a path at or below
//     plan would just move the gap), and each accepted move uses up one head
//     of that surplus;
//   - the target never receives more moves than it is short of plannedHeads.
//
// The upstream exposes no tool that lists associates or their current
// assignment, so the candidates are caller-supplied (the human knows who is
// movable); the rules rank and screen them, they never invent one. An
// associate with no stated origin path is kept but ranks last and says so,
// because nothing verifies that the move leaves another path whole.
//
// Pure values and functions only (no ports, no I/O, no clock).
package policy

import (
	"cmp"
	"fmt"
	"slices"
)

// MaxReallocationMoves bounds the ranked list a proposal returns. It is a
// presentation bound (a human reads the list), not a staffing threshold.
const MaxReallocationMoves = 3

// Reason codes a move carries. They are part of the published contract.
const (
	// ReasonTargetUnderstaffed: workforce-management reports the target
	// path understaffed (activeHeads below plannedHeads).
	ReasonTargetUnderstaffed = "target_path_understaffed"
	// ReasonOriginAbovePlan: the associate's current path has more active
	// heads than planned, so it can give one up without opening a gap.
	ReasonOriginAbovePlan = "origin_path_above_plan"
	// ReasonNoCurrentPath: the caller named no current path for the
	// associate, so no origin reading backs the move.
	ReasonNoCurrentPath = "no_current_path_stated"
)

// Reasons a candidate was screened out. Skipped candidates are reported so
// an operator can see why a name they supplied did not make the list.
const (
	SkipDuplicateAssociate      = "duplicate_associate"
	SkipAlreadyOnTargetPath     = "already_on_target_path"
	SkipOriginStatusUnavailable = "origin_status_unavailable"
	SkipOriginUnderstaffed      = "origin_path_understaffed"
	SkipOriginNotAbovePlan      = "origin_path_not_above_plan"
	SkipOriginSurplusUsed       = "origin_surplus_already_used"
	SkipMoveLimitReached        = "move_limit_reached"
)

// PathStaffing is one get_staffing_gap reading, as returned.
type PathStaffing struct {
	PathId       string
	PlannedHeads int
	ActiveHeads  int
	Understaffed bool
}

// ReallocationCandidate is one associate the caller offers for a move.
// FromPathId is empty when the caller did not state a current path.
type ReallocationCandidate struct {
	AssociateId string
	FromPathId  string
}

// ReallocationScope is the site and shift the readings were taken for. It
// only feeds the next-check text.
type ReallocationScope struct {
	SiteCode string
	ShiftId  string
}

// ReallocationEvidence is one reading a move rests on.
type ReallocationEvidence struct {
	Tool    string
	Summary string
}

// ReallocationMove is one ranked proposal. FromPathId is empty when the
// caller stated no current path. NextCheck is the read-only check an
// operator can do before approving.
type ReallocationMove struct {
	Rank        int
	AssociateId string
	FromPathId  string
	ToPathId    string
	ReasonCodes []string
	Evidence    []ReallocationEvidence
	NextCheck   string
}

// SkippedCandidate says why a supplied candidate is not in the list.
type SkippedCandidate struct {
	AssociateId string
	Reason      string
}

// ReallocationResult is the ranked list plus the screened-out candidates.
type ReallocationResult struct {
	Moves   []ReallocationMove
	Skipped []SkippedCandidate
}

const staffingGapTool = "workforce-management get_staffing_gap"

// ProposeLaborReallocation ranks at most MaxReallocationMoves moves onto the
// target path. perPathStatus holds the readings of the origin paths that
// could be read; a candidate whose origin has no reading is screened out
// (unknown is never treated as surplus). No move is proposed unless the
// target is reported understaffed and is actually short of plannedHeads.
func ProposeLaborReallocation(gap PathStaffing, perPathStatus map[string]PathStaffing, assignments []ReallocationCandidate, scope ReallocationScope) ReallocationResult {
	res := ReallocationResult{Moves: []ReallocationMove{}, Skipped: []SkippedCandidate{}}
	if !gap.Understaffed || gap.ActiveHeads >= gap.PlannedHeads {
		return res
	}
	budget := min(gap.PlannedHeads-gap.ActiveHeads, MaxReallocationMoves)

	eligible, skipped := screenCandidates(gap, perPathStatus, assignments)
	res.Skipped = append(res.Skipped, skipped...)
	rankCandidates(eligible)

	surplusLeft := map[string]int{}
	for id, st := range perPathStatus {
		surplusLeft[id] = st.ActiveHeads - st.PlannedHeads
	}
	for _, c := range eligible {
		if len(res.Moves) >= budget {
			res.Skipped = append(res.Skipped, SkippedCandidate{AssociateId: c.cand.AssociateId, Reason: SkipMoveLimitReached})
			continue
		}
		if c.cand.FromPathId != "" {
			if surplusLeft[c.cand.FromPathId] <= 0 {
				res.Skipped = append(res.Skipped, SkippedCandidate{AssociateId: c.cand.AssociateId, Reason: SkipOriginSurplusUsed})
				continue
			}
			surplusLeft[c.cand.FromPathId]--
		}
		res.Moves = append(res.Moves, buildMove(len(res.Moves)+1, gap, c.cand, perPathStatus, scope))
	}
	return res
}

// rankedCandidate pairs a candidate with its ranking key.
type rankedCandidate struct {
	cand    ReallocationCandidate
	surplus int // origin activeHeads - plannedHeads; 0 when no origin stated
}

// screenCandidates drops every candidate that must not be proposed and
// returns the rest with their ranking key.
func screenCandidates(gap PathStaffing, perPathStatus map[string]PathStaffing, assignments []ReallocationCandidate) ([]rankedCandidate, []SkippedCandidate) {
	var eligible []rankedCandidate
	var skipped []SkippedCandidate
	seen := map[string]bool{}
	for _, c := range assignments {
		if seen[c.AssociateId] {
			skipped = append(skipped, SkippedCandidate{AssociateId: c.AssociateId, Reason: SkipDuplicateAssociate})
			continue
		}
		seen[c.AssociateId] = true
		if reason := originSkipReason(gap.PathId, c.FromPathId, perPathStatus); reason != "" {
			skipped = append(skipped, SkippedCandidate{AssociateId: c.AssociateId, Reason: reason})
			continue
		}
		rc := rankedCandidate{cand: c}
		if c.FromPathId != "" {
			st := perPathStatus[c.FromPathId]
			rc.surplus = st.ActiveHeads - st.PlannedHeads
		}
		eligible = append(eligible, rc)
	}
	return eligible, skipped
}

// originSkipReason returns why a candidate's stated origin rules the move
// out, or "" when the origin is fine (or not stated).
func originSkipReason(target, from string, perPathStatus map[string]PathStaffing) string {
	if from == "" {
		return ""
	}
	if from == target {
		return SkipAlreadyOnTargetPath
	}
	st, ok := perPathStatus[from]
	if !ok {
		return SkipOriginStatusUnavailable
	}
	if st.Understaffed {
		return SkipOriginUnderstaffed
	}
	if st.ActiveHeads <= st.PlannedHeads {
		return SkipOriginNotAbovePlan
	}
	return ""
}

// rankCandidates orders candidates best-first: origins above plan before
// candidates with no stated origin, a bigger surplus first, then associate
// id so the order is deterministic.
func rankCandidates(cs []rankedCandidate) {
	slices.SortStableFunc(cs, func(a, b rankedCandidate) int {
		return cmp.Or(
			// origins stated first
			-cmp.Compare(boolRank(a.cand.FromPathId != ""), boolRank(b.cand.FromPathId != "")),
			// bigger surplus first
			-cmp.Compare(a.surplus, b.surplus),
			cmp.Compare(a.cand.AssociateId, b.cand.AssociateId),
		)
	})
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

func buildMove(rank int, gap PathStaffing, c ReallocationCandidate, perPathStatus map[string]PathStaffing, scope ReallocationScope) ReallocationMove {
	m := ReallocationMove{
		Rank:        rank,
		AssociateId: c.AssociateId,
		FromPathId:  c.FromPathId,
		ToPathId:    gap.PathId,
		ReasonCodes: []string{ReasonTargetUnderstaffed},
		Evidence:    []ReallocationEvidence{{Tool: staffingGapTool, Summary: readingSummary(gap)}},
	}
	if c.FromPathId == "" {
		m.ReasonCodes = append(m.ReasonCodes, ReasonNoCurrentPath)
		m.NextCheck = nextCheck(scope, gap.PathId, "")
		return m
	}
	m.ReasonCodes = append(m.ReasonCodes, ReasonOriginAbovePlan)
	m.Evidence = append(m.Evidence, ReallocationEvidence{Tool: staffingGapTool, Summary: readingSummary(perPathStatus[c.FromPathId])})
	m.NextCheck = nextCheck(scope, gap.PathId, c.FromPathId)
	return m
}

func readingSummary(s PathStaffing) string {
	return fmt.Sprintf("path %s: plannedHeads=%d activeHeads=%d understaffed=%t", s.PathId, s.PlannedHeads, s.ActiveHeads, s.Understaffed)
}

// nextCheck names the read-only call an operator can make before approving.
func nextCheck(scope ReallocationScope, to, from string) string {
	s := fmt.Sprintf("Read-only: call workforce-management get_staffing_gap (siteCode=%s, shiftId=%s) for pathId=%s and confirm it is still understaffed", scope.SiteCode, scope.ShiftId, to)
	if from == "" {
		return s + "; the associate's current path was not stated, so also confirm where they are assigned before approving"
	}
	return s + fmt.Sprintf(", and for pathId=%s and confirm it is still above its plannedHeads", from)
}
